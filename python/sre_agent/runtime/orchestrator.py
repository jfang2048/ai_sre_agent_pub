"""Top-level orchestration engine for production reasoning workflows."""

from __future__ import annotations

import logging
import uuid
from typing import Any, Dict, List

from sre_agent.agents.base import AnalysisResult

from .config import RuntimeConfig
from .context_store import ContextStore
from .haystack_runtime import HaystackPipelineRuntime, is_haystack_available
from .memory_store import BoundedMemoryStore, MemoryEntry
from .planner import Planner
from .reasoner import Reasoner
from .structured_log import emit
from .tool_executor import ToolExecutor, ToolRegistry
from .tools import ContextLookupTool, LogPatternTool, MemoryRecallTool, MetricTriageTool
from .types import ExecutionPlan, PlanStep, ReasoningOutput, StepTrace, TelemetryEnvelope

logger = logging.getLogger(__name__)


class Orchestrator:
    """Coordinates plan -> tools -> reason flow with explicit state."""

    def __init__(self, config: RuntimeConfig | None = None) -> None:
        self._config = config or RuntimeConfig.from_env()
        self._memory = BoundedMemoryStore(
            max_events=self._config.memory_max_events,
            file_path=self._config.memory_file,
            logger=logger,
        )
        self._context = (
            ContextStore(
                paths=self._config.context_paths,
                max_chars=self._config.context_max_chars,
                extensions=self._config.context_extensions,
                logger=logger,
            )
            if self._config.context_enabled
            else None
        )

        self._planner = Planner(self._config)
        self._reasoner = Reasoner(self._config, logger)
        self._registry = ToolRegistry()
        self._executor = ToolExecutor(logger)
        self._register_tools()

        self._runtime_backend = self._resolve_runtime_backend()
        self._haystack_runtime = (
            self._build_haystack_runtime() if self._runtime_backend == "haystack" else None
        )

    def analyze(
        self,
        node_name: str,
        metrics: List[Dict[str, Any]],
        logs: List[Dict[str, Any]],
    ) -> AnalysisResult:
        """Analyze telemetry with explicit orchestration."""
        request_id = uuid.uuid4().hex
        telemetry = TelemetryEnvelope(
            node_name=node_name or "unknown",
            metrics=list(metrics[: self._config.max_metrics]),
            logs=list(logs[-self._config.max_logs :]),
            request_id=request_id,
        )

        emit(
            logger,
            "orchestrator_started",
            request_id=request_id,
            node_name=telemetry.node_name,
            metric_count=len(telemetry.metrics),
            log_count=len(telemetry.logs),
            runtime_backend=self._runtime_backend,
        )

        if not self._config.reasoning_enabled:
            return AnalysisResult(
                issue_detected=False,
                issue_type="reasoning_disabled",
                severity="info",
                confidence=0.0,
                root_cause="Reasoning runtime disabled via SRE_AGENT_REASONING_ENABLED.",
                remediation="Enable runtime to activate orchestrated incident analysis.",
                metadata={"request_id": request_id, "runtime_backend": self._runtime_backend},
            )

        plan = self._planner.build_plan(telemetry)
        active_backend = self._runtime_backend

        if self._runtime_backend == "haystack" and self._haystack_runtime is not None:
            try:
                execution = self._haystack_runtime.run(plan, telemetry)
                state = execution.state
                traces = execution.traces
                decision = execution.decision
            except Exception as exc:  # pylint: disable=broad-except
                emit(
                    logger,
                    "runtime_backend_execution_failed",
                    level="warning",
                    backend="haystack",
                    request_id=request_id,
                    error=str(exc),
                )
                if not self._config.runtime_fail_open:
                    raise
                active_backend = "native"
                state, traces, decision = self._run_native(plan, telemetry)
        else:
            state, traces, decision = self._run_native(plan, telemetry)
            active_backend = "native"

        if decision.issue_detected:
            self._memory.record(
                MemoryEntry(
                    node_name=telemetry.node_name,
                    issue_type=decision.issue_type,
                    severity=decision.severity,
                    confidence=decision.confidence,
                    root_cause=decision.root_cause,
                    remediation=decision.remediation,
                    metadata={"request_id": request_id, "provider": decision.provider},
                )
            )

        emit(
            logger,
            "orchestrator_completed",
            request_id=request_id,
            node_name=telemetry.node_name,
            issue_detected=decision.issue_detected,
            issue_type=decision.issue_type,
            severity=decision.severity,
            confidence=decision.confidence,
            provider=decision.provider,
            model=decision.model,
            runtime_backend=active_backend,
        )

        metadata = {
            "request_id": request_id,
            "plan_version": plan.version,
            "provider": decision.provider,
            "model": decision.model,
            "notes": decision.notes,
            "evidence": decision.evidence,
            "execution_trace": [_trace_to_dict(item) for item in traces],
            "debug": decision.debug,
            "runtime_backend": active_backend,
        }

        return AnalysisResult(
            issue_detected=decision.issue_detected,
            issue_type=decision.issue_type,
            severity=decision.severity,
            confidence=decision.confidence,
            root_cause=decision.root_cause,
            remediation=decision.remediation,
            metadata=metadata,
        )

    def _resolve_runtime_backend(self) -> str:
        backend = self._config.runtime_backend
        if backend not in {"haystack", "native"}:
            emit(
                logger,
                "runtime_backend_invalid",
                level="warning",
                backend=backend,
                fallback="haystack",
            )
            backend = "haystack"

        if backend == "haystack" and not is_haystack_available():
            emit(
                logger,
                "runtime_backend_unavailable",
                level="warning",
                requested_backend="haystack",
                fallback="native",
            )
            if not self._config.runtime_fail_open:
                raise RuntimeError(
                    "Haystack runtime backend requested but haystack-ai is unavailable and fail-open is disabled"
                )
            return "native"

        return backend

    def _build_haystack_runtime(self) -> HaystackPipelineRuntime | None:
        if self._runtime_backend != "haystack":
            return None

        tools = self._registry.all()
        return HaystackPipelineRuntime(
            tools=tools,
            reasoner=self._reasoner,
            logger=logger,
        )

    def _run_native(
        self,
        plan: ExecutionPlan,
        telemetry: TelemetryEnvelope,
    ) -> tuple[Dict[str, Any], List[StepTrace], ReasoningOutput]:
        state: Dict[str, Any] = {}
        traces: List[StepTrace] = []
        decision = None

        for step in plan.steps:
            if step.kind == "tool":
                trace, output = self._run_tool_step(step, telemetry, state)
                traces.append(trace)
                if trace.status == "success":
                    state[step.tool_name] = output
                elif step.required:
                    state[f"{step.tool_name}_error"] = trace.error
            elif step.kind == "reason":
                decision = self._reasoner.synthesize(telemetry, state, traces)

        if decision is None:
            decision = self._reasoner.synthesize(telemetry, state, traces)

        return state, traces, decision

    def _register_tools(self) -> None:
        self._registry.register(MemoryRecallTool(self._memory))
        self._registry.register(MetricTriageTool())
        self._registry.register(LogPatternTool())
        if self._context is not None:
            self._registry.register(ContextLookupTool(self._context))

    def _run_tool_step(
        self,
        step: PlanStep,
        telemetry: TelemetryEnvelope,
        state: Dict[str, Any],
    ) -> tuple[StepTrace, Dict[str, Any]]:
        tool = self._registry.get(step.tool_name)
        if tool is None:
            trace = StepTrace(
                step_id=step.step_id,
                kind=step.kind,
                tool_name=step.tool_name,
                status="failed",
                started_at=telemetry.received_at,
                finished_at=telemetry.received_at,
                duration_ms=0,
                attempt=1,
                error=f"tool {step.tool_name} is not registered",
                output_summary={},
            )
            return trace, {}

        return self._executor.execute(step, tool, telemetry, state)


def _trace_to_dict(trace: StepTrace) -> Dict[str, Any]:
    return {
        "step_id": trace.step_id,
        "kind": trace.kind,
        "tool_name": trace.tool_name,
        "status": trace.status,
        "started_at": trace.started_at.isoformat(),
        "finished_at": trace.finished_at.isoformat(),
        "duration_ms": trace.duration_ms,
        "attempt": trace.attempt,
        "error": trace.error,
        "output_summary": trace.output_summary,
    }
