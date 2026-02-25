"""Runtime-orchestrated reasoning agent."""

from typing import Any, Dict, List, Optional

from .base import AnalysisResult, BaseAgent
from ..runtime.orchestrator import Orchestrator


class ReasoningAgent(BaseAgent):
    """Agent wrapper over the explicit orchestration runtime."""

    def __init__(self, orchestrator: Orchestrator | None = None) -> None:
        super().__init__("reasoning_agent")
        self._orchestrator = orchestrator or Orchestrator()

    def analyze_for_node(
        self,
        node_name: str,
        metrics: List[Dict[str, Any]],
        logs: List[Dict[str, Any]],
    ) -> Optional[AnalysisResult]:
        return self._orchestrator.analyze(node_name, metrics, logs)

    def analyze(
        self,
        metrics: List[Dict[str, Any]],
        logs: List[Dict[str, Any]],
    ) -> Optional[AnalysisResult]:
        node_name = "unknown"
        if metrics:
            labels = metrics[0].get("labels", {}) or {}
            node_name = str(labels.get("node_name") or labels.get("node") or node_name)
        return self._orchestrator.analyze(node_name, metrics, logs)
