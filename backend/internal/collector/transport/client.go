package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/spool"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	defaultDialTimeout   = 10 * time.Second
	defaultRPCTimeout    = 10 * time.Second
	defaultTLSReloadFreq = 30 * time.Second
)

// ErrorKind identifies the stage where transport delivery failed.
type ErrorKind string

const (
	ErrorKindConfig       ErrorKind = "config"
	ErrorKindDial         ErrorKind = "dial"
	ErrorKindStream       ErrorKind = "stream"
	ErrorKindDecode       ErrorKind = "decode"
	ErrorKindSend         ErrorKind = "send"
	ErrorKindReceive      ErrorKind = "receive"
	ErrorKindRetryExhaust ErrorKind = "retry_exhausted"
	ErrorKindTLS          ErrorKind = "tls"
)

var (
	// ErrNoEndpoints indicates no controller endpoint is configured.
	ErrNoEndpoints = errors.New("no controller endpoints configured")
	// ErrInvalidBatch indicates payload decoding succeeded but core fields were invalid.
	ErrInvalidBatch = errors.New("invalid telemetry batch payload")
	// ErrEmptyAckBatchID indicates controller acknowledged without a batch id.
	ErrEmptyAckBatchID = errors.New("empty ack batch id")
)

// Error describes a transport failure with endpoint and attempt context.
type Error struct {
	Kind     ErrorKind
	Endpoint string
	Attempt  int
	Err      error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Endpoint == "" {
		return fmt.Sprintf("transport %s error: %v", e.Kind, e.Err)
	}
	return fmt.Sprintf("transport %s error (endpoint=%s, attempt=%d): %v", e.Kind, e.Endpoint, e.Attempt, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Config controls transport behavior.
type Config struct {
	Endpoints   []string
	Mirror      bool
	Compress    bool
	DialTimeout time.Duration
	RPCTimeout  time.Duration
	TLS         TLSConfig
}

// TLSConfig controls mutual TLS transport settings.
type TLSConfig struct {
	Enabled            bool
	CAFile             string
	CertFile           string
	KeyFile            string
	ServerName         string
	InsecureSkipVerify bool
	ReloadInterval     time.Duration
}

// Client manages telemetry delivery to controller endpoints.
type Client struct {
	logger *zap.Logger

	mu      sync.RWMutex
	cfg     Config
	nextIdx int
	stats   clientStats

	tlsMu       sync.Mutex
	cachedCreds credentials.TransportCredentials
	lastTLSLoad time.Time

	sendToEndpointFn func(context.Context, int, string, []byte) (*telemetryv1.Ack, error)
}

type clientStats struct {
	mu             sync.RWMutex
	lastSendMs     float64
	lastAckMs      float64
	lastErrs       uint64
	lastRetries    uint64
	lastEndpoint   string
	lastCompressed bool
	lastErrorKind  string
}

// New creates a transport client.
func New(cfg Config, logger *zap.Logger) (*Client, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	cfg = normalizeConfig(cfg)
	if len(cfg.Endpoints) == 0 {
		return nil, &Error{Kind: ErrorKindConfig, Err: ErrNoEndpoints}
	}
	client := &Client{
		cfg:    cfg,
		logger: logger.With(zap.String("component", "transport")),
	}
	client.sendToEndpointFn = client.sendToEndpoint
	return client, nil
}

// ApplyConfig updates runtime transport settings.
func (c *Client) ApplyConfig(cfg Config) error {
	cfg = normalizeConfig(cfg)
	if len(cfg.Endpoints) == 0 {
		return &Error{Kind: ErrorKindConfig, Err: ErrNoEndpoints}
	}

	c.mu.Lock()
	c.cfg = cfg
	c.nextIdx = 0
	c.mu.Unlock()

	c.tlsMu.Lock()
	c.cachedCreds = nil
	c.lastTLSLoad = time.Time{}
	c.tlsMu.Unlock()

	return nil
}

// Send sends a payload to one or more controllers.
func (c *Client) Send(ctx context.Context, payload []byte) (*telemetryv1.Ack, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("send context canceled: %w", err)
	}

	cfg, endpointOrder := c.snapshotConfigAndOrder()
	if len(endpointOrder) == 0 {
		return nil, &Error{Kind: ErrorKindConfig, Err: ErrNoEndpoints}
	}

	if cfg.Mirror {
		return c.sendMirror(ctx, endpointOrder, payload)
	}
	return c.sendWithFailover(ctx, endpointOrder, payload)
}

func (c *Client) sendMirror(ctx context.Context, endpoints []string, payload []byte) (*telemetryv1.Ack, error) {
	var (
		lastAck *telemetryv1.Ack
		errs    []error
	)
	for attempt, endpoint := range endpoints {
		ack, err := c.sendToEndpointFn(ctx, attempt+1, endpoint, payload)
		if err != nil {
			errs = append(errs, err)
			c.stats.bumpErr(err)
			c.logger.Warn("mirror send failed", zap.String("endpoint", endpoint), zap.Error(err))
			continue
		}
		lastAck = ack
	}
	if lastAck == nil {
		return nil, &Error{
			Kind: ErrorKindRetryExhaust,
			Err:  fmt.Errorf("mirror send failed across %d endpoints: %w", len(endpoints), errors.Join(errs...)),
		}
	}
	return lastAck, nil
}

func (c *Client) sendWithFailover(ctx context.Context, endpoints []string, payload []byte) (*telemetryv1.Ack, error) {
	var errs []error
	for attempt, endpoint := range endpoints {
		if attempt > 0 {
			c.stats.bumpRetry(1)
		}
		ack, err := c.sendToEndpointFn(ctx, attempt+1, endpoint, payload)
		if err == nil {
			return ack, nil
		}
		errs = append(errs, err)
		c.stats.bumpErr(err)
	}
	return nil, &Error{
		Kind: ErrorKindRetryExhaust,
		Err:  fmt.Errorf("all endpoints failed (%d attempts): %w", len(endpoints), errors.Join(errs...)),
	}
}

// Drain attempts to flush the spool.
func (c *Client) Drain(ctx context.Context, sp *spool.Spool, send func([]byte) (string, error)) error {
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("drain aborted: %w", err)
		}

		payload, nextOffset, err := sp.Next()
		if err != nil {
			return fmt.Errorf("read spool payload: %w", err)
		}
		if payload == nil {
			return nil
		}

		if _, err := send(payload); err != nil {
			if IsPermanentPayloadError(err) {
				c.logger.Warn("dropping permanently invalid spool payload", zap.Error(err))
				if err := sp.Commit(nextOffset); err != nil {
					return fmt.Errorf("commit dropped spool offset: %w", err)
				}
				continue
			}
			return fmt.Errorf("send spool payload: %w", err)
		}
		if err := sp.Commit(nextOffset); err != nil {
			return fmt.Errorf("commit spool offset: %w", err)
		}
	}
}

// IsPermanentPayloadError reports whether the payload itself is invalid and
// should be dropped from the spool rather than retried indefinitely.
func IsPermanentPayloadError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrInvalidBatch) {
		return true
	}

	var typed *Error
	if errors.As(err, &typed) {
		switch typed.Kind {
		case ErrorKindDecode:
			return true
		case ErrorKindReceive, ErrorKindSend, ErrorKindStream:
			switch status.Code(typed.Err) {
			case codes.InvalidArgument, codes.FailedPrecondition, codes.OutOfRange:
				return true
			}
		}
		if typed.Err != nil && IsPermanentPayloadError(typed.Err) {
			return true
		}
	}

	type multiUnwrapper interface {
		Unwrap() []error
	}
	if multi, ok := err.(multiUnwrapper); ok {
		for _, inner := range multi.Unwrap() {
			if IsPermanentPayloadError(inner) {
				return true
			}
		}
	}
	if inner := errors.Unwrap(err); inner != nil {
		if IsPermanentPayloadError(inner) {
			return true
		}
	}
	return false
}

func (c *Client) sendToEndpoint(ctx context.Context, attempt int, endpoint string, payload []byte) (*telemetryv1.Ack, error) {
	cfg := c.snapshotConfig()
	rpcCtx, cancel := context.WithTimeout(ctx, cfg.RPCTimeout)
	defer cancel()

	conn, err := c.dial(rpcCtx, endpoint)
	if err != nil {
		return nil, &Error{Kind: ErrorKindDial, Endpoint: endpoint, Attempt: attempt, Err: err}
	}
	defer conn.Close()

	client := telemetryv1.NewTelemetryIngestClient(conn)
	stream, err := client.Push(rpcCtx)
	if err != nil {
		return nil, &Error{Kind: ErrorKindStream, Endpoint: endpoint, Attempt: attempt, Err: err}
	}

	batch, err := decodeBatchPayload(payload)
	if err != nil {
		return nil, &Error{Kind: ErrorKindDecode, Endpoint: endpoint, Attempt: attempt, Err: err}
	}

	sendStart := time.Now()
	if err := stream.Send(batch); err != nil {
		return nil, &Error{Kind: ErrorKindSend, Endpoint: endpoint, Attempt: attempt, Err: err}
	}
	sendElapsed := time.Since(sendStart)

	ackStart := time.Now()
	ack, err := stream.Recv()
	if err != nil {
		return nil, &Error{Kind: ErrorKindReceive, Endpoint: endpoint, Attempt: attempt, Err: err}
	}
	if ack == nil || strings.TrimSpace(ack.BatchId) == "" {
		return nil, &Error{Kind: ErrorKindReceive, Endpoint: endpoint, Attempt: attempt, Err: ErrEmptyAckBatchID}
	}
	ackElapsed := time.Since(ackStart)

	if err := stream.CloseSend(); err != nil {
		c.logger.Debug("close send stream failed",
			zap.String("endpoint", endpoint),
			zap.Int("attempt", attempt),
			zap.Error(err),
		)
	}
	c.stats.update(endpoint, sendElapsed, ackElapsed, cfg.Compress)
	return ack, nil
}

func decodeBatchPayload(payload []byte) (*telemetryv1.TelemetryBatch, error) {
	batch := &telemetryv1.TelemetryBatch{}
	if err := proto.Unmarshal(payload, batch); err != nil {
		return nil, err
	}
	if err := validateBatch(batch); err != nil {
		return nil, err
	}
	return batch, nil
}

func validateBatch(batch *telemetryv1.TelemetryBatch) error {
	if batch == nil {
		return ErrInvalidBatch
	}
	if strings.TrimSpace(batch.BatchId) == "" {
		return fmt.Errorf("%w: batch_id is required", ErrInvalidBatch)
	}
	if batch.Collector == nil || strings.TrimSpace(batch.Collector.CollectorId) == "" {
		return fmt.Errorf("%w: collector.collector_id is required", ErrInvalidBatch)
	}
	return nil
}

func (c *Client) dial(ctx context.Context, endpoint string) (*grpc.ClientConn, error) {
	cfg := c.snapshotConfig()
	dialCtx, cancel := context.WithTimeout(ctx, cfg.DialTimeout)
	defer cancel()

	transportCreds, err := c.currentTransportCredentials(cfg.TLS)
	if err != nil {
		return nil, &Error{Kind: ErrorKindTLS, Endpoint: endpoint, Err: err}
	}

	opts := []grpc.DialOption{grpc.WithTransportCredentials(transportCreds)}
	if cfg.Compress {
		opts = append(opts, grpc.WithDefaultCallOptions(grpc.UseCompressor(gzip.Name)))
	}

	conn, err := grpc.DialContext(dialCtx, endpoint, opts...)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func (c *Client) currentTransportCredentials(cfg TLSConfig) (credentials.TransportCredentials, error) {
	if !cfg.Enabled {
		return insecure.NewCredentials(), nil
	}

	reloadEvery := cfg.ReloadInterval
	if reloadEvery <= 0 {
		reloadEvery = defaultTLSReloadFreq
	}

	c.tlsMu.Lock()
	defer c.tlsMu.Unlock()

	if c.cachedCreds != nil && time.Since(c.lastTLSLoad) < reloadEvery {
		return c.cachedCreds, nil
	}

	creds, err := loadTLSCredentials(cfg)
	if err != nil {
		return nil, err
	}
	c.cachedCreds = creds
	c.lastTLSLoad = time.Now()
	return creds, nil
}

func loadTLSCredentials(cfg TLSConfig) (credentials.TransportCredentials, error) {
	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         cfg.ServerName,
		InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // explicitly configured by operators for break-glass
	}

	if cfg.CAFile != "" {
		caPEM, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read tls ca file %q: %w", cfg.CAFile, err)
		}
		caPool := x509.NewCertPool()
		if ok := caPool.AppendCertsFromPEM(caPEM); !ok {
			return nil, fmt.Errorf("parse tls ca file %q: invalid PEM", cfg.CAFile)
		}
		tlsCfg.RootCAs = caPool
	}

	if cfg.CertFile != "" || cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load tls client cert/key: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return credentials.NewTLS(tlsCfg), nil
}

func normalizeConfig(cfg Config) Config {
	cfg.Endpoints = normalizeEndpoints(cfg.Endpoints)
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = defaultDialTimeout
	}
	if cfg.RPCTimeout <= 0 {
		cfg.RPCTimeout = defaultRPCTimeout
	}
	if cfg.TLS.ReloadInterval <= 0 {
		cfg.TLS.ReloadInterval = defaultTLSReloadFreq
	}
	return cfg
}

func normalizeEndpoints(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, endpoint := range in {
		endpoint = strings.TrimSpace(endpoint)
		if endpoint == "" {
			continue
		}
		if _, ok := seen[endpoint]; ok {
			continue
		}
		seen[endpoint] = struct{}{}
		out = append(out, endpoint)
	}
	return out
}

func (c *Client) snapshotConfig() Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg
}

func (c *Client) snapshotConfigAndOrder() (Config, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	cfg := c.cfg
	endpoints := append([]string(nil), cfg.Endpoints...)
	if len(endpoints) == 0 {
		return cfg, nil
	}
	if cfg.Mirror || len(endpoints) == 1 {
		return cfg, endpoints
	}

	start := c.nextIdx % len(endpoints)
	c.nextIdx++
	ordered := make([]string, 0, len(endpoints))
	for i := 0; i < len(endpoints); i++ {
		ordered = append(ordered, endpoints[(start+i)%len(endpoints)])
	}
	return cfg, ordered
}

func (c *Client) LastSendMs() float64   { return c.stats.lastSendMsValue() }
func (c *Client) LastAckMs() float64    { return c.stats.lastAckMsValue() }
func (c *Client) LastErrs() uint64      { return c.stats.lastErrsValue() }
func (c *Client) LastRetries() uint64   { return c.stats.lastRetriesValue() }
func (c *Client) LastEndpoint() string  { return c.stats.lastEndpointValue() }
func (c *Client) LastCompressed() bool  { return c.stats.lastCompressedValue() }
func (c *Client) LastErrorKind() string { return c.stats.lastErrorKindValue() }

func (s *clientStats) update(endpoint string, send time.Duration, ack time.Duration, compressed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastEndpoint = endpoint
	s.lastSendMs = float64(send.Milliseconds())
	s.lastAckMs = float64(ack.Milliseconds())
	s.lastCompressed = compressed
}

func (s *clientStats) bumpErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastErrs++
	var typed *Error
	if errors.As(err, &typed) {
		s.lastErrorKind = string(typed.Kind)
		return
	}
	s.lastErrorKind = "unknown"
}

func (s *clientStats) bumpRetry(count uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRetries += count
}

func (s *clientStats) lastSendMsValue() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastSendMs
}

func (s *clientStats) lastAckMsValue() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastAckMs
}

func (s *clientStats) lastErrsValue() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastErrs
}

func (s *clientStats) lastRetriesValue() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastRetries
}

func (s *clientStats) lastEndpointValue() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastEndpoint
}

func (s *clientStats) lastCompressedValue() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastCompressed
}

func (s *clientStats) lastErrorKindValue() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastErrorKind
}
