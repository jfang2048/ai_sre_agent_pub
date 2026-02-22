"""Compatibility shim for legacy ReActRAGAgent imports.

This class delegates to the production reasoning runtime agent.
"""

import logging
from typing import Any, Dict, List, Optional

from .base import AnalysisResult, BaseAgent
from .reasoning_agent import ReasoningAgent

logger = logging.getLogger(__name__)


class ReActRAGAgent(BaseAgent):
    """Backward-compatible alias over the Haystack-backed reasoning runtime."""

    def __init__(self, delegate: ReasoningAgent | None = None) -> None:
        super().__init__("react_rag_agent")
        self._delegate = delegate or ReasoningAgent()
        logger.warning(
            "ReActRAGAgent is deprecated; using reasoning_agent runtime pipeline."
        )

    def analyze(
        self,
        metrics: List[Dict[str, Any]],
        logs: List[Dict[str, Any]],
    ) -> Optional[AnalysisResult]:
        return self._delegate.analyze(metrics, logs)
