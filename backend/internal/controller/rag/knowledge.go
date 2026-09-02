package rag

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type queryPlan struct {
	RawQuery        string
	NormalizedQuery string
	Intent          string
	Tokens          []string
	Domains         map[string]struct{}
	KnowledgeTypes  map[string]struct{}
	CaseTypes       map[string]struct{}
	SourceTypes     map[string]struct{}
}

type sourceProfile struct {
	Family           string
	Domain           string
	OperationalValue string
	FreshnessHint    string
}

var knowledgeKeywordMap = map[string][]string{
	"gpu":        {"gpu", "sm", "cuda", "显卡", "加速卡", "显存"},
	"thermal":    {"thermal", "temperature", "热", "温度", "过热"},
	"power":      {"power", "watt", "功耗", "电源", "电流"},
	"pcie":       {"pcie", "pcie", "link", "链路", "总线"},
	"oom":        {"oom", "out of memory", "memory leak", "内存不足", "内存泄漏"},
	"memory":     {"memory", "rss", "psi", "swap", "内存", "回收"},
	"network":    {"latency", "timeout", "retransmit", "jitter", "packet", "网络", "超时", "重传", "抖动", "丢包"},
	"deployment": {"deploy", "deployment", "rollout", "release", "发布", "部署", "回滚"},
	"security":   {"security", "cve", "exploit", "malware", "权限", "安全", "证书", "入侵"},
	"storage":    {"disk", "storage", "iops", "filesystem", "io", "磁盘", "存储", "文件系统"},
	"dns":        {"dns", "resolver", "域名", "解析"},
	"cache":      {"cache", "credential", "secret", "缓存", "凭据", "密钥"},
	"retry":      {"retry", "backoff", "重试"},
	"latency":    {"latency", "slow", "性能", "延迟", "慢"},
}

var runbookHeadingKeywords = []string{
	"runbook", "playbook", "guide", "manual", "troubleshoot", "remediation",
	"步骤", "处理", "排查", "修复", "操作", "检查", "建议",
}

var causeHeadingKeywords = []string{
	"cause", "root cause", "why", "原因", "根因", "触发", "故障原因",
}

var symptomHeadingKeywords = []string{
	"symptom", "impact", "signal", "alert", "现象", "症状", "影响", "告警", "异常",
}

var evidenceHeadingKeywords = []string{
	"evidence", "signal", "metric", "observation", "log", "trace", "证据", "指标", "日志", "观察",
}

func finalizeDocument(doc SourceDocument) SourceDocument {
	doc.SourcePath = strings.TrimSpace(doc.SourcePath)
	doc.SourceType = strings.TrimSpace(doc.SourceType)
	doc.Title = strings.TrimSpace(doc.Title)
	doc.Content = strings.TrimSpace(normalizeWhitespace(doc.Content))
	doc.Metadata = cloneMap(doc.Metadata)
	if doc.Metadata == nil {
		doc.Metadata = make(map[string]string, 8)
	}
	if doc.Title == "" {
		doc.Title = chooseTitle(doc.Content, doc.SourcePath)
	}

	profile := inferSourceProfile(doc.SourcePath)
	setSourceProfileMetadata(&doc, profile)

	manualSummary := firstNonEmpty(
		structuredFieldValue(doc.Metadata, "summary", "description", "question", "query", "title"),
		firstParagraph(doc.Content),
		doc.Title,
	)
	doc.Summary = strings.TrimSpace(firstNonEmpty(doc.Summary, manualSummary))
	doc.Symptoms = uniqueLimited(append(doc.Symptoms, inferSymptoms(doc)...), 8)
	doc.Evidence = uniqueLimited(append(doc.Evidence, inferEvidence(doc)...), 8)
	doc.LikelyCauses = uniqueLimited(append(doc.LikelyCauses, inferLikelyCauses(doc)...), 8)
	doc.RemediationSteps = uniqueLimited(append(doc.RemediationSteps, inferRemediationSteps(doc)...), 8)
	doc.Commands = uniqueLimited(append(doc.Commands, inferCommands(doc.Content)...), 8)
	doc.Environment = uniqueLimited(append(doc.Environment, inferEnvironment(doc)...), 8)
	doc.Signals = uniqueLimited(append(doc.Signals, inferSignalTags(doc.Title, doc.Content)...), 10)
	refineSourceProfileMetadata(&doc)

	doc.KnowledgeType = strings.TrimSpace(firstNonEmpty(doc.KnowledgeType, classifyKnowledgeType(doc)))
	doc.CaseType = strings.TrimSpace(firstNonEmpty(doc.CaseType, classifyCaseType(doc)))
	doc.RetrievalWeight = positiveOrDefault(doc.RetrievalWeight, defaultRetrievalWeight(doc))
	doc.Metadata["knowledge_type"] = doc.KnowledgeType
	doc.Metadata["case_type"] = doc.CaseType
	doc.Metadata["retrieval_weight"] = fmt.Sprintf("%.2f", doc.RetrievalWeight)

	tags := append([]string{}, doc.Tags...)
	tags = append(tags, doc.KnowledgeType, doc.CaseType)
	tags = append(tags, doc.Environment...)
	tags = append(tags, doc.Signals...)
	tags = append(tags,
		doc.Metadata["source_family"],
		doc.Metadata["source_domain"],
		doc.Metadata["operational_value"],
	)
	doc.Tags = uniqueLimited(tags, 24)
	doc.RetrievalText = strings.TrimSpace(firstNonEmpty(doc.RetrievalText, buildRetrievalText(doc)))
	doc.EmbeddingText = strings.TrimSpace(firstNonEmpty(doc.EmbeddingText, buildEmbeddingText(doc)))
	if doc.Metadata["retrieval_excluded"] == "" && doc.KnowledgeType == "dataset_meta" {
		doc.Metadata["retrieval_excluded"] = "true"
	}
	return doc
}

func classifyKnowledgeType(doc SourceDocument) string {
	path := strings.ToLower(doc.SourcePath)
	title := strings.ToLower(doc.Title)
	content := strings.ToLower(doc.Content)
	switch {
	case strings.Contains(path, "manifest.json"),
		strings.Contains(path, "/readme"),
		strings.Contains(path, "dataset layout"),
		strings.Contains(title, "dataset layout"):
		return "dataset_meta"
	case containsAny(path, "runbook", "playbook", "manual", "guide", "troubleshoot", "faq"),
		containsAny(title, runbookHeadingKeywords...),
		len(doc.RemediationSteps) >= 2,
		len(doc.Commands) >= 2:
		return "runbook"
	case containsAny(path, "incident", "postmortem", "history", "rca"),
		containsAny(title, "incident", "postmortem", "history", "rca", "故障", "异常", "失败"),
		len(doc.LikelyCauses) > 0:
		return "historical_incident"
	case structuredFieldValue(doc.Metadata, "query", "question") != "",
		containsAny(title, "question", "faq", "问题", "排查"),
		containsAny(content, "query:", "question:", "问题:"):
		return "question_pattern"
	case containsAny(path, "security", "audit", "threat"),
		containsAny(title, "security", "威胁", "安全"):
		return "security_reference"
	default:
		return "reference"
	}
}

func classifyCaseType(doc SourceDocument) string {
	switch {
	case containsAny(strings.ToLower(strings.Join(doc.Tags, " ")), "security", "安全"):
		return "security_event"
	case doc.KnowledgeType == "runbook":
		return "runbook"
	case doc.KnowledgeType == "historical_incident":
		return "historical_incident"
	case doc.KnowledgeType == "question_pattern":
		return "operational_qa"
	case doc.KnowledgeType == "dataset_meta":
		return "dataset_meta"
	default:
		return "reference"
	}
}

func defaultRetrievalWeight(doc SourceDocument) float64 {
	family := strings.ToLower(strings.TrimSpace(doc.Metadata["source_family"]))
	switch doc.KnowledgeType {
	case "runbook":
		switch family {
		case "prometheus_operator_runbook":
			return 1.35
		case "scoutflo_k8s_playbook":
			return 1.28
		case "nvidia_gpu_doc_processed":
			return 1.26
		case "nvidia_gpu_doc_raw":
			return 0.82
		case "scoutflo_aws_playbook", "scoutflo_sentry_playbook":
			return 1.02
		default:
			return 1.15
		}
	case "historical_incident":
		return 1.2
	case "question_pattern":
		switch family {
		case "structured_helpdesk":
			return 0.45
		case "structured_operational_qa":
			return 0.92
		}
		return 0.95
	case "security_reference":
		return 1.05
	case "dataset_meta":
		return 0.1
	default:
		switch family {
		case "archive_manual":
			return 0.58
		case "nvidia_gpu_doc_processed":
			return 1.18
		case "nvidia_gpu_doc_raw":
			return 0.72
		case "structured_helpdesk":
			return 0.35
		}
		return 0.9
	}
}

func buildRetrievalText(doc SourceDocument) string {
	parts := []string{
		doc.Title,
		doc.Summary,
		strings.Join(doc.Symptoms, "\n"),
		strings.Join(doc.Evidence, "\n"),
		strings.Join(doc.LikelyCauses, "\n"),
		strings.Join(doc.RemediationSteps, "\n"),
		strings.Join(doc.Commands, "\n"),
		strings.Join(doc.Environment, " "),
		strings.Join(doc.Signals, " "),
		structuredFieldValue(doc.Metadata, "query", "question", "document", "service", "topic", "linktoanswer", "link_to_answer"),
		doc.Content,
	}
	return compactKnowledgeText(parts...)
}

func buildEmbeddingText(doc SourceDocument) string {
	parts := []string{
		doc.Title,
		doc.Summary,
		strings.Join(doc.Symptoms, "\n"),
		strings.Join(doc.LikelyCauses, "\n"),
		strings.Join(doc.RemediationSteps, "\n"),
		strings.Join(doc.Signals, " "),
		structuredFieldValue(doc.Metadata, "query", "question", "document", "service", "topic"),
	}
	text := compactKnowledgeText(parts...)
	if text == "" {
		return doc.Content
	}
	return text
}

func inferSymptoms(doc SourceDocument) []string {
	out := make([]string, 0, 6)
	queryText := structuredFieldValue(doc.Metadata, "query", "question")
	if queryText != "" {
		out = append(out, normalizeSentence(queryText))
	}
	for _, section := range extractSectionItems(doc.Content, symptomHeadingKeywords) {
		out = append(out, normalizeSentence(section))
	}
	if len(out) == 0 && looksLikeProblemStatement(doc.Title) {
		out = append(out, normalizeSentence(doc.Title))
	}
	return dedupeStrings(out)
}

func inferEvidence(doc SourceDocument) []string {
	out := make([]string, 0, 8)
	for _, item := range extractSectionItems(doc.Content, evidenceHeadingKeywords) {
		out = append(out, normalizeSentence(item))
	}
	for _, signal := range inferSignalTags(doc.Title, doc.Content) {
		out = append(out, signal)
	}
	return dedupeStrings(out)
}

func inferLikelyCauses(doc SourceDocument) []string {
	out := make([]string, 0, 6)
	for _, item := range extractSectionItems(doc.Content, causeHeadingKeywords) {
		out = append(out, normalizeSentence(item))
	}
	lines := splitNonEmptyLines(doc.Content)
	for _, line := range lines {
		lower := strings.ToLower(line)
		if containsAny(lower, "because", "caused by", "due to", "原因", "由于", "导致") {
			out = append(out, normalizeSentence(line))
		}
		if len(out) >= 6 {
			break
		}
	}
	return dedupeStrings(out)
}

func inferRemediationSteps(doc SourceDocument) []string {
	out := make([]string, 0, 8)
	for _, item := range extractSectionItems(doc.Content, runbookHeadingKeywords) {
		out = append(out, normalizeSentence(item))
	}
	for _, line := range splitNonEmptyLines(doc.Content) {
		if isImperativeLine(line) {
			out = append(out, normalizeSentence(line))
		}
		if len(out) >= 8 {
			break
		}
	}
	return dedupeStrings(out)
}

func inferCommands(content string) []string {
	lines := splitNonEmptyLines(content)
	out := make([]string, 0, 6)
	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimPrefix(line, "-"))
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(trimmed, "$") ||
			containsAny(lower,
				"kubectl ", "curl ", "grep ", "awk ", "sed ", "journalctl ", "systemctl ",
				"docker ", "docker-compose ", "go test ", "python ", "python3 ", "bash ",
				"helm ", "cat ", "ls ", "ss ", "ip ") {
			out = append(out, trimmed)
		}
		if len(out) >= 6 {
			break
		}
	}
	return dedupeStrings(out)
}

func inferEnvironment(doc SourceDocument) []string {
	values := []string{}
	for _, part := range strings.Split(filepath.ToSlash(doc.SourcePath), "/") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		part = strings.TrimSuffix(part, filepath.Ext(part))
		if isUsefulEnvironmentToken(part) {
			values = append(values, part)
		}
	}
	for _, key := range []string{"document", "service", "topic", "category", "language"} {
		if value := structuredFieldValue(doc.Metadata, key); value != "" {
			values = append(values, value)
		}
	}
	return dedupeStrings(values)
}

func inferSignalTags(title, content string) []string {
	search := strings.ToLower(strings.TrimSpace(title + "\n" + content))
	tags := make([]string, 0, 8)
	for tag, keywords := range knowledgeKeywordMap {
		for _, keyword := range keywords {
			if keyword != "" && strings.Contains(search, strings.ToLower(keyword)) {
				tags = append(tags, tag)
				break
			}
		}
	}
	sort.Strings(tags)
	return dedupeStrings(tags)
}

func buildQueryPlan(req QueryRequest) queryPlan {
	raw := strings.TrimSpace(req.Query)
	intent := strings.TrimSpace(strings.ToLower(req.Intent))
	if intent == "" {
		intent = inferQueryIntent(raw)
	}
	expanded := expandQuery(raw, intent)
	tokens := tokenize(expanded)
	return queryPlan{
		RawQuery:        raw,
		NormalizedQuery: expanded,
		Intent:          intent,
		Tokens:          tokens,
		Domains:         inferQueryDomains(expanded, tokens),
		KnowledgeTypes:  normalizeFilterSet(req.KnowledgeTypes),
		CaseTypes:       normalizeFilterSet(req.CaseTypes),
		SourceTypes:     normalizeFilterSet(req.SourceTypes),
	}
}

func inferQueryIntent(query string) string {
	lower := strings.ToLower(strings.TrimSpace(query))
	switch {
	case containsAny(lower, "runbook", "playbook", "steps", "how to", "remediation", "排查", "处理", "修复", "操作"):
		return "runbook"
	case containsAny(lower, "history", "similar", "prior", "incident", "postmortem", "案例", "历史"):
		return "historical_incident"
	case containsAny(lower, "root cause", "rca", "why", "cause", "根因", "原因"):
		return "rca"
	case containsAny(lower, "risk", "joint", "correlation", "weak signal", "风险", "关联", "弱信号"):
		return "joint_risk"
	case containsAny(lower, "recommend", "next step", "action", "建议"):
		return "recommendation"
	case containsAny(lower, "security", "malware", "credential", "权限", "证书", "安全"):
		return "security"
	default:
		return "general"
	}
}

func expandQuery(query, intent string) string {
	parts := []string{strings.TrimSpace(query)}
	switch intent {
	case "runbook":
		parts = append(parts, "runbook playbook remediation troubleshooting steps")
	case "historical_incident":
		parts = append(parts, "historical incident prior case analogy postmortem")
	case "rca":
		parts = append(parts, "root cause evidence remediation runbook similar incident")
	case "joint_risk":
		parts = append(parts, "co-occurring weak signals latent risk escalation")
	case "recommendation":
		parts = append(parts, "immediate investigation containment remediation prevention")
	case "security":
		parts = append(parts, "security runtime event containment least privilege")
	}
	lower := strings.ToLower(query)
	for tag, keywords := range knowledgeKeywordMap {
		for _, keyword := range keywords {
			if keyword != "" && strings.Contains(lower, strings.ToLower(keyword)) {
				parts = append(parts, tag)
				parts = append(parts, strings.Join(keywords[:minInt(len(keywords), 3)], " "))
				break
			}
		}
	}
	return compactKnowledgeText(parts...)
}

func chunkMatchesQueryFilters(chunk Chunk, plan queryPlan) bool {
	if len(plan.KnowledgeTypes) > 0 {
		if _, ok := plan.KnowledgeTypes[strings.ToLower(strings.TrimSpace(chunk.KnowledgeType))]; !ok {
			return false
		}
	}
	if len(plan.CaseTypes) > 0 {
		if _, ok := plan.CaseTypes[strings.ToLower(strings.TrimSpace(chunk.CaseType))]; !ok {
			return false
		}
	}
	if len(plan.SourceTypes) > 0 {
		if _, ok := plan.SourceTypes[strings.ToLower(strings.TrimSpace(chunk.SourceType))]; !ok {
			return false
		}
	}
	if strings.EqualFold(chunk.Metadata["retrieval_excluded"], "true") {
		return false
	}
	return true
}

func rerankChunkScore(chunk Chunk, base float64, plan queryPlan) float64 {
	score := base
	if score <= 0 {
		return 0
	}
	score *= positiveOrDefault(chunk.RetrievalWeight, 1)
	score += 0.04 * float64(countTokenOverlap(plan.Tokens, chunk.Tags))
	switch plan.Intent {
	case "runbook":
		if chunk.KnowledgeType == "runbook" {
			score += 0.18
		}
		if len(chunk.RemediationSteps) > 0 || len(chunk.Commands) > 0 {
			score += 0.08
		}
	case "historical_incident", "rca":
		if chunk.KnowledgeType == "historical_incident" || chunk.CaseType == "historical_incident" {
			score += 0.18
		}
		if len(chunk.LikelyCauses) > 0 {
			score += 0.08
		}
	case "joint_risk":
		if chunk.KnowledgeType == "historical_incident" || chunk.KnowledgeType == "question_pattern" {
			score += 0.14
		}
		if len(chunk.Signals) > 0 {
			score += 0.08
		}
	case "recommendation":
		if len(chunk.RemediationSteps) > 0 {
			score += 0.14
		}
		if chunk.KnowledgeType == "runbook" {
			score += 0.1
		}
	case "security":
		if chunk.CaseType == "security_event" || chunk.KnowledgeType == "security_reference" {
			score += 0.16
		}
	}
	score = rerankBySourceProfile(chunk, score, plan)
	return score
}

func inferSourceProfile(sourcePath string) sourceProfile {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(sourcePath)))
	switch {
	case strings.Contains(lower, "/processed/gpu-docs/"):
		return sourceProfile{
			Family:           "nvidia_gpu_doc_processed",
			Domain:           "gpu",
			OperationalValue: "high",
			FreshnessHint:    "active_ops_seed",
		}
	case strings.Contains(lower, "/sources/web/"):
		return sourceProfile{
			Family:           "nvidia_gpu_doc_raw",
			Domain:           "gpu",
			OperationalValue: "medium",
			FreshnessHint:    "reference_seed",
		}
	case strings.Contains(lower, "/sources/git/prometheus-operator-runbooks/content/runbooks/"):
		return sourceProfile{
			Family:           "prometheus_operator_runbook",
			Domain:           inferPrometheusRunbookDomain(lower),
			OperationalValue: "high",
			FreshnessHint:    "active_ops_seed",
		}
	case strings.Contains(lower, "/sources/git/scoutflo-sre-playbooks/k8s playbooks/"):
		return sourceProfile{
			Family:           "scoutflo_k8s_playbook",
			Domain:           inferScoutfloK8sDomain(lower),
			OperationalValue: "high",
			FreshnessHint:    "active_ops_seed",
		}
	case strings.Contains(lower, "/sources/git/scoutflo-sre-playbooks/aws playbooks/"):
		return sourceProfile{
			Family:           "scoutflo_aws_playbook",
			Domain:           "aws",
			OperationalValue: "medium",
			FreshnessHint:    "reference_seed",
		}
	case strings.Contains(lower, "/sources/git/scoutflo-sre-playbooks/sentry playbooks/"):
		return sourceProfile{
			Family:           "scoutflo_sentry_playbook",
			Domain:           "sentry",
			OperationalValue: "medium",
			FreshnessHint:    "reference_seed",
		}
	case strings.Contains(lower, "helpdesk_dataset.csv"):
		return sourceProfile{
			Family:           "structured_helpdesk",
			Domain:           "generic",
			OperationalValue: "low",
			FreshnessHint:    "background",
		}
	case strings.Contains(lower, "question.jsonl"):
		return sourceProfile{
			Family:           "structured_operational_qa",
			Domain:           "generic",
			OperationalValue: "medium",
			FreshnessHint:    "seed",
		}
	case strings.Contains(lower, "/raw/structured/"):
		return sourceProfile{
			Family:           "structured_dataset",
			Domain:           "generic",
			OperationalValue: "medium",
			FreshnessHint:    "seed",
		}
	case strings.Contains(lower, "/raw/archives/"):
		return sourceProfile{
			Family:           "archive_manual",
			Domain:           inferGenericDomain(lower),
			OperationalValue: "low",
			FreshnessHint:    "background",
		}
	default:
		return sourceProfile{
			Family:           "generic_reference",
			Domain:           inferGenericDomain(lower),
			OperationalValue: "medium",
			FreshnessHint:    "unknown",
		}
	}
}

func setSourceProfileMetadata(doc *SourceDocument, profile sourceProfile) {
	if doc == nil || doc.Metadata == nil {
		return
	}
	if value := strings.TrimSpace(firstNonEmpty(doc.Metadata["source_family"], profile.Family)); value != "" {
		doc.Metadata["source_family"] = value
	}
	if value := strings.TrimSpace(firstNonEmpty(doc.Metadata["source_domain"], profile.Domain)); value != "" {
		doc.Metadata["source_domain"] = value
	}
	if value := strings.TrimSpace(firstNonEmpty(doc.Metadata["operational_value"], profile.OperationalValue)); value != "" {
		doc.Metadata["operational_value"] = value
	}
	if value := strings.TrimSpace(firstNonEmpty(doc.Metadata["freshness_hint"], profile.FreshnessHint)); value != "" {
		doc.Metadata["freshness_hint"] = value
	}
}

func refineSourceProfileMetadata(doc *SourceDocument) {
	if doc == nil || doc.Metadata == nil {
		return
	}
	if strings.TrimSpace(doc.Metadata["source_domain"]) == "" || strings.EqualFold(doc.Metadata["source_domain"], "generic") {
		refined := inferDomainFromSignals(doc.Signals, strings.ToLower(doc.Title+"\n"+doc.Content))
		if refined != "" {
			doc.Metadata["source_domain"] = refined
		}
	}
	if strings.TrimSpace(doc.Metadata["source_family"]) == "" {
		doc.Metadata["source_family"] = "generic_reference"
	}
	if strings.TrimSpace(doc.Metadata["operational_value"]) == "" {
		doc.Metadata["operational_value"] = "medium"
	}
	if strings.TrimSpace(doc.Metadata["freshness_hint"]) == "" {
		doc.Metadata["freshness_hint"] = "unknown"
	}
}

func inferPrometheusRunbookDomain(path string) string {
	switch {
	case strings.Contains(path, "/runbooks/node/"):
		return "linux_node"
	case strings.Contains(path, "/runbooks/kubernetes/"),
		strings.Contains(path, "/runbooks/kube-state-metrics/"),
		strings.Contains(path, "/runbooks/etcd/"):
		return "kubernetes"
	case strings.Contains(path, "/runbooks/prometheus/"),
		strings.Contains(path, "/runbooks/prometheus-operator/"),
		strings.Contains(path, "/runbooks/alertmanager/"):
		return "prometheus"
	default:
		return "infrastructure"
	}
}

func inferScoutfloK8sDomain(path string) string {
	switch {
	case strings.Contains(path, "/02-nodes/"):
		return "linux_node"
	case strings.Contains(path, "/05-networking/"):
		return "network"
	case strings.Contains(path, "/06-storage/"):
		return "storage"
	default:
		return "kubernetes"
	}
}

func inferGenericDomain(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	switch {
	case containsAny(lower, "gpu", "cuda", "nvidia", "dcgm", "nvml", "nvlink", "mig"):
		return "gpu"
	case containsAny(lower, "prometheus", "alertmanager", "tsdb", "promql", "scrape"):
		return "prometheus"
	case containsAny(lower, "kubernetes", "kube", "kubelet", "pod", "daemonset", "statefulset", "deployment", "container"):
		return "kubernetes"
	case containsAny(lower, "nodefilesystem", "nodehigh", "node", "raid", "conntrack", "filesystem", "systemd"):
		return "linux_node"
	case containsAny(lower, "aws", "ec2", "eks", "lambda", "cloudwatch", "rds", "s3", "iam", "route 53", "route53"):
		return "aws"
	case containsAny(lower, "sentry"):
		return "sentry"
	case containsAny(lower, "security", "credential", "certificate", "malware", "cve"):
		return "security"
	case containsAny(lower, "network", "timeout", "retransmit", "packet", "dns", "latency"):
		return "network"
	case containsAny(lower, "disk", "storage", "iops", "filesystem", "io"):
		return "storage"
	default:
		return "generic"
	}
}

func inferDomainFromSignals(signals []string, fallbackText string) string {
	if len(signals) > 0 {
		signalSet := normalizeFilterSet(signals)
		switch {
		case hasFilterValue(signalSet, "gpu"), hasFilterValue(signalSet, "thermal"), hasFilterValue(signalSet, "power"), hasFilterValue(signalSet, "pcie"):
			return "gpu"
		case hasFilterValue(signalSet, "network"), hasFilterValue(signalSet, "dns"):
			return "network"
		case hasFilterValue(signalSet, "storage"):
			return "storage"
		case hasFilterValue(signalSet, "security"):
			return "security"
		}
	}
	return inferGenericDomain(fallbackText)
}

func inferQueryDomains(query string, tokens []string) map[string]struct{} {
	lower := strings.ToLower(strings.TrimSpace(query))
	out := make(map[string]struct{}, 4)
	addDomain := func(domain string) {
		if strings.TrimSpace(domain) != "" {
			out[domain] = struct{}{}
		}
	}
	if containsAny(lower, "gpu", "cuda", "nvidia", "dcgm", "nvlink", "mig", "gpu operator") {
		addDomain("gpu")
	}
	if containsAny(lower, "prometheus", "alertmanager", "tsdb", "scrape", "remote write", "targetdown") {
		addDomain("prometheus")
	}
	if containsAny(lower, "kubernetes", "kube", "kubelet", "pod", "container", "daemonset", "deployment", "statefulset", "cluster") {
		addDomain("kubernetes")
	}
	if containsAny(lower, "node", "filesystem", "raid", "conntrack", "systemd", "node exporter") {
		addDomain("linux_node")
	}
	if containsAny(lower, "aws", "ec2", "eks", "lambda", "cloudwatch", "rds", "s3", "iam", "route53", "route 53", "cloudfront") {
		addDomain("aws")
	}
	if containsAny(lower, "sentry") {
		addDomain("sentry")
	}
	if containsAny(lower, "security", "credential", "certificate", "cve", "malware") {
		addDomain("security")
	}
	if containsAny(lower, "disk", "storage", "iops", "filesystem", "io", "iowait", "await") {
		addDomain("storage")
	}
	if containsAny(lower, "network", "timeout", "retransmit", "dns", "packet", "latency") {
		addDomain("network")
	}
	if len(out) == 0 && len(tokens) > 0 {
		for _, token := range tokens {
			if token == "gpu" || token == "cuda" {
				addDomain("gpu")
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func rerankBySourceProfile(chunk Chunk, score float64, plan queryPlan) float64 {
	family := strings.ToLower(strings.TrimSpace(chunk.Metadata["source_family"]))
	domain := strings.ToLower(strings.TrimSpace(chunk.Metadata["source_domain"]))
	operationalValue := strings.ToLower(strings.TrimSpace(chunk.Metadata["operational_value"]))
	freshnessHint := strings.ToLower(strings.TrimSpace(chunk.Metadata["freshness_hint"]))

	switch operationalValue {
	case "high":
		score += 0.1
	case "medium":
		score += 0.03
	case "low":
		score *= 0.88
	}

	switch freshnessHint {
	case "active_ops_seed":
		score += 0.04
	case "background":
		score *= 0.9
	case "meta":
		score *= 0.75
	}

	switch family {
	case "prometheus_operator_runbook":
		if hasPlanDomain(plan, "prometheus") || hasPlanDomain(plan, "kubernetes") || hasPlanDomain(plan, "linux_node") {
			score += 0.16
		}
	case "scoutflo_k8s_playbook":
		if hasPlanDomain(plan, "kubernetes") || hasPlanDomain(plan, "linux_node") {
			score += 0.14
		}
	case "nvidia_gpu_doc_processed":
		if hasPlanDomain(plan, "gpu") {
			score += 0.18
		}
	case "nvidia_gpu_doc_raw":
		score *= 0.7
	case "scoutflo_aws_playbook":
		if hasPlanDomain(plan, "aws") {
			score += 0.12
		} else if len(plan.Domains) > 0 {
			score *= 0.78
		}
	case "scoutflo_sentry_playbook":
		if hasPlanDomain(plan, "sentry") {
			score += 0.1
		} else if len(plan.Domains) > 0 {
			score *= 0.8
		}
	case "structured_helpdesk":
		score *= 0.6
	case "archive_manual":
		if hasPlanDomain(plan, "telecom") {
			score += 0.06
		} else {
			score *= 0.72
		}
	}

	if len(plan.Domains) > 0 && domain != "" {
		if _, ok := plan.Domains[domain]; ok {
			score += 0.12
		} else if domain == "generic" {
			score *= 0.96
		} else {
			score *= 0.9
		}
	}

	return score
}

func hasPlanDomain(plan queryPlan, domain string) bool {
	if len(plan.Domains) == 0 {
		return false
	}
	_, ok := plan.Domains[strings.ToLower(strings.TrimSpace(domain))]
	return ok
}

func hasFilterValue(values map[string]struct{}, key string) bool {
	if len(values) == 0 {
		return false
	}
	_, ok := values[strings.ToLower(strings.TrimSpace(key))]
	return ok
}

func summarizeKnowledgeMix(hits []SearchHit) string {
	if len(hits) == 0 {
		return "no knowledge hits matched the query"
	}
	docs := make(map[string]struct{}, len(hits))
	kinds := make(map[string]int, 4)
	for _, hit := range hits {
		docs[hit.DocID] = struct{}{}
		kind := firstNonEmpty(hit.KnowledgeType, "reference")
		kinds[kind]++
	}
	summaryParts := make([]string, 0, len(kinds))
	for _, key := range sortedStringKeys(kinds) {
		summaryParts = append(summaryParts, fmt.Sprintf("%s=%d", key, kinds[key]))
	}
	return fmt.Sprintf("retrieved %d knowledge hits across %d documents (%s)", len(hits), len(docs), strings.Join(summaryParts, ", "))
}

func knowledgeCategories(hits []SearchHit) map[string]int {
	counts := make(map[string]int, 4)
	for _, hit := range hits {
		kind := firstNonEmpty(hit.KnowledgeType, "reference")
		counts[kind]++
	}
	return counts
}

func effectiveChunkRetrievalText(chunk Chunk) string {
	return strings.TrimSpace(firstNonEmpty(chunk.RetrievalText, chunk.Summary, chunk.Title+"\n"+chunk.Content))
}

func effectiveChunkEmbeddingText(chunk Chunk) string {
	return strings.TrimSpace(firstNonEmpty(chunk.EmbeddingText, chunk.RetrievalText, chunk.Summary, chunk.Title+"\n"+chunk.Content))
}

func normalizeFilterSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		out[value] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func countTokenOverlap(queryTokens, tags []string) int {
	if len(queryTokens) == 0 || len(tags) == 0 {
		return 0
	}
	tagSet := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tagSet[strings.ToLower(strings.TrimSpace(tag))] = struct{}{}
	}
	overlap := 0
	seen := map[string]struct{}{}
	for _, token := range queryTokens {
		token = strings.ToLower(strings.TrimSpace(token))
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		if _, ok := tagSet[token]; ok {
			overlap++
		}
	}
	return overlap
}

func structuredFieldValue(metadata map[string]string, keys ...string) string {
	if len(metadata) == 0 {
		return ""
	}
	for _, key := range keys {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		for _, candidate := range []string{
			key,
			"field." + key,
			"attr." + key,
			strings.ReplaceAll(key, " ", "_"),
		} {
			if value := strings.TrimSpace(metadata[candidate]); value != "" {
				return value
			}
		}
	}
	return ""
}

func extractSectionItems(content string, headings []string) []string {
	lines := splitNonEmptyLines(content)
	out := make([]string, 0, 8)
	collect := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(strings.Trim(trimmed, "#* -"))
		if matchesHeading(lower, headings) {
			collect = true
			continue
		}
		if collect && isLikelyHeading(trimmed) {
			collect = false
		}
		if !collect {
			continue
		}
		if bullet := stripBulletPrefix(trimmed); bullet != "" {
			out = append(out, bullet)
		} else if len(trimmed) <= 160 {
			out = append(out, trimmed)
		}
		if len(out) >= 8 {
			break
		}
	}
	return dedupeStrings(out)
}

func firstParagraph(content string) string {
	for _, section := range splitParagraphSections(content) {
		section = normalizeSentence(section)
		if section != "" {
			return section
		}
	}
	return ""
}

func compactKnowledgeText(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(normalizeWhitespace(part))
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return strings.Join(dedupeStrings(out), "\n")
}

func uniqueLimited(values []string, limit int) []string {
	values = dedupeStrings(values)
	if limit > 0 && len(values) > limit {
		values = values[:limit]
	}
	return values
}

func normalizeSentence(text string) string {
	text = strings.TrimSpace(normalizeWhitespace(text))
	text = stripBulletPrefix(text)
	if len([]rune(text)) > 240 {
		text = string([]rune(text)[:240])
	}
	return strings.TrimSpace(text)
}

func splitNonEmptyLines(content string) []string {
	lines := strings.Split(normalizeWhitespace(content), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func looksLikeProblemStatement(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return containsAny(lower,
		"timeout", "latency", "error", "oom", "fail", "stuck", "slow",
		"超时", "异常", "失败", "告警", "排查", "故障")
}

func isImperativeLine(line string) bool {
	trimmed := strings.ToLower(stripBulletPrefix(strings.TrimSpace(line)))
	return containsAny(trimmed,
		"check ", "inspect ", "verify ", "review ", "revert ", "restart ", "confirm ", "collect ",
		"检查", "确认", "查看", "回滚", "重启", "执行", "排查", "验证")
}

func isUsefulEnvironmentToken(token string) bool {
	lower := strings.ToLower(strings.TrimSpace(token))
	if lower == "" || len([]rune(lower)) > 48 {
		return false
	}
	if containsAny(lower, "dataset", "raw", "structured", "archives", "documents", "graphics", "publish", "files") {
		return false
	}
	hasLetter := false
	for _, r := range lower {
		if unicode.IsLetter(r) || unicode.Is(unicode.Han, r) {
			hasLetter = true
			break
		}
	}
	return hasLetter
}

func matchesHeading(line string, headings []string) bool {
	for _, heading := range headings {
		heading = strings.ToLower(strings.TrimSpace(heading))
		if heading == "" {
			continue
		}
		if strings.Contains(line, heading) {
			return true
		}
	}
	return false
}

func isLikelyHeading(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "#") {
		return true
	}
	runes := []rune(trimmed)
	return len(runes) <= 24 && !strings.Contains(trimmed, " ")
}

func stripBulletPrefix(line string) string {
	line = strings.TrimSpace(line)
	for _, prefix := range []string{"- ", "* ", "+ ", "• "} {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	if len(line) >= 3 && unicode.IsDigit(rune(line[0])) && line[1] == '.' {
		return strings.TrimSpace(line[2:])
	}
	return line
}

func containsAny(text string, values ...string) bool {
	text = strings.ToLower(text)
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func positiveOrDefault(value, fallback float64) float64 {
	if value > 0 {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func sortedStringKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func mergeStructuredMetadata(metadata map[string]string, value any) {
	if metadata == nil {
		return
	}
	typed, ok := value.(map[string]any)
	if !ok {
		return
	}
	for key, raw := range typed {
		key = strings.TrimSpace(strings.ToLower(key))
		if key == "" {
			continue
		}
		switch cast := raw.(type) {
		case string:
			if text := strings.TrimSpace(cast); text != "" {
				metadata["field."+key] = truncateMetadataValue(text)
			}
		case float64:
			metadata["field."+key] = strconv.FormatFloat(cast, 'f', -1, 64)
		case bool:
			metadata["field."+key] = strconv.FormatBool(cast)
		}
	}
}

func truncateMetadataValue(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= 240 {
		return value
	}
	return string(runes[:240])
}
