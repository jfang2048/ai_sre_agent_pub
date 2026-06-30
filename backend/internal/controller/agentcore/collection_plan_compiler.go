package agent

import (
	"fmt"
	"strings"
	"time"
)

const maxSceneRecollectionRounds = 2

func compileCollectionPlan(state *workflowState, roundIndex int) CollectionPlan {
	scene := state.sceneClassification.SceneFamily
	if scene == "" {
		scene = SceneFamilyResourceContention
	}

	modules := sceneModules(scene)
	targetScope := impactedScopeFromState(state)
	if len(targetScope) == 0 {
		targetScope = compactStrings(firstNonEmpty(state.collectorID, "fleet"))
	}

	samplingInterval := 3 * time.Second
	collectionWindow := 4 * time.Minute
	processTopK := 12
	logBudget := 20
	gpuMode := "summary"
	maxOverhead := 2.0
	maxBytes := int64(256 * 1024)
	maxEvents := 128

	switch scene {
	case SceneFamilyChangeInduced, SceneFamilyDeploymentRollout:
		samplingInterval = 5 * time.Second
		collectionWindow = 8 * time.Minute
		processTopK = 8
		logBudget = 40
		maxOverhead = 1.5
		maxBytes = 192 * 1024
	case SceneFamilyNetworkConnectivity:
		samplingInterval = 2 * time.Second
		collectionWindow = 3 * time.Minute
		processTopK = 10
		logBudget = 24
	case SceneFamilyStorageIO:
		samplingInterval = 2 * time.Second
		collectionWindow = 4 * time.Minute
		processTopK = 16
		logBudget = 18
	case SceneFamilyGPUInference:
		samplingInterval = 2 * time.Second
		collectionWindow = 3 * time.Minute
		processTopK = 12
		logBudget = 16
		gpuMode = "inference"
		maxOverhead = 2.5
	case SceneFamilyGPUTrainingOrCollective:
		samplingInterval = 2 * time.Second
		collectionWindow = 3 * time.Minute
		processTopK = 16
		logBudget = 20
		gpuMode = "collective"
		maxOverhead = 3.0
	case SceneFamilyKubernetesWorkload:
		samplingInterval = 4 * time.Second
		collectionWindow = 5 * time.Minute
		processTopK = 10
		logBudget = 30
	case SceneFamilyBareMetalKernelOrIRQ:
		samplingInterval = 2 * time.Second
		collectionWindow = 2 * time.Minute
		processTopK = 14
		logBudget = 18
		maxOverhead = 2.5
	case SceneFamilyDatabaseLikeLatencyPath:
		samplingInterval = 2 * time.Second
		collectionWindow = 4 * time.Minute
		processTopK = 12
		logBudget = 32
	case SceneFamilySecurityOrProcessAnomaly:
		samplingInterval = 2 * time.Second
		collectionWindow = 3 * time.Minute
		processTopK = 20
		logBudget = 24
		maxOverhead = 2.5
	}

	if roundIndex > 1 {
		collectionWindow = maxDuration(90*time.Second, collectionWindow/2)
		samplingInterval = maxDuration(1*time.Second, samplingInterval/2)
		processTopK = minInt(processTopK+4, 24)
		logBudget = minInt(logBudget+8, 48)
		maxEvents = minInt(maxEvents+64, 256)
	}

	goals := dedupeStrings(append(
		append([]string(nil), state.sceneClassification.MissingEvidence...),
		state.sceneClassification.CollectionHints...,
	))
	if len(goals) == 0 {
		goals = []string{"narrow scene uncertainty", "validate top hypothesis"}
	}

	return CollectionPlan{
		PlanID:                    fmt.Sprintf("plan-%s-r%d", sanitizeID(firstNonEmpty(state.workflowID, "scene")), roundIndex),
		SceneFamily:               scene,
		RoundIndex:                roundIndex,
		TargetScope:               targetScope,
		TargetCollectorsOrModules: modules,
		SamplingInterval:          samplingInterval,
		CollectionWindow:          collectionWindow,
		ProcessTopK:               processTopK,
		LogBudget:                 logBudget,
		EventFilters:              sceneEventFilters(scene, state),
		GPUDetailMode:             gpuMode,
		MaxOverheadPercent:        maxOverhead,
		MaxBytes:                  maxBytes,
		MaxEvents:                 maxEvents,
		EvidenceGoals:             goals,
		StopConditions:            sceneStopConditions(scene),
		TTL:                       collectionWindow + 2*time.Minute,
		Replayable:                true,
	}
}

func sceneModules(scene SceneFamily) []string {
	switch scene {
	case SceneFamilyChangeInduced:
		return []string{"change", "logs", "config", "kubernetes"}
	case SceneFamilyNetworkConnectivity:
		return []string{"metrics", "connectivity", "dns", "runtime", "topology"}
	case SceneFamilyStorageIO:
		return []string{"metrics", "storage", "process", "logs"}
	case SceneFamilyGPUInference:
		return []string{"gpu", "process", "logs", "service_health"}
	case SceneFamilyGPUTrainingOrCollective:
		return []string{"gpu", "process", "logs", "runtime"}
	case SceneFamilyDeploymentRollout:
		return []string{"change", "logs", "kubernetes", "container_revision"}
	case SceneFamilyKubernetesWorkload:
		return []string{"kubernetes", "container_revision", "logs", "service_health"}
	case SceneFamilyBareMetalKernelOrIRQ:
		return []string{"runtime", "process", "metrics", "logs"}
	case SceneFamilyDatabaseLikeLatencyPath:
		return []string{"metrics", "logs", "storage", "connectivity", "service_health"}
	case SceneFamilySecurityOrProcessAnomaly:
		return []string{"security", "security_graph", "process", "runtime", "logs"}
	default:
		return []string{"metrics", "process", "logs", "service_health"}
	}
}

func sceneEventFilters(scene SceneFamily, state *workflowState) []string {
	filters := []string{}
	switch scene {
	case SceneFamilyChangeInduced, SceneFamilyDeploymentRollout:
		filters = append(filters, "deploy", "rollout", "config", "revision", "feature_flag")
	case SceneFamilyNetworkConnectivity:
		filters = append(filters, "timeout", "dns", "connect", "retransmit", "socket")
	case SceneFamilyStorageIO:
		filters = append(filters, "io", "disk", "storage", "fsync", "throttle")
	case SceneFamilyGPUInference:
		filters = append(filters, "gpu", "cuda", "memory", "inference")
	case SceneFamilyGPUTrainingOrCollective:
		filters = append(filters, "gpu", "nccl", "collective", "allreduce", "fabric")
	case SceneFamilyKubernetesWorkload:
		filters = append(filters, "pod", "container", "restart", "evicted", "image")
	case SceneFamilyBareMetalKernelOrIRQ:
		filters = append(filters, "kernel", "irq", "softirq", "sched", "driver")
	case SceneFamilyDatabaseLikeLatencyPath:
		filters = append(filters, "latency", "query", "database", "transaction", "replica")
	case SceneFamilySecurityOrProcessAnomaly:
		filters = append(filters, "security", "process", "exec", "permission", "outbound")
	default:
		filters = append(filters, "cpu", "memory", "pressure", "resource")
	}
	for _, hypothesis := range state.hypotheses {
		filters = append(filters, strings.Fields(strings.ToLower(strings.TrimSpace(hypothesis.Title)))...)
		if len(filters) >= 12 {
			break
		}
	}
	return dedupeStrings(filters)
}

func sceneStopConditions(scene SceneFamily) []string {
	return []string{
		"evidence_goals_met",
		"scene_confidence_ge_0_75",
		"top_hypothesis_confidence_ge_0_80",
		"budget_exhausted",
		"ttl_expired",
	}
}

func budgetStateFromPlan(plan CollectionPlan, completedRounds int) InvestigationBudgetState {
	remainingRounds := maxSceneRecollectionRounds - completedRounds
	if remainingRounds < 0 {
		remainingRounds = 0
	}
	return InvestigationBudgetState{
		MaxOverheadPercent: plan.MaxOverheadPercent,
		RemainingBytes:     plan.MaxBytes,
		RemainingEvents:    plan.MaxEvents,
		RemainingRounds:    remainingRounds,
	}
}
