// Package probe implements network root cause analysis collection.
// This provides process-level and connection-level network attribution for diagnosing network issues.
package probe

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NetworkProcessInfo holds detailed network information for a process
type NetworkProcessInfo struct {
	PID         int
	Name        string
	RxBytes     uint64
	TxBytes     uint64
	RxPackets   uint64
	TxPackets   uint64
	Connections []ConnectionInfo
	ListenPorts []int
	RxBytesRate float64
	TxBytesRate float64
}

// ConnectionInfo holds information about a network connection
type ConnectionInfo struct {
	Protocol    string // tcp, tcp6, udp, udp6
	LocalAddr   string
	LocalPort   int
	RemoteAddr  string
	RemotePort  int
	State       string
	TxQueue     int
	RxQueue     int
	Inode       uint64
	UID         int
	PID         int
	ProcessName string
}

// NetworkInterfaceInfo holds detailed network information for an interface
type NetworkInterfaceInfo struct {
	Interface   string
	RxBytes     uint64
	TxBytes     uint64
	RxPackets   uint64
	TxPackets   uint64
	RxErrors    uint64
	TxErrors    uint64
	RxDropped   uint64
	TxDropped   uint64
	RxBytesRate float64
	TxBytesRate float64
	Utilization float64 // if link speed known
}

// NetworkRootCauseCollector collects detailed network attribution data
type NetworkRootCauseCollector struct {
	mu sync.Mutex

	// Previous state for rate calculations
	lastProcessNet   map[int]*NetworkProcessInfo
	lastInterfaceNet map[string]*NetworkInterfaceInfo
	lastCollect      time.Time

	// Inode to PID mapping cache
	inodeToPID map[uint64]int

	// Configuration
	topN           int
	topConnections int
}

// NewNetworkRootCauseCollector creates a new network RCA collector
func NewNetworkRootCauseCollector(topN, topConnections int) *NetworkRootCauseCollector {
	if topN <= 0 {
		topN = 20
	}
	if topConnections <= 0 {
		topConnections = 10
	}
	return &NetworkRootCauseCollector{
		lastProcessNet:   make(map[int]*NetworkProcessInfo),
		lastInterfaceNet: make(map[string]*NetworkInterfaceInfo),
		inodeToPID:       make(map[uint64]int),
		topN:             topN,
		topConnections:   topConnections,
	}
}

// Collect gathers network root cause metrics
func (c *NetworkRootCauseCollector) Collect(now time.Time) ([]Metric, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elapsed := now.Sub(c.lastCollect).Seconds()
	if elapsed <= 0 {
		elapsed = 1
	}

	var metrics []Metric

	// Build inode to PID mapping
	c.buildInodePIDMap()

	// Collect interface-level metrics
	interfaceMetrics := c.collectInterfaceMetrics(now, elapsed)
	metrics = append(metrics, interfaceMetrics...)

	// Collect connection-level metrics
	connectionMetrics := c.collectConnectionMetrics(now)
	metrics = append(metrics, connectionMetrics...)

	// Update state
	c.lastCollect = now

	return metrics, nil
}

func (c *NetworkRootCauseCollector) collectInterfaceMetrics(now time.Time, elapsed float64) []Metric {
	var metrics []Metric

	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return metrics
	}
	defer f.Close()

	newInterfaceNet := make(map[string]*NetworkInterfaceInfo)

	scanner := bufio.NewScanner(f)
	lineIdx := 0
	for scanner.Scan() {
		line := scanner.Text()
		if lineIdx < 2 {
			lineIdx++
			continue
		}
		lineIdx++

		parts := strings.Split(line, ":")
		if len(parts) < 2 {
			continue
		}

		iface := strings.TrimSpace(parts[0])
		if iface == "lo" {
			continue
		}

		fields := strings.Fields(parts[1])
		if len(fields) < 16 {
			continue
		}

		info := &NetworkInterfaceInfo{Interface: iface}
		info.RxBytes, _ = strconv.ParseUint(fields[0], 10, 64)
		info.RxPackets, _ = strconv.ParseUint(fields[1], 10, 64)
		info.RxErrors, _ = strconv.ParseUint(fields[2], 10, 64)
		info.RxDropped, _ = strconv.ParseUint(fields[3], 10, 64)
		info.TxBytes, _ = strconv.ParseUint(fields[8], 10, 64)
		info.TxPackets, _ = strconv.ParseUint(fields[9], 10, 64)
		info.TxErrors, _ = strconv.ParseUint(fields[10], 10, 64)
		info.TxDropped, _ = strconv.ParseUint(fields[11], 10, 64)

		// Calculate rates
		if prev, ok := c.lastInterfaceNet[iface]; ok {
			rxDelta := info.RxBytes - prev.RxBytes
			txDelta := info.TxBytes - prev.TxBytes
			info.RxBytesRate = float64(rxDelta) / elapsed
			info.TxBytesRate = float64(txDelta) / elapsed

			if info.RxBytesRate < 0 {
				info.RxBytesRate = 0
			}
			if info.TxBytesRate < 0 {
				info.TxBytesRate = 0
			}

			// Try to calculate utilization if speed is known
			speed := c.getInterfaceSpeed(iface)
			if speed > 0 {
				totalBitsRate := (info.RxBytesRate + info.TxBytesRate) * 8
				info.Utilization = (totalBitsRate / float64(speed)) * 100.0
				if info.Utilization > 100 {
					info.Utilization = 100
				}
			}
		}

		newInterfaceNet[iface] = info

		// Emit metrics
		labels := map[string]string{"interface": iface}

		metrics = append(metrics, Metric{
			Name:      "rca_net_interface_rx_bytes_total",
			Type:      "counter",
			Value:     float64(info.RxBytes),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_net_interface_tx_bytes_total",
			Type:      "counter",
			Value:     float64(info.TxBytes),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_net_interface_rx_bytes_per_second",
			Type:      "gauge",
			Value:     info.RxBytesRate,
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_net_interface_tx_bytes_per_second",
			Type:      "gauge",
			Value:     info.TxBytesRate,
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_net_interface_rx_errors_total",
			Type:      "counter",
			Value:     float64(info.RxErrors),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_net_interface_tx_errors_total",
			Type:      "counter",
			Value:     float64(info.TxErrors),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_net_interface_rx_dropped_total",
			Type:      "counter",
			Value:     float64(info.RxDropped),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_net_interface_tx_dropped_total",
			Type:      "counter",
			Value:     float64(info.TxDropped),
			Labels:    labels,
			Timestamp: now,
		})

		if info.Utilization > 0 {
			metrics = append(metrics, Metric{
				Name:      "rca_net_interface_utilization_percent",
				Type:      "gauge",
				Value:     info.Utilization,
				Labels:    labels,
				Timestamp: now,
			})
		}
	}

	c.lastInterfaceNet = newInterfaceNet

	return metrics
}

func (c *NetworkRootCauseCollector) collectConnectionMetrics(now time.Time) []Metric {
	var metrics []Metric

	// Collect TCP connections
	tcpConns := c.parseNetTCP("/proc/net/tcp", "tcp")
	tcp6Conns := c.parseNetTCP("/proc/net/tcp6", "tcp6")

	allConns := append(tcpConns, tcp6Conns...)

	// Aggregate by process
	processConns := make(map[int][]ConnectionInfo)
	for _, conn := range allConns {
		processConns[conn.PID] = append(processConns[conn.PID], conn)
	}

	// Sort processes by number of connections
	type procConnCount struct {
		PID   int
		Count int
		Conns []ConnectionInfo
	}
	var procs []procConnCount
	for pid, conns := range processConns {
		if pid > 0 {
			procs = append(procs, procConnCount{
				PID:   pid,
				Count: len(conns),
				Conns: conns,
			})
		}
	}

	sort.Slice(procs, func(i, j int) bool {
		return procs[i].Count > procs[j].Count
	})

	// Emit metrics for top processes
	count := c.topN
	if count > len(procs) {
		count = len(procs)
	}

	for _, p := range procs[:count] {
		if len(p.Conns) == 0 {
			continue
		}

		name := p.Conns[0].ProcessName
		labels := map[string]string{
			"pid":  fmt.Sprintf("%d", p.PID),
			"name": sanitizeProcName(name),
		}
		labels = applyProcessContextLabels(labels, buildProcessContext(p.PID, name))

		// Count connections by state
		stateCounts := make(map[string]int)
		var totalQueued int
		for _, conn := range p.Conns {
			stateCounts[conn.State]++
			totalQueued += conn.RxQueue + conn.TxQueue
		}

		metrics = append(metrics, Metric{
			Name:      "rca_net_process_connections",
			Type:      "gauge",
			Value:     float64(len(p.Conns)),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_net_process_queued_bytes",
			Type:      "gauge",
			Value:     float64(totalQueued),
			Labels:    labels,
			Timestamp: now,
		})

		// Emit by state
		for state, cnt := range stateCounts {
			stateLabels := copyLabels(labels)
			stateLabels["state"] = state
			metrics = append(metrics, Metric{
				Name:      "rca_net_process_connections_by_state",
				Type:      "gauge",
				Value:     float64(cnt),
				Labels:    stateLabels,
				Timestamp: now,
			})
		}

		// Top connections for this process
		// Sort by queue size
		sort.Slice(p.Conns, func(i, j int) bool {
			return (p.Conns[i].RxQueue + p.Conns[i].TxQueue) > (p.Conns[j].RxQueue + p.Conns[j].TxQueue)
		})

		connCount := c.topConnections
		if connCount > len(p.Conns) {
			connCount = len(p.Conns)
		}

		for _, conn := range p.Conns[:connCount] {
			connLabels := copyLabels(labels)
			connLabels["protocol"] = conn.Protocol
			connLabels["local"] = fmt.Sprintf("%s:%d", conn.LocalAddr, conn.LocalPort)
			connLabels["remote"] = fmt.Sprintf("%s:%d", conn.RemoteAddr, conn.RemotePort)
			connLabels["state"] = conn.State

			metrics = append(metrics, Metric{
				Name:      "rca_net_connection_queue_bytes",
				Type:      "gauge",
				Value:     float64(conn.RxQueue + conn.TxQueue),
				Labels:    connLabels,
				Timestamp: now,
			})
		}
	}

	// Overall connection state summary
	allStateCounts := make(map[string]int)
	for _, conn := range allConns {
		allStateCounts[conn.State]++
	}

	for state, cnt := range allStateCounts {
		metrics = append(metrics, Metric{
			Name:      "rca_net_connections_by_state",
			Type:      "gauge",
			Value:     float64(cnt),
			Labels:    map[string]string{"state": state},
			Timestamp: now,
		})
	}

	return metrics
}

func (c *NetworkRootCauseCollector) parseNetTCP(path, protocol string) []ConnectionInfo {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var conns []ConnectionInfo

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum == 1 {
			continue // Skip header
		}

		fields := strings.Fields(scanner.Text())
		if len(fields) < 12 {
			continue
		}

		conn := ConnectionInfo{Protocol: protocol}

		// Parse local address
		localParts := strings.Split(fields[1], ":")
		if len(localParts) == 2 {
			conn.LocalAddr = hexToIP(localParts[0], protocol)
			port, _ := strconv.ParseInt(localParts[1], 16, 32)
			conn.LocalPort = int(port)
		}

		// Parse remote address
		remoteParts := strings.Split(fields[2], ":")
		if len(remoteParts) == 2 {
			conn.RemoteAddr = hexToIP(remoteParts[0], protocol)
			port, _ := strconv.ParseInt(remoteParts[1], 16, 32)
			conn.RemotePort = int(port)
		}

		// State
		stateNum, _ := strconv.ParseInt(fields[3], 16, 32)
		conn.State = tcpStateToString(int(stateNum))

		// Queues
		queueParts := strings.Split(fields[4], ":")
		if len(queueParts) == 2 {
			txQueue, _ := strconv.ParseInt(queueParts[0], 16, 32)
			rxQueue, _ := strconv.ParseInt(queueParts[1], 16, 32)
			conn.TxQueue = int(txQueue)
			conn.RxQueue = int(rxQueue)
		}

		// UID
		conn.UID, _ = strconv.Atoi(fields[7])

		// Inode
		conn.Inode, _ = strconv.ParseUint(fields[9], 10, 64)

		// Map inode to PID
		if pid, ok := c.inodeToPID[conn.Inode]; ok {
			conn.PID = pid
			conn.ProcessName = c.getProcessName(pid)
		}

		conns = append(conns, conn)
	}

	return conns
}

func (c *NetworkRootCauseCollector) buildInodePIDMap() {
	c.inodeToPID = make(map[uint64]int)

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		fdPath := filepath.Join("/proc", entry.Name(), "fd")
		fds, err := os.ReadDir(fdPath)
		if err != nil {
			continue
		}

		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdPath, fd.Name()))
			if err != nil {
				continue
			}

			// Look for socket inodes: socket:[12345]
			if strings.HasPrefix(link, "socket:[") && strings.HasSuffix(link, "]") {
				inodeStr := link[8 : len(link)-1]
				inode, err := strconv.ParseUint(inodeStr, 10, 64)
				if err == nil {
					c.inodeToPID[inode] = pid
				}
			}
		}
	}
}

func (c *NetworkRootCauseCollector) getProcessName(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (c *NetworkRootCauseCollector) getInterfaceSpeed(iface string) uint64 {
	// Try to read from /sys/class/net/<iface>/speed
	data, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/speed", iface))
	if err != nil {
		return 0
	}

	speed, _ := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if speed > 0 && speed < 100000 { // Reasonable range in Mbps
		return speed * 1000000 // Convert to bps
	}
	return 0
}

func hexToIP(hexStr string, protocol string) string {
	if protocol == "tcp6" || protocol == "udp6" {
		// IPv6: 32 hex chars
		if len(hexStr) != 32 {
			return hexStr
		}

		bytes, err := hex.DecodeString(hexStr)
		if err != nil {
			return hexStr
		}

		// Reverse byte order for each 4-byte group
		ip := make([]byte, 16)
		for i := 0; i < 4; i++ {
			for j := 0; j < 4; j++ {
				ip[i*4+j] = bytes[i*4+(3-j)]
			}
		}

		return net.IP(ip).String()
	}

	// IPv4: 8 hex chars (reversed)
	if len(hexStr) != 8 {
		return hexStr
	}

	bytes, err := hex.DecodeString(hexStr)
	if err != nil {
		return hexStr
	}

	// Reverse bytes (little-endian to big-endian)
	return fmt.Sprintf("%d.%d.%d.%d", bytes[3], bytes[2], bytes[1], bytes[0])
}

func tcpStateToString(state int) string {
	states := map[int]string{
		1:  "ESTABLISHED",
		2:  "SYN_SENT",
		3:  "SYN_RECV",
		4:  "FIN_WAIT1",
		5:  "FIN_WAIT2",
		6:  "TIME_WAIT",
		7:  "CLOSE",
		8:  "CLOSE_WAIT",
		9:  "LAST_ACK",
		10: "LISTEN",
		11: "CLOSING",
	}

	if s, ok := states[state]; ok {
		return s
	}
	return fmt.Sprintf("UNKNOWN_%d", state)
}
