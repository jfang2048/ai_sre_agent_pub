"""Haystack-backed execution runtime for SRE agent orchestration."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timezone
import logging
from typing import Any, Dict, List, Mapping, Optional

from .reasoner import Reasoner
from .tool_executor import ToolExecutor
from .tools import Tool
from .types import ExecutionPlan, PlanStep, ReasoningOutput, StepTrace, TelemetryEnvelope

try:  # Optional dependency: runtime can fail open to native execution when unavailable.
    from haystack import Pipeline, component
except Exception:  # pragma: no cover - exercised when dependency is absent
    Pipeline = None
    component = None


def is_haystack_available() -> bool:
    """Return whether Haystack is importable in the current runtime."""
    return Pipeline is not None and component is not None


@dataclass
class PipelineExecutionResult:
    """Result bundle returned by the runtime pipeline."""

    state: Dict[str, Any]
    traces: List[StepTrace]
    decision: ReasoningOutput


if component is not None:

    @component
    class ToolStepComponent:
        """Execute one registered tool with controlled retry/timeout semantics."""

        def __init__(self, tool: Tool, executor: ToolExecutor) -> None:
            self._tool = tool
            self._executor = executor

        @component.output_types(output=dict, trace=StepTrace)
        def run(
            self,
            telemetry: TelemetryEnvelope,
            step: PlanStep,
            state: Optional[Dict[str, Any]] = None,
            enabled: bool = True,
        ) -> Dict[str, Any]:
            if not enabled:
                now = datetime.now(timezone.utc)
                return {
                    "output": {},
                    "trace": StepTrace(
                        step_id=step.step_id,
                        kind=step.kind,
                        tool_name=step.tool_name,
                        status="skipped",
                        started_at=now,
                        finished_at=now,
                        duration_ms=0,
                        attempt=1,
                        error="",
                        output_summary={},
                    ),
                }

            trace, output = self._executor.execute(step, self._tool, telemetry, state or {})
            return {"output": output, "trace": trace}


    @component
    class PlanStateComponent:
        """Materialize deterministic runtime state and ordered traces from tool outputs."""

        @component.output_types(state=dict, traces=list)
        def run(
            self,
            plan: ExecutionPlan,
            memory_recall_output: Optional[Dict[str, Any]] = None,
            memory_recall_trace: Optional[StepTrace] = None,
            metric_triage_output: Optional[Dict[str, Any]] = None,
            metric_triage_trace: Optional[StepTrace] = None,
            log_pattern_scan_output: Optional[Dict[str, Any]] = None,
            log_pattern_scan_trace: Optional[StepTrace] = None,
            context_lookup_output: Optional[Dict[str, Any]] = None,
            context_lookup_trace: Optional[StepTrace] = None,
        ) -> Dict[str, Any]:
            outputs: Dict[str, Dict[str, Any]] = {
                "memory_recall": memory_recall_output or {},
                "metric_triage": metric_triage_output or {},
                "log_pattern_scan": log_pattern_scan_output or {},
                "context_lookup": context_lookup_output or {},
            }
            traces_by_tool: Dict[str, Optional[StepTrace]] = {
                "memory_recall": memory_recall_trace,
                "metric_triage": metric_triage_trace,
                "log_pattern_scan": log_pattern_scan_trace,
                "context_lookup": context_lookup_trace,
            }

            state: Dict[str, Any] = {}
            traces: List[StepTrace] = []

            for step in plan.steps:
                if step.kind != "tool":
                    continue

                trace = traces_by_tool.get(step.tool_name)
                if trace is None:
                    now = datetime.now(timezone.utc)
                    trace = StepTrace(
                        step_id=step.step_id,
                        kind=step.kind,
                        tool_name=step.tool_name,
                        status="failed",
                        started_at=now,
                        finished_at=now,
                        duration_ms=0,
                        attempt=1,
                        error=f"tool {step.tool_name} missing from pipeline output",
                        output_summary={},
                    )

                traces.append(trace)
                if trace.status == "success":
                    state[step.tool_name] = outputs.get(step.tool_name, {})
                elif step.required:
                    state[f"{step.tool_name}_error"] = trace.error

            return {"state": state, "traces": traces}


    @component
    class ReasoningComponent:
        """Run deterministic + optional LLM reasoning over accumulated state."""

        def __init__(self, reasoner: Reasoner) -> None:
            self._reasoner = reasoner

        @component.output_types(decision=ReasoningOutput)
        def run(
            self,
            telemetry: TelemetryEnvelope,
            state: Dict[str, Any],
            traces: List[StepTrace],
        ) -> Dict[str, Any]:
            return {"decision": self._reasoner.synthesize(telemetry, state, traces)}


class HaystackPipelineRuntime:
    """Build and execute the Haystack pipeline for one request."""

    def __init__(
        self,
        tools: Mapping[str, Tool],
        reasoner: Reasoner,
        logger: logging.Logger,
    ) -> None:
        if not is_haystack_available():
            raise RuntimeError("Haystack runtime requested but haystack-ai is not installed")

        assert Pipeline is not None
        self._logger = logger
        self._tool_executor = ToolExecutor(logger)
        self._tool_names = ["memory_recall", "metric_triage", "log_pattern_scan"]
        if "context_lookup" in tools:
            self._tool_names.append("context_lookup")

        self._pipeline = Pipeline()
        for tool_name in self._tool_names:
            tool = tools[tool_name]
            self._pipeline.add_component(
                tool_name,
                ToolStepComponent(tool=tool, executor=self._tool_executor),
            )

        self._pipeline.add_component("state_builder", PlanStateComponent())
        self._pipeline.add_component("reasoning", ReasoningComponent(reasoner))

        for tool_name in self._tool_names:
            self._pipeline.connect(f"{tool_name}.output", f"state_builder.{tool_name}_output")
            self._pipeline.connect(f"{tool_name}.trace", f"state_builder.{tool_name}_trace")

        self._pipeline.connect("state_builder.state", "reasoning.state")
        self._pipeline.connect("state_builder.traces", "reasoning.traces")

    def run(self, plan: ExecutionPlan, telemetry: TelemetryEnvelope) -> PipelineExecutionResult:
        """Execute the configured pipeline for one plan + telemetry bundle."""
        assert Pipeline is not None

        planned_steps = {
            step.tool_name: step for step in plan.steps if step.kind == "tool" and step.tool_name
        }

        data: Dict[str, Dict[str, Any]] = {
            "state_builder": {"plan": plan},
            "reasoning": {"telemetry": telemetry},
        }

        for tool_name in self._tool_names:
            step = planned_steps.get(tool_name)
            data[tool_name] = {
                "telemetry": telemetry,
                "step": step if step is not None else _skipped_step(tool_name),
                "state": {},
                "enabled": step is not None,
            }

        result = self._pipeline.run(data=data)

        state_result = result.get("state_builder", {})
        decision_result = result.get("reasoning", {})

        traces = state_result.get("traces", [])
        if not isinstance(traces, list):
            traces = []

        decision = decision_result.get("decision")
        if not isinstance(decision, ReasoningOutput):
            raise RuntimeError("haystack reasoning component returned an invalid decision payload")

        return PipelineExecutionResult(
            state=state_result.get("state", {}),
            traces=traces,
            decision=decision,
        )


def _skipped_step(tool_name: str) -> PlanStep:
    return PlanStep(
        step_id=f"step-{tool_name}-skipped",
        kind="tool",
        tool_name=tool_name,
        description="Tool skipped by planner.",
        required=False,
        timeout_seconds=0.1,
        retries=0,
    )
