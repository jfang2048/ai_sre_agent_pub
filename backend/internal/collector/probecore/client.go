package probecore

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	probeipcv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/probeipc/v1"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

const restartDelay = 1 * time.Second

var validCollectorModules = map[string]struct{}{
	"all":     {},
	"host":    {},
	"disk":    {},
	"network": {},
	"rdma":    {},
	"netlink": {},
	"ethtool": {},
	"perf":    {},
	"ebpf":    {},
	"gpu":     {},
	"process": {},
}

// Config controls the C++ probe-core subprocess runtime and IPC boundaries.
type Config struct {
	BinaryPath         string
	Collectors         []string
	Args               []string
	Interval           time.Duration
	TopK               int
	WindowSamples      int
	QueueDepth         int
	Compression        string
	GPUIntervalSamples int
	EBPFSocketPath     string
	StartupTimeout     time.Duration
	StaleAfter         time.Duration
	FrameMaxBytes      int
}

// Stats captures parser/runtime health for the C++ probe-core stream.
type Stats struct {
	FramesReceived  uint64
	DecodeErrors    uint64
	CRCFailures     uint64
	Restarts        uint64
	LastSequence    uint32
	LastError       string
	LastReceivedAt  time.Time
	LastReceivedAge time.Duration
}

type snapshot struct {
	batch      *probeipcv1.ProbeBatch
	receivedAt time.Time
}

// Client runs and reads a C++ probe-core process that streams ProbeBatch envelopes over stdout.
type Client struct {
	cfg    Config
	logger *zap.Logger

	mu      sync.RWMutex
	latest  snapshot
	running bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	framesReceived atomic.Uint64
	decodeErrors   atomic.Uint64
	crcFailures    atomic.Uint64
	restarts       atomic.Uint64
	lastSequence   atomic.Uint32

	lastErr atomic.Value // string
}

// NewClient creates a probe-core IPC client; call Start to begin streaming.
func NewClient(cfg Config, logger *zap.Logger) (*Client, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	c := &Client{
		cfg:    cfg,
		logger: logger.With(zap.String("component", "probe-core-ipc")),
	}
	c.lastErr.Store("")
	return c, nil
}

// Start launches the C++ probe-core subprocess and reader loop.
func (c *Client) Start(parent context.Context) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(parent)
	c.cancel = cancel
	c.running = true
	c.wg.Add(1)
	go c.supervisor(runCtx)
	c.mu.Unlock()

	if c.cfg.StartupTimeout <= 0 {
		return nil
	}

	deadline := time.NewTimer(c.cfg.StartupTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		if _, ok := c.Latest(c.cfg.StaleAfter); ok {
			return nil
		}
		select {
		case <-deadline.C:
			lastErr, _ := c.lastErr.Load().(string)
			if strings.TrimSpace(lastErr) == "" {
				lastErr = "no frame received"
			}
			c.Stop()
			return fmt.Errorf("probe-core startup timeout after %s: %s", c.cfg.StartupTimeout, lastErr)
		case <-ticker.C:
		case <-runCtx.Done():
			c.Stop()
			return runCtx.Err()
		}
	}
}

// Stop terminates the subprocess and waits for reader shutdown.
func (c *Client) Stop() {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	cancel := c.cancel
	c.cancel = nil
	c.running = false
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	c.wg.Wait()
}

// Latest returns the most recent ProbeBatch if it is not stale.
func (c *Client) Latest(maxAge time.Duration) (*probeipcv1.ProbeBatch, bool) {
	c.mu.RLock()
	snap := c.latest
	c.mu.RUnlock()
	if snap.batch == nil {
		return nil, false
	}
	if maxAge > 0 && time.Since(snap.receivedAt) > maxAge {
		return nil, false
	}
	return snap.batch, true
}

// Stats reports runtime counters and freshness.
func (c *Client) Stats() Stats {
	stats := Stats{
		FramesReceived: c.framesReceived.Load(),
		DecodeErrors:   c.decodeErrors.Load(),
		CRCFailures:    c.crcFailures.Load(),
		Restarts:       c.restarts.Load(),
		LastSequence:   c.lastSequence.Load(),
	}
	if lastErr, ok := c.lastErr.Load().(string); ok {
		stats.LastError = lastErr
	}

	c.mu.RLock()
	receivedAt := c.latest.receivedAt
	c.mu.RUnlock()
	if !receivedAt.IsZero() {
		stats.LastReceivedAt = receivedAt
		stats.LastReceivedAge = time.Since(receivedAt)
	}
	return stats
}

func (c *Client) supervisor(ctx context.Context) {
	defer c.wg.Done()
	for {
		err := c.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			c.setLastError(err)
			c.logger.Warn("probe-core process stopped", zap.Error(err))
		}
		c.restarts.Add(1)
		select {
		case <-ctx.Done():
			return
		case <-time.After(restartDelay):
		}
	}
}

func (c *Client) runOnce(ctx context.Context) error {
	args := c.buildArgs()
	cmd := exec.CommandContext(ctx, c.cfg.BinaryPath, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create probe-core stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("create probe-core stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start probe-core %q: %w", c.cfg.BinaryPath, err)
	}
	c.logger.Info("probe-core process started", zap.String("binary", c.cfg.BinaryPath), zap.Strings("args", args))

	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			c.logger.Debug("probe-core stderr", zap.String("line", line))
		}
		if scanErr := scanner.Err(); scanErr != nil {
			c.logger.Debug("probe-core stderr scanner stopped", zap.Error(scanErr))
		}
	}()

	readErr := c.readLoop(stdout)
	waitErr := cmd.Wait()
	<-stderrDone

	if ctx.Err() != nil {
		return ctx.Err()
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return readErr
	}
	if waitErr != nil {
		return waitErr
	}
	if readErr != nil {
		return readErr
	}
	return nil
}

func (c *Client) readLoop(r io.Reader) error {
	lenBuf := make([]byte, 4)
	for {
		if _, err := io.ReadFull(r, lenBuf); err != nil {
			return err
		}
		frameLen := binary.LittleEndian.Uint32(lenBuf)
		if frameLen == 0 {
			c.decodeErrors.Add(1)
			c.setLastError(fmt.Errorf("received zero-length probe-core frame"))
			continue
		}
		if frameLen > uint32(c.cfg.FrameMaxBytes) {
			c.decodeErrors.Add(1)
			return fmt.Errorf("probe-core frame length %d exceeds max %d", frameLen, c.cfg.FrameMaxBytes)
		}
		frame := make([]byte, int(frameLen))
		if _, err := io.ReadFull(r, frame); err != nil {
			return err
		}
		batch, err := c.decodeFrame(frame)
		if err != nil {
			c.decodeErrors.Add(1)
			c.setLastError(err)
			continue
		}

		now := time.Now()
		c.mu.Lock()
		c.latest = snapshot{batch: batch, receivedAt: now}
		c.mu.Unlock()

		c.framesReceived.Add(1)
		c.lastSequence.Store(batch.GetSequence())
		c.setLastError(nil)
	}
}

func (c *Client) decodeFrame(frame []byte) (*probeipcv1.ProbeBatch, error) {
	env := &probeipcv1.FrameEnvelope{}
	if err := proto.Unmarshal(frame, env); err != nil {
		return nil, fmt.Errorf("decode frame envelope: %w", err)
	}

	payload := env.GetPayload()
	if payload == nil {
		return nil, fmt.Errorf("empty frame payload")
	}
	actual := crc32.ChecksumIEEE(payload)
	if actual != env.GetPayloadCrc32() {
		c.crcFailures.Add(1)
		return nil, fmt.Errorf("frame crc mismatch: got %d want %d", actual, env.GetPayloadCrc32())
	}

	decodedPayload, err := c.decodePayload(payload, env.GetCompression())
	if err != nil {
		return nil, err
	}

	batch := &probeipcv1.ProbeBatch{}
	if err := proto.Unmarshal(decodedPayload, batch); err != nil {
		return nil, fmt.Errorf("decode probe batch: %w", err)
	}
	return batch, nil
}

func (c *Client) decodePayload(payload []byte, compression probeipcv1.Compression) ([]byte, error) {
	switch compression {
	case probeipcv1.Compression_COMPRESSION_NONE:
		return payload, nil
	case probeipcv1.Compression_COMPRESSION_GZIP:
		zr, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("open gzip payload: %w", err)
		}
		defer zr.Close()

		limit := int64(c.cfg.FrameMaxBytes * 4)
		if limit < 1024 {
			limit = 1024
		}
		limited := &io.LimitedReader{R: zr, N: limit + 1}
		decoded, err := io.ReadAll(limited)
		if err != nil {
			return nil, fmt.Errorf("read gzip payload: %w", err)
		}
		if int64(len(decoded)) > limit {
			return nil, fmt.Errorf("gzip payload exceeds decoded limit %d bytes", limit)
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("unsupported frame compression: %s", compression.String())
	}
}

func (c *Client) buildArgs() []string {
	args := make([]string, 0, 16)
	intervalMS := int(c.cfg.Interval / time.Millisecond)
	if intervalMS < 100 {
		intervalMS = 100
	}
	args = append(args,
		"--interval-ms", strconv.Itoa(intervalMS),
		"--topk", strconv.Itoa(maxInt(1, c.cfg.TopK)),
		"--window-samples", strconv.Itoa(maxInt(1, c.cfg.WindowSamples)),
		"--queue-depth", strconv.Itoa(maxInt(1, c.cfg.QueueDepth)),
		"--compression", normalizeCompression(c.cfg.Compression),
		"--gpu-interval-samples", strconv.Itoa(maxInt(1, c.cfg.GPUIntervalSamples)),
	)
	collectors := normalizeCollectors(c.cfg.Collectors)
	if len(collectors) > 0 {
		args = append(args, "--collectors", strings.Join(collectors, ","))
	}
	if socket := strings.TrimSpace(c.cfg.EBPFSocketPath); socket != "" {
		args = append(args, "--ebpf-socket", socket)
	}
	args = append(args, c.cfg.Args...)
	return args
}

func (c *Client) setLastError(err error) {
	if err == nil {
		c.lastErr.Store("")
		return
	}
	c.lastErr.Store(err.Error())
}

func (cfg Config) validate() error {
	if strings.TrimSpace(cfg.BinaryPath) == "" {
		return fmt.Errorf("probe-core binary path is required")
	}
	if cfg.Interval <= 0 {
		return fmt.Errorf("probe-core interval must be > 0")
	}
	if cfg.TopK <= 0 {
		return fmt.Errorf("probe-core topk must be > 0")
	}
	if cfg.WindowSamples <= 0 {
		return fmt.Errorf("probe-core window_samples must be > 0")
	}
	if cfg.QueueDepth <= 0 {
		return fmt.Errorf("probe-core queue_depth must be > 0")
	}
	if cfg.GPUIntervalSamples <= 0 {
		return fmt.Errorf("probe-core gpu_interval_samples must be > 0")
	}
	if cfg.StartupTimeout <= 0 {
		return fmt.Errorf("probe-core startup_timeout must be > 0")
	}
	if cfg.StaleAfter <= 0 {
		return fmt.Errorf("probe-core stale_after must be > 0")
	}
	if cfg.FrameMaxBytes <= 0 {
		return fmt.Errorf("probe-core frame_max_bytes must be > 0")
	}
	for _, collector := range cfg.Collectors {
		module := strings.TrimSpace(strings.ToLower(collector))
		if module == "" {
			continue
		}
		if _, ok := validCollectorModules[module]; !ok {
			return fmt.Errorf("probe-core collectors contains unsupported module %q", collector)
		}
	}
	if len(normalizeCollectors(cfg.Collectors)) > 0 && containsCollectorsFlag(cfg.Args) {
		return fmt.Errorf("probe-core collectors conflicts with args (--collectors); use one source")
	}
	switch normalizeCompression(cfg.Compression) {
	case "none", "gzip":
	default:
		return fmt.Errorf("probe-core compression must be one of: none, gzip")
	}
	return nil
}

func normalizeCompression(raw string) string {
	mode := strings.TrimSpace(strings.ToLower(raw))
	if mode == "gzip" {
		return "gzip"
	}
	return "none"
}

func normalizeCollectors(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		module := strings.TrimSpace(strings.ToLower(item))
		if module == "" {
			continue
		}
		if _, ok := validCollectorModules[module]; !ok {
			continue
		}
		if module == "all" {
			return nil
		}
		if _, exists := seen[module]; exists {
			continue
		}
		seen[module] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	ordered := []string{
		"host",
		"disk",
		"network",
		"rdma",
		"netlink",
		"ethtool",
		"perf",
		"ebpf",
		"gpu",
		"process",
	}
	out := make([]string, 0, len(seen))
	for _, module := range ordered {
		if _, ok := seen[module]; ok {
			out = append(out, module)
		}
	}
	return out
}

func containsCollectorsFlag(args []string) bool {
	for _, arg := range args {
		normalized := strings.TrimSpace(strings.ToLower(arg))
		if normalized == "--collectors" || strings.HasPrefix(normalized, "--collectors=") {
			return true
		}
	}
	return false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
