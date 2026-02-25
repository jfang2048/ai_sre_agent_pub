"""Tool registry and controlled tool execution."""

from concurrent.futures import ThreadPoolExecutor, TimeoutError as FuturesTimeoutError
from datetime import datetime, timezone
import logging
import time
from typing import Any, Dict, Optional, Tuple

from .structured_log import emit
from .tools import Tool
from .types import PlanStep, StepTrace, TelemetryEnvelope


class ToolRegistry:
    """Simple tool registry by name."""

    def __init__(self) -> None:
        self._tools: Dict[str, Tool] = {}

    def register(self, tool: Tool) -> None:
        self._tools[tool.name] = tool

    def get(self, tool_name: str) -> Optional[Tool]:
        return self._tools.get(tool_name)

    def all(self) -> Dict[str, Tool]:
        """Return a shallow copy of all registered tools."""
        return dict(self._tools)


class ToolExecutor:
    """Executes tools with explicit timeout and retry control."""

    def __init__(self, logger: logging.Logger) -> None:
        self._logger = logger

    def execute(
        self,
        step: PlanStep,
        tool: Tool,
        telemetry: TelemetryEnvelope,
        state: Dict[str, Any],
    ) -> Tuple[StepTrace, Dict[str, Any]]:
        max_attempts = max(1, step.retries + 1)
        last_trace: Optional[StepTrace] = None
        output: Dict[str, Any] = {}

        for attempt in range(1, max_attempts + 1):
            started = datetime.now(timezone.utc)
            monotonic_start = time.monotonic()
            status = "failed"
            error = ""
            output = {}

            try:
                with ThreadPoolExecutor(max_workers=1) as pool:
                    future = pool.submit(tool.run, telemetry, step.input_payload, state)
                    output = future.result(timeout=max(0.1, step.timeout_seconds))
                status = "success"
            except FuturesTimeoutError:
                status = "timeout"
                error = f"tool exceeded {step.timeout_seconds:.2f}s timeout"
            except Exception as exc:  # pylint: disable=broad-except
                status = "failed"
                error = str(exc)

            finished = datetime.now(timezone.utc)
            duration_ms = int((time.monotonic() - monotonic_start) * 1000)
            last_trace = StepTrace(
                step_id=step.step_id,
                kind=step.kind,
                tool_name=step.tool_name,
                status=status,
                started_at=started,
                finished_at=finished,
                duration_ms=duration_ms,
                attempt=attempt,
                error=error,
                output_summary=_summarize_output(output),
            )

            emit(
                self._logger,
                "tool_step_completed",
                request_id=telemetry.request_id,
                step_id=step.step_id,
                tool=step.tool_name,
                status=status,
                attempt=attempt,
                duration_ms=duration_ms,
                error=error,
            )

            if status == "success":
                return last_trace, output
            if attempt < max_attempts:
                time.sleep(min(0.15 * attempt, 0.5))

        assert last_trace is not None
        return last_trace, output


def _summarize_output(payload: Dict[str, Any]) -> Dict[str, Any]:
    if not isinstance(payload, dict):
        return {"type": type(payload).__name__}

    summary: Dict[str, Any] = {}
    for key, value in payload.items():
        if isinstance(value, list):
            summary[key] = {"type": "list", "len": len(value)}
        elif isinstance(value, dict):
            summary[key] = {"type": "dict", "keys": list(value.keys())[:8]}
        else:
            summary[key] = value
    return summary
