package agent

import (
	"context"
	"time"
)

func (state *workflowState) beginPipelineStage(ctx context.Context, stageName string) PipelineStageResult {
	stage := PipelineStageResult{
		Name:         stageName,
		DetailStatus: "running",
		StartedAt:    time.Now().UTC(),
	}
	if state == nil || state.durableRun == nil {
		return stage
	}
	_ = state.engine.orchestrator.RecordStageStarted(ctx, state.workflowID, stageName)
	return stage
}

func (state *workflowState) completePipelineStage(ctx context.Context, stage *PipelineStageResult) {
	if state == nil || stage == nil {
		return
	}
	stage.DetailStatus = "completed"
	stage.CompletedAt = time.Now().UTC()
	stage.Summary = state.stageSummary(stage.Name)
	normalizePipelineStageResult(stage)
	state.stages = append(state.stages, *stage)
	state.engine.audit(state.workflowID, state.workflowType, stage.Name, "stage.completed", "success", state.collectorID, state.dryRun, state.engine.cfg.RequireApproval, true, nil, stage.Summary, nil)
	if state.durableRun != nil {
		_ = state.engine.orchestrator.RecordStageCompleted(ctx, state.workflowID, *stage)
	}
}

func (state *workflowState) failPipelineStage(ctx context.Context, stage *PipelineStageResult, stageErr error) error {
	if state == nil || stage == nil {
		return stageErr
	}
	stage.DetailStatus = "failed"
	stage.CompletedAt = time.Now().UTC()
	if stageErr != nil {
		stage.Summary = truncateString(stageErr.Error(), 220)
	}
	normalizePipelineStageResult(stage)
	state.stages = append(state.stages, *stage)
	state.engine.audit(state.workflowID, state.workflowType, stage.Name, "stage.failed", "failed", state.collectorID, state.dryRun, state.engine.cfg.RequireApproval, false, nil, stage.Summary, stageErr)
	if state.durableRun != nil {
		_ = state.engine.orchestrator.RecordStageFailed(ctx, state.workflowID, *stage)
	}
	return stageErr
}
