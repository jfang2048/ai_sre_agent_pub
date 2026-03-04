package linux

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"go.uber.org/zap"
	"golang.org/x/sys/unix"
)

// KernelEventCollector collects kernel events using tracepoints and netlink
type KernelEventCollector struct {
	config *KernelEventsConfig
	logger *zap.Logger

	// Tracepoint file descriptors
	tracepointFds map[string]int

	// Netlink socket
	netlinkSock int

	// Event channels
	eventCh chan KernelEvent

	mu      sync.RWMutex
	running bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// KernelEventsConfig configures kernel event collection
type KernelEventsConfig struct {
	Enabled        bool          `yaml:"enabled"`
	SampleInterval time.Duration `yaml:"sample_interval"`

	// Tracepoints to enable
	EnableSyscalls  bool `yaml:"enable_syscalls"`
	EnableScheduler bool `yaml:"enable_scheduler"`
	EnableSignal    bool `yaml:"enable_signal"`
	EnableNet       bool `yaml:"enable_net"`

	// Netlink events
	EnableNetlink bool   `yaml:"enable_netlink"`
	NetlinkGroups uint32 `yaml:"netlink_groups"`

	// Event filtering
	FilterByPID  bool   `yaml:"filter_by_pid"`
	TargetPID    int    `yaml:"target_pid"`
	FilterByComm bool   `yaml:"filter_by_comm"`
	TargetComm   string `yaml:"target_comm"`
}

// KernelEvent represents a kernel event
type KernelEvent struct {
	Timestamp time.Time              `json:"timestamp"`
	Type      string                 `json:"type"`
	CPU       int                    `json:"cpu"`
	PID       int                    `json:"pid"`
	TID       int                    `json:"tid"`
	Comm      string                 `json:"comm"`
	Data      map[string]interface{} `json:"data"`
	RawData   []byte                 `json:"-"`
}

// TracepointConfig holds tracepoint configuration
type TracepointConfig struct {
	Subsystem string
	Name      string
	ID        int
}

const (
	// Tracepoint categories
	TRACEPOINT_SYSCALL_ENTER  = "syscalls:sys_enter"
	TRACEPOINT_SYSCALL_EXIT   = "syscalls:sys_exit"
	TRACEPOINT_SCHED_SWITCH   = "sched:sched_switch"
	TRACEPOINT_SCHED_WAKEUP   = "sched:sched_wakeup"
	TRACEPOINT_SIGNAL_DELIVER = "signal:signal_generate"
	TRACEPOINT_NET_XMIT       = "net:net_dev_xmit"

	// Netlink groups
	NETLINK_GROUP_LINK  = 1  // Link configuration
	NETLINK_GROUP_IPV4  = 2  // IPv4
	NETLINK_GROUP_IPV6  = 4  // IPv6
	NETLINK_GROUP_ROUTE = 8  // Routing
	NETLINK_GROUP_NEIGH = 16 // Neighbour
)

// perf event attr bits for tracepoints
const (
	PERF_TYPE_TRACEPOINT  = 2
	PERF_SAMPLE_IP        = 1 << 0
	PERF_SAMPLE_TID       = 1 << 1
	PERF_SAMPLE_TIME      = 1 << 2
	PERF_SAMPLE_ADDR      = 1 << 3
	PERF_SAMPLE_READ      = 1 << 4
	PERF_SAMPLE_CALLCHAIN = 1 << 5
	PERF_SAMPLE_ID        = 1 << 6
	PERF_SAMPLE_CPU       = 1 << 7
	PERF_SAMPLE_PERIOD    = 1 << 8
	PERF_SAMPLE_STREAM_ID = 1 << 9
	PERF_SAMPLE_RAW       = 1 << 10
	PERF_FORMAT_ID        = 1 << 3
	PERF_FORMAT_GROUP     = 1 << 4
)

// NewKernelEventCollector creates a new kernel event collector
func NewKernelEventCollector(config *KernelEventsConfig, logger *zap.Logger) (*KernelEventCollector, error) {
	if !config.Enabled {
		return nil, fmt.Errorf("kernel event collector is disabled")
	}

	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("kernel event collector requires Linux")
	}

	return &KernelEventCollector{
		config:        config,
		logger:        logger.With(zap.String("collector", "kernel_events")),
		tracepointFds: make(map[string]int),
		eventCh:       make(chan KernelEvent, 1000),
		running:       false,
	}, nil
}

// Start starts the kernel event collector
func (kc *KernelEventCollector) Start(ctx context.Context) error {
	kc.mu.Lock()
	defer kc.mu.Unlock()

	if kc.running {
		return nil
	}

	ctx, kc.cancel = context.WithCancel(ctx)

	// Initialize tracepoints
	if err := kc.setupTracepoints(); err != nil {
		kc.logger.Warn("failed to setup tracepoints", zap.Error(err))
	}

	// Initialize netlink
	if kc.config.EnableNetlink {
		if err := kc.setupNetlink(); err != nil {
			kc.logger.Warn("failed to setup netlink", zap.Error(err))
		}
	}

	// Start event readers
	kc.wg.Add(2)
	go kc.readTracepointEvents(ctx)
	go kc.readNetlinkEvents(ctx)

	kc.running = true
	kc.logger.Info("kernel event collector started")

	return nil
}

// Stop stops the kernel event collector
func (kc *KernelEventCollector) Stop() error {
	kc.mu.Lock()
	defer kc.mu.Unlock()

	if !kc.running {
		return nil
	}

	if kc.cancel != nil {
		kc.cancel()
	}

	kc.wg.Wait()

	// Close tracepoint fds
	for name, fd := range kc.tracepointFds {
		unix.Close(fd)
		kc.logger.Debug("closed tracepoint", zap.String("tracepoint", name))
	}

	// Close netlink socket
	if kc.netlinkSock > 0 {
		unix.Close(kc.netlinkSock)
	}

	close(kc.eventCh)
	kc.running = false
	kc.logger.Info("kernel event collector stopped")

	return nil
}

// Events returns the kernel event channel
func (kc *KernelEventCollector) Events() <-chan KernelEvent {
	return kc.eventCh
}

// setupTracepoints sets up perf event for tracepoints
func (kc *KernelEventCollector) setupTracepoints() error {
	tracepoints := []struct {
		name    string
		enabled bool
	}{
		{TRACEPOINT_SCHED_SWITCH, kc.config.EnableScheduler},
		{TRACEPOINT_SCHED_WAKEUP, kc.config.EnableScheduler},
		{TRACEPOINT_SIGNAL_DELIVER, kc.config.EnableSignal},
	}

	for _, tp := range tracepoints {
		if !tp.enabled {
			continue
		}

		// Get tracepoint ID from /sys/kernel/debug/tracing/events
		id, err := kc.getTracepointID(tp.name)
		if err != nil {
			kc.logger.Debug("failed to get tracepoint ID",
				zap.String("tracepoint", tp.name),
				zap.Error(err))
			continue
		}

		fd, err := kc.openTracepoint(id)
		if err != nil {
			kc.logger.Debug("failed to open tracepoint",
				zap.String("tracepoint", tp.name),
				zap.Error(err))
			continue
		}

		kc.tracepointFds[tp.name] = fd
		kc.logger.Debug("opened tracepoint", zap.String("tracepoint", tp.name), zap.Int("id", id))
	}

	return nil
}

// getTracepointID reads the tracepoint ID from debugfs
func (kc *KernelEventCollector) getTracepointID(tracepoint string) (int, error) {
	parts := bytes.Split([]byte(tracepoint), []byte(":"))
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid tracepoint format: %s", tracepoint)
	}

	subsystem := string(parts[0])
	name := string(parts[1])

	idPath := fmt.Sprintf("/sys/kernel/debug/tracing/events/%s/%s/id", subsystem, name)
	data, err := os.ReadFile(idPath)
	if err != nil {
		return 0, fmt.Errorf("failed to read tracepoint ID: %w", err)
	}

	var id int
	_, err = fmt.Sscanf(string(data), "%d", &id)
	if err != nil {
		return 0, fmt.Errorf("failed to parse tracepoint ID: %w", err)
	}

	return id, nil
}

// openTracepoint opens a perf event for a tracepoint
func (kc *KernelEventCollector) openTracepoint(tracepointID int) (int, error) {
	attr := perfAttr{
		Type:   PERF_TYPE_TRACEPOINT,
		Size:   uint32(unsafe.Sizeof(perfAttr{})),
		Config: uint64(tracepointID),
		Sample: PERF_SAMPLE_RAW | PERF_SAMPLE_TIME | PERF_SAMPLE_CPU | PERF_SAMPLE_TID,
		Flags:  0,
	}

	attr.Flags |= perfDisabled | perfAttrInherit

	fd, _, errno := unix.Syscall6(
		298, // __NR_perf_event_open
		uintptr(unsafe.Pointer(&attr)),
		^uintptr(0), // pid = -1 (all pids)
		^uintptr(0), // cpu = -1 (any cpu)
		uintptr(unsafe.Pointer(&attr.Size)),
		0, // flags
		0, // signal fd
	)

	if errno != 0 {
		return -1, fmt.Errorf("perf_event_open failed for tracepoint %d: %d", tracepointID, errno)
	}

	// Enable the event
	_, _, errno = unix.Syscall6(
		294, // __NR_ioctl
		fd,
		0x2401, // PERF_EVENT_IOC_ENABLE
		0,
		0,
		0,
		0,
	)

	if errno != 0 {
		unix.Close(int(fd))
		return -1, fmt.Errorf("ioctl enable failed: %d", errno)
	}

	return int(fd), nil
}

// setupNetlink sets up netlink socket for kernel events
func (kc *KernelEventCollector) setupNetlink() error {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW, unix.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		return fmt.Errorf("failed to create netlink socket: %w", err)
	}

	// Bind to netlink socket
	addr := &unix.SockaddrNetlink{
		Family: unix.AF_NETLINK,
		Groups: kc.config.NetlinkGroups,
		Pid:    uint32(os.Getpid()),
	}

	if err := unix.Bind(fd, addr); err != nil {
		unix.Close(fd)
		return fmt.Errorf("failed to bind netlink socket: %w", err)
	}

	kc.netlinkSock = fd
	kc.logger.Debug("netlink socket created", zap.Int("fd", fd))

	return nil
}

// readTracepointEvents reads events from tracepoint perf fds
func (kc *KernelEventCollector) readTracepointEvents(ctx context.Context) {
	defer kc.wg.Done()

	buf := make([]byte, 4096)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		for name, fd := range kc.tracepointFds {
			n, err := unix.Read(fd, buf)
			if err != nil {
				if err == unix.EAGAIN || err == unix.EINTR {
					continue
				}
				kc.logger.Error("error reading tracepoint",
					zap.String("tracepoint", name),
					zap.Error(err))
				continue
			}

			if n > 0 {
				event := kc.parseTracepointEvent(buf[:n], name)
				if event != nil && kc.shouldIncludeEvent(event) {
					select {
					case kc.eventCh <- *event:
					default:
						kc.logger.Warn("kernel event channel full, dropping event")
					}
				}
			}
		}

		time.Sleep(100 * time.Millisecond)
	}
}

// readNetlinkEvents reads events from netlink socket
func (kc *KernelEventCollector) readNetlinkEvents(ctx context.Context) {
	defer kc.wg.Done()

	if kc.netlinkSock <= 0 {
		return
	}

	buf := make([]byte, os.Getpagesize())

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, _, err := unix.Recvfrom(kc.netlinkSock, buf, 0)
		if err != nil {
			if err == unix.EAGAIN || err == unix.EINTR {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			kc.logger.Error("error reading netlink", zap.Error(err))
			continue
		}

		if n > 0 {
			event := kc.parseNetlinkEvent(buf[:n])
			if event != nil {
				select {
				case kc.eventCh <- *event:
				default:
					kc.logger.Warn("kernel event channel full, dropping netlink event")
				}
			}
		}
	}
}

// parseTracepointEvent parses a tracepoint event from perf data
func (kc *KernelEventCollector) parseTracepointEvent(data []byte, tracepointName string) *KernelEvent {
	if len(data) < 24 {
		return nil
	}

	// Parse perf event header
	// struct perf_event_header {
	//     uint32_t type;
	//     uint16_t misc;
	//     uint16_t size;
	// };
	header := &struct {
		Type uint32
		Misc uint16
		Size uint16
	}{}

	err := binary.Read(bytes.NewReader(data), binary.LittleEndian, header)
	if err != nil {
		return nil
	}

	event := &KernelEvent{
		Timestamp: time.Now(),
		Type:      tracepointName,
		Data:      make(map[string]interface{}),
	}

	// Extract basic info (simplified - real implementation would parse perf_sample_data)
	offset := int(header.Size)

	if len(data) > offset+16 {
		// Try to extract TID from raw data
		event.PID = int(binary.LittleEndian.Uint32(data[offset : offset+4]))
		event.TID = int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		event.CPU = int(binary.LittleEndian.Uint32(data[offset+12 : offset+16]))
	}

	// Store raw data for further parsing
	event.RawData = data

	return event
}

// parseNetlinkEvent parses a netlink event
func (kc *KernelEventCollector) parseNetlinkEvent(data []byte) *KernelEvent {
	if len(data) < 16 {
		return nil
	}

	// Parse netlink header
	nlh := &struct {
		Len   uint32
		Type  uint16
		Flags uint16
		Seq   uint32
		Pid   uint32
	}{}

	err := binary.Read(bytes.NewReader(data), binary.LittleEndian, nlh)
	if err != nil {
		return nil
	}

	event := &KernelEvent{
		Timestamp: time.Now(),
		Type:      "netlink",
		CPU:       -1,
		Data:      make(map[string]interface{}),
	}

	event.Data["nlmsg_type"] = nlh.Type
	event.Data["nlmsg_flags"] = nlh.Flags
	event.Data["nlmsg_seq"] = nlh.Seq

	// Parse payload if available
	if nlh.Len > 16 && len(data) >= int(nlh.Len) {
		payload := data[16:nlh.Len]
		event.Data["payload"] = string(payload)
	}

	return event
}

// shouldIncludeEvent filters events based on configuration
func (kc *KernelEventCollector) shouldIncludeEvent(event *KernelEvent) bool {
	if kc.config.FilterByPID && kc.config.TargetPID > 0 {
		if event.PID != kc.config.TargetPID {
			return false
		}
	}

	if kc.config.FilterByComm && kc.config.TargetComm != "" {
		if event.Comm != kc.config.TargetComm {
			return false
		}
	}

	return true
}

// GetEventStats returns statistics about collected events
func (kc *KernelEventCollector) GetEventStats() map[string]int64 {
	kc.mu.RLock()
	defer kc.mu.RUnlock()

	stats := make(map[string]int64)
	stats["tracepoints"] = int64(len(kc.tracepointFds))
	stats["netlink_enabled"] = 0
	if kc.netlinkSock > 0 {
		stats["netlink_enabled"] = 1
	}

	return stats
}

// PerfEventType represents a perf event type
type PerfEventType string

const (
	PerfEventTypeMmap       PerfEventType = "mmap"
	PerfEventTypeLost       PerfEventType = "lost"
	PerfEventTypeComm       PerfEventType = "comm"
	PerfEventTypeExit       PerfEventType = "exit"
	PerfEventTypeThrottle   PerfEventType = "throttle"
	PerfEventTypeUnthrottle PerfEventType = "unthrottle"
	PerfEventTypeFork       PerfEventType = "fork"
	PerfEventTypeRead       PerfEventType = "read"
	PerfEventTypeSample     PerfEventType = "sample"
	PerfEventTypeMax        PerfEventType = "max"
)
