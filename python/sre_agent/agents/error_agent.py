import logging
from typing import List, Dict, Any, Optional
from .base import BaseAgent, AnalysisResult

logger = logging.getLogger(__name__)

class ErrorAgent(BaseAgent):
    """Agent focused on deterministic error-log triage."""

    def __init__(self) -> None:
        super().__init__("error_agent")
        self._patterns = {
            "connection refused": (
                "connection_failure",
                "error",
                0.88,
                "Target dependency refused TCP connections.",
                "Validate service endpoint, network policy, and security group rules.",
            ),
            "timeout": (
                "upstream_timeout",
                "warning",
                0.80,
                "Upstream call timed out under current load or network state.",
                "Check upstream latency, retry policies, and saturation signals.",
            ),
            "no space left on device": (
                "disk_capacity",
                "critical",
                0.92,
                "Disk capacity exhaustion detected.",
                "Free disk space and rotate/prune logs immediately.",
            ),
            "oom": (
                "memory_pressure",
                "critical",
                0.90,
                "Out-of-memory condition detected from log signatures.",
                "Inspect memory growth and increase resource limits if needed.",
            ),
        }

    def analyze(
        self,
        metrics: List[Dict[str, Any]],
        logs: List[Dict[str, Any]],
    ) -> Optional[AnalysisResult]:
        del metrics

        # Filter for error logs
        error_logs = [
            log for log in logs if str(log.get("level", "")).lower() in ["error", "critical", "fatal"]
        ]

        if not error_logs:
            return None

        # Analyze the most recent error
        latest_error = error_logs[-1]
        msg = str(latest_error.get("message", ""))
        lower = msg.lower()

        for needle, pattern in self._patterns.items():
            if needle in lower:
                issue_type, severity, confidence, root_cause, remediation = pattern
                return AnalysisResult(
                    issue_detected=True,
                    issue_type=issue_type,
                    severity=severity,
                    confidence=confidence,
                    root_cause=root_cause,
                    remediation=remediation,
                    metadata={"error": msg},
                )

        # Fallback
        return AnalysisResult(
            issue_detected=True,
            issue_type="application_error",
            severity="error",
            confidence=0.6,
            root_cause="Application logged an error.",
            remediation="Investigate application logs.",
            metadata={"error": msg},
        )
