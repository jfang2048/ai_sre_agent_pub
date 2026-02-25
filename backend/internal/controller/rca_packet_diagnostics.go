package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
)

const (
	rcaPacketDefaultSortKey       = "severity"
	rcaPacketDefaultSortDirection = "desc"
	rcaPacketDefaultFormat        = "json"
	rcaPacketTopWorkloadLimit     = 12
	rcaPacketTopRowsLimit         = 6
)

type rcaPacketDiagnosticsResponse struct {
	CollectorID              string                 `json:"collector_id,omitempty"`
	Cluster                  string                 `json:"cluster,omitempty"`
	Namespace                string                 `json:"namespace,omitempty"`
	Service                  string                 `json:"service,omitempty"`
	SortKey                  string                 `json:"sort_key"`
	SortDirection            string                 `json:"sort_direction"`
	Format                   string                 `json:"format"`
	WorkloadLimit            int                    `json:"workload_limit"`
	GeneratedAt              time.Time              `json:"generated_at"`
	FileName                 string                 `json:"file_name"`
	Markdown                 string                 `json:"markdown"`
	PacketSHA256             string                 `json:"packet_sha256"`
	ContentBytes             int                    `json:"content_bytes"`
	PacketSignature          string                 `json:"packet_signature,omitempty"`
	PacketSignatureAlgorithm string                 `json:"packet_signature_algorithm,omitempty"`
	PacketSignatureKeyID     string                 `json:"packet_signature_key_id,omitempty"`
	Summary                  rcaPacketSummary       `json:"summary"`
	SourceMetadata           rcaPacketSourceSummary `json:"source_metadata"`
}

type rcaPacketSummary struct {
	RootCauseFindings int `json:"root_cause_findings"`
	CriticalFindings  int `json:"critical_findings"`
	DegradedFindings  int `json:"degraded_findings"`
	KernelNodes       int `json:"kernel_nodes"`
	Workloads         int `json:"workloads"`
	NetworkRanked     int `json:"network_ranked"`
	StorageRanked     int `json:"storage_ranked"`
	ProbeCoreRanked   int `json:"probe_core_ranked"`
}

type rcaPacketSourceSummary struct {
	DataPathEndpoint     string `json:"data_path_endpoint"`
	KernelPathEndpoint   string `json:"kernel_path_endpoint"`
	RootCauseEndpoint    string `json:"root_cause_endpoint"`
	WorkloadPathEndpoint string `json:"workload_path_endpoint"`
}

type rcaPacketSigningConfig struct {
	Enabled   bool
	Algorithm string
	Key       []byte
	KeyID     string
}

func (c *Controller) handleRCAPacketDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query := r.URL.Query()
	collectorID := strings.TrimSpace(query.Get("collector_id"))
	if collectorID == "" {
		collectorID = strings.TrimSpace(query.Get("collector"))
	}
	cluster := strings.TrimSpace(query.Get("cluster"))
	namespace := strings.TrimSpace(query.Get("namespace"))
	service := strings.TrimSpace(query.Get("service"))
	workloadLimit := parseWorkloadPathLimit(firstNonEmpty(strings.TrimSpace(query.Get("workload_limit")), strings.TrimSpace(query.Get("limit"))))
	sortKey := parseRCAPacketSortKey(strings.TrimSpace(query.Get("sort_key")))
	sortDirection := parseRCAPacketSortDirection(strings.TrimSpace(query.Get("sort_direction")))
	format := parseRCAPacketFormat(strings.TrimSpace(query.Get("format")))
	forceDownload := parseRCAPacketBool(strings.TrimSpace(query.Get("download")))
	signingCfg := loadRCAPacketSigningConfig()

	resp := c.buildRCAPacketDiagnostics(
		collectorID,
		cluster,
		namespace,
		service,
		sortKey,
		sortDirection,
		workloadLimit,
		signingCfg,
	)
	resp.Format = format

	if format == "markdown" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("X-AI-SRE-Packet-SHA256", resp.PacketSHA256)
		w.Header().Set("X-AI-SRE-Packet-Bytes", fmt.Sprintf("%d", resp.ContentBytes))
		if resp.PacketSignature != "" {
			w.Header().Set("X-AI-SRE-Packet-Signature", resp.PacketSignature)
			w.Header().Set("X-AI-SRE-Packet-Signature-Algorithm", resp.PacketSignatureAlgorithm)
			if resp.PacketSignatureKeyID != "" {
				w.Header().Set("X-AI-SRE-Packet-Signature-Key-ID", resp.PacketSignatureKeyID)
			}
		}
		if forceDownload {
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, resp.FileName))
		}
		_, _ = w.Write([]byte(resp.Markdown))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (c *Controller) buildRCAPacketDiagnostics(
	collectorID string,
	cluster string,
	namespace string,
	service string,
	sortKey string,
	sortDirection string,
	workloadLimit int,
	signingCfg rcaPacketSigningConfig,
) rcaPacketDiagnosticsResponse {
	dataPath := c.buildDataPathDiagnostics(collectorID)
	rootCause := c.buildRootCauseDiagnostics(collectorID)
	kernelPath := c.buildKernelPathDiagnostics(collectorID)

	var workloadResp workloadPathDiagnosticsResponse
	if c.k8sManager != nil {
		var ingestSnapshots []*ingest.NodeSnapshot
		if c.ingestStore != nil {
			ingestSnapshots = c.ingestStore.Snapshot()
		}
		workloadResp = buildWorkloadPathDiagnostics(
			cluster,
			namespace,
			service,
			workloadLimit,
			c.k8sManager.Snapshots(),
			ingestSnapshots,
		)
	} else {
		workloadResp = workloadPathDiagnosticsResponse{
			Cluster:     cluster,
			Namespace:   namespace,
			Service:     service,
			GeneratedAt: time.Now(),
			Summary:     workloadPathDiagnosticsSummary{},
			Workloads:   []workloadPathDiagnosticsWorkload{},
		}
	}

	sortedWorkloads := append([]workloadPathDiagnosticsWorkload{}, workloadResp.Workloads...)
	sortRCAPacketWorkloads(sortedWorkloads, sortKey, sortDirection)

	generatedAt := rootCause.GeneratedAt
	if generatedAt.IsZero() {
		generatedAt = dataPath.GeneratedAt
	}
	if generatedAt.IsZero() {
		generatedAt = time.Now()
	}

	filters := rcaPacketFilters{
		Cluster:   cluster,
		Namespace: namespace,
		Service:   service,
	}
	markdown := buildRCAPacketMarkdown(
		generatedAt,
		filters,
		sortKey,
		sortDirection,
		rootCause,
		kernelPath,
		dataPath,
		sortedWorkloads,
	)
	packetSHA, contentBytes := digestRCAPacket(markdown)
	packetSignature, signatureAlgorithm, signatureKeyID := signRCAPacket(markdown, signingCfg)

	return rcaPacketDiagnosticsResponse{
		CollectorID:              collectorID,
		Cluster:                  cluster,
		Namespace:                namespace,
		Service:                  service,
		SortKey:                  sortKey,
		SortDirection:            sortDirection,
		Format:                   rcaPacketDefaultFormat,
		WorkloadLimit:            workloadLimit,
		GeneratedAt:              generatedAt,
		FileName:                 buildRCAPacketFileName(generatedAt, filters),
		Markdown:                 markdown,
		PacketSHA256:             packetSHA,
		ContentBytes:             contentBytes,
		PacketSignature:          packetSignature,
		PacketSignatureAlgorithm: signatureAlgorithm,
		PacketSignatureKeyID:     signatureKeyID,
		Summary: rcaPacketSummary{
			RootCauseFindings: rootCause.Summary.FindingCount,
			CriticalFindings:  rootCause.Summary.CriticalFindings,
			DegradedFindings:  rootCause.Summary.DegradedFindings,
			KernelNodes:       len(kernelPath.Nodes),
			Workloads:         len(sortedWorkloads),
			NetworkRanked:     len(dataPath.Network.Rankings),
			StorageRanked:     len(dataPath.Storage.Rankings),
			ProbeCoreRanked:   len(dataPath.ProbeCore.Rankings),
		},
		SourceMetadata: rcaPacketSourceSummary{
			DataPathEndpoint:     "/api/v1/diagnostics/data-path",
			KernelPathEndpoint:   "/api/v1/diagnostics/kernel-path",
			RootCauseEndpoint:    "/api/v1/diagnostics/root-cause",
			WorkloadPathEndpoint: "/api/v1/diagnostics/workload-path",
		},
	}
}

func digestRCAPacket(markdown string) (string, int) {
	body := []byte(markdown)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), len(body)
}

func signRCAPacket(markdown string, cfg rcaPacketSigningConfig) (signature string, algorithm string, keyID string) {
	if !cfg.Enabled || len(cfg.Key) == 0 {
		return "", "", ""
	}
	mac := hmac.New(sha256.New, cfg.Key)
	_, _ = mac.Write([]byte(markdown))
	return hex.EncodeToString(mac.Sum(nil)), cfg.Algorithm, cfg.KeyID
}

func loadRCAPacketSigningConfig() rcaPacketSigningConfig {
	key := strings.TrimSpace(os.Getenv("SRE_RCA_PACKET_SIGNING_KEY"))
	if key == "" {
		return rcaPacketSigningConfig{}
	}
	return rcaPacketSigningConfig{
		Enabled:   true,
		Algorithm: "hmac-sha256",
		Key:       []byte(key),
		KeyID:     strings.TrimSpace(os.Getenv("SRE_RCA_PACKET_SIGNING_KEY_ID")),
	}
}

type rcaPacketFilters struct {
	Cluster   string
	Namespace string
	Service   string
}

func buildRCAPacketMarkdown(
	generatedAt time.Time,
	filters rcaPacketFilters,
	sortKey string,
	sortDirection string,
	rootCause rootCauseDiagnosticsResponse,
	kernelPath kernelPathDiagnosticsResponse,
	dataPath dataPathDiagnosticsResponse,
	workloads []workloadPathDiagnosticsWorkload,
) string {
	lines := make([]string, 0, 256)
	lines = append(lines, "# AI SRE RCA Packet")
	lines = append(lines, "Generated: "+generatedAt.Format(time.RFC3339))
	lines = append(lines, fmt.Sprintf(
		"Scope: cluster=%s, namespace=%s, service=%s",
		defaultScopeToken(filters.Cluster),
		defaultScopeToken(filters.Namespace),
		defaultScopeToken(filters.Service),
	))
	lines = append(lines, fmt.Sprintf("Sort: %s (%s)", rcaSortKeyLabel(sortKey), sortDirection))
	lines = append(lines, "")

	lines = append(lines, "## Root Cause Summary")
	lines = append(lines, fmt.Sprintf(
		"Findings: %d (critical %d, degraded %d)",
		rootCause.Summary.FindingCount,
		rootCause.Summary.CriticalFindings,
		rootCause.Summary.DegradedFindings,
	))
	lines = append(lines, fmt.Sprintf("Linked anomalies: %d", rootCause.DataPath.TotalAnomalies))
	if len(rootCause.Findings) == 0 {
		lines = append(lines, "- No active findings in current window.")
	} else {
		for _, finding := range rootCause.Findings[:minIntLocal(len(rootCause.Findings), rcaPacketTopRowsLimit)] {
			nodes := make([]string, 0, len(finding.AffectedNodes))
			for _, node := range finding.AffectedNodes {
				name := strings.TrimSpace(node.Hostname)
				if name == "" {
					name = node.CollectorID
				}
				if name != "" {
					nodes = append(nodes, name)
				}
			}
			lines = append(lines, fmt.Sprintf("- [%s] %s (confidence %s)", finding.Severity, finding.Title, formatRCAPacketConfidence(finding.Confidence)))
			lines = append(lines, "  - hypothesis: "+finding.Hypothesis)
			lines = append(lines, "  - impact: "+finding.Impact)
			lines = append(lines, "  - nodes: "+defaultScopeToken(strings.Join(nodes[:minIntLocal(len(nodes), 4)], ", ")))
			lines = append(lines, "  - signals: "+rcaJoinedOrDash(finding.CorrelatedSignal, " · "))
			lines = append(lines, "  - actions: "+rcaJoinedOrDash(finding.Actions, " | "))
		}
	}
	lines = append(lines, "")

	lines = append(lines, "## Kernel Path Snapshot")
	lines = append(lines, "Top storage stage: "+formatRCAPacketKernelStage(kernelPath.Summary.TopStorageStage))
	lines = append(lines, "Top network stage: "+formatRCAPacketKernelStage(kernelPath.Summary.TopNetworkStage))
	if len(kernelPath.Nodes) == 0 {
		lines = append(lines, "- No kernel-path nodes in current window.")
	} else {
		for _, node := range kernelPath.Nodes[:minIntLocal(len(kernelPath.Nodes), rcaPacketTopRowsLimit)] {
			hostname := strings.TrimSpace(node.Hostname)
			if hostname == "" {
				hostname = node.CollectorID
			}
			lines = append(lines, fmt.Sprintf(
				"- [%s] %s storage=%s(%.2f) network=%s(%.2f) bottlenecks=%s",
				node.OverallSeverity,
				hostname,
				formatRCAPacketKernelStage(node.Storage.TopStage),
				node.Storage.Score,
				formatRCAPacketKernelStage(node.Network.TopStage),
				node.Network.Score,
				rcaJoinedOrDash(node.Bottlenecks, ","),
			))
		}
	}
	lines = append(lines, "")

	lines = append(lines, "## Resource Pressure Snapshot")
	lines = append(lines, "### Network Pressure Ranking")
	lines = append(lines, buildRCAPacketRankingLines(dataPath.Network.Rankings)...)
	lines = append(lines, "### Storage Pressure Ranking")
	lines = append(lines, buildRCAPacketRankingLines(dataPath.Storage.Rankings)...)
	lines = append(lines, "### Probe-core Reliability Ranking")
	lines = append(lines, buildRCAPacketRankingLines(dataPath.ProbeCore.Rankings)...)
	lines = append(lines, "")

	lines = append(lines, "# Workload Path Handoff")
	lines = append(lines, "Generated: "+generatedAt.Format(time.RFC3339))
	lines = append(lines, fmt.Sprintf(
		"Scope: cluster=%s, namespace=%s, service=%s",
		defaultScopeToken(filters.Cluster),
		defaultScopeToken(filters.Namespace),
		defaultScopeToken(filters.Service),
	))
	lines = append(lines, fmt.Sprintf("Sort: %s (%s)", rcaSortKeyLabel(sortKey), sortDirection))
	lines = append(lines, fmt.Sprintf("Workloads: %d", len(workloads)))
	lines = append(lines, "")
	if len(workloads) == 0 {
		lines = append(lines, "- No workloads in current scope.")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "## Top Workloads")
	for _, workload := range workloads[:minIntLocal(len(workloads), rcaPacketTopWorkloadLimit)] {
		topNode := workloadPathNode{}
		if len(workload.Nodes) > 0 {
			topNode = workload.Nodes[0]
		}
		lines = append(lines, fmt.Sprintf(
			"- %s/%s [%s] bottleneck=%s C/N/S/O=%.2f/%.2f/%.2f/%.2f coverage=%.0f%% risks=%s",
			workload.Namespace,
			workload.Name,
			workload.Severity,
			workload.Bottleneck,
			workload.ComputeScore,
			workload.NetworkScore,
			workload.StorageScore,
			workload.OverallScore,
			workload.TelemetryCoveragePct,
			rcaJoinedOrFallback(workload.Risks, ",", "none"),
		))
		topNodeName := strings.TrimSpace(topNode.Hostname)
		if topNodeName == "" {
			topNodeName = strings.TrimSpace(topNode.NodeName)
		}
		lines = append(lines, fmt.Sprintf(
			"  - node=%s stages=storage:%s,network:%s",
			defaultScopeToken(topNodeName),
			defaultScopeToken(workload.TopStorageStage),
			defaultScopeToken(workload.TopNetworkStage),
		))
		lines = append(lines, "  - evidence="+rcaCompactSignalSummary(workload.Signals, 3))
	}
	return strings.Join(lines, "\n")
}

func buildRCAPacketRankingLines(rows []resourcePressureRow) []string {
	if len(rows) == 0 {
		return []string{"- No ranked nodes in current window."}
	}
	lines := make([]string, 0, minIntLocal(len(rows), rcaPacketTopRowsLimit))
	for _, row := range rows[:minIntLocal(len(rows), rcaPacketTopRowsLimit)] {
		hostname := strings.TrimSpace(row.Hostname)
		if hostname == "" {
			hostname = row.CollectorID
		}
		lines = append(lines, fmt.Sprintf(
			"- [%s] %s score=%.2f signals=%s",
			row.Severity,
			hostname,
			row.Score,
			rcaCompactSignalSummary(row.Signals, 3),
		))
	}
	return lines
}

func sortRCAPacketWorkloads(workloads []workloadPathDiagnosticsWorkload, sortKey, sortDirection string) {
	sort.Slice(workloads, func(i, j int) bool {
		leftMetric := rcaWorkloadSortMetric(workloads[i], sortKey)
		rightMetric := rcaWorkloadSortMetric(workloads[j], sortKey)
		cmp := rightMetric - leftMetric
		if cmp == 0 {
			cmp = float64(workloadSeverityRank(workloads[j].Severity) - workloadSeverityRank(workloads[i].Severity))
		}
		if cmp == 0 {
			cmp = workloads[j].OverallScore - workloads[i].OverallScore
		}
		if cmp == 0 {
			return rcaWorkloadIdentity(workloads[i]) < rcaWorkloadIdentity(workloads[j])
		}
		if sortDirection == "asc" {
			return cmp < 0
		}
		return cmp > 0
	})
}

func rcaWorkloadSortMetric(workload workloadPathDiagnosticsWorkload, sortKey string) float64 {
	switch sortKey {
	case "severity":
		return float64(workloadSeverityRank(workload.Severity))
	case "overall":
		return workload.OverallScore
	case "coverage":
		return workload.TelemetryCoveragePct
	case "network":
		return workload.NetworkScore
	case "storage":
		return workload.StorageScore
	default:
		return workload.OverallScore
	}
}

func parseRCAPacketSortKey(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "severity", "overall", "coverage", "network", "storage":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return rcaPacketDefaultSortKey
	}
}

func parseRCAPacketSortDirection(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "asc", "desc":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return rcaPacketDefaultSortDirection
	}
}

func parseRCAPacketFormat(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "json":
		return "json"
	case "markdown", "md":
		return "markdown"
	default:
		return rcaPacketDefaultFormat
	}
}

func parseRCAPacketBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func buildRCAPacketFileName(generatedAt time.Time, filters rcaPacketFilters) string {
	stamp := generatedAt.UTC().Format("20060102T150405Z")
	scope := strings.Join([]string{
		sanitizeRCAPacketFileToken(firstNonEmpty(filters.Cluster, "all-clusters")),
		sanitizeRCAPacketFileToken(firstNonEmpty(filters.Namespace, "all-namespaces")),
		sanitizeRCAPacketFileToken(firstNonEmpty(filters.Service, "all-services")),
	}, "_")
	return fmt.Sprintf("ai-sre-rca-packet_%s_%s.md", scope, stamp)
}

func sanitizeRCAPacketFileToken(raw string) string {
	var b strings.Builder
	lastDash := false
	for _, ch := range strings.ToLower(strings.TrimSpace(raw)) {
		switch {
		case (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '.':
			b.WriteRune(ch)
			lastDash = false
		case ch == '-':
			if !lastDash && b.Len() > 0 {
				b.WriteRune(ch)
				lastDash = true
			}
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "all"
	}
	return out
}

func rcaCompactSignalSummary(signals map[string]float64, limit int) string {
	type pair struct {
		Key   string
		Value float64
	}
	pairs := make([]pair, 0, len(signals))
	for key, value := range signals {
		if !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0 {
			pairs = append(pairs, pair{Key: key, Value: value})
		}
	}
	if len(pairs) == 0 {
		return "—"
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Value != pairs[j].Value {
			return pairs[i].Value > pairs[j].Value
		}
		return pairs[i].Key < pairs[j].Key
	})
	if limit <= 0 {
		limit = 3
	}
	pairs = pairs[:minIntLocal(len(pairs), limit)]
	lines := make([]string, 0, len(pairs))
	for _, item := range pairs {
		if item.Value >= 100 {
			lines = append(lines, fmt.Sprintf("%s=%.0f", item.Key, item.Value))
		} else {
			lines = append(lines, fmt.Sprintf("%s=%.2f", item.Key, item.Value))
		}
	}
	return strings.Join(lines, " · ")
}

func formatRCAPacketConfidence(confidence float64) string {
	if math.IsNaN(confidence) || math.IsInf(confidence, 0) {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", clampRange(confidence, 0, 1)*100.0)
}

func formatRCAPacketKernelStage(stage string) string {
	stage = strings.TrimSpace(stage)
	if stage == "" {
		return "—"
	}
	return strings.ReplaceAll(stage, "_", " ")
}

func rcaSortKeyLabel(sortKey string) string {
	switch sortKey {
	case "severity":
		return "severity"
	case "overall":
		return "overall score"
	case "coverage":
		return "telemetry coverage"
	case "network":
		return "network score"
	case "storage":
		return "storage score"
	default:
		return "severity"
	}
}

func rcaWorkloadIdentity(workload workloadPathDiagnosticsWorkload) string {
	return workload.Cluster + "/" + workload.Namespace + "/" + workload.Kind + "/" + workload.Name
}

func rcaJoinedOrDash(values []string, sep string) string {
	return rcaJoinedOrFallback(values, sep, "—")
}

func rcaJoinedOrFallback(values []string, sep, fallback string) string {
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		item := strings.TrimSpace(value)
		if item != "" {
			trimmed = append(trimmed, item)
		}
	}
	if len(trimmed) == 0 {
		return fallback
	}
	return strings.Join(trimmed, sep)
}

func defaultScopeToken(raw string) string {
	item := strings.TrimSpace(raw)
	if item == "" {
		return "*"
	}
	return item
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
