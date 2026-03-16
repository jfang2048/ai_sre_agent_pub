"""Shared runtime data contracts."""

from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Dict, List


def utc_now() -> datetime:
    """Return current UTC time."""
    return datetime.now(timezone.utc)


@dataclass
class TelemetryEnvelope:
    """Per-request telemetry envelope consumed by the runtime."""

    node_name: str
    metrics: List[Dict[str, Any]]
    logs: List[Dict[str, Any]]
    request_id: str
    received_at: datetime = field(default_factory=utc_now)


@dataclass
class PlanStep:
    """One explicit step in the execution plan."""

    step_id: str
    kind: str  # tool | reason
    description: str
    tool_name: str = ""
    input_payload: Dict[str, Any] = field(default_factory=dict)
    required: bool = True
    timeout_seconds: float = 2.0
    retries: int = 0


@dataclass
class ExecutionPlan:
    """Deterministic plan emitted by the planner."""

    version: str
    steps: List[PlanStep] = field(default_factory=list)
    created_at: datetime = field(default_factory=utc_now)


@dataclass
class StepTrace:
    """Trace record for one executed step."""

    step_id: str
    kind: str
    tool_name: str
    status: str
    started_at: datetime
    finished_at: datetime
    duration_ms: int
    attempt: int = 1
    error: str = ""
    output_summary: Dict[str, Any] = field(default_factory=dict)


@dataclass
class ReasoningOutput:
    """Structured reasoning decision produced by the reasoner."""

    issue_detected: bool = False
    issue_type: str = "unknown"
    severity: str = "info"
    confidence: float = 0.0
    root_cause: str = ""
    remediation: str = ""
    notes: str = ""
    evidence: List[str] = field(default_factory=list)
    provider: str = "deterministic"
    model: str = "rules"
    debug: Dict[str, Any] = field(default_factory=dict)
