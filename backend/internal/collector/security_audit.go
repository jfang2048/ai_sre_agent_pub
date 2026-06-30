package collector

import (
	"bufio"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/probe"
	ebpfcore "github.com/jfang2048/ai_sre_agent_pub/internal/collector/probe/ebpf"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"
)

const (
	securityMaxTrackedProcesses = 4096
)

type collectorSecurityInput struct {
	Source             string
	Processes          []*telemetryv1.ProcessSample
	AllowedListenPorts []int
	EBPFSummary        probe.EBPFSummary
	EBPFEvents         []probe.EBPFEvent
}

type collectorSecurityAuditor struct {
	cfg    SecurityConfig
	logger *zap.Logger

	mu sync.Mutex

	lastAudit time.Time
	cache     []*telemetryv1.Metric

	fileModes         map[string]os.FileMode
	fileSizes         map[string]int64
	portBaseline      map[int]*securityObservationProfile
	outboundBaseline  map[string]*securityObservationProfile
	processBaseline   map[string]*securityProcessProfile
	schedulerBaseline map[string]*securityObservationProfile
}

type securityObservationProfile struct {
	Count     int
	FirstSeen time.Time
	LastSeen  time.Time
	LastActor string
}

type securityProcessProfile struct {
	Observations int
	AvgConnect   float64
	AvgBind      float64
	AvgExec      float64
	AvgFork      float64
	AvgOpen      float64
	AvgCPU       float64
	AvgRSS       float64
	LastSeen     time.Time
}

type collectorSecurityFinding struct {
	FindingID           string
	Category            string
	Severity            string
	Scope               string
	Summary             string
	Evidence            []string
	Confidence          float64
	Timestamp           time.Time
	RecommendedNextStep string
	Source              string
	PID                 string
	Process             string
	Path                string
	RemoteIP            string
	Port                int
}

type securitySocketRecord struct {
	Proto      string
	Port       int
	RemoteIP   string
	RemotePort int
	PID        int
	PPID       int
	Process    string
	ExePath    string
}

type securityProcessInfo struct {
	PID     int
	PPID    int
	Name    string
	ExePath string
	Cmdline string
	UID     int
	EUID    int
	CapEff  string
}

type securityPermissionObservation struct {
	Path              string
	Mode              os.FileMode
	WorldWritable     bool
	SensitiveReadable bool
}

type securityPermissionDrift struct {
	Path     string
	Previous os.FileMode
	Current  os.FileMode
}

type securityLargeFileObservation struct {
	Path   string
	Size   int64
	Growth int64
}

type securitySchedulerObservation struct {
	Kind       string
	Name       string
	Path       string
	Reason     string
	Executable string
}

type securitySysctlObservation struct {
	Path   string
	Value  int64
	Reason string
}

type securitySUIDObservation struct {
	Path  string
	Mode  os.FileMode
	Owner int
}

type securityProcessResource struct {
	CPUPercent float64
	RSSBytes   uint64
}

func newCollectorSecurityAuditor(cfg SecurityConfig, logger *zap.Logger) *collectorSecurityAuditor {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &collectorSecurityAuditor{
		cfg:               cfg,
		logger:            logger.With(zap.String("component", "collector_security_audit")),
		fileModes:         make(map[string]os.FileMode),
		fileSizes:         make(map[string]int64),
		portBaseline:      make(map[int]*securityObservationProfile),
		outboundBaseline:  make(map[string]*securityObservationProfile),
		processBaseline:   make(map[string]*securityProcessProfile),
		schedulerBaseline: make(map[string]*securityObservationProfile),
	}
}

func (a *collectorSecurityAuditor) Collect(now time.Time, input collectorSecurityInput) []*telemetryv1.Metric {
	if a == nil || !a.cfg.Enabled {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.lastAudit.IsZero() && now.Sub(a.lastAudit) < a.cfg.AuditInterval && len(a.cache) > 0 {
		return cloneSecurityMetrics(a.cache, now)
	}

	metrics := a.collectLocked(now, input)
	a.lastAudit = now
	a.cache = metrics
	return cloneSecurityMetrics(metrics, now)
}

func cloneSecurityMetrics(metrics []*telemetryv1.Metric, now time.Time) []*telemetryv1.Metric {
	if len(metrics) == 0 {
		return nil
	}
	out := make([]*telemetryv1.Metric, 0, len(metrics))
	for _, metric := range metrics {
		if metric == nil {
			continue
		}
		copyMetric := &telemetryv1.Metric{
			Name:              metric.Name,
			Value:             metric.Value,
			TimestampUnixNano: now.UnixNano(),
		}
		if len(metric.Labels) > 0 {
			copyMetric.Labels = make([]*telemetryv1.Label, 0, len(metric.Labels))
			for _, label := range metric.Labels {
				if label == nil {
					continue
				}
				copyMetric.Labels = append(copyMetric.Labels, &telemetryv1.Label{Key: label.Key, Value: label.Value})
			}
		}
		out = append(out, copyMetric)
	}
	return out
}

func (a *collectorSecurityAuditor) collectLocked(now time.Time, input collectorSecurityInput) []*telemetryv1.Metric {
	procInfo, socketOwners := scanSecurityProcessInventory()
	listeners, outbound := scanSecuritySocketRecords(socketOwners)
	permissions, drifts := a.scanSensitivePathPermissions()
	largeFiles := a.scanLargeFileGrowth()
	suid := scanDetailedSUIDSGIDBinaries()
	schedulers := a.scanSchedulerArtifacts()
	sysctls := scanRiskySysctlsDetailed()
	firewallDisabled := scanFirewallDisabled() > 0
	selinuxDisabled, apparmorDisabled := scanMACStatus()
	containerPrivileged, containerCapabilityRisk := scanContainerEscapeRisk()
	resourceByPID, resourceByName := buildProcessResourceIndex(input.Processes)

	findings := make([]collectorSecurityFinding, 0, 24)
	findings = append(findings, a.findingsFromPermissions(now, permissions, drifts)...)
	findings = append(findings, a.findingsFromSUID(now, suid)...)
	findings = append(findings, a.findingsFromLargeFiles(now, largeFiles)...)
	findings = append(findings, a.findingsFromSockets(now, listeners, outbound, input.AllowedListenPorts, resourceByPID, resourceByName)...)
	findings = append(findings, a.findingsFromProcesses(now, procInfo, resourceByPID, resourceByName)...)
	findings = append(findings, a.findingsFromSchedulers(now, schedulers)...)
	findings = append(findings, a.findingsFromKernelPosture(now, sysctls, firewallDisabled, selinuxDisabled > 0, apparmorDisabled > 0)...)
	findings = append(findings, a.findingsFromContainers(now, containerPrivileged, containerCapabilityRisk)...)
	findings = append(findings, a.findingsFromProcessProfiles(now, input.EBPFSummary.ProcessStats, resourceByPID, resourceByName)...)
	findings = append(findings, a.findingsFromEBPFEvents(now, input.EBPFEvents, resourceByPID, resourceByName)...)
	findings = dedupeCollectorSecurityFindings(findings)

	metrics := make([]*telemetryv1.Metric, 0, len(findings)+48)
	appendGauge := func(name string, value float64) {
		metrics = append(metrics, &telemetryv1.Metric{Name: name, Value: value, TimestampUnixNano: now.UnixNano()})
	}

	worldWritable := 0
	sensitiveReadable := 0
	for _, item := range permissions {
		if item.WorldWritable {
			worldWritable++
		}
		if item.SensitiveReadable {
			sensitiveReadable++
		}
	}
	unexpectedPorts := countUnexpectedListeningPorts(listeners, input.AllowedListenPorts)
	suspiciousOutbound := countSuspiciousOutbound(outbound)
	privilegedOddProcesses, parentChildAnomalies, privilegeTransitions := countSuspiciousProcesses(procInfo)
	cronAnomalies, systemdAnomalies := countSchedulerAnomalies(schedulers)
	processPortMismatch := countFindingsByCategory(findings, "process_port_mismatch")
	sensitivePathAccess := countFindingsByCategory(findings, "sensitive_path_access")
	permissionDrift := len(drifts)
	suspiciousProcesses := countFindingsByCategory(findings, "suspicious_process")
	kernelPostureDrift := countFindingsByCategory(findings, "kernel_posture")

	appendGauge("node_security_world_writable_sensitive_paths", float64(worldWritable))
	appendGauge("node_security_sensitive_readable_files_count", float64(sensitiveReadable))
	appendGauge("node_security_weak_permission_count", float64(worldWritable+sensitiveReadable+permissionDrift))
	appendGauge("node_security_permission_change_anomalies_count", float64(permissionDrift))
	appendGauge("node_security_suid_sgid_binaries_count", float64(len(suid)))
	appendGauge("node_security_large_files_count", float64(len(largeFiles)))
	appendGauge("node_security_large_file_growth_bytes", float64(totalLargeFileGrowth(largeFiles)))
	appendGauge("node_security_listening_ports_count", float64(len(uniqueSecurityPorts(listeners))))
	appendGauge("node_security_unexpected_listening_ports_count", float64(unexpectedPorts))
	appendGauge("node_security_suspicious_outbound_destinations_count", float64(suspiciousOutbound))
	appendGauge("node_security_sysctl_risky_count", float64(len(sysctls)))
	appendGauge("node_security_firewall_disabled", boolToFloat64(firewallDisabled))
	appendGauge("node_security_selinux_disabled", float64(selinuxDisabled))
	appendGauge("node_security_apparmor_disabled", float64(apparmorDisabled))
	appendGauge("node_security_privileged_unusual_path_process_count", float64(privilegedOddProcesses))
	appendGauge("node_security_abnormal_parent_child_count", float64(parentChildAnomalies))
	appendGauge("node_security_privilege_escalation_patterns_count", float64(privilegeTransitions))
	appendGauge("node_security_cron_anomalies_count", float64(cronAnomalies))
	appendGauge("node_security_systemd_unknown_units_count", float64(systemdAnomalies))
	appendGauge("node_security_scheduler_suspicious_units_count", float64(cronAnomalies+systemdAnomalies))
	appendGauge("node_security_container_privileged_count", float64(containerPrivileged))
	appendGauge("node_security_container_capability_risk_count", float64(containerCapabilityRisk))
	appendGauge("node_security_package_vulnerability_count", packageVulnerabilityHint())
	appendGauge("node_security_process_port_mismatch_count", float64(processPortMismatch))
	appendGauge("node_security_sensitive_path_access_count", float64(sensitivePathAccess))
	appendGauge("node_security_suspicious_process_count", float64(suspiciousProcesses))
	appendGauge("node_security_kernel_posture_drift_count", float64(kernelPostureDrift))

	severityCounts := map[string]int{}
	categoryCounts := map[string]int{}
	for _, finding := range findings {
		severityCounts[finding.Severity]++
		categoryCounts[finding.Category]++
		metrics = append(metrics, findingMetric(now, finding))
	}
	for severity, count := range severityCounts {
		metrics = append(metrics, &telemetryv1.Metric{
			Name:              "node_security_findings_total",
			Value:             float64(count),
			TimestampUnixNano: now.UnixNano(),
			Labels:            buildLabels(map[string]string{"severity": severity}),
		})
	}
	for category, count := range categoryCounts {
		metrics = append(metrics, &telemetryv1.Metric{
			Name:              "node_security_findings_by_category_total",
			Value:             float64(count),
			TimestampUnixNano: now.UnixNano(),
			Labels:            buildLabels(map[string]string{"category": category}),
		})
	}

	sort.Slice(metrics, func(i, j int) bool {
		if metrics[i] == nil || metrics[j] == nil {
			return i < j
		}
		if metrics[i].Name != metrics[j].Name {
			return metrics[i].Name < metrics[j].Name
		}
		return labelString(metrics[i].Labels) < labelString(metrics[j].Labels)
	})
	return metrics
}

func labelString(labels []*telemetryv1.Label) string {
	if len(labels) == 0 {
		return ""
	}
	parts := make([]string, 0, len(labels))
	for _, label := range labels {
		if label == nil {
			continue
		}
		parts = append(parts, label.Key+"="+label.Value)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func findingMetric(now time.Time, finding collectorSecurityFinding) *telemetryv1.Metric {
	labels := map[string]string{
		"finding_id":   finding.FindingID,
		"category":     finding.Category,
		"severity":     finding.Severity,
		"scope":        finding.Scope,
		"summary":      truncateOutput(finding.Summary, maxLabelValueRunes),
		"evidence":     truncateOutput(strings.Join(finding.Evidence, " || "), maxLabelValueRunes),
		"next_step":    truncateOutput(finding.RecommendedNextStep, maxLabelValueRunes),
		"source":       finding.Source,
		"confidence":   fmt.Sprintf("%.2f", finding.Confidence),
		"ts_unix_nano": strconv.FormatInt(finding.Timestamp.UnixNano(), 10),
	}
	if finding.PID != "" {
		labels["pid"] = finding.PID
	}
	if finding.Process != "" {
		labels["process"] = finding.Process
	}
	if finding.Path != "" {
		labels["path"] = truncateOutput(finding.Path, maxLabelValueRunes)
	}
	if finding.RemoteIP != "" {
		labels["remote_ip"] = finding.RemoteIP
	}
	if finding.Port > 0 {
		labels["port"] = strconv.Itoa(finding.Port)
	}
	return &telemetryv1.Metric{
		Name:              "node_security_finding",
		Value:             clampSecurityConfidence(finding.Confidence),
		TimestampUnixNano: now.UnixNano(),
		Labels:            buildLabels(labels),
	}
}

func dedupeCollectorSecurityFindings(findings []collectorSecurityFinding) []collectorSecurityFinding {
	if len(findings) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(findings))
	out := make([]collectorSecurityFinding, 0, len(findings))
	for _, finding := range findings {
		key := strings.TrimSpace(finding.FindingID)
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(finding.Category + ":" + finding.Summary))
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		finding.Evidence = dedupeStringsLocal(finding.Evidence)
		out = append(out, finding)
	}
	sort.Slice(out, func(i, j int) bool {
		if securitySeverityRank(out[i].Severity) != securitySeverityRank(out[j].Severity) {
			return securitySeverityRank(out[i].Severity) > securitySeverityRank(out[j].Severity)
		}
		if out[i].Confidence != out[j].Confidence {
			return out[i].Confidence > out[j].Confidence
		}
		return out[i].FindingID < out[j].FindingID
	})
	return out
}

func securitySeverityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	default:
		return 1
	}
}

func (a *collectorSecurityAuditor) findingsFromPermissions(now time.Time, observations []securityPermissionObservation, drifts []securityPermissionDrift) []collectorSecurityFinding {
	findings := make([]collectorSecurityFinding, 0, 3)
	worldWritable := make([]string, 0, 8)
	sensitiveReadable := make([]string, 0, 8)
	for _, item := range observations {
		if item.WorldWritable {
			worldWritable = append(worldWritable, fmt.Sprintf("%s mode=%#o", item.Path, item.Mode.Perm()))
		}
		if item.SensitiveReadable {
			sensitiveReadable = append(sensitiveReadable, fmt.Sprintf("%s mode=%#o", item.Path, item.Mode.Perm()))
		}
	}
	if len(worldWritable) > 0 {
		severity := "high"
		confidence := 0.84
		if len(worldWritable) >= 8 {
			severity = "critical"
			confidence = 0.92
		}
		findings = append(findings, a.newFinding(now, "filesystem_permissions", severity, "host", "Sensitive paths are world-writable", worldWritable, confidence, "Remove world-writable bits from sensitive paths and restore owner/group policy.", "collector_security_audit"))
	}
	if len(sensitiveReadable) > 0 {
		findings = append(findings, a.newFinding(now, "filesystem_permissions", "high", "host", "Sensitive files are readable beyond the expected owner/group boundary", sensitiveReadable, 0.81, "Restrict readable bits on secrets, keys, and backup artifacts.", "collector_security_audit"))
	}
	if len(drifts) > 0 {
		evidence := make([]string, 0, len(drifts))
		for _, drift := range drifts {
			evidence = append(evidence, fmt.Sprintf("%s %#o->%#o", drift.Path, drift.Previous.Perm(), drift.Current.Perm()))
		}
		findings = append(findings, a.newFinding(now, "permission_drift", "high", "host", "Sensitive path permissions drifted toward a weaker state", evidence, 0.87, "Review recent chmod/chown activity and restore expected ACLs or file modes.", "collector_security_audit"))
	}
	return findings
}

func (a *collectorSecurityAuditor) findingsFromSUID(now time.Time, observations []securitySUIDObservation) []collectorSecurityFinding {
	if len(observations) == 0 {
		return nil
	}
	anomalous := make([]string, 0, len(observations))
	for _, item := range observations {
		if strings.HasPrefix(item.Path, "/usr/local/") || strings.HasPrefix(item.Path, "/opt/") || item.Owner != 0 {
			anomalous = append(anomalous, fmt.Sprintf("%s owner=%d mode=%#o", item.Path, item.Owner, item.Mode.Perm()))
		}
	}
	if len(anomalous) == 0 && len(observations) <= 80 {
		return nil
	}
	severity := "medium"
	confidence := 0.58
	if len(anomalous) > 0 {
		severity = "high"
		confidence = 0.83
	}
	evidence := anomalous
	if len(evidence) == 0 {
		evidence = []string{fmt.Sprintf("suid_sgid_inventory=%d", len(observations))}
	}
	return []collectorSecurityFinding{a.newFinding(now, "suid_sgid_anomaly", severity, "host", "SUID/SGID inventory contains risky or non-baseline binaries", evidence, confidence, "Validate SUID/SGID inventory and remove elevated bits from non-essential binaries.", "collector_security_audit")}
}

func (a *collectorSecurityAuditor) findingsFromLargeFiles(now time.Time, observations []securityLargeFileObservation) []collectorSecurityFinding {
	if len(observations) == 0 {
		return nil
	}
	evidence := make([]string, 0, len(observations))
	for _, item := range observations {
		if item.Growth >= a.cfg.RapidGrowthThresholdB || strings.HasPrefix(item.Path, "/tmp/") || strings.HasPrefix(item.Path, "/var/tmp/") {
			evidence = append(evidence, fmt.Sprintf("%s size=%d growth=%d", item.Path, item.Size, item.Growth))
		}
	}
	if len(evidence) == 0 {
		return nil
	}
	severity := "medium"
	confidence := 0.66
	if totalLargeFileGrowth(observations) >= a.cfg.RapidGrowthThresholdB*4 {
		severity = "high"
		confidence = 0.79
	}
	return []collectorSecurityFinding{a.newFinding(now, "large_file_growth", severity, "host", "Rapid file growth or oversized files detected in writable paths", evidence, confidence, "Inspect growing files for unexpected dumps, archive staging, or exfiltration staging.", "collector_security_audit")}
}

func (a *collectorSecurityAuditor) findingsFromSockets(now time.Time, listeners, outbound []securitySocketRecord, allowedPorts []int, resourceByPID map[int]securityProcessResource, resourceByName map[string]securityProcessResource) []collectorSecurityFinding {
	findings := make([]collectorSecurityFinding, 0, 4)
	unexpected := make([]string, 0, 12)
	mismatches := make([]string, 0, 12)
	outboundEvidence := make([]string, 0, 12)

	for _, item := range listeners {
		processKey := normalizeProcessKey(item.Process, item.ExePath)
		newPort, ownerChanged, lowReputation := a.observePort(item.Port, processKey, now)
		if !knownSecurityServicePort(item.Port, allowedPorts) || newPort || ownerChanged || lowReputation {
			evidence := fmt.Sprintf("port=%d process=%s pid=%d exe=%s", item.Port, firstNonEmptyLocal(item.Process, "unknown"), item.PID, item.ExePath)
			if _, extra := resourceEvidence(item.PID, item.Process, resourceByPID, resourceByName); len(extra) > 0 {
				evidence += " " + strings.Join(extra, " ")
			}
			unexpected = append(unexpected, evidence)
		}
		if looksLikeProcessPortMismatch(item.Port, item.Process, item.ExePath) {
			mismatches = append(mismatches, fmt.Sprintf("port=%d process=%s pid=%d exe=%s", item.Port, firstNonEmptyLocal(item.Process, "unknown"), item.PID, item.ExePath))
		}
	}

	for _, item := range outbound {
		if !isSecurityRemoteIPExternal(item.RemoteIP) {
			continue
		}
		processKey := normalizeProcessKey(item.Process, item.ExePath)
		_, _, lowReputation := a.observeOutbound(processKey+"->"+item.RemoteIP, processKey, now)
		if !lowReputation && !looksSuspiciousOutboundProcess(item.Process, item.ExePath) {
			continue
		}
		evidence := fmt.Sprintf("remote=%s:%d process=%s pid=%d", item.RemoteIP, item.RemotePort, firstNonEmptyLocal(item.Process, "unknown"), item.PID)
		if _, extra := resourceEvidence(item.PID, item.Process, resourceByPID, resourceByName); len(extra) > 0 {
			evidence += " " + strings.Join(extra, " ")
		}
		outboundEvidence = append(outboundEvidence, evidence)
	}

	if len(unexpected) > 0 {
		severity := "high"
		confidence := 0.82
		if len(unexpected) >= 6 {
			severity = "critical"
			confidence = 0.91
		}
		findings = append(findings, a.newFinding(now, "network_exposure", severity, "host", "Unexpected listening ports or new listeners were observed", unexpected, confidence, "Close unmanaged listeners and map every exposed port to approved service inventory.", "collector_security_audit"))
	}
	if len(mismatches) > 0 {
		findings = append(findings, a.newFinding(now, "process_port_mismatch", "high", "host", "Sensitive ports are owned by non-baseline processes", mismatches, 0.86, "Validate privileged port ownership and restrict binds to the approved service binary.", "collector_security_audit"))
	}
	if len(outboundEvidence) > 0 {
		findings = append(findings, a.newFinding(now, "unexpected_outbound", "high", "runtime", "Unexpected outbound connections escaped the local baseline", outboundEvidence, 0.8, "Validate destination reputation and tighten egress policy for non-baseline endpoints.", "collector_security_audit"))
	}
	return findings
}

func (a *collectorSecurityAuditor) findingsFromProcesses(now time.Time, procInfo map[int]securityProcessInfo, resourceByPID map[int]securityProcessResource, resourceByName map[string]securityProcessResource) []collectorSecurityFinding {
	if len(procInfo) == 0 {
		return nil
	}
	findings := make([]collectorSecurityFinding, 0, 3)
	suspiciousProc := make([]string, 0, 12)
	parentChild := make([]string, 0, 12)
	privEsc := make([]string, 0, 12)
	for pid, proc := range procInfo {
		if isSuspiciousExecutablePath(proc.ExePath) || looksSuspiciousProcessName(proc.Name, proc.ExePath, proc.Cmdline) {
			evidence := fmt.Sprintf("pid=%d name=%s exe=%s", pid, firstNonEmptyLocal(proc.Name, "unknown"), proc.ExePath)
			if _, extra := resourceEvidence(pid, proc.Name, resourceByPID, resourceByName); len(extra) > 0 {
				evidence += " " + strings.Join(extra, " ")
			}
			suspiciousProc = append(suspiciousProc, evidence)
		}
		if proc.EUID == 0 && proc.UID != 0 {
			privEsc = append(privEsc, fmt.Sprintf("pid=%d name=%s uid=%d euid=%d exe=%s", pid, proc.Name, proc.UID, proc.EUID, proc.ExePath))
		} else if proc.UID != 0 && strings.TrimSpace(proc.CapEff) != "" && proc.CapEff != "0000000000000000" {
			privEsc = append(privEsc, fmt.Sprintf("pid=%d name=%s uid=%d cap_eff=%s", pid, proc.Name, proc.UID, proc.CapEff))
		}
	}
	for _, proc := range procInfo {
		parent, ok := procInfo[proc.PPID]
		if !ok {
			continue
		}
		if isSuspiciousParentChild(parent, proc) {
			parentChild = append(parentChild, fmt.Sprintf("%s(%d)->%s(%d)", parent.Name, parent.PID, proc.Name, proc.PID))
		}
	}
	if len(suspiciousProc) > 0 {
		severity := "high"
		confidence := 0.81
		if countRootSuspiciousProcesses(procInfo) > 0 {
			severity = "critical"
			confidence = 0.9
		}
		findings = append(findings, a.newFinding(now, "suspicious_process", severity, "runtime", "Suspicious or unusual processes were found on the host", suspiciousProc, confidence, "Validate binary provenance, process lineage, and whether the executable path is writable by untrusted users.", "collector_security_audit"))
	}
	if len(parentChild) > 0 {
		findings = append(findings, a.newFinding(now, "abnormal_parent_child", "high", "runtime", "Abnormal parent-child process chains were observed", parentChild, 0.83, "Inspect process lineage and look for service-to-shell or downloader-to-interpreter pivots.", "collector_security_audit"))
	}
	if len(privEsc) > 0 {
		findings = append(findings, a.newFinding(now, "privilege_escalation", "critical", "runtime", "Privilege escalation patterns were observed in live processes", privEsc, 0.92, "Freeze the affected workload and validate recent account/capability transitions.", "collector_security_audit"))
	}
	return findings
}

func (a *collectorSecurityAuditor) findingsFromSchedulers(now time.Time, observations []securitySchedulerObservation) []collectorSecurityFinding {
	if len(observations) == 0 {
		return nil
	}
	cronEvidence := make([]string, 0, 8)
	systemdEvidence := make([]string, 0, 8)
	for _, item := range observations {
		evidence := fmt.Sprintf("%s=%s reason=%s exec=%s", item.Kind, item.Path, item.Reason, item.Executable)
		if item.Kind == "cron" {
			cronEvidence = append(cronEvidence, evidence)
		} else {
			systemdEvidence = append(systemdEvidence, evidence)
		}
	}
	findings := make([]collectorSecurityFinding, 0, 2)
	if len(cronEvidence) > 0 {
		findings = append(findings, a.newFinding(now, "scheduler_behavior", "medium", "host", "Suspicious cron configuration or drift detected", cronEvidence, 0.72, "Review cron jobs for temporary-path execution, backup staging, or unmanaged persistence.", "collector_security_audit"))
	}
	if len(systemdEvidence) > 0 {
		findings = append(findings, a.newFinding(now, "scheduler_behavior", "high", "host", "Suspicious systemd units or service drift detected", systemdEvidence, 0.8, "Review systemd unit provenance and ensure ExecStart paths are immutable and approved.", "collector_security_audit"))
	}
	return findings
}

func (a *collectorSecurityAuditor) findingsFromKernelPosture(now time.Time, sysctls []securitySysctlObservation, firewallDisabled, selinuxDisabled, apparmorDisabled bool) []collectorSecurityFinding {
	findings := make([]collectorSecurityFinding, 0, 2)
	if len(sysctls) > 0 || firewallDisabled || selinuxDisabled || apparmorDisabled {
		evidence := make([]string, 0, len(sysctls)+3)
		for _, item := range sysctls {
			evidence = append(evidence, fmt.Sprintf("%s=%d (%s)", item.Path, item.Value, item.Reason))
		}
		if firewallDisabled {
			evidence = append(evidence, "firewall=disabled")
		}
		if selinuxDisabled {
			evidence = append(evidence, "selinux=disabled")
		}
		if apparmorDisabled {
			evidence = append(evidence, "apparmor=disabled")
		}
		severity := "high"
		confidence := 0.81
		if firewallDisabled && (selinuxDisabled || apparmorDisabled) {
			severity = "critical"
			confidence = 0.9
		}
		findings = append(findings, a.newFinding(now, "kernel_posture", severity, "host", "Kernel or host security posture drifted from hardened defaults", evidence, confidence, "Restore sysctl hardening and ensure MAC/firewall controls are active on the host.", "collector_security_audit"))
	}
	return findings
}

func (a *collectorSecurityAuditor) findingsFromContainers(now time.Time, privileged, capabilityRisk int) []collectorSecurityFinding {
	if privileged == 0 && capabilityRisk == 0 {
		return nil
	}
	evidence := []string{}
	if privileged > 0 {
		evidence = append(evidence, "container_privileged_or_high_capability_context=1")
	}
	if capabilityRisk > 0 {
		evidence = append(evidence, "cap_eff_non_zero=1")
	}
	severity := "medium"
	confidence := 0.68
	if privileged > 0 && capabilityRisk > 0 {
		severity = "high"
		confidence = 0.8
	}
	return []collectorSecurityFinding{a.newFinding(now, "container_runtime_posture", severity, "host", "Container runtime context exposes elevated capabilities", evidence, confidence, "Reduce container capabilities and avoid privileged runtime settings unless operationally required.", "collector_security_audit")}
}

func (a *collectorSecurityAuditor) findingsFromProcessProfiles(now time.Time, stats []ebpfcore.ProcessStatsSnapshot, resourceByPID map[int]securityProcessResource, resourceByName map[string]securityProcessResource) []collectorSecurityFinding {
	if len(stats) == 0 {
		return nil
	}
	evidence := make([]string, 0, 8)
	for _, item := range stats {
		key := normalizeProcessKey(item.Comm, "")
		if key == "" {
			continue
		}
		profile := a.processBaseline[key]
		if profile == nil {
			profile = &securityProcessProfile{}
			a.processBaseline[key] = profile
		}

		resource := resourceByPID[item.PID]
		if resource.CPUPercent == 0 {
			resource = resourceByName[strings.ToLower(strings.TrimSpace(item.Comm))]
		}
		connects := float64(item.ConnectCalls)
		binds := float64(item.BindCalls)
		execs := float64(item.ExecCalls)
		forks := float64(item.ForkCalls)
		opens := float64(item.OpenCalls)
		rss := float64(resource.RSSBytes)
		cpu := resource.CPUPercent

		if profile.Observations >= a.cfg.BaselineWarmupSamples {
			connectSpike := connects > maxFloatLocal(profile.AvgConnect*3, 30)
			execSpike := execs > maxFloatLocal(profile.AvgExec*3, 8)
			forkSpike := forks > maxFloatLocal(profile.AvgFork*3, 16)
			openSpike := opens > maxFloatLocal(profile.AvgOpen*3, 80)
			bindSpike := binds > maxFloatLocal(profile.AvgBind*3, 4)
			if connectSpike || execSpike || forkSpike || openSpike || bindSpike {
				parts := []string{fmt.Sprintf("process=%s pid=%d connect=%.0f bind=%.0f exec=%.0f fork=%.0f open=%.0f", item.Comm, item.PID, connects, binds, execs, forks, opens)}
				if _, extra := resourceEvidence(item.PID, item.Comm, resourceByPID, resourceByName); len(extra) > 0 {
					parts = append(parts, strings.Join(extra, " "))
				}
				evidence = append(evidence, strings.Join(parts, " "))
			}
		}

		profile.Observations++
		profile.AvgConnect = rollingAverage(profile.AvgConnect, connects, profile.Observations)
		profile.AvgBind = rollingAverage(profile.AvgBind, binds, profile.Observations)
		profile.AvgExec = rollingAverage(profile.AvgExec, execs, profile.Observations)
		profile.AvgFork = rollingAverage(profile.AvgFork, forks, profile.Observations)
		profile.AvgOpen = rollingAverage(profile.AvgOpen, opens, profile.Observations)
		profile.AvgCPU = rollingAverage(profile.AvgCPU, cpu, profile.Observations)
		profile.AvgRSS = rollingAverage(profile.AvgRSS, rss, profile.Observations)
		profile.LastSeen = now
	}
	if len(evidence) == 0 {
		return nil
	}
	return []collectorSecurityFinding{a.newFinding(now, "process_behavior_profile", "high", "runtime", "Per-process behavior deviated sharply from the local baseline", evidence, 0.79, "Inspect the highlighted process for recent deployment changes, suspicious bursts, or abuse patterns.", "collector_security_audit")}
}

func (a *collectorSecurityAuditor) findingsFromEBPFEvents(now time.Time, events []probe.EBPFEvent, resourceByPID map[int]securityProcessResource, resourceByName map[string]securityProcessResource) []collectorSecurityFinding {
	if len(events) == 0 {
		return nil
	}
	tmpExec := make([]string, 0, 8)
	sensitiveAccess := make([]string, 0, 8)
	privEsc := make([]string, 0, 8)
	abnormalBind := make([]string, 0, 8)
	suspiciousOutbound := make([]string, 0, 8)
	connectBursts := map[string]int{}
	for _, event := range events {
		etype := strings.ToLower(strings.TrimSpace(event.Type))
		path := strings.ToLower(strings.TrimSpace(event.Path))
		switch {
		case etype == "execve" && isSuspiciousExecutablePath(path):
			entry := fmt.Sprintf("%s pid=%d path=%s", event.EvidenceID, event.PID, event.Path)
			if _, extra := resourceEvidence(event.PID, event.Comm, resourceByPID, resourceByName); len(extra) > 0 {
				entry += " " + strings.Join(extra, " ")
			}
			tmpExec = append(tmpExec, entry)
		case etype == "privilege_escalation":
			privEsc = append(privEsc, fmt.Sprintf("%s pid=%d %s", event.EvidenceID, event.PID, truncateOutput(event.Description, 120)))
		case etype == "abnormal_bind_port" || (etype == "bind" && event.Port > 0 && !knownSecurityServicePort(event.Port, nil)):
			abnormalBind = append(abnormalBind, fmt.Sprintf("%s pid=%d port=%d process=%s", event.EvidenceID, event.PID, event.Port, event.Comm))
		case strings.HasPrefix(path, "/etc/") || strings.HasPrefix(path, "/root/") || strings.Contains(path, "/.ssh/"):
			sensitiveAccess = append(sensitiveAccess, fmt.Sprintf("%s pid=%d path=%s type=%s", event.EvidenceID, event.PID, event.Path, etype))
		}
		if etype == "connect" {
			bucket := event.Timestamp.UTC().Format("2006-01-02T15:04")
			connectBursts[bucket]++
			if isSecurityRemoteIPExternal(event.RemoteIP) {
				suspiciousOutbound = append(suspiciousOutbound, fmt.Sprintf("%s pid=%d remote=%s process=%s", event.EvidenceID, event.PID, event.RemoteIP, event.Comm))
			}
		}
	}
	findings := make([]collectorSecurityFinding, 0, 5)
	if len(tmpExec) > 0 {
		findings = append(findings, a.newFinding(now, "execution_from_suspicious_path", "high", "runtime", "Execution from temporary or user-writable paths was observed", tmpExec, 0.88, "Block execution from writable temporary paths and inspect the parent process lineage.", "collector_security_audit"))
	}
	if len(sensitiveAccess) > 0 {
		findings = append(findings, a.newFinding(now, "sensitive_path_access", "high", "runtime", "Runtime access to sensitive files or directories was observed", sensitiveAccess, 0.84, "Confirm whether access to system secrets or root-owned paths was expected for the workload.", "collector_security_audit"))
	}
	if len(privEsc) > 0 {
		findings = append(findings, a.newFinding(now, "privilege_escalation", "critical", "runtime", "eBPF detected privilege escalation behavior", privEsc, 0.94, "Freeze the affected workload and capture process lineage and credentials for triage.", "collector_security_audit"))
	}
	if len(abnormalBind) > 0 {
		findings = append(findings, a.newFinding(now, "network_exposure", "high", "runtime", "eBPF detected abnormal port binds outside the steady-state baseline", abnormalBind, 0.87, "Validate new listeners against service inventory and close unexpected bind paths.", "collector_security_audit"))
	}
	maxBurst := 0
	for _, count := range connectBursts {
		if count > maxBurst {
			maxBurst = count
		}
	}
	if len(suspiciousOutbound) > 0 || maxBurst >= 30 {
		evidence := suspiciousOutbound
		if maxBurst >= 30 {
			evidence = append(evidence, fmt.Sprintf("connect_burst_per_minute=%d", maxBurst))
		}
		findings = append(findings, a.newFinding(now, "unexpected_outbound", "high", "runtime", "eBPF observed suspicious outbound behavior or connection bursts", evidence, 0.8, "Validate destination reputation and correlate the burst with deployment or process ownership.", "collector_security_audit"))
	}
	return findings
}

func (a *collectorSecurityAuditor) newFinding(now time.Time, category, severity, scope, summary string, evidence []string, confidence float64, nextStep, source string) collectorSecurityFinding {
	evidence = dedupeStringsLocal(evidence)
	sort.Strings(evidence)
	return collectorSecurityFinding{
		FindingID:           securityFindingID(category, severity, summary, evidence),
		Category:            strings.TrimSpace(category),
		Severity:            strings.TrimSpace(severity),
		Scope:               strings.TrimSpace(scope),
		Summary:             strings.TrimSpace(summary),
		Evidence:            evidence,
		Confidence:          clampSecurityConfidence(confidence),
		Timestamp:           now.UTC(),
		RecommendedNextStep: strings.TrimSpace(nextStep),
		Source:              strings.TrimSpace(source),
	}
}

func (a *collectorSecurityAuditor) observePort(port int, actor string, now time.Time) (bool, bool, bool) {
	profile := a.portBaseline[port]
	if profile == nil {
		a.portBaseline[port] = &securityObservationProfile{Count: 1, FirstSeen: now, LastSeen: now, LastActor: actor}
		return true, false, true
	}
	ownerChanged := profile.LastActor != "" && actor != "" && profile.LastActor != actor
	profile.Count++
	profile.LastSeen = now
	if actor != "" {
		profile.LastActor = actor
	}
	return false, ownerChanged, profile.Count < a.cfg.BaselineWarmupSamples
}

func (a *collectorSecurityAuditor) observeOutbound(key, actor string, now time.Time) (bool, bool, bool) {
	profile := a.outboundBaseline[key]
	if profile == nil {
		a.outboundBaseline[key] = &securityObservationProfile{Count: 1, FirstSeen: now, LastSeen: now, LastActor: actor}
		return true, false, true
	}
	actorChanged := profile.LastActor != "" && actor != "" && profile.LastActor != actor
	profile.Count++
	profile.LastSeen = now
	if actor != "" {
		profile.LastActor = actor
	}
	return false, actorChanged, profile.Count < a.cfg.BaselineWarmupSamples
}

func (a *collectorSecurityAuditor) scanSensitivePathPermissions() ([]securityPermissionObservation, []securityPermissionDrift) {
	roots := []string{"/etc", "/opt", "/var/lib", "/home"}
	sensitiveParts := []string{".env", ".pem", ".key", "id_rsa", "id_ed25519", "authorized_keys", "shadow", "passwd", "backup", ".bak"}
	observations := make([]securityPermissionObservation, 0, 32)
	drifts := make([]securityPermissionDrift, 0, 16)
	seen := 0
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if seen >= a.cfg.MaxWalkEntries {
				return filepath.SkipDir
			}
			seen++
			if d.IsDir() {
				if depthFromAuditRoot(root, path) > 4 {
					return filepath.SkipDir
				}
				info, infoErr := d.Info()
				if infoErr == nil && info.Mode().Perm()&0o002 != 0 {
					observations = append(observations, securityPermissionObservation{Path: path, Mode: info.Mode(), WorldWritable: true})
				}
				return nil
			}
			info, infoErr := d.Info()
			if infoErr != nil {
				return nil
			}
			mode := info.Mode()
			entry := securityPermissionObservation{Path: path, Mode: mode}
			if mode.Perm()&0o002 != 0 {
				entry.WorldWritable = true
			}
			name := strings.ToLower(filepath.Base(path))
			for _, part := range sensitiveParts {
				if strings.Contains(name, part) && (mode.Perm()&0o004 != 0 || mode.Perm()&0o040 != 0) {
					entry.SensitiveReadable = true
					break
				}
			}
			if entry.WorldWritable || entry.SensitiveReadable {
				observations = append(observations, entry)
			}
			if previous, ok := a.fileModes[path]; ok && permissionsWeakened(previous, mode) {
				drifts = append(drifts, securityPermissionDrift{Path: path, Previous: previous, Current: mode})
			}
			a.fileModes[path] = mode
			return nil
		})
	}
	return observations, drifts
}

func (a *collectorSecurityAuditor) scanLargeFileGrowth() []securityLargeFileObservation {
	roots := []string{"/var/log", "/var/lib", "/tmp", "/var/tmp"}
	observations := make([]securityLargeFileObservation, 0, 16)
	seen := 0
	currentSizes := make(map[string]int64, 128)
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if depthFromAuditRoot(root, path) > 4 {
					return filepath.SkipDir
				}
				return nil
			}
			if seen >= a.cfg.MaxWalkEntries {
				return filepath.SkipDir
			}
			seen++
			info, infoErr := d.Info()
			if infoErr != nil {
				return nil
			}
			size := info.Size()
			if size < a.cfg.LargeFileThresholdBytes {
				return nil
			}
			growth := int64(0)
			if prev, ok := a.fileSizes[path]; ok && size > prev {
				growth = size - prev
			}
			currentSizes[path] = size
			observations = append(observations, securityLargeFileObservation{Path: path, Size: size, Growth: growth})
			return nil
		})
	}
	a.fileSizes = currentSizes
	return observations
}

func (a *collectorSecurityAuditor) scanSchedulerArtifacts() []securitySchedulerObservation {
	out := make([]securitySchedulerObservation, 0, 16)
	cronRoots := []string{"/etc/cron.d", "/etc/cron.daily", "/etc/cron.hourly"}
	now := time.Now().UTC()
	for _, root := range cronRoots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			path := filepath.Join(root, entry.Name())
			info, infoErr := entry.Info()
			if infoErr != nil {
				continue
			}
			reason := ""
			body, _ := os.ReadFile(path)
			text := strings.ToLower(truncateOutput(string(body), 512))
			if info.Mode().Perm()&0o002 != 0 {
				reason = "world_writable"
			} else if strings.Contains(entry.Name(), "tmp") || strings.Contains(text, "/tmp/") || strings.Contains(text, "/var/tmp/") {
				reason = "temporary_path_execution"
			} else if strings.Contains(text, "curl ") || strings.Contains(text, "wget ") || strings.Contains(text, "nc ") || strings.Contains(text, "bash -c") {
				reason = "network_or_shell_launcher"
			}
			if reason == "" {
				continue
			}
			key := "cron:" + path
			_, _, lowRep := a.observeScheduler(key, entry.Name(), now)
			if lowRep || reason != "" {
				out = append(out, securitySchedulerObservation{Kind: "cron", Name: entry.Name(), Path: path, Reason: reason})
			}
		}
	}
	if entries, err := os.ReadDir("/etc/systemd/system"); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := strings.ToLower(entry.Name())
			if !strings.HasSuffix(name, ".service") && !strings.HasSuffix(name, ".timer") {
				continue
			}
			path := filepath.Join("/etc/systemd/system", entry.Name())
			data, _ := os.ReadFile(path)
			exec := extractSystemdExecStart(string(data))
			low := strings.ToLower(exec)
			reason := ""
			if strings.Contains(name, "tmp") || strings.Contains(name, "debug") || strings.Contains(low, "/tmp/") || strings.Contains(low, "/var/tmp/") {
				reason = "temporary_or_debug_unit"
			} else if strings.Contains(low, "curl ") || strings.Contains(low, "wget ") || strings.Contains(low, "bash -c") || strings.Contains(low, "nc ") {
				reason = "network_or_shell_launcher"
			}
			if reason == "" {
				continue
			}
			key := "systemd:" + path
			_, _, lowRep := a.observeScheduler(key, entry.Name(), now)
			if lowRep || reason != "" {
				out = append(out, securitySchedulerObservation{Kind: "systemd", Name: entry.Name(), Path: path, Reason: reason, Executable: exec})
			}
		}
	}
	return out
}

func (a *collectorSecurityAuditor) observeScheduler(key, actor string, now time.Time) (bool, bool, bool) {
	profile := a.schedulerBaseline[key]
	if profile == nil {
		a.schedulerBaseline[key] = &securityObservationProfile{Count: 1, FirstSeen: now, LastSeen: now, LastActor: actor}
		return true, false, true
	}
	actorChanged := profile.LastActor != "" && actor != "" && profile.LastActor != actor
	profile.Count++
	profile.LastSeen = now
	if actor != "" {
		profile.LastActor = actor
	}
	return false, actorChanged, profile.Count < a.cfg.BaselineWarmupSamples
}

func scanSecurityProcessInventory() (map[int]securityProcessInfo, map[string]securityProcessInfo) {
	processes := make(map[int]securityProcessInfo, 256)
	owners := make(map[string]securityProcessInfo, 512)
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return processes, owners
	}
	processed := 0
	for _, entry := range entries {
		if processed >= securityMaxTrackedProcesses || !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		status, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "status"))
		if err != nil {
			continue
		}
		info := parseSecurityProcessStatus(pid, string(status))
		info.ExePath, _ = os.Readlink(filepath.Join("/proc", entry.Name(), "exe"))
		cmdline, _ := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		info.Cmdline = strings.TrimSpace(strings.ReplaceAll(string(cmdline), "\x00", " "))
		processes[pid] = info
		processed++
	}
	for pid, info := range processes {
		fds, err := os.ReadDir(filepath.Join("/proc", strconv.Itoa(pid), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
				continue
			}
			inode := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
			if _, ok := owners[inode]; !ok {
				owners[inode] = info
			}
		}
	}
	return processes, owners
}

func parseSecurityProcessStatus(pid int, status string) securityProcessInfo {
	info := securityProcessInfo{PID: pid, UID: -1, EUID: -1}
	scanner := bufio.NewScanner(strings.NewReader(status))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "Name:"):
			info.Name = strings.TrimSpace(strings.TrimPrefix(line, "Name:"))
		case strings.HasPrefix(line, "PPid:"):
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				info.PPID, _ = strconv.Atoi(fields[1])
			}
		case strings.HasPrefix(line, "Uid:"):
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				info.UID, _ = strconv.Atoi(fields[1])
				info.EUID, _ = strconv.Atoi(fields[2])
			}
		case strings.HasPrefix(line, "CapEff:"):
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				info.CapEff = strings.TrimSpace(fields[1])
			}
		}
	}
	return info
}

func scanSecuritySocketRecords(owners map[string]securityProcessInfo) ([]securitySocketRecord, []securitySocketRecord) {
	listeners := make([]securitySocketRecord, 0, 32)
	outbound := make([]securitySocketRecord, 0, 64)
	for _, item := range parseProcNetSockets("/proc/net/tcp", false, owners) {
		if item.state == "0A" {
			listeners = append(listeners, item.record)
		} else if item.state == "01" {
			outbound = append(outbound, item.record)
		}
	}
	for _, item := range parseProcNetSockets("/proc/net/tcp6", true, owners) {
		if item.state == "0A" {
			listeners = append(listeners, item.record)
		} else if item.state == "01" {
			outbound = append(outbound, item.record)
		}
	}
	return listeners, outbound
}

type procNetRecord struct {
	state  string
	record securitySocketRecord
}

func parseProcNetSockets(path string, v6 bool, owners map[string]securityProcessInfo) []procNetRecord {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	out := make([]procNetRecord, 0, 64)
	scanner := bufio.NewScanner(file)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum == 1 {
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}
		_, localPort := decodeProcAddr(fields[1], v6)
		remoteHost, remotePort := decodeProcAddr(fields[2], v6)
		inode := fields[9]
		owner := owners[inode]
		out = append(out, procNetRecord{
			state: fields[3],
			record: securitySocketRecord{
				Proto:      filepath.Base(path),
				Port:       localPort,
				RemoteIP:   ipString(remoteHost),
				RemotePort: remotePort,
				PID:        owner.PID,
				PPID:       owner.PPID,
				Process:    owner.Name,
				ExePath:    owner.ExePath,
			},
		})
	}
	return out
}

func decodeProcAddr(raw string, v6 bool) (net.IP, int) {
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return nil, 0
	}
	port, err := strconv.ParseInt(parts[1], 16, 32)
	if err != nil {
		return nil, 0
	}
	return decodeProcIPHex(parts[0], v6), int(port)
}

func decodeProcIPHex(raw string, v6 bool) net.IP {
	if !v6 {
		if len(raw) != 8 {
			return nil
		}
		b1, err1 := strconv.ParseUint(raw[6:8], 16, 8)
		b2, err2 := strconv.ParseUint(raw[4:6], 16, 8)
		b3, err3 := strconv.ParseUint(raw[2:4], 16, 8)
		b4, err4 := strconv.ParseUint(raw[0:2], 16, 8)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			return nil
		}
		return net.IPv4(byte(b1), byte(b2), byte(b3), byte(b4))
	}
	if len(raw) != 32 {
		return nil
	}
	ip := make(net.IP, net.IPv6len)
	for i := 0; i < 16; i++ {
		v, err := strconv.ParseUint(raw[(15-i)*2:(15-i)*2+2], 16, 8)
		if err != nil {
			return nil
		}
		ip[i] = byte(v)
	}
	return ip
}

func scanDetailedSUIDSGIDBinaries() []securitySUIDObservation {
	dirs := []string{"/bin", "/sbin", "/usr/bin", "/usr/sbin", "/usr/local/bin", "/opt/bin"}
	out := make([]securitySUIDObservation, 0, 64)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			mode := info.Mode()
			if mode&os.ModeSetuid == 0 && mode&os.ModeSetgid == 0 {
				continue
			}
			owner := 0
			if stat, ok := info.Sys().(*unix.Stat_t); ok {
				owner = int(stat.Uid)
			}
			out = append(out, securitySUIDObservation{Path: filepath.Join(dir, entry.Name()), Mode: mode, Owner: owner})
		}
	}
	return out
}

func scanRiskySysctlsDetailed() []securitySysctlObservation {
	targets := map[string]func(int64) (bool, string){
		"/proc/sys/net/ipv4/conf/all/accept_redirects": func(v int64) (bool, string) { return v == 1, "accept_redirects_enabled" },
		"/proc/sys/net/ipv4/conf/all/send_redirects":   func(v int64) (bool, string) { return v == 1, "send_redirects_enabled" },
		"/proc/sys/net/ipv4/ip_forward":                func(v int64) (bool, string) { return v == 1, "ip_forward_enabled" },
		"/proc/sys/kernel/kptr_restrict":               func(v int64) (bool, string) { return v < 2, "kptr_restrict_weak" },
		"/proc/sys/kernel/randomize_va_space":          func(v int64) (bool, string) { return v < 2, "aslr_weakened" },
		"/proc/sys/kernel/yama/ptrace_scope":           func(v int64) (bool, string) { return v < 1, "ptrace_scope_weak" },
	}
	out := make([]securitySysctlObservation, 0, len(targets))
	for path, predicate := range targets {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		value, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		if err != nil {
			continue
		}
		if risky, reason := predicate(value); risky {
			out = append(out, securitySysctlObservation{Path: path, Value: value, Reason: reason})
		}
	}
	return out
}

func buildProcessResourceIndex(processes []*telemetryv1.ProcessSample) (map[int]securityProcessResource, map[string]securityProcessResource) {
	byPID := make(map[int]securityProcessResource, len(processes))
	byName := make(map[string]securityProcessResource, len(processes))
	for _, process := range processes {
		if process == nil {
			continue
		}
		resource := securityProcessResource{CPUPercent: process.CpuPercent, RSSBytes: process.RssBytes}
		if process.Pid > 0 {
			byPID[int(process.Pid)] = resource
		}
		name := strings.ToLower(strings.TrimSpace(process.Name))
		if name == "" {
			continue
		}
		if existing, ok := byName[name]; !ok || resource.CPUPercent > existing.CPUPercent || resource.RSSBytes > existing.RSSBytes {
			byName[name] = resource
		}
	}
	return byPID, byName
}

func resourceEvidence(pid int, process string, byPID map[int]securityProcessResource, byName map[string]securityProcessResource) (bool, []string) {
	resource := byPID[pid]
	if resource.CPUPercent == 0 && resource.RSSBytes == 0 {
		resource = byName[strings.ToLower(strings.TrimSpace(process))]
	}
	evidence := make([]string, 0, 2)
	anomalous := false
	if resource.CPUPercent >= 70 {
		evidence = append(evidence, fmt.Sprintf("cpu=%.1f%%", resource.CPUPercent))
		anomalous = true
	}
	if resource.RSSBytes >= 1<<30 {
		evidence = append(evidence, fmt.Sprintf("rss=%d", resource.RSSBytes))
		anomalous = true
	}
	return anomalous, evidence
}

func countUnexpectedListeningPorts(listeners []securitySocketRecord, allowed []int) int {
	count := 0
	for _, item := range listeners {
		if !knownSecurityServicePort(item.Port, allowed) {
			count++
		}
	}
	return count
}

func countSuspiciousOutbound(outbound []securitySocketRecord) int {
	seen := make(map[string]struct{}, len(outbound))
	for _, item := range outbound {
		if isSecurityRemoteIPExternal(item.RemoteIP) {
			seen[item.RemoteIP] = struct{}{}
		}
	}
	return len(seen)
}

func countFindingsByCategory(findings []collectorSecurityFinding, category string) int {
	count := 0
	for _, finding := range findings {
		if strings.EqualFold(strings.TrimSpace(finding.Category), strings.TrimSpace(category)) {
			count++
		}
	}
	return count
}

func countSchedulerAnomalies(observations []securitySchedulerObservation) (int, int) {
	cronCount := 0
	systemdCount := 0
	for _, item := range observations {
		if item.Kind == "cron" {
			cronCount++
		} else if item.Kind == "systemd" {
			systemdCount++
		}
	}
	return cronCount, systemdCount
}

func countSuspiciousProcesses(procInfo map[int]securityProcessInfo) (int, int, int) {
	suspicious := 0
	parentChild := 0
	privEsc := 0
	for _, proc := range procInfo {
		if isSuspiciousExecutablePath(proc.ExePath) || looksSuspiciousProcessName(proc.Name, proc.ExePath, proc.Cmdline) {
			suspicious++
		}
		if proc.EUID == 0 && proc.UID != 0 {
			privEsc++
		}
		parent, ok := procInfo[proc.PPID]
		if ok && isSuspiciousParentChild(parent, proc) {
			parentChild++
		}
	}
	return suspicious, parentChild, privEsc
}

func countRootSuspiciousProcesses(procInfo map[int]securityProcessInfo) int {
	count := 0
	for _, proc := range procInfo {
		if proc.EUID == 0 && isSuspiciousExecutablePath(proc.ExePath) {
			count++
		}
	}
	return count
}

func totalLargeFileGrowth(observations []securityLargeFileObservation) int64 {
	total := int64(0)
	for _, item := range observations {
		total += item.Growth
	}
	return total
}

func uniqueSecurityPorts(listeners []securitySocketRecord) []int {
	set := make(map[int]struct{}, len(listeners))
	for _, item := range listeners {
		set[item.Port] = struct{}{}
	}
	out := make([]int, 0, len(set))
	for port := range set {
		out = append(out, port)
	}
	sort.Ints(out)
	return out
}

func boolToFloat64(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func rollingAverage(current, sample float64, observations int) float64 {
	if observations <= 1 {
		return sample
	}
	return ((current * float64(observations-1)) + sample) / float64(observations)
}

func maxFloatLocal(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func clampSecurityConfidence(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func firstNonEmptyLocal(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func dedupeStringsLocal(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func securityFindingID(category, severity, summary string, evidence []string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(category))))
	_, _ = h.Write([]byte{'|'})
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(severity))))
	_, _ = h.Write([]byte{'|'})
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(summary))))
	for _, item := range evidence {
		_, _ = h.Write([]byte{'|'})
		_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(item))))
	}
	return fmt.Sprintf("sf-%x", h.Sum64())
}

func normalizeProcessKey(process, path string) string {
	value := strings.ToLower(strings.TrimSpace(process))
	if value != "" {
		return value
	}
	if path == "" {
		return "unknown"
	}
	return strings.ToLower(strings.TrimSpace(filepath.Base(path)))
}

func knownSecurityServicePort(port int, allowed []int) bool {
	for _, candidate := range allowed {
		if candidate == port {
			return true
		}
	}
	known := map[int]struct{}{22: {}, 53: {}, 80: {}, 123: {}, 443: {}, 2379: {}, 2380: {}, 3000: {}, 3306: {}, 5432: {}, 6379: {}, 6443: {}, 8080: {}, 8443: {}, 9090: {}, 9100: {}, 10250: {}, 10257: {}, 10259: {}}
	_, ok := known[port]
	return ok
}

func looksLikeProcessPortMismatch(port int, process, path string) bool {
	value := strings.ToLower(strings.TrimSpace(firstNonEmptyLocal(process, filepath.Base(path))))
	if value == "" {
		return false
	}
	expected := map[int][]string{
		22:   {"sshd", "dropbear"},
		53:   {"named", "dnsmasq", "coredns"},
		2379: {"etcd"},
		2380: {"etcd"},
		5432: {"postgres"},
		6443: {"kube-apiserver"},
		9090: {"prometheus"},
	}
	allow, ok := expected[port]
	if !ok {
		return false
	}
	for _, token := range allow {
		if strings.Contains(value, token) {
			return false
		}
	}
	return true
}

func looksSuspiciousOutboundProcess(process, path string) bool {
	value := strings.ToLower(strings.TrimSpace(firstNonEmptyLocal(process, filepath.Base(path), path)))
	if value == "" {
		return false
	}
	for _, token := range []string{"curl", "wget", "bash", "sh", "nc", "ncat", "socat", "python", "perl", "ruby"} {
		if strings.Contains(value, token) {
			return true
		}
	}
	return isSuspiciousExecutablePath(path)
}

func isSecurityRemoteIPExternal(ip string) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil || parsed.IsLoopback() || parsed.IsMulticast() || parsed.IsUnspecified() {
		return false
	}
	privateCIDRs := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "169.254.0.0/16", "fc00::/7", "fe80::/10"}
	for _, cidr := range privateCIDRs {
		_, network, _ := net.ParseCIDR(cidr)
		if network != nil && network.Contains(parsed) {
			return false
		}
	}
	return true
}

func isSuspiciousExecutablePath(path string) bool {
	low := strings.ToLower(strings.TrimSpace(path))
	if low == "" {
		return false
	}
	if strings.Contains(low, " (deleted)") {
		return true
	}
	prefixes := []string{"/tmp/", "/var/tmp/", "/dev/shm/", "/run/user/", "/home/"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(low, prefix) {
			return true
		}
	}
	return false
}

func looksSuspiciousProcessName(name, path, cmdline string) bool {
	search := strings.ToLower(strings.TrimSpace(strings.Join([]string{name, path, cmdline}, " ")))
	if search == "" {
		return false
	}
	for _, token := range []string{"curl http", "wget http", "base64 -d", "bash -c", "nc -e", "socat tcp", "python -c", "perl -e"} {
		if strings.Contains(search, token) {
			return true
		}
	}
	return isSuspiciousExecutablePath(path)
}

func isSuspiciousParentChild(parent, child securityProcessInfo) bool {
	parentName := strings.ToLower(strings.TrimSpace(parent.Name))
	childSearch := strings.ToLower(strings.TrimSpace(strings.Join([]string{child.Name, child.ExePath, child.Cmdline}, " ")))
	if parentName == "" || childSearch == "" {
		return false
	}
	serviceParents := []string{"nginx", "apache", "httpd", "php-fpm", "gunicorn", "uwsgi", "node", "java", "sshd", "kubelet", "containerd", "dockerd"}
	launcherChildren := []string{"bash", "sh", "curl", "wget", "nc", "ncat", "socat", "python", "perl", "ruby"}
	parentMatch := false
	for _, token := range serviceParents {
		if strings.Contains(parentName, token) {
			parentMatch = true
			break
		}
	}
	if !parentMatch {
		return false
	}
	for _, token := range launcherChildren {
		if strings.Contains(childSearch, token) {
			return true
		}
	}
	return isSuspiciousExecutablePath(child.ExePath)
}

func permissionsWeakened(previous, current os.FileMode) bool {
	return current.Perm() > previous.Perm() || (current.Perm()&0o022) > (previous.Perm()&0o022)
}

func depthFromAuditRoot(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

func scanFirewallDisabled() int {
	hasIPTables := false
	hasNFT := false
	if data, err := os.ReadFile("/proc/net/ip_tables_names"); err == nil {
		hasIPTables = strings.TrimSpace(string(data)) != ""
	}
	if data, err := os.ReadFile("/proc/net/nf_tables"); err == nil {
		hasNFT = strings.TrimSpace(string(data)) != ""
	}
	if hasIPTables || hasNFT {
		return 0
	}
	return 1
}

func scanMACStatus() (int, int) {
	selinuxDisabled := 0
	if data, err := os.ReadFile("/sys/fs/selinux/enforce"); err == nil {
		if strings.TrimSpace(string(data)) != "1" {
			selinuxDisabled = 1
		}
	} else {
		selinuxDisabled = 1
	}
	apparmorDisabled := 0
	if data, err := os.ReadFile("/sys/module/apparmor/parameters/enabled"); err == nil {
		trimmed := strings.TrimSpace(strings.ToLower(string(data)))
		if trimmed != "y" && trimmed != "yes" && trimmed != "1" {
			apparmorDisabled = 1
		}
	} else {
		apparmorDisabled = 1
	}
	return selinuxDisabled, apparmorDisabled
}

func scanContainerEscapeRisk() (int, int) {
	status, err := os.ReadFile("/proc/1/status")
	if err != nil {
		return 0, 0
	}
	capRisk := 0
	scanner := bufio.NewScanner(strings.NewReader(string(status)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "CapEff:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[1] != "0000000000000000" {
				capRisk = 1
			}
		}
	}
	privileged := 0
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		body := strings.ToLower(string(data))
		if strings.Contains(body, "docker") || strings.Contains(body, "kubepods") || strings.Contains(body, "containerd") {
			if capRisk > 0 {
				privileged = 1
			}
		}
	}
	return privileged, capRisk
}

func packageVulnerabilityHint() float64 {
	raw := strings.TrimSpace(os.Getenv("SRE_COLLECTOR_SECURITY_PACKAGE_VULN_COUNT"))
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func extractSystemdExecStart(body string) string {
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "ExecStart=") {
			return strings.TrimSpace(strings.TrimPrefix(line, "ExecStart="))
		}
	}
	return ""
}

func ipString(ip net.IP) string {
	if ip == nil {
		return ""
	}
	return ip.String()
}
