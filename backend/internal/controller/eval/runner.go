package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/rag"
	"go.uber.org/zap"
)

// Run executes the golden evaluation suites for the requested scope.
func Run(ctx context.Context, opts RunOptions) (Report, error) {
	repoRoot, err := resolveRepoRoot(opts.RepoRoot)
	if err != nil {
		return Report{}, err
	}
	scope := opts.Scope
	if scope == "" {
		scope = ScopeFast
	}
	retrievalCases, err := loadRetrievalCases(repoRoot)
	if err != nil {
		return Report{}, err
	}
	incidentCases, err := loadIncidentCases(repoRoot)
	if err != nil {
		return Report{}, err
	}
	retrievalCases = filterRetrievalCases(retrievalCases, scope)
	incidentCases = filterIncidentCases(incidentCases, scope)

	kb, cleanup, err := buildKnowledgeBase(ctx, repoRoot)
	if err != nil {
		return Report{}, err
	}
	defer cleanup()

	retrievalReport, err := runRetrievalSuite(ctx, kb, retrievalCases)
	if err != nil {
		return Report{}, err
	}
	workflowReport, err := runWorkflowSuite(ctx, kb, incidentCases)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		GeneratedAt: time.Now().UTC(),
		Scope:       scope,
		Retrieval:   retrievalReport,
		Workflow:    workflowReport,
		Passed:      retrievalReport.CasesRun == retrievalReport.CasesPassed && workflowReport.CasesRun == workflowReport.CasesPassed,
	}
	return report, nil
}

func buildKnowledgeBase(ctx context.Context, repoRoot string) (rag.KnowledgeBase, func(), error) {
	tempDir, err := os.MkdirTemp("", "ai-sre-agent-eval-rag-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tempDir) }

	cfg := rag.DefaultConfig()
	cfg.Enabled = true
	cfg.DatasetPath = filepath.Join(repoRoot, "eval_data", "knowledge")
	cfg.IndexPath = filepath.Join(tempDir, "index.json")
	cfg.RebuildPolicy = "manual"
	cfg.TopK = 4
	cfg.MaxSnippetChars = 320
	service, err := rag.NewService(cfg, zap.NewNop())
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if _, err := service.Rebuild(ctx); err != nil {
		cleanup()
		return nil, nil, err
	}
	return service, cleanup, nil
}

func runRetrievalSuite(ctx context.Context, kb rag.KnowledgeBase, cases []RetrievalCase) (RetrievalSuiteReport, error) {
	report := RetrievalSuiteReport{
		Cases: make([]RetrievalCaseResult, 0, len(cases)),
	}
	if len(cases) == 0 {
		return report, nil
	}

	sumRecall := 0.0
	sumPrecision := 0.0
	sumContextPrecision := 0.0
	sumContextRecall := 0.0
	sumSignalCoverage := 0.0
	sumIntentAccuracy := 0.0
	sumNoise := 0.0
	sumLatency := 0.0

	for _, item := range cases {
		result, err := runRetrievalCase(ctx, kb, item)
		if err != nil {
			return report, err
		}
		report.Cases = append(report.Cases, result)
		report.CasesRun++
		if result.Passed {
			report.CasesPassed++
		} else {
			report.FailedCaseIDs = append(report.FailedCaseIDs, result.ID)
		}
		sumRecall += result.RecallAtK
		sumPrecision += result.PrecisionAtK
		sumContextPrecision += result.ContextPrecision
		sumContextRecall += result.ContextRecall
		sumSignalCoverage += result.SignalCoverage
		if result.IntentMatched {
			sumIntentAccuracy += 1
		}
		if item.NoisyQuery != "" {
			sumNoise += result.NoisyRecallAtK
		} else {
			sumNoise += 1
		}
		sumLatency += float64(result.LatencyMS)
	}

	count := float64(report.CasesRun)
	report.RecallAtK = sumRecall / count
	report.PrecisionAtK = sumPrecision / count
	report.ContextPrecision = sumContextPrecision / count
	report.ContextRecall = sumContextRecall / count
	report.SignalCoverage = sumSignalCoverage / count
	report.IntentAccuracy = sumIntentAccuracy / count
	report.NoiseRobustness = sumNoise / count
	report.AverageLatencyMS = sumLatency / count
	return report, nil
}

func runRetrievalCase(ctx context.Context, kb rag.KnowledgeBase, item RetrievalCase) (RetrievalCaseResult, error) {
	topK := item.TopK
	if topK <= 0 {
		topK = 4
	}
	minRecall := item.MinRecallAtK
	if minRecall <= 0 {
		minRecall = 1
	}
	minPrecision := item.MinPrecisionAtK
	if minPrecision <= 0 {
		minPrecision = 0.25
	}
	minNoisy := item.MinNoisyRecallAtK
	if item.NoisyQuery != "" && minNoisy <= 0 {
		minNoisy = minRecall
	}

	req := rag.QueryRequest{
		Query:  item.Query,
		TopK:   topK,
		Intent: item.Intent,
	}
	result, err := kb.Query(ctx, req)
	if err != nil {
		return RetrievalCaseResult{}, err
	}
	recall, precision, relevantHitCount, matchedPaths := scoreRetrievalHits(result.Hits, item.ExpectedPaths)
	contextPrecision := precision
	contextRecall := recall
	signalCoverage := scoreSignalCoverage(result.Hits, item.ExpectedSignals)
	intentMatched := item.Intent == "" || strings.EqualFold(strings.TrimSpace(result.Intent), strings.TrimSpace(item.Intent))
	knowledgeTypes := topKnowledgeTypes(result.Hits)
	sourcePaths := topSourcePaths(result.Hits)
	noisyRecall := 0.0
	if strings.TrimSpace(item.NoisyQuery) != "" {
		noisy, noisyErr := kb.Query(ctx, rag.QueryRequest{
			Query:  item.NoisyQuery,
			TopK:   topK,
			Intent: item.Intent,
		})
		if noisyErr != nil {
			return RetrievalCaseResult{}, noisyErr
		}
		noisyRecall, _, _, _ = scoreRetrievalHits(noisy.Hits, item.ExpectedPaths)
	}

	out := RetrievalCaseResult{
		ID:                item.ID,
		Description:       item.Description,
		TopK:              topK,
		RecallAtK:         recall,
		PrecisionAtK:      precision,
		ContextPrecision:  contextPrecision,
		ContextRecall:     contextRecall,
		SignalCoverage:    signalCoverage,
		IntentMatched:     intentMatched,
		NoisyRecallAtK:    noisyRecall,
		LatencyMS:         result.LatencyMS,
		TopSourcePaths:    sourcePaths,
		TopKnowledgeTypes: knowledgeTypes,
	}
	if recall < minRecall {
		out.Failures = append(out.Failures, fmt.Sprintf("recall@k %.2f below minimum %.2f", recall, minRecall))
	}
	if precision < minPrecision {
		out.Failures = append(out.Failures, fmt.Sprintf("precision@k %.2f below minimum %.2f", precision, minPrecision))
	}
	if len(item.ExpectedKnowledgeTypes) > 0 && !hitSetContains(knowledgeTypes, item.ExpectedKnowledgeTypes) {
		out.Failures = append(out.Failures, fmt.Sprintf("top knowledge types %v do not include expected %v", knowledgeTypes, item.ExpectedKnowledgeTypes))
	}
	if len(item.ExpectedCaseTypes) > 0 && !caseTypeHit(result.Hits, item.ExpectedCaseTypes) {
		out.Failures = append(out.Failures, fmt.Sprintf("retrieved case types do not include expected %v", item.ExpectedCaseTypes))
	}
	if !intentMatched {
		out.Failures = append(out.Failures, fmt.Sprintf("retrieval intent %q did not match expected %q", result.Intent, item.Intent))
	}
	if strings.TrimSpace(item.NoisyQuery) != "" && noisyRecall < minNoisy {
		out.Failures = append(out.Failures, fmt.Sprintf("noisy recall@k %.2f below minimum %.2f", noisyRecall, minNoisy))
	}
	if relevantHitCount == 0 {
		out.Failures = append(out.Failures, fmt.Sprintf("expected paths %v not found in %v", item.ExpectedPaths, sourcePaths))
	}
	if len(matchedPaths) == 0 && len(item.ExpectedPaths) > 0 {
		out.Failures = append(out.Failures, "no expected retrieval targets were matched")
	}
	out.Passed = len(out.Failures) == 0
	return out, nil
}

func runWorkflowSuite(ctx context.Context, kb rag.KnowledgeBase, cases []IncidentCase) (WorkflowSuiteReport, error) {
	report := WorkflowSuiteReport{
		Cases: make([]WorkflowCaseResult, 0, len(cases)),
	}
	if len(cases) == 0 {
		return report, nil
	}

	sumTop1 := 0.0
	sumTop3 := 0.0
	sumFault := 0.0
	sumEvidence := 0.0
	sumTrajectory := 0.0
	sumQueryPath := 0.0
	sumRecCorrect := 0.0
	sumRecSafety := 0.0
	sumGrounded := 0.0
	improvementEligible := 0.0
	improvementPassed := 0.0

	for _, item := range cases {
		result, err := runWorkflowCase(ctx, kb, item)
		if err != nil {
			return report, err
		}
		report.Cases = append(report.Cases, result)
		report.CasesRun++
		if result.Passed {
			report.CasesPassed++
		} else {
			report.FailedCaseIDs = append(report.FailedCaseIDs, result.ID)
		}
		if result.RootCauseTop1 {
			sumTop1 += 1
		}
		if result.RootCauseTop3 {
			sumTop3 += 1
		}
		if result.FaultDomainMatched {
			sumFault += 1
		}
		sumEvidence += result.EvidenceCoverage
		sumTrajectory += result.TrajectoryScore
		if item.Expected.QueryShouldUseRAG {
			if result.QueryUsedRAG {
				sumQueryPath += 1
			}
		} else {
			sumQueryPath += 1
		}
		sumRecCorrect += result.RecommendationCoverageWithRAG
		if result.RecommendationSafety {
			sumRecSafety += 1
		}
		sumGrounded += result.GroundedCommandRate
		if item.Expected.RAGShouldImproveRecommendations {
			improvementEligible += 1
			if result.RAGImproved {
				improvementPassed += 1
			}
		}
	}

	count := float64(report.CasesRun)
	report.RootCauseAccuracyAt1 = sumTop1 / count
	report.RootCauseAccuracyAt3 = sumTop3 / count
	report.FaultDomainAccuracy = sumFault / count
	report.EvidenceCoverage = sumEvidence / count
	report.TrajectoryAccuracy = sumTrajectory / count
	report.QueryPathAccuracy = sumQueryPath / count
	report.RecommendationCorrectness = sumRecCorrect / count
	report.RecommendationSafety = sumRecSafety / count
	report.GroundedCommandRate = sumGrounded / count
	if improvementEligible > 0 {
		report.RAGImprovementRate = improvementPassed / improvementEligible
	} else {
		report.RAGImprovementRate = 1
	}
	return report, nil
}

func runWorkflowCase(ctx context.Context, kb rag.KnowledgeBase, item IncidentCase) (WorkflowCaseResult, error) {
	now := time.Now().UTC()
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	if err := seedIncidentScenario(store, index, item.CollectorID, item.Scenario, now); err != nil {
		return WorkflowCaseResult{}, err
	}

	window := time.Duration(item.WindowMinutes) * time.Minute
	if window <= 0 {
		window = 45 * time.Minute
	}
	trigger := firstNonEmpty(item.Trigger, "incident_alert")

	noRAGEngine := agentcore.NewWorkflowEngine(agentcore.DefaultWorkflowConfig(), store, index, nil, zap.NewNop())
	withRAGEngine := agentcore.NewWorkflowEngine(agentcore.DefaultWorkflowConfig(), store, index, nil, zap.NewNop())
	withRAGEngine.SetKnowledgeBase(kb)

	var err error
	_, err = noRAGEngine.EvaluateJointRisk(ctx, agentcore.WorkflowRequest{
		CollectorID: item.CollectorID,
		Window:      window,
		Trigger:     trigger,
	})
	if err != nil {
		return WorkflowCaseResult{}, err
	}
	withRAGJoint, err := withRAGEngine.EvaluateJointRisk(ctx, agentcore.WorkflowRequest{
		CollectorID: item.CollectorID,
		Window:      window,
		Trigger:     trigger,
	})
	if err != nil {
		return WorkflowCaseResult{}, err
	}
	noRAGRCA, err := noRAGEngine.BuildRCAWorkflow(ctx, agentcore.WorkflowRequest{
		CollectorID: item.CollectorID,
		Window:      window,
		Trigger:     trigger,
	})
	if err != nil {
		return WorkflowCaseResult{}, err
	}
	withRAGRCA, err := withRAGEngine.BuildRCAWorkflow(ctx, agentcore.WorkflowRequest{
		CollectorID: item.CollectorID,
		Window:      window,
		Trigger:     trigger,
	})
	if err != nil {
		return WorkflowCaseResult{}, err
	}

	queryNoRAG, queryWithRAG, err := runQueryComparisons(ctx, store, kb, item)
	if err != nil {
		return WorkflowCaseResult{}, err
	}

	top1 := rootCauseMatch(withRAGRCA, item.Expected.RootCauseAny, 1)
	top3 := rootCauseMatch(withRAGRCA, item.Expected.RootCauseAny, 3)
	faultMatched := faultDomainMatch(withRAGJoint, withRAGRCA, item.Expected.FaultDomains)
	evidenceCoverage := evidenceCoverage(withRAGRCA, item.Expected.RequiredEvidence)
	trajectoryScore := trajectoryCoverage(withRAGJoint, withRAGRCA, item.Expected)
	recCoverageNoRAG := recommendationCoverage(noRAGRCA.Recommendations, item.Expected.RequiredRecommendationSubstrings)
	recCoverageWithRAG := recommendationCoverage(withRAGRCA.Recommendations, item.Expected.RequiredRecommendationSubstrings)
	recSafety, safetyFailures := recommendationSafety(withRAGRCA.Recommendations, item.Expected.ForbiddenRecommendationSubstrings)
	groundedRate, groundedFailures := groundedCommandRate(withRAGRCA)
	queryUsedRAG := len(queryWithRAG.RetrievedDocs) > 0
	queryPathRecall := retrievalPathCoverage(searchHitPaths(queryWithRAG.RetrievedDocs), item.Expected.RequiredRetrievalPaths)
	ragPaths := mergedRetrievalPaths(withRAGJoint, withRAGRCA)
	ragImproved := recCoverageWithRAG > recCoverageNoRAG+0.01

	out := WorkflowCaseResult{
		ID:                            item.ID,
		Description:                   item.Description,
		RootCauseTop1:                 top1,
		RootCauseTop3:                 top3,
		FaultDomainMatched:            faultMatched,
		EvidenceCoverage:              evidenceCoverage,
		TrajectoryScore:               trajectoryScore,
		QueryUsedRAG:                  queryUsedRAG,
		QueryRAGPathRecall:            queryPathRecall,
		RecommendationCoverageNoRAG:   recCoverageNoRAG,
		RecommendationCoverageWithRAG: recCoverageWithRAG,
		RecommendationSafety:          recSafety,
		GroundedCommandRate:           groundedRate,
		RAGImproved:                   ragImproved,
		TopRootCause:                  topRootCause(withRAGRCA),
		TopRecommendation:             topRecommendation(withRAGRCA.Recommendations),
		RetrievalPaths:                ragPaths,
	}

	if !top3 {
		out.Failures = append(out.Failures, fmt.Sprintf("top-3 root cause did not match expected %v (top=%q)", item.Expected.RootCauseAny, out.TopRootCause))
	}
	if !faultMatched && len(item.Expected.FaultDomains) > 0 {
		out.Failures = append(out.Failures, fmt.Sprintf("fault domains did not match expected %v", item.Expected.FaultDomains))
	}
	if evidenceCoverage < 0.5 && len(item.Expected.RequiredEvidence) > 0 {
		out.Failures = append(out.Failures, fmt.Sprintf("evidence coverage %.2f below minimum 0.50", evidenceCoverage))
	}
	if trajectoryScore < 0.66 {
		out.Failures = append(out.Failures, fmt.Sprintf("trajectory score %.2f below minimum 0.66", trajectoryScore))
	}
	if item.Expected.QueryShouldUseRAG && !queryUsedRAG {
		out.Failures = append(out.Failures, "query-service did not attach the expected retrieval evidence")
	}
	if len(item.Expected.RequiredRetrievalPaths) > 0 && retrievalPathCoverage(ragPaths, item.Expected.RequiredRetrievalPaths) <= 0 {
		out.Failures = append(out.Failures, fmt.Sprintf("workflow retrieval did not surface expected paths %v", item.Expected.RequiredRetrievalPaths))
	}
	out.Failures = append(out.Failures, safetyFailures...)
	out.Failures = append(out.Failures, groundedFailures...)
	if len(queryNoRAG.RetrievedDocs) > 0 {
		out.Failures = append(out.Failures, "no-rag query baseline unexpectedly returned retrieved documents")
	}
	out.Passed = len(out.Failures) == 0
	return out, nil
}

func runQueryComparisons(ctx context.Context, store *ingest.MemoryStore, kb rag.KnowledgeBase, item IncidentCase) (agentcore.QueryResponse, agentcore.QueryResponse, error) {
	cfg := agentcore.DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.RAG = false
	noRAGService, err := agentcore.NewQueryService(cfg, store, nil, zap.NewNop())
	if err != nil {
		return agentcore.QueryResponse{}, agentcore.QueryResponse{}, err
	}
	cfg.RAG = true
	withRAGService, err := agentcore.NewQueryService(cfg, store, nil, zap.NewNop())
	if err != nil {
		return agentcore.QueryResponse{}, agentcore.QueryResponse{}, err
	}
	withRAGService.SetRetriever(kb)

	noRAG, err := noRAGService.Query(ctx, agentcore.QueryRequest{
		Query: item.Query,
		Node:  item.CollectorID,
	})
	if err != nil {
		return agentcore.QueryResponse{}, agentcore.QueryResponse{}, err
	}
	withRAG, err := withRAGService.Query(ctx, agentcore.QueryRequest{
		Query: item.Query,
		Node:  item.CollectorID,
	})
	if err != nil {
		return agentcore.QueryResponse{}, agentcore.QueryResponse{}, err
	}
	return noRAG, withRAG, nil
}

func scoreRetrievalHits(hits []rag.SearchHit, expectedPaths []string) (recall, precision float64, relevantHits int, matchedPaths []string) {
	if len(expectedPaths) == 0 {
		return 1, 1, 0, nil
	}
	seen := map[string]struct{}{}
	for _, hit := range hits {
		path := strings.ToLower(strings.TrimSpace(hit.SourcePath))
		for _, expected := range expectedPaths {
			expected = strings.ToLower(strings.TrimSpace(expected))
			if expected == "" || !strings.Contains(path, expected) {
				continue
			}
			relevantHits++
			if _, ok := seen[expected]; !ok {
				seen[expected] = struct{}{}
				matchedPaths = append(matchedPaths, expected)
			}
			break
		}
	}
	recall = float64(len(matchedPaths)) / float64(len(expectedPaths))
	if len(hits) == 0 {
		return recall, 0, relevantHits, matchedPaths
	}
	precision = float64(relevantHits) / float64(len(hits))
	return recall, precision, relevantHits, matchedPaths
}

func scoreSignalCoverage(hits []rag.SearchHit, expectedSignals []string) float64 {
	if len(expectedSignals) == 0 {
		return 1
	}
	joined := strings.ToLower(strings.Join(flattenHitSignals(hits), " "))
	matched := 0
	for _, signal := range expectedSignals {
		if strings.Contains(joined, strings.ToLower(strings.TrimSpace(signal))) {
			matched++
		}
	}
	return float64(matched) / float64(len(expectedSignals))
}

func caseTypeHit(hits []rag.SearchHit, expected []string) bool {
	expectedSet := make(map[string]struct{}, len(expected))
	for _, item := range expected {
		expectedSet[strings.ToLower(strings.TrimSpace(item))] = struct{}{}
	}
	for _, hit := range hits {
		if _, ok := expectedSet[strings.ToLower(strings.TrimSpace(hit.CaseType))]; ok {
			return true
		}
	}
	return false
}

func topSourcePaths(hits []rag.SearchHit) []string {
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		if path := strings.TrimSpace(hit.SourcePath); path != "" {
			out = append(out, path)
		}
		if len(out) >= 4 {
			break
		}
	}
	return out
}

func topKnowledgeTypes(hits []rag.SearchHit) []string {
	out := make([]string, 0, len(hits))
	seen := map[string]struct{}{}
	for _, hit := range hits {
		typ := strings.TrimSpace(hit.KnowledgeType)
		if typ == "" {
			continue
		}
		if _, ok := seen[typ]; ok {
			continue
		}
		seen[typ] = struct{}{}
		out = append(out, typ)
		if len(out) >= 4 {
			break
		}
	}
	return out
}

func hitSetContains(actual []string, expected []string) bool {
	actualSet := map[string]struct{}{}
	for _, item := range actual {
		actualSet[strings.ToLower(strings.TrimSpace(item))] = struct{}{}
	}
	for _, item := range expected {
		if _, ok := actualSet[strings.ToLower(strings.TrimSpace(item))]; ok {
			return true
		}
	}
	return false
}

func flattenHitSignals(hits []rag.SearchHit) []string {
	out := make([]string, 0, len(hits)*4)
	for _, hit := range hits {
		out = append(out, hit.Signals...)
		out = append(out, hit.Tags...)
	}
	return out
}

func rootCauseMatch(report agentcore.RCAWorkflowReport, expected []string, topN int) bool {
	if len(expected) == 0 {
		return true
	}
	texts := []string{
		report.StructuredReport.MostLikelyCause,
		report.SynthesizedIncident.CandidateRootCauseCluster,
	}
	for i, hyp := range report.Hypotheses {
		if topN > 0 && i >= topN {
			break
		}
		texts = append(texts, hyp.Title, hyp.Description)
	}
	return containsAnyText(texts, expected)
}

func topRootCause(report agentcore.RCAWorkflowReport) string {
	if len(report.Hypotheses) > 0 {
		return firstNonEmpty(report.Hypotheses[0].Title, report.Hypotheses[0].Description, report.StructuredReport.MostLikelyCause)
	}
	return report.StructuredReport.MostLikelyCause
}

func faultDomainMatch(joint agentcore.JointRiskAssessment, report agentcore.RCAWorkflowReport, expected []string) bool {
	if len(expected) == 0 {
		return true
	}
	domains := derivedFaultDomains(joint, report)
	for _, item := range expected {
		if !slices.Contains(domains, strings.ToLower(strings.TrimSpace(item))) {
			return false
		}
	}
	return true
}

func derivedFaultDomains(joint agentcore.JointRiskAssessment, report agentcore.RCAWorkflowReport) []string {
	joined := strings.ToLower(strings.Join([]string{
		joint.Summary,
		joint.ActionableWhy,
		topRootCause(report),
		report.StructuredReport.IncidentSummary,
		report.StructuredReport.MostLikelyCause,
		strings.Join(report.StructuredReport.SupportingSignals, " "),
		strings.Join(report.StructuredReport.Symptoms, " "),
	}, " "))
	domains := make([]string, 0, 5)
	add := func(name string) {
		if !slices.Contains(domains, name) {
			domains = append(domains, name)
		}
	}
	if strings.Contains(joined, "memory") || strings.Contains(joined, "rss") || strings.Contains(joined, "oom") || strings.Contains(joined, "reclaim") {
		add("memory")
		add("runtime")
	}
	if strings.Contains(joined, "storage") || strings.Contains(joined, "disk") || strings.Contains(joined, "checkpoint") || strings.Contains(joined, "io") {
		add("storage")
	}
	if strings.Contains(joined, "network") || strings.Contains(joined, "packet loss") || strings.Contains(joined, "retransmit") || strings.Contains(joined, "softnet") {
		add("network")
	}
	for _, trend := range joint.TrendAssessments {
		switch strings.ToLower(strings.TrimSpace(trend.Category)) {
		case "hardware":
			add("hardware")
		case "runtime":
			add("runtime")
		}
	}
	for _, event := range joint.InvestigationEvents {
		if strings.EqualFold(strings.TrimSpace(event.Category), "hardware_warning") {
			add("hardware")
		}
	}
	sort.Strings(domains)
	return domains
}

func evidenceCoverage(report agentcore.RCAWorkflowReport, expected []string) float64 {
	if len(expected) == 0 {
		return 1
	}
	texts := make([]string, 0, len(report.Evidence)+len(report.RetrievedDocs)+8)
	for _, evidence := range report.Evidence {
		texts = append(texts, evidence.Summary, evidence.Snippet)
	}
	for _, item := range report.RetrievedDocs {
		texts = append(texts, item.Title, item.Summary, item.Snippet)
	}
	for _, item := range report.RetrievedCases {
		texts = append(texts, item.Title, item.Summary, item.Snippet)
	}
	for _, item := range report.RetrievedRunbooks {
		texts = append(texts, item.Title, item.Summary, item.Snippet)
	}
	matched := 0
	for _, target := range expected {
		if containsAnyText(texts, []string{target}) {
			matched++
		}
	}
	return float64(matched) / float64(len(expected))
}

func trajectoryCoverage(joint agentcore.JointRiskAssessment, report agentcore.RCAWorkflowReport, expected IncidentExpectations) float64 {
	scores := make([]float64, 0, 3)
	if len(expected.ExpectedTrendKeys) > 0 {
		actual := make([]string, 0, len(joint.TrendAssessments))
		for _, item := range joint.TrendAssessments {
			actual = append(actual, item.SeriesKey)
		}
		scores = append(scores, stringCoverage(actual, expected.ExpectedTrendKeys))
	}
	if len(expected.ExpectedEventCategories) > 0 {
		actual := make([]string, 0, len(joint.InvestigationEvents))
		for _, item := range joint.InvestigationEvents {
			actual = append(actual, item.Category)
		}
		scores = append(scores, stringCoverage(actual, expected.ExpectedEventCategories))
	}
	if len(expected.ExpectedToolCalls) > 0 {
		actual := make([]string, 0, len(report.ToolCalls)+len(joint.ToolCalls))
		for _, item := range joint.ToolCalls {
			actual = append(actual, string(item.Tool))
		}
		for _, item := range report.ToolCalls {
			actual = append(actual, string(item.Tool))
		}
		scores = append(scores, stringCoverage(actual, expected.ExpectedToolCalls))
	}
	if len(scores) == 0 {
		return 1
	}
	total := 0.0
	for _, score := range scores {
		total += score
	}
	return total / float64(len(scores))
}

func recommendationCoverage(recs []agentcore.WorkflowRecommendation, expected []string) float64 {
	if len(expected) == 0 {
		return 1
	}
	texts := make([]string, 0, len(recs)*4)
	for _, rec := range recs {
		texts = append(texts, rec.Summary, rec.Details, rec.Rationale)
		texts = append(texts, rec.Checks...)
	}
	matched := 0
	for _, target := range expected {
		if containsAnyText(texts, []string{target}) {
			matched++
		}
	}
	return float64(matched) / float64(len(expected))
}

func recommendationSafety(recs []agentcore.WorkflowRecommendation, forbidden []string) (bool, []string) {
	failures := make([]string, 0, 4)
	if len(recs) > 0 && !recs[0].Safe && !recs[0].DryRunDefault {
		failures = append(failures, "top recommendation is not safe-first or dry-run-first")
	}
	for _, rec := range recs {
		if !rec.Safe {
			if !rec.RequiresApproval {
				failures = append(failures, fmt.Sprintf("unsafe recommendation %q is missing approval guard", rec.Summary))
			}
			if !rec.DryRunDefault {
				failures = append(failures, fmt.Sprintf("unsafe recommendation %q is missing dry-run default", rec.Summary))
			}
			if strings.TrimSpace(rec.RollbackHint) == "" {
				failures = append(failures, fmt.Sprintf("unsafe recommendation %q is missing rollback guidance", rec.Summary))
			}
		}
		for _, text := range append([]string{rec.Summary, rec.Details}, rec.Checks...) {
			for _, bad := range forbidden {
				if bad != "" && strings.Contains(strings.ToLower(text), strings.ToLower(bad)) {
					failures = append(failures, fmt.Sprintf("forbidden recommendation text %q matched %q", text, bad))
				}
			}
		}
	}
	return len(failures) == 0, failures
}

func groundedCommandRate(report agentcore.RCAWorkflowReport) (float64, []string) {
	docCommands := make([]string, 0, len(report.RetrievedDocs)*4)
	for _, item := range report.RetrievedDocs {
		docCommands = append(docCommands, item.Commands...)
	}
	for _, item := range report.RetrievedCases {
		docCommands = append(docCommands, item.Commands...)
	}
	for _, item := range report.RetrievedRunbooks {
		docCommands = append(docCommands, item.Commands...)
	}
	if len(docCommands) == 0 {
		return 1, nil
	}
	total := 0
	matched := 0
	failures := make([]string, 0, 4)
	for _, rec := range report.Recommendations {
		for _, check := range rec.Checks {
			trimmed := strings.TrimSpace(check)
			if !strings.HasPrefix(strings.ToLower(trimmed), "run:") {
				continue
			}
			total++
			command := strings.TrimSpace(strings.TrimPrefix(trimmed, "run:"))
			if containsAnyText(docCommands, []string{command}) {
				matched++
			} else {
				failures = append(failures, fmt.Sprintf("command %q was not grounded in retrieved commands", command))
			}
		}
	}
	if total == 0 {
		return 1, nil
	}
	return float64(matched) / float64(total), failures
}

func mergedRetrievalPaths(joint agentcore.JointRiskAssessment, report agentcore.RCAWorkflowReport) []string {
	out := make([]string, 0, len(joint.RetrievedDocs)+len(report.RetrievedDocs)+len(report.RetrievedCases)+len(report.RetrievedRunbooks))
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || slices.Contains(out, path) {
			return
		}
		out = append(out, path)
	}
	for _, item := range joint.RetrievedDocs {
		add(item.SourcePath)
	}
	for _, item := range joint.RetrievedCases {
		add(item.SourcePath)
	}
	for _, item := range joint.RetrievedRunbooks {
		add(item.SourcePath)
	}
	for _, item := range report.RetrievedDocs {
		add(item.SourcePath)
	}
	for _, item := range report.RetrievedCases {
		add(item.SourcePath)
	}
	for _, item := range report.RetrievedRunbooks {
		add(item.SourcePath)
	}
	for _, item := range report.SimilarIncidentPatterns {
		add(item.SourcePath)
	}
	return out
}

func retrievalPathCoverage(paths []string, expected []string) float64 {
	if len(expected) == 0 {
		return 1
	}
	matched := 0
	for _, target := range expected {
		target = strings.ToLower(strings.TrimSpace(target))
		for _, path := range paths {
			if strings.Contains(strings.ToLower(strings.TrimSpace(path)), target) {
				matched++
				break
			}
		}
	}
	return float64(matched) / float64(len(expected))
}

func searchHitPaths(hits []rag.SearchHit) []string {
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		if path := strings.TrimSpace(hit.SourcePath); path != "" {
			out = append(out, path)
		}
	}
	return out
}

func stringCoverage(actual []string, expected []string) float64 {
	if len(expected) == 0 {
		return 1
	}
	matched := 0
	for _, target := range expected {
		for _, item := range actual {
			if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(target)) {
				matched++
				break
			}
		}
	}
	return float64(matched) / float64(len(expected))
}

func containsAnyText(texts []string, needles []string) bool {
	for _, text := range texts {
		lower := strings.ToLower(strings.TrimSpace(text))
		if lower == "" {
			continue
		}
		for _, needle := range needles {
			needle = strings.ToLower(strings.TrimSpace(needle))
			if needle != "" && strings.Contains(lower, needle) {
				return true
			}
		}
	}
	return false
}

func topRecommendation(recs []agentcore.WorkflowRecommendation) string {
	for _, rec := range recs {
		if trimmed := strings.TrimSpace(rec.Summary); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
