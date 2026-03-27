package agent

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ProcessNode represents a single process in the runtime graph.
type ProcessNode struct {
	PID         int                `json:"pid"`
	PPID        int                `json:"ppid"`
	Binary      string             `json:"binary"`
	Args        string             `json:"args,omitempty"`
	StartTime   time.Time          `json:"start_time"`
	ExecChain   []string           `json:"exec_chain,omitempty"`
	Fingerprint ProcessFingerprint `json:"fingerprint"`
	Children    []int              `json:"children,omitempty"`
}

// ProcessFingerprint captures behavioral characteristics of a process.
type ProcessFingerprint struct {
	SyscallRates   map[string]float64 `json:"syscall_rates,omitempty"`
	FileAccesses   []string           `json:"file_accesses,omitempty"`
	NetworkConns   int                `json:"network_conns"`
	PortsListening []int              `json:"ports_listening,omitempty"`
	AvgCPU         float64            `json:"avg_cpu"`
	AvgMemBytes    float64            `json:"avg_mem_bytes"`
	SpawnRate      float64            `json:"spawn_rate"`
	Privileged     bool               `json:"privileged"`
}

// AbnormalLineageResult describes a detected lineage anomaly.
type AbnormalLineageResult struct {
	PID         int      `json:"pid"`
	Binary      string   `json:"binary"`
	ParentChain []string `json:"parent_chain"`
	Reason      string   `json:"reason"`
	Severity    string   `json:"severity"`
}

// NewPatternResult describes a process not seen in the baseline.
type NewPatternResult struct {
	PID       int       `json:"pid"`
	Binary    string    `json:"binary"`
	PPID      int       `json:"ppid"`
	FirstSeen time.Time `json:"first_seen"`
	Reason    string    `json:"reason"`
}

// ProcessTree maintains the directed process graph for runtime analysis.
type ProcessTree struct {
	mu       sync.RWMutex
	nodes    map[int]*ProcessNode
	maxNodes int
}

// NewProcessTree creates a bounded process tree.
func NewProcessTree(maxNodes int) *ProcessTree {
	if maxNodes <= 0 {
		maxNodes = 65536
	}
	return &ProcessTree{
		nodes:    make(map[int]*ProcessNode, 256),
		maxNodes: maxNodes,
	}
}

// AddExecEvent records an exec event, updating or creating the process node.
func (pt *ProcessTree) AddExecEvent(pid, ppid int, binary, args string, ts time.Time) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	if len(pt.nodes) >= pt.maxNodes {
		pt.evictOldest()
	}

	node, exists := pt.nodes[pid]
	if exists {
		node.ExecChain = append(node.ExecChain, binary)
		node.Binary = binary
		node.Args = args
		return
	}

	node = &ProcessNode{
		PID:       pid,
		PPID:      ppid,
		Binary:    binary,
		Args:      args,
		StartTime: ts,
		ExecChain: []string{binary},
		Fingerprint: ProcessFingerprint{
			SyscallRates: make(map[string]float64),
		},
	}
	pt.nodes[pid] = node
	if parent, ok := pt.nodes[ppid]; ok {
		parent.Children = appendUniquePID(parent.Children, pid)
	}
}

// AddForkEvent records a fork/clone event creating a child process.
func (pt *ProcessTree) AddForkEvent(childPID, parentPID int, binary string, ts time.Time) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	if _, exists := pt.nodes[childPID]; exists {
		return
	}
	if len(pt.nodes) >= pt.maxNodes {
		pt.evictOldest()
	}

	pt.nodes[childPID] = &ProcessNode{
		PID:       childPID,
		PPID:      parentPID,
		Binary:    binary,
		StartTime: ts,
		ExecChain: []string{binary},
		Fingerprint: ProcessFingerprint{
			SyscallRates: make(map[string]float64),
		},
	}
	if parent, ok := pt.nodes[parentPID]; ok {
		parent.Children = appendUniquePID(parent.Children, childPID)
	}
}

// UpdateFingerprint updates the behavioral fingerprint of a process.
func (pt *ProcessTree) UpdateFingerprint(pid int, fp ProcessFingerprint) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	if node, ok := pt.nodes[pid]; ok {
		node.Fingerprint = fp
	}
}

// ByPID returns the process node for a given PID.
func (pt *ProcessTree) ByPID(pid int) *ProcessNode {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	node, ok := pt.nodes[pid]
	if !ok {
		return nil
	}
	cp := *node
	return &cp
}

// ByBinary returns all process nodes matching a binary name (substring match).
func (pt *ProcessTree) ByBinary(name string) []*ProcessNode {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	lower := strings.ToLower(name)
	var result []*ProcessNode
	for _, node := range pt.nodes {
		if strings.Contains(strings.ToLower(node.Binary), lower) {
			cp := *node
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PID < result[j].PID })
	return result
}

// ByTimeWindow returns process nodes started within [start, end].
func (pt *ProcessTree) ByTimeWindow(start, end time.Time) []*ProcessNode {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	var result []*ProcessNode
	for _, node := range pt.nodes {
		if !node.StartTime.Before(start) && !node.StartTime.After(end) {
			cp := *node
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].StartTime.Before(result[j].StartTime) })
	return result
}

// Size returns the number of processes tracked.
func (pt *ProcessTree) Size() int {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return len(pt.nodes)
}

// ParentChain returns the parent lineage of a process as binary names.
func (pt *ProcessTree) ParentChain(pid int) []string {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return pt.parentChainLocked(pid)
}

func (pt *ProcessTree) parentChainLocked(pid int) []string {
	var chain []string
	visited := make(map[int]bool)
	for current := pid; ; {
		node, ok := pt.nodes[current]
		if !ok || visited[current] {
			break
		}
		visited[current] = true
		chain = append(chain, node.Binary)
		if node.PPID == 0 || node.PPID == current {
			break
		}
		current = node.PPID
	}
	return chain
}

// suspiciousChildrenOf maps parent basenames to child basenames that indicate compromise.
var suspiciousChildrenOf = map[string]map[string]bool{
	"nginx":    {"bash": true, "sh": true, "curl": true, "wget": true, "python": true, "perl": true, "nc": true, "ncat": true},
	"httpd":    {"bash": true, "sh": true, "curl": true, "wget": true, "python": true, "perl": true, "nc": true},
	"java":     {"bash": true, "sh": true, "curl": true, "wget": true, "python": true, "nc": true},
	"node":     {"bash": true, "sh": true, "curl": true, "wget": true, "python": true, "nc": true},
	"postgres": {"curl": true, "wget": true, "python": true, "nc": true, "ncat": true},
	"mysqld":   {"curl": true, "wget": true, "python": true, "nc": true},
}

// suspiciousPathPrefixes are paths from which execution indicates compromise.
var suspiciousPathPrefixes = []string{"/tmp/", "/dev/shm/", "/var/tmp/"}

// DetectAbnormalLineage scans the process tree for unusual parent-child patterns.
func (pt *ProcessTree) DetectAbnormalLineage() []AbnormalLineageResult {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	var results []AbnormalLineageResult
	for _, node := range pt.nodes {
		results = append(results, pt.checkNodeLineage(node)...)
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Severity != results[j].Severity {
			return results[i].Severity == "high"
		}
		return results[i].PID < results[j].PID
	})
	return results
}

func (pt *ProcessTree) checkNodeLineage(node *ProcessNode) []AbnormalLineageResult {
	var results []AbnormalLineageResult
	childBase := binaryBaseName(node.Binary)

	// Check 1: suspicious parent spawning this child
	if parent, ok := pt.nodes[node.PPID]; ok {
		parentBase := binaryBaseName(parent.Binary)
		if children, found := suspiciousChildrenOf[parentBase]; found && children[childBase] {
			results = append(results, AbnormalLineageResult{
				PID:         node.PID,
				Binary:      node.Binary,
				ParentChain: pt.parentChainLocked(node.PID),
				Reason:      fmt.Sprintf("suspicious child %q spawned by %q", childBase, parentBase),
				Severity:    "high",
			})
		}
	}

	// Check 2: execution from writable/tmp paths
	for _, prefix := range suspiciousPathPrefixes {
		if strings.HasPrefix(node.Binary, prefix) {
			results = append(results, AbnormalLineageResult{
				PID:         node.PID,
				Binary:      node.Binary,
				ParentChain: pt.parentChainLocked(node.PID),
				Reason:      fmt.Sprintf("execution from suspicious path: %s", node.Binary),
				Severity:    "high",
			})
			break
		}
	}

	// Check 3: long exec chain (potential exec chain attack)
	if len(node.ExecChain) > 5 {
		results = append(results, AbnormalLineageResult{
			PID:         node.PID,
			Binary:      node.Binary,
			ParentChain: pt.parentChainLocked(node.PID),
			Reason:      fmt.Sprintf("unusually long exec chain (%d steps): %s", len(node.ExecChain), strings.Join(node.ExecChain, " → ")),
			Severity:    "medium",
		})
	}

	return results
}

// DetectNewPatterns compares the current tree against a known baseline set of binary names.
func (pt *ProcessTree) DetectNewPatterns(baselineBinaries map[string]bool) []NewPatternResult {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	var results []NewPatternResult
	for _, node := range pt.nodes {
		key := binaryBaseName(node.Binary)
		if key == "" || baselineBinaries[key] {
			continue
		}
		results = append(results, NewPatternResult{
			PID:       node.PID,
			Binary:    node.Binary,
			PPID:      node.PPID,
			FirstSeen: node.StartTime,
			Reason:    fmt.Sprintf("binary %q not previously observed in baseline", key),
		})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].PID < results[j].PID })
	return results
}

func (pt *ProcessTree) evictOldest() {
	if len(pt.nodes) == 0 {
		return
	}
	type pidTime struct {
		pid int
		ts  time.Time
	}
	pids := make([]pidTime, 0, len(pt.nodes))
	for _, n := range pt.nodes {
		pids = append(pids, pidTime{n.PID, n.StartTime})
	}
	sort.Slice(pids, func(i, j int) bool { return pids[i].ts.Before(pids[j].ts) })

	evictCount := len(pids) / 10
	if evictCount < 1 {
		evictCount = 1
	}
	for i := 0; i < evictCount && i < len(pids); i++ {
		delete(pt.nodes, pids[i].pid)
	}
}

func binaryBaseName(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx >= 0 && idx < len(path)-1 {
		return path[idx+1:]
	}
	return path
}

func appendUniquePID(slice []int, val int) []int {
	for _, v := range slice {
		if v == val {
			return slice
		}
	}
	return append(slice, val)
}
