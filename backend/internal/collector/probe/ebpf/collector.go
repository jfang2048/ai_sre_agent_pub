package ebpf

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/collections/ring"
)

const (
	defaultSocketPath             = "./data/collector/run/sre_collector_ebpf.sock"
	defaultRingSize               = 2048
	defaultMaxMessageBytes        = 64 * 1024
	defaultSyntheticPollInterval  = 10 * time.Second
	defaultLongLivedTCPThreshold  = 5 * time.Minute
	defaultEventFlushLimit        = 256
	defaultEventDescriptionMaxLen = 240
	defaultLabelKeyMaxLen         = 96
	defaultLabelValueMaxLen       = 240
)

// Config controls the mandatory eBPF runtime core.
type Config struct {
	SocketPath            string
	MaxMessageBytes       int
	RingSize              int
	EventFlushLimit       int
	Categories            []string
	AllowedListenPorts    []int
	SyntheticPoll         time.Duration
	LongLivedTCPThreshold time.Duration
}

// DefaultConfig returns hardened defaults for the eBPF runtime.
func DefaultConfig() Config {
	return Config{
		SocketPath:            defaultSocketPath,
		MaxMessageBytes:       defaultMaxMessageBytes,
		RingSize:              defaultRingSize,
		EventFlushLimit:       defaultEventFlushLimit,
		Categories:            []string{"syscall", "process", "network", "file", "security", "resource"},
		AllowedListenPorts:    []int{22, 53, 80, 443, 2379, 2380, 3000, 5432, 6443, 8080, 8443, 9090},
		SyntheticPoll:         defaultSyntheticPollInterval,
		LongLivedTCPThreshold: defaultLongLivedTCPThreshold,
	}
}

type wireEvent struct {
	Timestamp int64  `json:"timestamp"`
	Category  string `json:"category"`
	Type      string `json:"type"`
	PID       int    `json:"pid,omitempty"`
	PPID      int    `json:"ppid,omitempty"`
	UID       int    `json:"uid,omitempty"`
	EUID      int    `json:"euid,omitempty"`
	Comm      string `json:"comm,omitempty"`
	Cgroup    string `json:"cgroup,omitempty"`
	Device    string `json:"device,omitempty"`
	Bytes     uint64 `json:"bytes,omitempty"`
	LatencyNs uint64 `json:"latency_ns,omitempty"`
	Details   string `json:"details,omitempty"`
}

type legacySecurityEvent struct {
	Timestamp int64  `json:"timestamp"`
	Type      string `json:"type"`
	PID       int    `json:"pid"`
	Comm      string `json:"comm"`
	Details   string `json:"details"`
}

type processStats struct {
	pid       int
	comm      string
	lastSeen  time.Time
	syscalls  map[string]uint64
	openCalls uint64
	connect   uint64
	accept    uint64
	bind      uint64
	exec      uint64
	fork      uint64
	exit      uint64
	cpuUserMS uint64
	cpuSysMS  uint64
	rssBytes  uint64
}

type connectionState struct {
	key              string
	pid              int
	remoteIP         string
	startedAt        time.Time
	longLivedEmitted bool
}

// Collector is the mandatory eBPF-first runtime observability module.
//
// Why libbpf (not BCC):
//   - libbpf CO-RE is the production path because it does not require a runtime
//     compiler toolchain and is stable across heterogeneous kernels.
//   - This Go collector consumes the libbpf ring-buffer event stream over a Unix
//     socket and mirrors BPF-map-style aggregates in-process for API export.
type Collector struct {
	cfg Config

	mu            sync.RWMutex
	listener      net.Listener
	stopCh        chan struct{}
	wg            sync.WaitGroup
	events        *ring.Ring[Event]
	pendingEvents []Event
	seq           uint64

	runtimeMode string
	lastEventAt time.Time
	lastEmitAt  time.Time
	lastCounts  map[string]uint64

	syscallStats  map[string]uint64
	categoryStats map[string]uint64
	categoryBytes map[string]uint64
	categoryLatNS map[string]uint64
	categoryLatCt map[string]uint64
	processStats  map[int]*processStats
	processClass  map[string]uint64
	filePatterns  map[string]uint64
	networkCounts map[string]uint64
	remoteScopes  map[string]uint64
	pathScopes    map[string]uint64
	resourceStats map[string]map[string]uint64
	connections   map[string]*connectionState
	bindPorts     map[int]uint64
	privEscalate  uint64
}

// NewCollector creates a mandatory eBPF collector.
func NewCollector(cfg Config) *Collector {
	def := DefaultConfig()
	if strings.TrimSpace(cfg.SocketPath) == "" {
		cfg.SocketPath = def.SocketPath
	}
	if cfg.MaxMessageBytes <= 0 {
		cfg.MaxMessageBytes = def.MaxMessageBytes
	}
	if cfg.RingSize <= 0 {
		cfg.RingSize = def.RingSize
	}
	if cfg.EventFlushLimit <= 0 {
		cfg.EventFlushLimit = def.EventFlushLimit
	}
	if len(cfg.Categories) == 0 {
		cfg.Categories = def.Categories
	}
	if len(cfg.AllowedListenPorts) == 0 {
		cfg.AllowedListenPorts = def.AllowedListenPorts
	}
	if cfg.SyntheticPoll <= 0 {
		cfg.SyntheticPoll = def.SyntheticPoll
	}
	if cfg.LongLivedTCPThreshold <= 0 {
		cfg.LongLivedTCPThreshold = def.LongLivedTCPThreshold
	}

	return &Collector{
		cfg:           cfg,
		stopCh:        make(chan struct{}),
		events:        ring.New[Event](cfg.RingSize),
		pendingEvents: make([]Event, 0, cfg.EventFlushLimit*2),
		runtimeMode:   "libbpf_ringbuf",
		lastCounts:    make(map[string]uint64),
		syscallStats:  make(map[string]uint64),
		categoryStats: make(map[string]uint64),
		categoryBytes: make(map[string]uint64),
		categoryLatNS: make(map[string]uint64),
		categoryLatCt: make(map[string]uint64),
		processStats:  make(map[int]*processStats),
		processClass:  make(map[string]uint64),
		filePatterns:  make(map[string]uint64),
		networkCounts: make(map[string]uint64),
		remoteScopes:  make(map[string]uint64),
		pathScopes:    make(map[string]uint64),
		resourceStats: make(map[string]map[string]uint64),
		connections:   make(map[string]*connectionState),
		bindPorts:     make(map[int]uint64),
	}
}

// Start launches the socket receiver and synthetic fallback sampler.
func (c *Collector) Start() error {
	if c == nil {
		return nil
	}

	_ = os.Remove(c.cfg.SocketPath)
	if err := os.MkdirAll(filepath.Dir(c.cfg.SocketPath), 0o755); err != nil {
		return err
	}

	l, err := net.Listen("unix", c.cfg.SocketPath)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.listener = l
	c.mu.Unlock()

	c.wg.Add(2)
	go c.acceptLoop()
	go c.syntheticLoop()
	return nil
}

// Stop terminates background workers.
func (c *Collector) Stop() {
	if c == nil {
		return
	}
	select {
	case <-c.stopCh:
		return
	default:
		close(c.stopCh)
	}

	c.mu.Lock()
	if c.listener != nil {
		_ = c.listener.Close()
	}
	c.mu.Unlock()
	c.wg.Wait()
}

func (c *Collector) acceptLoop() {
	defer c.wg.Done()

	c.mu.RLock()
	l := c.listener
	c.mu.RUnlock()
	if l == nil {
		return
	}
	for {
		conn, err := l.Accept()
		if err != nil {
			select {
			case <-c.stopCh:
				return
			default:
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		c.wg.Add(1)
		go c.handleConn(conn)
	}
}

func (c *Collector) handleConn(conn net.Conn) {
	defer c.wg.Done()
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	buf := make([]byte, 0, c.cfg.MaxMessageBytes)
	scanner.Buffer(buf, c.cfg.MaxMessageBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		c.parseAndIngest(line)
	}
}

func (c *Collector) parseAndIngest(line string) {
	var evt wireEvent
	if err := json.Unmarshal([]byte(line), &evt); err == nil && evt.Type != "" {
		c.ingest(evt)
		return
	}

	var legacy legacySecurityEvent
	if err := json.Unmarshal([]byte(line), &legacy); err == nil && legacy.Type != "" {
		c.ingest(wireEvent{
			Timestamp: legacy.Timestamp,
			Category:  "security",
			Type:      legacy.Type,
			PID:       legacy.PID,
			Comm:      legacy.Comm,
			Details:   legacy.Details,
		})
	}
}

func (c *Collector) syntheticLoop() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.cfg.SyntheticPoll)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.collectSyntheticRuntimeStats()
		}
	}
}

func (c *Collector) collectSyntheticRuntimeStats() {
	now := time.Now().UTC()
	ports, connections := parseProcNetTCP()

	c.mu.Lock()
	defer c.mu.Unlock()

	if now.Sub(c.lastEventAt) > 2*c.cfg.SyntheticPoll {
		c.runtimeMode = "synthetic_proc_fallback"
	}

	allowed := make(map[int]struct{}, len(c.cfg.AllowedListenPorts))
	for _, port := range c.cfg.AllowedListenPorts {
		allowed[port] = struct{}{}
	}

	for port := range ports {
		c.networkCounts["listening"]++
		if _, ok := allowed[port]; !ok {
			c.bindPorts[port]++
			evt := c.newEvent(now, "network", "bind", 0, "", "node", fmt.Sprintf("abnormal listening port %d detected", port))
			evt.Port = port
			evt.Severity = "high"
			evt.Confidence = 0.86
			c.pushEventLocked(evt)
		}
	}

	for _, conn := range connections {
		key := conn.key
		state, ok := c.connections[key]
		if !ok {
			state = &connectionState{
				key:       key,
				pid:       conn.pid,
				remoteIP:  conn.remoteIP,
				startedAt: now,
			}
			c.connections[key] = state
			continue
		}
		if state.longLivedEmitted {
			continue
		}
		if now.Sub(state.startedAt) >= c.cfg.LongLivedTCPThreshold {
			evt := c.newEvent(now, "network", "long_lived_tcp", state.pid, "", "node", "long-lived TCP connection detected")
			evt.RemoteIP = conn.remoteIP
			evt.Severity = "medium"
			evt.Confidence = 0.74
			evt.Metadata = map[string]string{
				"duration": now.Sub(state.startedAt).String(),
			}
			state.longLivedEmitted = true
			c.pushEventLocked(evt)
		}
	}
}

type parsedConnection struct {
	key      string
	pid      int
	remoteIP string
}

func parseProcNetTCP() (map[int]struct{}, []parsedConnection) {
	listening := make(map[int]struct{})
	connections := make([]parsedConnection, 0, 64)

	data, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		return listening, connections
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		localAddr := fields[1]
		remoteAddr := fields[2]
		state := fields[3]
		lPort := parseHexPort(localAddr)
		rIP := parseHexIP(remoteAddr)
		switch state {
		case "0A": // LISTEN
			if lPort > 0 {
				listening[lPort] = struct{}{}
			}
		case "01": // ESTABLISHED
			key := localAddr + "-" + remoteAddr
			connections = append(connections, parsedConnection{
				key:      key,
				pid:      0,
				remoteIP: rIP,
			})
		}
	}
	return listening, connections
}

func parseHexPort(addr string) int {
	parts := strings.Split(addr, ":")
	if len(parts) != 2 {
		return 0
	}
	v, err := strconv.ParseInt(parts[1], 16, 64)
	if err != nil {
		return 0
	}
	return int(v)
}

func parseHexIP(addr string) string {
	parts := strings.Split(addr, ":")
	if len(parts) != 2 {
		return ""
	}
	hexIP := strings.TrimSpace(parts[0])
	if len(hexIP) != 8 {
		return ""
	}
	// /proc/net/tcp stores IPv4 in little-endian byte order.
	blocks := []string{
		hexIP[6:8],
		hexIP[4:6],
		hexIP[2:4],
		hexIP[0:2],
	}
	out := make([]string, 0, 4)
	for _, block := range blocks {
		v, err := strconv.ParseInt(block, 16, 64)
		if err != nil {
			return ""
		}
		out = append(out, strconv.Itoa(int(v)))
	}
	return strings.Join(out, ".")
}

func (c *Collector) ingest(evt wireEvent) {
	now := time.Now().UTC()
	if evt.Timestamp > 0 {
		now = time.Unix(0, evt.Timestamp).UTC()
	}

	details := parseDetails(evt.Details)
	etype := normalizeEventType(evt.Type)
	category := normalizeCategory(evt.Category, etype)

	if !allowedCategory(c.cfg.Categories, category) {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.runtimeMode = "libbpf_ringbuf"
	c.lastEventAt = now

	ps := c.ensureProcessStatsLocked(evt.PID, evt.Comm)
	ps.lastSeen = now
	c.categoryStats[category]++
	if evt.Bytes > 0 {
		c.categoryBytes[category] += evt.Bytes
	}
	if evt.LatencyNs > 0 {
		c.categoryLatNS[category] += evt.LatencyNs
		c.categoryLatCt[category]++
	}
	if evt.PID > 0 {
		classKey := fmt.Sprintf("%d|%s|%s", evt.PID, ps.comm, category)
		c.processClass[classKey]++
	}

	if etype != "" {
		c.syscallStats[etype]++
		if ps.syscalls == nil {
			ps.syscalls = make(map[string]uint64)
		}
		ps.syscalls[etype]++
	}

	var description string
	switch etype {
	case "execve":
		ps.exec++
		description = firstNonEmpty(details["description"], "process executed")
	case "fork":
		ps.fork++
		description = firstNonEmpty(details["description"], "process forked child")
	case "exit":
		ps.exit++
		description = firstNonEmpty(details["description"], "process exited")
	case "open":
		ps.openCalls++
		path := firstNonEmpty(details["path"], details["file"], details["target"])
		if path != "" {
			c.filePatterns[path]++
		}
		description = firstNonEmpty(details["description"], "file open/access observed")
	case "connect":
		ps.connect++
		c.networkCounts["connect"]++
		key := connectionKey(evt.PID, details)
		c.connections[key] = &connectionState{
			key:       key,
			pid:       evt.PID,
			remoteIP:  firstNonEmpty(details["remote_ip"], details["ip"]),
			startedAt: now,
		}
		description = firstNonEmpty(details["description"], "outbound network connect observed")
	case "accept":
		ps.accept++
		c.networkCounts["accept"]++
		description = firstNonEmpty(details["description"], "inbound accept observed")
	case "bind":
		ps.bind++
		c.networkCounts["bind"]++
		port := parseInt(details["port"])
		if port > 0 {
			allowed := isAllowedPort(c.cfg.AllowedListenPorts, port)
			if !allowed {
				c.bindPorts[port]++
			}
		}
		description = firstNonEmpty(details["description"], "bind/listen observed")
	default:
		description = firstNonEmpty(details["description"], "runtime behavior signal observed")
	}

	evtOut := c.newEvent(now, category, etype, evt.PID, evt.Comm, "node", truncateString(description, defaultEventDescriptionMaxLen))
	evtOut.Path = firstNonEmpty(details["path"], details["file"], details["target"])
	evtOut.Port = parseInt(details["port"])
	evtOut.RemoteIP = firstNonEmpty(details["remote_ip"], details["ip"])
	evtOut.Container = firstNonEmpty(details["container"], evt.Cgroup)
	if remoteScope := classifyRemoteScope(evtOut.RemoteIP); remoteScope != "" {
		c.remoteScopes[remoteScope]++
		if evtOut.Metadata == nil {
			evtOut.Metadata = make(map[string]string, 2)
		}
		evtOut.Metadata["remote_scope"] = remoteScope
	}
	if pathScope := classifySensitivePath(evtOut.Path); pathScope != "" {
		c.pathScopes[pathScope]++
		if evtOut.Metadata == nil {
			evtOut.Metadata = make(map[string]string, 2)
		}
		evtOut.Metadata["path_scope"] = pathScope
	}

	uid := parseInt(details["uid"])
	euid := parseInt(details["euid"])
	if uid > 0 && euid == 0 {
		c.privEscalate++
		evtOut.Category = "security"
		evtOut.Type = "privilege_escalation"
		evtOut.Severity = "high"
		evtOut.Confidence = 0.91
		evtOut.Description = "unexpected privilege transition to root"
	}
	if evtOut.Type == "bind" && evtOut.Port > 0 && !isAllowedPort(c.cfg.AllowedListenPorts, evtOut.Port) {
		evtOut.Category = "security"
		evtOut.Type = "abnormal_bind_port"
		evtOut.Severity = "high"
		evtOut.Confidence = 0.88
		evtOut.Description = fmt.Sprintf("abnormal bind port detected: %d", evtOut.Port)
	}

	if ps.cpuUserMS == 0 || ps.rssBytes == 0 {
		// Opportunistically hydrate process resource view for per-process BPF map style
		// aggregates without blocking on every event.
		cpuUserMS, cpuSysMS, rssBytes := readProcessResourceSnapshot(evt.PID)
		ps.cpuUserMS = cpuUserMS
		ps.cpuSysMS = cpuSysMS
		ps.rssBytes = rssBytes
	}

	if ps.cpuUserMS > 0 || ps.cpuSysMS > 0 || ps.rssBytes > 0 {
		resource := c.resourceStats["process"]
		if resource == nil {
			resource = make(map[string]uint64)
			c.resourceStats["process"] = resource
		}
		resource[fmt.Sprintf("pid_%d_cpu_user_ms", evt.PID)] = ps.cpuUserMS
		resource[fmt.Sprintf("pid_%d_cpu_sys_ms", evt.PID)] = ps.cpuSysMS
		resource[fmt.Sprintf("pid_%d_rss_bytes", evt.PID)] = ps.rssBytes
	}

	c.pushEventLocked(evtOut)
}

func readProcessResourceSnapshot(pid int) (uint64, uint64, uint64) {
	if pid <= 0 {
		return 0, 0, 0
	}
	statPath := filepath.Join("/proc", strconv.Itoa(pid), "stat")
	statusPath := filepath.Join("/proc", strconv.Itoa(pid), "status")

	var cpuUser, cpuSys, rss uint64
	if data, err := os.ReadFile(statPath); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) > 24 {
			if v, err := strconv.ParseUint(fields[13], 10, 64); err == nil {
				cpuUser = v
			}
			if v, err := strconv.ParseUint(fields[14], 10, 64); err == nil {
				cpuSys = v
			}
			if v, err := strconv.ParseUint(fields[23], 10, 64); err == nil {
				rss = v * uint64(os.Getpagesize())
			}
		}
	}
	if rss > 0 {
		return cpuUser, cpuSys, rss
	}
	if data, err := os.ReadFile(statusPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, "VmRSS:") {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				if v, err := strconv.ParseUint(parts[1], 10, 64); err == nil {
					rss = v * 1024
				}
			}
			break
		}
	}
	return cpuUser, cpuSys, rss
}

func (c *Collector) ensureProcessStatsLocked(pid int, comm string) *processStats {
	if pid <= 0 {
		pid = -1
	}
	ps := c.processStats[pid]
	if ps == nil {
		ps = &processStats{
			pid:      pid,
			comm:     strings.TrimSpace(comm),
			syscalls: make(map[string]uint64),
		}
		c.processStats[pid] = ps
	}
	if ps.comm == "" && comm != "" {
		ps.comm = strings.TrimSpace(comm)
	}
	return ps
}

func connectionKey(pid int, details map[string]string) string {
	return fmt.Sprintf("%d|%s|%s|%s",
		pid,
		firstNonEmpty(details["src"], details["local"], details["local_ip"]),
		firstNonEmpty(details["dst"], details["remote"], details["remote_ip"], details["ip"]),
		firstNonEmpty(details["port"], details["remote_port"]))
}

func parseDetails(raw string) map[string]string {
	out := make(map[string]string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	splitter := func(r rune) bool {
		return r == ' ' || r == ',' || r == ';' || r == '\t'
	}
	for _, tok := range strings.FieldsFunc(raw, splitter) {
		parts := strings.SplitN(tok, "=", 2)
		if len(parts) != 2 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(parts[0]))
		v := strings.Trim(strings.TrimSpace(parts[1]), "\"")
		if k == "" || v == "" {
			continue
		}
		out[k] = v
	}
	return out
}

func normalizeEventType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "exec", "execve", "sched_process_exec":
		return "execve"
	case "fork", "clone", "sched_process_fork":
		return "fork"
	case "exit", "sched_process_exit":
		return "exit"
	case "open", "openat":
		return "open"
	case "connect", "tcp_connect":
		return "connect"
	case "accept", "accept4":
		return "accept"
	case "bind", "listen_bind":
		return "bind"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func normalizeCategory(raw, eventType string) string {
	if strings.TrimSpace(raw) != "" {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	switch eventType {
	case "execve", "fork", "exit":
		return "process"
	case "open":
		return "file"
	case "connect", "accept", "bind", "long_lived_tcp":
		return "network"
	case "privilege_escalation", "abnormal_bind_port":
		return "security"
	default:
		return "syscall"
	}
}

func allowedCategory(allowed []string, category string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(item), category) {
			return true
		}
	}
	return false
}

func (c *Collector) newEvent(ts time.Time, category, etype string, pid int, comm, scope, desc string) Event {
	id := atomic.AddUint64(&c.seq, 1)
	return Event{
		EvidenceID:  fmt.Sprintf("ev-ebpf-%d-%d", ts.UnixNano(), id),
		Timestamp:   ts,
		Category:    firstNonEmpty(category, "syscall"),
		Type:        firstNonEmpty(etype, "runtime"),
		Scope:       firstNonEmpty(scope, "node"),
		PID:         pid,
		Comm:        strings.TrimSpace(comm),
		Severity:    "medium",
		Confidence:  0.68,
		Description: truncateString(desc, defaultEventDescriptionMaxLen),
	}
}

func (c *Collector) pushEventLocked(evt Event) {
	c.events.Push(evt)
	c.pendingEvents = append(c.pendingEvents, evt)
	if len(c.pendingEvents) > c.cfg.RingSize {
		c.pendingEvents = c.pendingEvents[len(c.pendingEvents)-c.cfg.RingSize:]
	}
}

// RecentEvents returns the latest normalized eBPF events.
func (c *Collector) RecentEvents(limit int) []Event {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	items := c.events.SliceOldest()
	if len(items) == 0 {
		return []Event{}
	}
	out := make([]Event, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp.After(out[j].Timestamp)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Summary returns structured aggregate state.
func (c *Collector) Summary() Summary {
	if c == nil {
		return Summary{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := Summary{
		GeneratedAt:                 time.Now().UTC(),
		RuntimeMode:                 c.runtimeMode,
		EventCount:                  len(c.events.SliceOldest()),
		SyscallStatistics:           cloneUint64Map(c.syscallStats),
		CategoryCounts:              cloneUint64Map(c.categoryStats),
		FileAccessPatterns:          cloneUint64Map(c.filePatterns),
		AbnormalBindPorts:           clonePortMap(c.bindPorts),
		PrivilegeEscalationAttempts: c.privEscalate,
		NetworkCounters:             cloneUint64Map(c.networkCounts),
		RemoteScopeCounts:           cloneUint64Map(c.remoteScopes),
		SensitivePathCounts:         cloneUint64Map(c.pathScopes),
		ResourceStats:               cloneNestedUint64Map(c.resourceStats),
		LongLivedTCPConnections:     make([]map[string]string, 0, len(c.connections)),
	}

	processes := make([]ProcessStatsSnapshot, 0, len(c.processStats))
	for _, p := range c.processStats {
		if p == nil {
			continue
		}
		processes = append(processes, ProcessStatsSnapshot{
			PID:               p.pid,
			Comm:              p.comm,
			LastSeen:          p.lastSeen,
			Syscalls:          cloneUint64Map(p.syscalls),
			OpenCalls:         p.openCalls,
			ConnectCalls:      p.connect,
			AcceptCalls:       p.accept,
			BindCalls:         p.bind,
			ExecCalls:         p.exec,
			ForkCalls:         p.fork,
			ExitCalls:         p.exit,
			ResourceCPUUserMS: p.cpuUserMS,
			ResourceCPUSysMS:  p.cpuSysMS,
			ResourceRSSBytes:  p.rssBytes,
		})
	}
	sort.Slice(processes, func(i, j int) bool {
		return processes[i].PID < processes[j].PID
	})
	out.ProcessStats = processes

	for _, conn := range c.connections {
		if conn == nil || !conn.longLivedEmitted {
			continue
		}
		out.LongLivedTCPConnections = append(out.LongLivedTCPConnections, map[string]string{
			"key":        conn.key,
			"pid":        strconv.Itoa(conn.pid),
			"remote_ip":  conn.remoteIP,
			"started_at": conn.startedAt.Format(time.RFC3339),
		})
	}
	sort.Slice(out.LongLivedTCPConnections, func(i, j int) bool {
		return out.LongLivedTCPConnections[i]["key"] < out.LongLivedTCPConnections[j]["key"]
	})

	return out
}

// MetricSamples returns metrics for telemetry ingestion. This includes
// aggregated counters and a bounded stream of per-event envelopes.
func (c *Collector) MetricSamples(now time.Time) []MetricSample {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	window := now.Sub(c.lastEmitAt)
	if c.lastEmitAt.IsZero() || window <= 0 {
		window = 10 * time.Second
	}

	metrics := make([]MetricSample, 0, 512)
	for syscall, count := range c.syscallStats {
		key := "syscall:" + syscall
		prev := c.lastCounts[key]
		delta := count - prev
		metrics = append(metrics,
			MetricSample{
				Name:   "node_ebpf_syscall_count",
				Type:   "counter",
				Value:  float64(count),
				Labels: map[string]string{"syscall": syscall},
			},
			MetricSample{
				Name:   "node_ebpf_syscall_rate",
				Type:   "gauge",
				Value:  float64(delta) / window.Seconds(),
				Labels: map[string]string{"syscall": syscall},
			},
		)
		c.lastCounts[key] = count
	}

	for category, count := range c.categoryStats {
		key := "category:" + category
		prev := c.lastCounts[key]
		delta := count - prev
		labels := map[string]string{"category": category}
		metrics = append(metrics,
			MetricSample{Name: "node_ebpf_category_events_total", Type: "counter", Value: float64(count), Labels: labels},
			MetricSample{Name: "node_ebpf_category_events_rate", Type: "gauge", Value: float64(delta) / window.Seconds(), Labels: labels},
		)
		c.lastCounts[key] = count

		if totalBytes, ok := c.categoryBytes[category]; ok {
			bytesKey := "category_bytes:" + category
			prevBytes := c.lastCounts[bytesKey]
			metrics = append(metrics,
				MetricSample{Name: "node_ebpf_category_bytes_total", Type: "counter", Value: float64(totalBytes), Labels: labels},
				MetricSample{Name: "node_ebpf_category_bytes_per_second", Type: "gauge", Value: float64(totalBytes-prevBytes) / window.Seconds(), Labels: labels},
			)
			c.lastCounts[bytesKey] = totalBytes
		}
		if latCount := c.categoryLatCt[category]; latCount > 0 {
			metrics = append(metrics, MetricSample{
				Name:   "node_ebpf_category_latency_seconds_avg",
				Type:   "gauge",
				Value:  (float64(c.categoryLatNS[category]) / float64(latCount)) / 1e9,
				Labels: labels,
			})
		}
	}

	for scope, count := range c.remoteScopes {
		key := "remote_scope:" + scope
		prev := c.lastCounts[key]
		delta := count - prev
		labels := map[string]string{"scope": scope}
		metrics = append(metrics,
			MetricSample{Name: "node_ebpf_remote_scope_events_total", Type: "counter", Value: float64(count), Labels: labels},
			MetricSample{Name: "node_ebpf_remote_scope_events_rate", Type: "gauge", Value: float64(delta) / window.Seconds(), Labels: labels},
		)
		c.lastCounts[key] = count
	}

	for scope, count := range c.pathScopes {
		key := "path_scope:" + scope
		prev := c.lastCounts[key]
		delta := count - prev
		labels := map[string]string{"scope": scope}
		metrics = append(metrics,
			MetricSample{Name: "node_ebpf_sensitive_path_events_total", Type: "counter", Value: float64(count), Labels: labels},
			MetricSample{Name: "node_ebpf_sensitive_path_events_rate", Type: "gauge", Value: float64(delta) / window.Seconds(), Labels: labels},
		)
		c.lastCounts[key] = count
	}

	type processClassMetric struct {
		pid      string
		process  string
		category string
		count    uint64
	}
	processClasses := make([]processClassMetric, 0, len(c.processClass))
	for key, count := range c.processClass {
		parts := strings.SplitN(key, "|", 3)
		if len(parts) != 3 {
			continue
		}
		processClasses = append(processClasses, processClassMetric{
			pid:      parts[0],
			process:  parts[1],
			category: parts[2],
			count:    count,
		})
	}
	sort.Slice(processClasses, func(i, j int) bool {
		if processClasses[i].count == processClasses[j].count {
			if processClasses[i].pid == processClasses[j].pid {
				return processClasses[i].category < processClasses[j].category
			}
			return processClasses[i].pid < processClasses[j].pid
		}
		return processClasses[i].count > processClasses[j].count
	})
	if len(processClasses) > 16 {
		processClasses = processClasses[:16]
	}
	for _, item := range processClasses {
		key := "process_class:" + item.pid + "|" + item.category
		prev := c.lastCounts[key]
		delta := item.count - prev
		labels := map[string]string{
			"pid":      item.pid,
			"process":  item.process,
			"category": item.category,
		}
		metrics = append(metrics,
			MetricSample{Name: "node_ebpf_process_category_events_total", Type: "counter", Value: float64(item.count), Labels: labels},
			MetricSample{Name: "node_ebpf_process_category_events_rate", Type: "gauge", Value: float64(delta) / window.Seconds(), Labels: labels},
		)
		c.lastCounts[key] = item.count
	}

	for _, ps := range c.processStats {
		if ps == nil {
			continue
		}
		labels := map[string]string{
			"pid":     strconv.Itoa(ps.pid),
			"process": ps.comm,
		}
		for syscall, count := range ps.syscalls {
			metrics = append(metrics, MetricSample{
				Name:   "node_ebpf_process_events_total",
				Type:   "counter",
				Value:  float64(count),
				Labels: mergeLabels(labels, map[string]string{"type": syscall}),
			})
		}
		metrics = append(metrics,
			MetricSample{Name: "node_ebpf_process_cpu_user_ms", Type: "gauge", Value: float64(ps.cpuUserMS), Labels: labels},
			MetricSample{Name: "node_ebpf_process_cpu_sys_ms", Type: "gauge", Value: float64(ps.cpuSysMS), Labels: labels},
			MetricSample{Name: "node_ebpf_process_rss_bytes", Type: "gauge", Value: float64(ps.rssBytes), Labels: labels},
		)
	}

	metrics = append(metrics,
		MetricSample{Name: "node_ebpf_privilege_escalation_attempts_total", Type: "counter", Value: float64(c.privEscalate)},
		MetricSample{Name: "node_ebpf_abnormal_bind_ports_count", Type: "gauge", Value: float64(len(c.bindPorts))},
		MetricSample{Name: "node_ebpf_long_lived_tcp_connections", Type: "gauge", Value: float64(countLongLived(c.connections))},
	)

	flushCount := c.cfg.EventFlushLimit
	if flushCount <= 0 {
		flushCount = defaultEventFlushLimit
	}
	if len(c.pendingEvents) > 0 {
		start := 0
		if len(c.pendingEvents) > flushCount {
			start = len(c.pendingEvents) - flushCount
		}
		events := c.pendingEvents[start:]
		for _, evt := range events {
			labels := map[string]string{
				"evidence_id":  evt.EvidenceID,
				"category":     evt.Category,
				"type":         evt.Type,
				"scope":        evt.Scope,
				"severity":     evt.Severity,
				"confidence":   fmt.Sprintf("%.2f", evt.Confidence),
				"pid":          strconv.Itoa(evt.PID),
				"comm":         evt.Comm,
				"node":         evt.Node,
				"container":    evt.Container,
				"path":         evt.Path,
				"port":         strconv.Itoa(evt.Port),
				"remote_ip":    evt.RemoteIP,
				"description":  truncateString(evt.Description, 160),
				"ts_unix_nano": strconv.FormatInt(evt.Timestamp.UnixNano(), 10),
			}
			if evt.Metadata != nil {
				if remoteScope := strings.TrimSpace(evt.Metadata["remote_scope"]); remoteScope != "" {
					labels["remote_scope"] = remoteScope
				}
				if pathScope := strings.TrimSpace(evt.Metadata["path_scope"]); pathScope != "" {
					labels["path_scope"] = pathScope
				}
			}
			metrics = append(metrics, MetricSample{
				Name:   "node_ebpf_runtime_event",
				Type:   "counter",
				Value:  1,
				Labels: compactLabels(labels),
			})
		}
		c.pendingEvents = c.pendingEvents[:0]
	}
	c.lastEmitAt = now
	return metrics
}

func countLongLived(m map[string]*connectionState) int {
	n := 0
	for _, item := range m {
		if item != nil && item.longLivedEmitted {
			n++
		}
	}
	return n
}

func classifyRemoteScope(raw string) string {
	ip := net.ParseIP(strings.TrimSpace(raw))
	if ip == nil {
		return ""
	}
	switch {
	case ip.IsLoopback():
		return "loopback"
	case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast():
		return "linklocal"
	case ip.IsPrivate():
		return "private"
	case ip.IsMulticast():
		return "multicast"
	case ip.IsUnspecified():
		return "unspecified"
	default:
		return "public"
	}
}

func classifySensitivePath(raw string) string {
	path := strings.TrimSpace(raw)
	if path == "" {
		return ""
	}
	path = filepath.Clean(path)
	switch {
	case path == "/etc/shadow", path == "/etc/passwd", path == "/etc/sudoers",
		strings.HasPrefix(path, "/etc/sudoers.d/"):
		return "auth_db"
	case path == "/etc/ld.so.preload":
		return "ld_preload"
	case path == "/var/run/docker.sock", path == "/run/docker.sock":
		return "docker_sock"
	case strings.HasPrefix(path, "/root/.ssh"), strings.Contains(path, "/.ssh/"):
		return "ssh"
	case strings.HasSuffix(path, "/.kube/config"), strings.HasPrefix(path, "/etc/kubernetes"):
		return "kubeconfig"
	case strings.HasPrefix(path, "/etc/cron"), strings.HasPrefix(path, "/var/spool/cron"):
		return "cron"
	case strings.HasPrefix(path, "/etc/systemd"), strings.HasPrefix(path, "/usr/lib/systemd"),
		strings.HasPrefix(path, "/lib/systemd"):
		return "systemd"
	case strings.HasPrefix(path, "/proc/sys"), strings.HasPrefix(path, "/etc/sysctl"),
		strings.HasPrefix(path, "/etc/selinux"), strings.HasPrefix(path, "/etc/apparmor"),
		strings.HasPrefix(path, "/etc/firewalld"):
		return "kernel_posture"
	case strings.HasPrefix(path, "/lib/modules"), strings.HasPrefix(path, "/usr/lib/modules"):
		return "kernel_modules"
	case strings.HasPrefix(path, "/tmp"), strings.HasPrefix(path, "/var/tmp"),
		strings.HasPrefix(path, "/dev/shm"):
		return "tmp_exec"
	default:
		return ""
	}
}

func isAllowedPort(allowed []int, port int) bool {
	for _, candidate := range allowed {
		if candidate == port {
			return true
		}
	}
	return false
}

func parseInt(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	v, err := strconv.Atoi(raw)
	if err == nil {
		return v
	}
	v64, err := strconv.ParseInt(raw, 10, 64)
	if err == nil {
		return int(v64)
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func truncateString(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return strings.TrimSpace(value[:max])
}

func cloneUint64Map(src map[string]uint64) map[string]uint64 {
	if len(src) == 0 {
		return map[string]uint64{}
	}
	out := make(map[string]uint64, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func clonePortMap(src map[int]uint64) map[int]uint64 {
	if len(src) == 0 {
		return map[int]uint64{}
	}
	out := make(map[int]uint64, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func cloneNestedUint64Map(src map[string]map[string]uint64) map[string]map[string]uint64 {
	if len(src) == 0 {
		return map[string]map[string]uint64{}
	}
	out := make(map[string]map[string]uint64, len(src))
	for k, v := range src {
		out[k] = cloneUint64Map(v)
	}
	return out
}

func mergeLabels(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return compactLabels(out)
}

func compactLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		key := truncateString(strings.TrimSpace(k), defaultLabelKeyMaxLen)
		val := truncateString(strings.TrimSpace(v), defaultLabelValueMaxLen)
		if key == "" || val == "" {
			continue
		}
		out[key] = val
	}
	return out
}
