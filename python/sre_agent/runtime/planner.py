"""Deterministic planning stage for runtime orchestration."""

from typing import List

from .config import RuntimeConfig
from .types import ExecutionPlan, PlanStep, TelemetryEnvelope


class Planner:
    """Builds an explicit execution plan without implicit chains."""

    VERSION = "haystack-runtime-plan-v1"

    def __init__(self, config: RuntimeConfig) -> None:
        self._config = config

    def build_plan(self, telemetry: TelemetryEnvelope) -> ExecutionPlan:
        """Create a deterministic step sequence for one analysis request."""
        steps: List[PlanStep] = [
            PlanStep(
                step_id="step-memory-recall",
                kind="tool",
                tool_name="memory_recall",
                description="Load recent incidents for node-level context.",
                input_payload={"limit": 5},
                required=False,
                timeout_seconds=self._config.tool_timeout_seconds,
                retries=self._config.tool_retries,
            ),
            PlanStep(
                step_id="step-metric-triage",
                kind="tool",
                tool_name="metric_triage",
                description="Detect abnormal metrics and rank top signals.",
                timeout_seconds=self._config.tool_timeout_seconds,
                retries=self._config.tool_retries,
            ),
        ]

        if telemetry.logs:
            steps.append(
                PlanStep(
                    step_id="step-log-patterns",
                    kind="tool",
                    tool_name="log_pattern_scan",
                    description="Extract error signatures and incident hints from logs.",
                    timeout_seconds=self._config.tool_timeout_seconds,
                    retries=self._config.tool_retries,
                )
            )

        if self._config.context_enabled:
            steps.append(
                PlanStep(
                    step_id="step-context-lookup",
                    kind="tool",
                    tool_name="context_lookup",
                    description="Retrieve runbook snippets aligned to current symptoms.",
                    input_payload={
                        "query": self._build_context_query(telemetry),
                        "limit": self._config.context_top_k,
                    },
                    required=False,
                    timeout_seconds=self._config.tool_timeout_seconds,
                    retries=self._config.tool_retries,
                )
            )

        steps.append(
            PlanStep(
                step_id="step-reasoning",
                kind="reason",
                description="Synthesize evidence into incident classification and remediation.",
                required=True,
            )
        )

        return ExecutionPlan(version=self.VERSION, steps=steps)

    def _build_context_query(self, telemetry: TelemetryEnvelope) -> str:
        metric_names = [str(metric.get("name", "")) for metric in telemetry.metrics[:8]]
        recent_errors = [
            str(log.get("message", ""))
            for log in telemetry.logs
            if str(log.get("level", "info")).lower() in {"error", "critical", "fatal"}
        ]
        query_parts = [telemetry.node_name, *metric_names]
        if recent_errors:
            query_parts.append(recent_errors[-1][:140])
        return " ".join(part for part in query_parts if part)
