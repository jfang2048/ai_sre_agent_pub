"""Runtime tool definitions."""

from abc import ABC, abstractmethod
from typing import Any, Dict, List

from .context_store import ContextStore
from .memory_store import BoundedMemoryStore, MemoryEntry
from .types import TelemetryEnvelope


class Tool(ABC):
    """Base tool contract."""

    name: str
    description: str

    @abstractmethod
    def run(
        self,
        telemetry: TelemetryEnvelope,
        input_payload: Dict[str, Any],
        state: Dict[str, Any],
    ) -> Dict[str, Any]:
        """Execute tool and return structured output."""


class MetricTriageTool(Tool):
    """Identifies high-signal metrics with explicit thresholds."""

    name = "metric_triage"
    description = "Summarize high-signal and abnormal metrics."

    _thresholds = {
        "cpu": (75.0, 90.0),
        "memory": (80.0, 92.0),
        "disk": (85.0, 95.0),
        "load": (4.0, 8.0),
        "network": (80.0, 92.0),
    }

    def run(
        self,
        telemetry: TelemetryEnvelope,
        input_payload: Dict[str, Any],
        state: Dict[str, Any],
    ) -> Dict[str, Any]:
        del input_payload, state

        findings: List[Dict[str, Any]] = []
        top_metrics: List[Dict[str, Any]] = []

        for metric in telemetry.metrics:
            name = str(metric.get("name", ""))
            value = _to_float(metric.get("value"))
            top_metrics.append({"name": name, "value": value})
            severity = self._classify(name, value)
            if severity:
                findings.append({"name": name, "value": value, "severity": severity})

        top_metrics.sort(key=lambda item: abs(item["value"]), reverse=True)

        return {
            "finding_count": len(findings),
            "high_signals": findings[:20],
            "top_metrics": top_metrics[:12],
            "critical": any(item["severity"] == "critical" for item in findings),
        }

    def _classify(self, metric_name: str, value: float) -> str:
        lowered = metric_name.lower()
        for key, (warn, crit) in self._thresholds.items():
            if key in lowered:
                if value >= crit:
                    return "critical"
                if value >= warn:
                    return "warning"
        if ("percent" in lowered or lowered.endswith(".usage")) and value >= 95.0:
            return "critical"
        if ("percent" in lowered or lowered.endswith(".usage")) and value >= 85.0:
            return "warning"
        return ""


class LogPatternTool(Tool):
    """Extracts failure patterns from recent logs."""

    name = "log_pattern_scan"
    description = "Scan logs for error signatures and symptom patterns."

    _pattern_map = {
        "connection_refused": ("connection refused", "upstream_dependency"),
        "timeout": ("timeout", "upstream_dependency"),
        "oom_killed": ("oom", "memory_pressure"),
        "disk_full": ("no space left on device", "disk_capacity"),
        "permission_denied": ("permission denied", "configuration_error"),
        "panic": ("panic", "application_error"),
        "traceback": ("traceback", "application_error"),
    }

    def run(
        self,
        telemetry: TelemetryEnvelope,
        input_payload: Dict[str, Any],
        state: Dict[str, Any],
    ) -> Dict[str, Any]:
        del input_payload, state

        error_count = 0
        critical_count = 0
        latest_error = ""
        signatures: Dict[str, int] = {}
        issue_hints: Dict[str, int] = {}

        for log in telemetry.logs:
            level = str(log.get("level", "info")).lower()
            message = str(log.get("message", ""))
            lower = message.lower()
            if level in {"error", "critical", "fatal"}:
                error_count += 1
                latest_error = message or latest_error
            if level in {"critical", "fatal"}:
                critical_count += 1

            for signature, (needle, hint) in self._pattern_map.items():
                if needle in lower:
                    signatures[signature] = signatures.get(signature, 0) + 1
                    issue_hints[hint] = issue_hints.get(hint, 0) + 1

        sorted_signatures = sorted(signatures.items(), key=lambda item: item[1], reverse=True)
        sorted_hints = sorted(issue_hints.items(), key=lambda item: item[1], reverse=True)

        return {
            "error_count": error_count,
            "critical_count": critical_count,
            "latest_error": latest_error,
            "signatures": [{"name": name, "count": count} for name, count in sorted_signatures],
            "issue_hints": [{"name": name, "count": count} for name, count in sorted_hints],
        }


class ContextLookupTool(Tool):
    """Retrieves relevant snippets from local runbooks/docs."""

    name = "context_lookup"
    description = "Retrieve local runbook/document snippets relevant to current symptoms."

    def __init__(self, context_store: ContextStore) -> None:
        self._context_store = context_store

    def run(
        self,
        telemetry: TelemetryEnvelope,
        input_payload: Dict[str, Any],
        state: Dict[str, Any],
    ) -> Dict[str, Any]:
        query = str(input_payload.get("query", "")).strip()
        limit = int(input_payload.get("limit", 4))

        if not query:
            query = _build_default_query(telemetry, state)

        snippets = self._context_store.search(query, limit=limit)
        return {
            "query": query,
            "snippet_count": len(snippets),
            "snippets": [
                {"path": item.path, "score": item.score, "content": item.content[:500]}
                for item in snippets
            ],
        }


class MemoryRecallTool(Tool):
    """Recalls recent incidents for the same node."""

    name = "memory_recall"
    description = "Recall recent node-specific incidents for contextual grounding."

    def __init__(self, memory_store: BoundedMemoryStore) -> None:
        self._memory_store = memory_store

    def run(
        self,
        telemetry: TelemetryEnvelope,
        input_payload: Dict[str, Any],
        state: Dict[str, Any],
    ) -> Dict[str, Any]:
        del state
        limit = int(input_payload.get("limit", 5))
        entries = self._memory_store.recent(telemetry.node_name, limit)
        return {
            "count": len(entries),
            "incidents": [_serialize_memory_entry(entry) for entry in entries],
        }


def _serialize_memory_entry(entry: MemoryEntry) -> Dict[str, Any]:
    return {
        "node_name": entry.node_name,
        "issue_type": entry.issue_type,
        "severity": entry.severity,
        "confidence": entry.confidence,
        "root_cause": entry.root_cause,
        "remediation": entry.remediation,
        "timestamp": entry.timestamp.isoformat(),
    }


def _build_default_query(telemetry: TelemetryEnvelope, state: Dict[str, Any]) -> str:
    metrics = state.get("metric_triage", {}).get("high_signals", [])
    metric_names = [item.get("name", "") for item in metrics[:5]]
    latest_error = state.get("log_pattern_scan", {}).get("latest_error", "")
    fragments = [telemetry.node_name, *metric_names]
    if latest_error:
        fragments.append(latest_error[:120])
    return " ".join(str(part) for part in fragments if part).strip()


def _to_float(value: Any) -> float:
    try:
        return float(value)
    except (TypeError, ValueError):
        return 0.0

