package securityaudit

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
)

// Severity is the normalized severity tier for security findings.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

// Finding is one normalized security signal consumed by APIs, workflows, and UI.
type Finding struct {
	ID                string    `json:"id"`
	FindingID         string    `json:"finding_id,omitempty"`
	EvidenceID        string    `json:"evidence_id"`
	Timestamp         time.Time `json:"timestamp"`
	PID               string    `json:"pid,omitempty"`
	Container         string    `json:"container,omitempty"`
	NodeScope         string    `json:"node_scope,omitempty"`
	Severity          Severity  `json:"severity"`
	Category          string    `json:"category"`
	Scope             string    `json:"scope"`
	CollectorID       string    `json:"collector_id"`
	Summary           string    `json:"summary"`
	Description       string    `json:"description"`
	Evidence          []string  `json:"evidence"`
	RecommendedAction string    `json:"recommended_action"`
	Score             float64   `json:"score"`
	Confidence        float64   `json:"confidence"`
	ObservedAt        time.Time `json:"observed_at"`
	Source            string    `json:"source"`
}

// Summary aggregates finding counts by severity.
type Summary struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
}

// TrendPoint is a chart-ready aggregated security risk point.
type TrendPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Critical  int       `json:"critical"`
	High      int       `json:"high"`
	Medium    int       `json:"medium"`
	Low       int       `json:"low"`
	Total     int       `json:"total"`
}

// Dashboard is the API payload for security dashboard views.
type Dashboard struct {
	Findings  []Finding    `json:"findings"`
	Summary   Summary      `json:"summary"`
	Trends    []TrendPoint `json:"trends"`
	Count     int          `json:"count"`
	Timestamp time.Time    `json:"timestamp"`
}

// Options controls security finding queries.
type Options struct {
	CollectorID string
	Severity    string
	Category    string
	Window      time.Duration
	Limit       int
}

// Evaluator computes security findings from ingested metrics/logs.
type Evaluator struct {
	store *ingest.MemoryStore
	index *logindex.Index
}

// NewEvaluator builds a security evaluator for controller-side APIs and workflows.
func NewEvaluator(store *ingest.MemoryStore, index *logindex.Index) *Evaluator {
	return &Evaluator{store: store, index: index}
}

// Findings returns normalized security findings sorted by severity and score.
func (e *Evaluator) Findings(opts Options) []Finding {
	if e == nil || e.store == nil {
		return nil
	}
	window := opts.Window
	if window <= 0 {
		window = 45 * time.Minute
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 200
	}

	nodes := e.store.Snapshot()
	collectorID := strings.TrimSpace(opts.CollectorID)
	out := make([]Finding, 0, 64)
	for _, node := range nodes {
		if node == nil {
			continue
		}
		if collectorID != "" && !strings.EqualFold(node.CollectorID, collectorID) {
			continue
		}
		out = append(out, analyzeNode(node, e.index, window)...)
	}

	sevFilter := strings.ToLower(strings.TrimSpace(opts.Severity))
	catFilter := strings.ToLower(strings.TrimSpace(opts.Category))
	filtered := make([]Finding, 0, len(out))
	for _, finding := range out {
		if sevFilter != "" && strings.ToLower(string(finding.Severity)) != sevFilter {
			continue
		}
		if catFilter != "" && strings.ToLower(finding.Category) != catFilter {
			continue
		}
		filtered = append(filtered, finding)
	}

	sort.Slice(filtered, func(i, j int) bool {
		left := filtered[i]
		right := filtered[j]
		if severityRank(left.Severity) != severityRank(right.Severity) {
			return severityRank(left.Severity) > severityRank(right.Severity)
		}
		if left.Score != right.Score {
			return left.Score > right.Score
		}
		if !left.ObservedAt.Equal(right.ObservedAt) {
			return left.ObservedAt.After(right.ObservedAt)
		}
		return left.ID < right.ID
	})

	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered
}

// Dashboard returns findings + summary + trends for UI/API consumers.
func (e *Evaluator) Dashboard(opts Options) Dashboard {
	window := opts.Window
	if window <= 0 {
		window = 45 * time.Minute
	}
	findings := e.Findings(opts)
	summary := summarize(findings)
	trends := e.buildTrends(opts.CollectorID, window)
	return Dashboard{
		Findings:  findings,
		Summary:   summary,
		Trends:    trends,
		Count:     len(findings),
		Timestamp: time.Now().UTC(),
	}
}

func (e *Evaluator) buildTrends(collectorID string, window time.Duration) []TrendPoint {
	if e == nil || e.store == nil {
		return nil
	}
	collectorID = strings.TrimSpace(collectorID)
	nodes := e.store.Snapshot()
	if collectorID == "" {
		var latest *ingest.NodeSnapshot
		for _, node := range nodes {
			if node == nil {
				continue
			}
			if latest == nil || node.UpdatedAt.After(latest.UpdatedAt) {
				latest = node
			}
		}
		if latest != nil {
			collectorID = latest.CollectorID
		}
	}
	if collectorID == "" {
		return nil
	}

	samples := e.store.MetricHistory(collectorID, time.Now().Add(-window), 120)
	if len(samples) == 0 {
		return nil
	}
	out := make([]TrendPoint, 0, len(samples))
	for _, sample := range samples {
		sev := classifySample(sample.Metrics)
		out = append(out, TrendPoint{
			Timestamp: sample.Timestamp,
			Critical:  sev.Critical,
			High:      sev.High,
			Medium:    sev.Medium,
			Low:       sev.Low,
			Total:     sev.Critical + sev.High + sev.Medium + sev.Low,
		})
	}
	return out
}

func analyzeNode(node *ingest.NodeSnapshot, idx *logindex.Index, window time.Duration) []Finding {
	if node == nil {
		return nil
	}
	metrics := node.Metrics
	now := node.UpdatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	collectorID := strings.TrimSpace(node.CollectorID)
	if collectorID == "" {
		collectorID = strings.TrimSpace(node.Hostname)
	}
	if collectorID == "" {
		collectorID = "unknown"
	}

	findings := make([]Finding, 0, 16)
	findings = append(findings, collectorSecurityFindings(node, now, collectorID)...)
	add := func(key string, severity Severity, category, summary string, score float64, evidence []string, action string, source string) {
		evidenceID := findingID(collectorID, key)
		findings = append(findings, Finding{
			ID:                evidenceID,
			FindingID:         evidenceID,
			EvidenceID:        evidenceID,
			Timestamp:         now,
			NodeScope:         collectorID,
			Severity:          severity,
			Category:          category,
			Scope:             "node",
			CollectorID:       collectorID,
			Summary:           summary,
			Description:       summary,
			Evidence:          dedupeStrings(evidence),
			RecommendedAction: action,
			Score:             clamp01(score),
			Confidence:        clamp01(score),
			ObservedAt:        now,
			Source:            source,
		})
	}

	worldWritable := maxMetric(metrics,
		"node_security_world_writable_sensitive_paths",
		"node_filesystem_world_writable_count",
	)
	if worldWritable > 0 {
		severity := SeverityHigh
		score := 0.72
		if worldWritable >= 10 {
			severity = SeverityCritical
			score = 0.9
		}
		add(
			"world_writable_sensitive",
			severity,
			"filesystem_permissions",
			fmt.Sprintf("Sensitive filesystem paths are world-writable (count=%.0f)", worldWritable),
			score,
			[]string{fmt.Sprintf("world_writable_count=%.0f", worldWritable)},
			"Inspect offending paths and set strict mode (0640/0750 or stricter) on service-owned files.",
			"collector_metrics",
		)
	}

	weakPerm := maxMetric(metrics,
		"node_security_weak_permission_count",
		"node_permissions_weak_total",
		"node_security_ssh_weak_permissions_count",
	)
	if weakPerm > 0 {
		severity := SeverityHigh
		score := 0.68
		if weakPerm >= 8 {
			severity = SeverityCritical
			score = 0.86
		}
		add(
			"weak_permissions",
			severity,
			"filesystem_permissions",
			fmt.Sprintf("Weak permission drift detected (count=%.0f)", weakPerm),
			score,
			[]string{fmt.Sprintf("weak_permission_signals=%.0f", weakPerm)},
			"Review permission drift timeline and restore owner/group policy on affected files.",
			"collector_metrics",
		)
	}

	suid := maxMetric(metrics, "node_security_suid_sgid_binaries_count")
	if suid > 0 {
		severity := SeverityMedium
		score := 0.35
		if suid > 80 {
			severity = SeverityHigh
			score = 0.62
		}
		add(
			"suid_sgid_inventory",
			severity,
			"filesystem_permissions",
			fmt.Sprintf("SUID/SGID binary inventory is elevated (count=%.0f)", suid),
			score,
			[]string{fmt.Sprintf("suid_sgid_binaries=%.0f", suid)},
			"Confirm baseline inventory and remove unnecessary SUID/SGID bits from custom binaries.",
			"collector_metrics",
		)
	}

	sensitiveReadable := maxMetric(metrics, "node_security_sensitive_readable_files_count")
	if sensitiveReadable > 0 {
		add(
			"sensitive_files_readable",
			SeverityCritical,
			"filesystem_permissions",
			fmt.Sprintf("Sensitive files are readable by group/world (count=%.0f)", sensitiveReadable),
			0.92,
			[]string{fmt.Sprintf("sensitive_readable_files=%.0f", sensitiveReadable)},
			"Restrict secrets/backups to owner-only permissions and rotate exposed credentials.",
			"collector_metrics",
		)
	}

	largeFiles := maxMetric(metrics, "node_security_large_files_count")
	growthBytes := maxMetric(metrics, "node_security_large_file_growth_bytes")
	if largeFiles > 0 || growthBytes > 0 {
		severity := SeverityMedium
		score := 0.42
		if growthBytes > 512*1024*1024 {
			severity = SeverityHigh
			score = 0.74
		}
		evidence := []string{}
		if largeFiles > 0 {
			evidence = append(evidence, fmt.Sprintf("large_files_count=%.0f", largeFiles))
		}
		if growthBytes > 0 {
			evidence = append(evidence, fmt.Sprintf("large_file_growth_bytes=%.0f", growthBytes))
		}
		add(
			"large_file_growth",
			severity,
			"filesystem_growth",
			"Large files are accumulating and growing in runtime paths",
			score,
			evidence,
			"Check top growth paths, rotate logs/artifacts, and enforce retention quotas.",
			"collector_metrics",
		)
	}

	ports := maxMetric(metrics, "node_security_listening_ports_count")
	unexpectedPorts := maxMetric(metrics, "node_security_unexpected_listening_ports_count")
	stalePorts := maxMetric(metrics, "node_security_stale_listening_ports_count")
	if ports > 0 || unexpectedPorts > 0 || stalePorts > 0 {
		severity := SeverityMedium
		score := 0.46
		if unexpectedPorts > 0 || stalePorts > 0 {
			severity = SeverityHigh
			score = 0.76
		}
		evidence := []string{}
		if ports > 0 {
			evidence = append(evidence, fmt.Sprintf("listening_ports=%.0f", ports))
		}
		if unexpectedPorts > 0 {
			evidence = append(evidence, fmt.Sprintf("unexpected_ports=%.0f", unexpectedPorts))
		}
		if stalePorts > 0 {
			evidence = append(evidence, fmt.Sprintf("stale_listening_ports=%.0f", stalePorts))
		}
		add(
			"listening_port_exposure",
			severity,
			"network_exposure",
			"Listening-port exposure exceeds baseline ownership policy",
			score,
			evidence,
			"Map exposed ports to owning services and close or firewall unmanaged listeners.",
			"collector_metrics",
		)
	}

	suspiciousOutbound := maxMetric(metrics, "node_security_suspicious_outbound_destinations_count")
	if suspiciousOutbound > 0 {
		add(
			"suspicious_outbound_destinations",
			SeverityHigh,
			"network_exposure",
			fmt.Sprintf("Unexpected outbound destination activity detected (count=%.0f)", suspiciousOutbound),
			0.78,
			[]string{fmt.Sprintf("suspicious_outbound_destinations=%.0f", suspiciousOutbound)},
			"Inspect destination allow-list and validate process-level outbound ownership.",
			"collector_metrics",
		)
	}

	synBacklog := maxMetric(metrics, "node_security_syn_backlog_pressure_ratio")
	retrans := maxMetric(metrics, "node_tcp_retransmit_ratio")
	softnetDrop := maxMetric(metrics, "node_softnet_dropped_per_second")
	if synBacklog >= 0.2 || retrans >= 0.01 || softnetDrop >= 5 {
		severity := SeverityMedium
		score := clamp01(0.35 + synBacklog*0.35 + retrans*6 + softnetDrop*0.01)
		if synBacklog >= 0.5 || retrans >= 0.02 {
			severity = SeverityHigh
		}
		add(
			"network_pressure_security",
			severity,
			"network_posture",
			"Network pressure signals indicate elevated exposure to connection abuse or service instability",
			score,
			[]string{
				fmt.Sprintf("syn_backlog_pressure_ratio=%.3f", synBacklog),
				fmt.Sprintf("tcp_retransmit_ratio=%.4f", retrans),
				fmt.Sprintf("softnet_dropped_per_second=%.2f", softnetDrop),
			},
			"Inspect SYN backlog, retransmit spikes, and frontend rate controls before they escalate.",
			"collector_metrics",
		)
	}

	sysctlRisky := maxMetric(metrics, "node_security_sysctl_risky_count")
	firewallDisabled := maxMetric(metrics, "node_security_firewall_disabled")
	selinuxDisabled := maxMetric(metrics, "node_security_selinux_disabled")
	apparmorDisabled := maxMetric(metrics, "node_security_apparmor_disabled")
	if sysctlRisky > 0 || firewallDisabled > 0 || selinuxDisabled > 0 || apparmorDisabled > 0 {
		severity := SeverityMedium
		score := 0.5
		if firewallDisabled > 0 {
			severity = SeverityCritical
			score = 0.93
		} else if sysctlRisky >= 2 || (selinuxDisabled > 0 && apparmorDisabled > 0) {
			severity = SeverityHigh
			score = 0.76
		}
		add(
			"kernel_posture",
			severity,
			"kernel_posture",
			"Kernel/network hardening posture contains risky defaults",
			score,
			[]string{
				fmt.Sprintf("sysctl_risky_count=%.0f", sysctlRisky),
				fmt.Sprintf("firewall_disabled=%.0f", firewallDisabled),
				fmt.Sprintf("selinux_disabled=%.0f", selinuxDisabled),
				fmt.Sprintf("apparmor_disabled=%.0f", apparmorDisabled),
			},
			"Apply hardened sysctl profile, verify firewall ruleset, and keep at least one MAC framework enforcing.",
			"collector_metrics",
		)
	}

	vulns := maxMetric(metrics, "node_security_package_vulnerability_count")
	if vulns > 0 {
		severity := SeverityMedium
		score := 0.45
		if vulns >= 10 {
			severity = SeverityHigh
			score = 0.79
		}
		add(
			"package_vulnerabilities",
			severity,
			"vulnerability",
			fmt.Sprintf("Package vulnerability scanner reported pending issues (count=%.0f)", vulns),
			score,
			[]string{fmt.Sprintf("package_vulnerability_count=%.0f", vulns)},
			"Patch affected packages or isolate vulnerable services with compensating controls.",
			"collector_metrics",
		)
	}

	privProc := maxMetric(metrics, "node_security_privileged_unusual_path_process_count")
	cronAnom := maxMetric(metrics, "node_security_cron_anomalies_count")
	systemdAnom := maxMetric(metrics, "node_security_systemd_unknown_units_count")
	containerPriv := maxMetric(metrics, "node_security_container_privileged_count")
	containerCaps := maxMetric(metrics, "node_security_container_capability_risk_count")
	if privProc > 0 || cronAnom > 0 || systemdAnom > 0 || containerPriv > 0 || containerCaps > 0 {
		severity := SeverityMedium
		score := 0.55
		if privProc > 0 || containerPriv > 0 {
			severity = SeverityHigh
			score = 0.82
		}
		add(
			"execution_posture",
			severity,
			"process_execution",
			"Execution posture indicates unusual privileged workloads or scheduler drift",
			score,
			[]string{
				fmt.Sprintf("privileged_unusual_path_process_count=%.0f", privProc),
				fmt.Sprintf("cron_anomalies_count=%.0f", cronAnom),
				fmt.Sprintf("systemd_unknown_units_count=%.0f", systemdAnom),
				fmt.Sprintf("container_privileged_count=%.0f", containerPriv),
				fmt.Sprintf("container_capability_risk_count=%.0f", containerCaps),
			},
			"Validate ownership of privileged processes/units and reduce unnecessary container privileges/capabilities.",
			"collector_metrics",
		)
	}

	processQueueFindings := 0
	for _, sample := range node.ProcessNetwork {
		if sample == nil {
			continue
		}
		if sample.Connections >= 400 || sample.QueuedBytes >= 64*1024*1024 {
			processQueueFindings++
		}
	}
	if processQueueFindings > 0 {
		add(
			"process_network_anomalies",
			SeverityHigh,
			"network_exposure",
			fmt.Sprintf("Per-process socket pressure is elevated for %d process scopes", processQueueFindings),
			0.71,
			[]string{fmt.Sprintf("process_socket_pressure_scopes=%d", processQueueFindings)},
			"Inspect the busiest process sockets and validate service ownership for exposed listeners.",
			"ingest_process_network",
		)
	}

	logHints := collectSecurityLogHints(node, idx, window)
	if len(logHints) > 0 {
		severity := SeverityMedium
		score := 0.52
		if len(logHints) >= 5 {
			severity = SeverityHigh
			score = 0.72
		}
		add(
			"security_log_burst",
			severity,
			"log_security",
			"Security-related log warnings/errors are rising in the current window",
			score,
			logHints,
			"Correlate log bursts with deployment and permission/network changes before applying remediations.",
			"log_index",
		)
	}

	findings = append(findings, analyzeRuntimeEvents(node, now, collectorID)...)

	return dedupeFindings(findings)
}

func collectorSecurityFindings(node *ingest.NodeSnapshot, now time.Time, collectorID string) []Finding {
	if node == nil || len(node.SecurityFindings) == 0 {
		return nil
	}
	out := make([]Finding, 0, len(node.SecurityFindings))
	for _, item := range node.SecurityFindings {
		ts := item.Timestamp
		if ts.IsZero() {
			ts = now
		}
		findingIDValue := strings.TrimSpace(item.FindingID)
		if findingIDValue == "" {
			findingIDValue = findingID(collectorID, item.Category+"-"+item.Summary)
		}
		out = append(out, Finding{
			ID:                findingIDValue,
			FindingID:         findingIDValue,
			EvidenceID:        findingIDValue,
			Timestamp:         ts,
			PID:               strings.TrimSpace(item.PID),
			NodeScope:         collectorID,
			Severity:          normalizeSeverity(item.Severity),
			Category:          strings.TrimSpace(item.Category),
			Scope:             firstNonEmpty(strings.TrimSpace(item.Scope), "host"),
			CollectorID:       collectorID,
			Summary:           strings.TrimSpace(item.Summary),
			Description:       strings.TrimSpace(item.Summary),
			Evidence:          dedupeStrings(item.Evidence),
			RecommendedAction: strings.TrimSpace(item.RecommendedNextStep),
			Score:             clamp01(item.Confidence),
			Confidence:        clamp01(item.Confidence),
			ObservedAt:        ts,
			Source:            firstNonEmpty(strings.TrimSpace(item.Source), "collector_security_audit"),
		})
	}
	return out
}

func analyzeRuntimeEvents(node *ingest.NodeSnapshot, now time.Time, collectorID string) []Finding {
	if node == nil || len(node.RuntimeSecurityEvents) == 0 {
		return nil
	}

	events := append([]ingest.RuntimeSecurityEvent(nil), node.RuntimeSecurityEvents...)
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.Before(events[j].Timestamp)
	})

	tmpExecEvidence := make([]string, 0, 8)
	spawnByPID := make(map[string]int)
	connectByMinute := make(map[string]int)
	suspiciousIPEvidence := make([]string, 0, 8)
	privEscEvidence := make([]string, 0, 8)
	abnormalBindEvidence := make([]string, 0, 8)
	longLivedEvidence := make([]string, 0, 8)
	sensitivePathEvidence := make([]string, 0, 8)
	rootPathEvidence := make([]string, 0, 8)
	entropyEvidence := make([]string, 0, 8)
	processPortMismatch := make([]string, 0, 8)

	for _, event := range events {
		etype := strings.ToLower(strings.TrimSpace(event.Type))
		path := strings.ToLower(strings.TrimSpace(event.Path))
		desc := strings.TrimSpace(event.Description)
		if desc == "" {
			desc = etype
		}

		if etype == "execve" && (strings.HasPrefix(path, "/tmp/") || strings.HasPrefix(path, "/var/tmp/") || strings.HasPrefix(path, "/dev/shm/")) {
			tmpExecEvidence = append(tmpExecEvidence, fmt.Sprintf("%s pid=%s path=%s", event.EvidenceID, event.PID, event.Path))
		}

		if etype == "fork" {
			if event.PID != "" {
				spawnByPID[event.PID]++
			}
		}

		if etype == "connect" {
			bucket := event.Timestamp.UTC().Format("2006-01-02T15:04")
			connectByMinute[bucket]++
			if isSuspiciousRemoteIP(event.RemoteIP) {
				suspiciousIPEvidence = append(suspiciousIPEvidence, fmt.Sprintf("%s remote_ip=%s pid=%s", event.EvidenceID, event.RemoteIP, event.PID))
			}
		}

		if etype == "privilege_escalation" {
			privEscEvidence = append(privEscEvidence, fmt.Sprintf("%s pid=%s %s", event.EvidenceID, event.PID, truncateString(desc, 120)))
		}

		if etype == "abnormal_bind_port" || (etype == "bind" && event.Port > 0) {
			abnormalBindEvidence = append(abnormalBindEvidence, fmt.Sprintf("%s port=%d pid=%s", event.EvidenceID, event.Port, event.PID))
			if event.Port == 22 || event.Port == 2379 || event.Port == 6443 {
				processPortMismatch = append(processPortMismatch, fmt.Sprintf("%s critical_port=%d pid=%s", event.EvidenceID, event.Port, event.PID))
			}
		}

		if etype == "long_lived_tcp" {
			longLivedEvidence = append(longLivedEvidence, fmt.Sprintf("%s remote_ip=%s", event.EvidenceID, event.RemoteIP))
		}

		if strings.HasPrefix(path, "/etc/") || strings.HasPrefix(path, "/root/") || strings.Contains(path, "/.ssh/") {
			sensitivePathEvidence = append(sensitivePathEvidence, fmt.Sprintf("%s path=%s type=%s", event.EvidenceID, event.Path, etype))
		}

		if strings.HasPrefix(path, "/") && event.PID == "1" && etype == "execve" && !isExpectedRootBinary(path) {
			rootPathEvidence = append(rootPathEvidence, fmt.Sprintf("%s path=%s", event.EvidenceID, event.Path))
		}

		if score := commandEntropy(event.Description); score >= 3.8 {
			entropyEvidence = append(entropyEvidence, fmt.Sprintf("%s entropy=%.2f", event.EvidenceID, score))
		}
	}

	findings := make([]Finding, 0, 12)
	addEventFinding := func(key string, severity Severity, category, summary string, confidence float64, evidence []string, action string) {
		evidence = dedupeStrings(evidence)
		if len(evidence) == 0 {
			return
		}
		evidenceID := strings.TrimSpace(strings.SplitN(evidence[0], " ", 2)[0])
		findings = append(findings, Finding{
			ID:                findingID(collectorID, key),
			EvidenceID:        firstNonEmpty(evidenceID, findingID(collectorID, key)),
			Timestamp:         now,
			NodeScope:         collectorID,
			Severity:          severity,
			Category:          category,
			Scope:             "runtime",
			CollectorID:       collectorID,
			Summary:           summary,
			Description:       summary,
			Evidence:          evidence,
			RecommendedAction: action,
			Score:             clamp01(confidence),
			Confidence:        clamp01(confidence),
			ObservedAt:        now,
			Source:            "ebpf_runtime",
		})
	}

	if len(tmpExecEvidence) > 0 {
		addEventFinding(
			"runtime_tmp_exec",
			SeverityHigh,
			"abnormal_process",
			"Execution from temporary/unusual paths detected",
			0.88,
			tmpExecEvidence,
			"Block execution from temporary directories and validate parent process lineage.",
		)
	}

	for pid, count := range spawnByPID {
		if count < 12 {
			continue
		}
		addEventFinding(
			"runtime_spawn_spike_"+pid,
			SeverityHigh,
			"abnormal_process",
			fmt.Sprintf("High-frequency spawn pattern detected for pid=%s (count=%d)", pid, count),
			0.82,
			[]string{fmt.Sprintf("spawn_count pid=%s count=%d", pid, count)},
			"Inspect fork/exec burst source and throttle runaway process trees.",
		)
	}

	maxConnectBurst := 0
	for _, count := range connectByMinute {
		if count > maxConnectBurst {
			maxConnectBurst = count
		}
	}
	if maxConnectBurst >= 30 {
		addEventFinding(
			"runtime_short_tcp_burst",
			SeverityHigh,
			"abnormal_network",
			fmt.Sprintf("High-frequency short TCP burst detected (max_per_minute=%d)", maxConnectBurst),
			0.79,
			[]string{fmt.Sprintf("max_connects_per_minute=%d", maxConnectBurst)},
			"Correlate connection burst with process ownership and remote endpoint allow-list.",
		)
	}

	if len(suspiciousIPEvidence) > 0 {
		addEventFinding(
			"runtime_suspicious_remote_ip",
			SeverityHigh,
			"abnormal_network",
			"Suspicious remote IP outbound patterns detected",
			0.81,
			suspiciousIPEvidence,
			"Validate destination reputation and enforce egress policy for non-baseline CIDRs.",
		)
	}

	if len(privEscEvidence) > 0 {
		addEventFinding(
			"runtime_privilege_transition",
			SeverityCritical,
			"abnormal_process",
			"Unexpected privilege transition or escalation behavior detected",
			0.93,
			privEscEvidence,
			"Freeze affected workload, capture process lineage, and validate account transition policy.",
		)
	}

	if len(abnormalBindEvidence) > 0 {
		addEventFinding(
			"runtime_abnormal_bind",
			SeverityHigh,
			"abnormal_network",
			"New listening ports outside baseline were observed",
			0.87,
			abnormalBindEvidence,
			"Close unmanaged listeners and map exposed ports to approved service inventory.",
		)
	}

	if len(longLivedEvidence) > 0 {
		addEventFinding(
			"runtime_long_lived_tcp",
			SeverityMedium,
			"abnormal_network",
			"Long-lived TCP connections exceeded threshold",
			0.74,
			longLivedEvidence,
			"Inspect long-lived sessions for stale listeners, abuse patterns, or resource leaks.",
		)
	}

	if len(processPortMismatch) > 0 {
		addEventFinding(
			"runtime_process_port_mismatch",
			SeverityHigh,
			"abnormal_network",
			"Process-to-port ownership mismatch detected on sensitive ports",
			0.84,
			processPortMismatch,
			"Validate process binary to privileged port mapping against service baseline.",
		)
	}

	if len(sensitivePathEvidence) > 0 {
		addEventFinding(
			"runtime_sensitive_path_access",
			SeverityHigh,
			"file_permission_security",
			"Access to sensitive runtime paths was observed",
			0.85,
			sensitivePathEvidence,
			"Confirm access intent for /etc, /root, and SSH key material paths.",
		)
	}

	if len(rootPathEvidence) > 0 {
		addEventFinding(
			"runtime_root_unexpected_path",
			SeverityCritical,
			"abnormal_process",
			"Root process execution from unexpected binary path detected",
			0.94,
			rootPathEvidence,
			"Validate root executable provenance and quarantine unexpected root binaries.",
		)
	}

	if len(entropyEvidence) > 0 {
		addEventFinding(
			"runtime_high_entropy_cmd",
			SeverityMedium,
			"abnormal_process",
			"High-entropy command line patterns detected",
			0.71,
			entropyEvidence,
			"Inspect encoded/obfuscated command lines for malicious execution behavior.",
		)
	}

	if score, total := syscallSpikeScore(node.SyscallStatistics); score > 0 {
		severity := SeverityMedium
		if score >= 0.8 {
			severity = SeverityHigh
		}
		addEventFinding(
			"runtime_syscall_spike",
			severity,
			"kernel_runtime_integrity",
			fmt.Sprintf("Suspicious syscall rate spike detected (score=%.2f total=%d)", score, total),
			score,
			[]string{fmt.Sprintf("syscall_total=%d", total)},
			"Inspect syscall-heavy workloads and validate recent runtime changes against baseline.",
		)
	}

	if moduleLoads := node.SyscallStatistics["init_module"] + node.SyscallStatistics["finit_module"]; moduleLoads > 0 {
		addEventFinding(
			"runtime_kernel_module_activity",
			SeverityHigh,
			"kernel_runtime_integrity",
			fmt.Sprintf("Kernel module load activity observed (count=%d)", moduleLoads),
			0.84,
			[]string{fmt.Sprintf("kernel_module_load_calls=%d", moduleLoads)},
			"Validate kernel module inventory and verify only signed/approved modules are loaded.",
		)
	}

	return findings
}

func isExpectedRootBinary(path string) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	if path == "" {
		return true
	}
	prefixes := []string{
		"/usr/sbin/",
		"/usr/bin/",
		"/sbin/",
		"/bin/",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func isSuspiciousRemoteIP(ip string) bool {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return false
	}
	if strings.HasPrefix(ip, "127.") || strings.HasPrefix(ip, "10.") || strings.HasPrefix(ip, "192.168.") {
		return false
	}
	if strings.HasPrefix(ip, "172.") {
		parts := strings.Split(ip, ".")
		if len(parts) >= 2 {
			if block, err := strconv.Atoi(parts[1]); err == nil && block >= 16 && block <= 31 {
				return false
			}
		}
	}
	return true
}

func commandEntropy(text string) float64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	freq := make(map[rune]float64, len(text))
	total := 0.0
	for _, r := range text {
		if r == ' ' || r == '\t' || r == '\n' {
			continue
		}
		freq[r]++
		total++
	}
	if total == 0 {
		return 0
	}
	entropy := 0.0
	for _, count := range freq {
		p := count / total
		entropy += -p * mathLog2(p)
	}
	return entropy
}

func mathLog2(v float64) float64 {
	if v <= 0 {
		return 0
	}
	return math.Log2(v)
}

func syscallSpikeScore(stats map[string]uint64) (float64, uint64) {
	if len(stats) == 0 {
		return 0, 0
	}
	total := uint64(0)
	weight := 0.0
	for syscall, count := range stats {
		total += count
		lower := strings.ToLower(strings.TrimSpace(syscall))
		switch {
		case strings.Contains(lower, "exec"), strings.Contains(lower, "fork"), strings.Contains(lower, "clone"):
			weight += float64(count) * 1.6
		case strings.Contains(lower, "connect"), strings.Contains(lower, "accept"), strings.Contains(lower, "bind"):
			weight += float64(count) * 1.2
		case strings.Contains(lower, "open"), strings.Contains(lower, "read"), strings.Contains(lower, "write"):
			weight += float64(count) * 0.7
		default:
			weight += float64(count) * 0.4
		}
	}
	if total == 0 {
		return 0, 0
	}
	score := weight / float64(total) / 2.0
	if total >= 800 {
		score += 0.35
	} else if total >= 400 {
		score += 0.18
	} else if total >= 200 {
		score += 0.08
	}
	return clamp01(score), total
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func collectSecurityLogHints(node *ingest.NodeSnapshot, idx *logindex.Index, window time.Duration) []string {
	hints := make([]string, 0, 12)
	for _, fingerprint := range node.Logs {
		if fingerprint == nil {
			continue
		}
		line := strings.TrimSpace(fingerprint.Example)
		if line == "" {
			continue
		}
		low := strings.ToLower(line)
		if looksSecurityLine(low) {
			hints = append(hints, truncateString(line, 180))
		}
	}

	if idx != nil {
		windowDur := window
		if windowDur <= 0 {
			windowDur = 45 * time.Minute
		}
		search := idx.Search(logindex.SearchQuery{
			CollectorID: node.CollectorID,
			Text:        "unauthorized forbidden weak permission world-writable open port failed password privilege escalation",
			Since:       time.Now().Add(-windowDur),
			Until:       time.Now().UTC(),
			Limit:       80,
		})
		for _, entry := range search.Entries {
			msg := strings.TrimSpace(entry.Message)
			if msg == "" {
				continue
			}
			low := strings.ToLower(msg)
			if looksSecurityLine(low) {
				hints = append(hints, truncateString(msg, 180))
			}
		}
	}

	hints = dedupeStrings(hints)
	if len(hints) > 10 {
		hints = hints[:10]
	}
	return hints
}

func summarize(findings []Finding) Summary {
	out := Summary{}
	for _, finding := range findings {
		switch finding.Severity {
		case SeverityCritical:
			out.Critical++
		case SeverityHigh:
			out.High++
		case SeverityMedium:
			out.Medium++
		default:
			out.Low++
		}
	}
	return out
}

func classifySample(metrics map[string]float64) Summary {
	if len(metrics) == 0 {
		return Summary{}
	}
	out := Summary{}
	worldWritable := maxMetric(metrics, "node_security_world_writable_sensitive_paths", "node_filesystem_world_writable_count")
	if worldWritable >= 10 {
		out.Critical++
	} else if worldWritable > 0 {
		out.High++
	}

	weakPerm := maxMetric(metrics, "node_security_weak_permission_count", "node_permissions_weak_total")
	if weakPerm >= 8 {
		out.Critical++
	} else if weakPerm > 0 {
		out.High++
	}

	if maxMetric(metrics, "node_security_sensitive_readable_files_count") > 0 {
		out.Critical++
	}
	if maxMetric(metrics, "node_security_firewall_disabled") > 0 {
		out.Critical++
	}

	if maxMetric(metrics, "node_security_unexpected_listening_ports_count", "node_security_stale_listening_ports_count") > 0 {
		out.High++
	}
	if maxMetric(metrics, "node_security_process_port_mismatch_count", "node_security_privilege_escalation_patterns_count") > 0 {
		out.High++
	}
	if maxMetric(metrics, "node_security_suspicious_outbound_destinations_count") > 0 {
		out.High++
	}
	if maxMetric(metrics, "node_security_privileged_unusual_path_process_count", "node_security_container_privileged_count") > 0 {
		out.High++
	}
	if maxMetric(metrics, "node_security_sensitive_path_access_count", "node_security_suspicious_process_count") > 0 {
		out.High++
	}

	if maxMetric(metrics, "node_security_sysctl_risky_count") > 0 {
		out.Medium++
	}
	if maxMetric(metrics, "node_security_suid_sgid_binaries_count") > 0 {
		out.Medium++
	}
	if maxMetric(metrics, "node_security_permission_change_anomalies_count", "node_security_scheduler_suspicious_units_count", "node_security_kernel_posture_drift_count") > 0 {
		out.Medium++
	}
	if maxMetric(metrics, "node_security_package_vulnerability_count") > 0 {
		out.Medium++
	}
	if maxMetric(metrics, "node_tcp_retransmit_ratio") > 0.01 {
		out.Medium++
	}
	if maxMetric(metrics, "node_softnet_dropped_per_second") > 5 {
		out.Low++
	}

	return out
}

func dedupeFindings(findings []Finding) []Finding {
	if len(findings) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(findings))
	out := make([]Finding, 0, len(findings))
	for _, finding := range findings {
		key := strings.TrimSpace(firstNonEmpty(finding.FindingID, finding.ID, finding.Category+"|"+finding.Summary))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if finding.FindingID == "" {
			finding.FindingID = finding.ID
		}
		out = append(out, finding)
	}
	return out
}

func normalizeSeverity(raw string) Severity {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "critical":
		return SeverityCritical
	case "high":
		return SeverityHigh
	case "medium":
		return SeverityMedium
	default:
		return SeverityLow
	}
}

func findingID(collectorID, key string) string {
	collectorID = strings.ToLower(strings.TrimSpace(collectorID))
	collectorID = strings.ReplaceAll(collectorID, " ", "-")
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, " ", "-")
	return fmt.Sprintf("sec-%s-%s", collectorID, key)
}

func severityRank(severity Severity) int {
	switch severity {
	case SeverityCritical:
		return 4
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	default:
		return 1
	}
}

func maxMetric(metrics map[string]float64, names ...string) float64 {
	max := 0.0
	for _, name := range names {
		value := metricValue(metrics, name)
		if value > max {
			max = value
		}
	}
	return max
}

func metricValue(metrics map[string]float64, name string) float64 {
	if len(metrics) == 0 || strings.TrimSpace(name) == "" {
		return 0
	}
	return metrics[name]
}

func looksSecurityLine(line string) bool {
	if line == "" {
		return false
	}
	keywords := []string{
		"unauthorized",
		"forbidden",
		"permission denied",
		"weak permission",
		"world-writable",
		"open port",
		"failed password",
		"privilege",
		"capability",
		"suid",
		"sgid",
		"security",
	}
	for _, keyword := range keywords {
		if strings.Contains(line, keyword) {
			return true
		}
	}
	return false
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func truncateString(input string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(input) <= limit {
		return input
	}
	if limit <= 3 {
		return input[:limit]
	}
	return input[:limit-3] + "..."
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
