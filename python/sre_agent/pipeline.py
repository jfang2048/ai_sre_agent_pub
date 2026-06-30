import logging
import os
from typing import Any, Dict, List, Optional

from .agents.base import AnalysisResult, BaseAgent
from .agents.causal_agent import CausalAgent
from .agents.forecasting_agent import ForecastingAgent
from .agents.memory_agent import MemoryAgent
from .agents.reasoning_agent import ReasoningAgent
from .models.anomaly import AnomalyDetector

logger = logging.getLogger(__name__)


class AnalysisPipeline:
    """
    Modular AI Analysis Pipeline.
    Stages:
    1. Feature Engineering
    2. Multi-Model Inference (Anomaly, Forecasting, Classification)
    3. Context Building & Correlation
    4. Recommendation Generation
    """

    def __init__(
        self,
        agents: Optional[List[BaseAgent]] = None,
        anomaly_detector: Optional[AnomalyDetector] = None,
    ):
        # Model Registry
        self.anomaly_detector = anomaly_detector or AnomalyDetector()

        if agents is not None:
            self.agents = list(agents)
            return

        # Specialized Agents (Classifiers/RCA)
        self.agents: List[BaseAgent] = [MemoryAgent(), ForecastingAgent(), CausalAgent()]

        if self._env_enabled("SRE_AGENT_REASONING_ENABLED", default=True):
            self.agents.append(ReasoningAgent())

    def run(
        self, node_name: str, metrics: List[Dict[str, Any]], logs: List[Dict[str, Any]]
    ) -> List[AnalysisResult]:
        logger.info(f"Starting analysis pipeline for {node_name}")
        results = []

        # Stage 1: Feature Engineering (explicit pipeline step before per-agent analysis)
        features = self._extract_features(metrics)

        # Stage 2: Anomaly Detection (Global)
        for name, values in features.items():
            if len(values) > 5:  # Min history
                ad_res = self.anomaly_detector.detect(name, values)
                if ad_res["is_anomaly"]:
                    results.append(
                        AnalysisResult(
                            issue_detected=True,
                            issue_type="anomaly_pattern",
                            severity="warning",
                            confidence=0.7,
                            root_cause=f"Anomaly detected in {name} (Score: {ad_res['score']:.2f})",
                            remediation="Investigate unusual metric pattern.",
                            metadata=ad_res,
                        )
                    )

        # Stage 3: Multi-Agent Inference (Classification & RCA)
        for agent in self.agents:
            try:
                # Agents perform their own Feature -> Model -> Result logic
                if hasattr(agent, "analyze_for_node"):
                    res = agent.analyze_for_node(node_name, metrics, logs)
                else:
                    res = agent.analyze(metrics, logs)
                if res and res.issue_detected:
                    results.append(res)
            except Exception as e:
                logger.error(f"Agent {agent.name} failed: {e}")

        # Stage 4: Context Correlation & Deduplication
        final_results = self._correlate_and_prioritize(results)

        return final_results

    @staticmethod
    def _env_enabled(key: str, default: bool = False) -> bool:
        """
        Returns True when the given environment variable is set to a truthy value.
        """
        raw = os.getenv(key)
        if raw is None:
            return default
        return raw.lower() in {"1", "true", "yes", "on"}

    def _extract_features(self, metrics: List[Dict]) -> Dict[str, List[float]]:
        """
        Groups metric values by name for time-series analysis.
        """
        feats = {}
        for m in metrics:
            name = m.get("name")
            val = float(m.get("value", 0))
            if name not in feats:
                feats[name] = []
            feats[name].append(val)
        return feats

    def _correlate_and_prioritize(self, results: List[AnalysisResult]) -> List[AnalysisResult]:
        """
        Deduplicates and prioritizes results based on confidence and severity.
        """
        if not results:
            return []

        # Simple priority sort
        severity_score = {"critical": 3, "warning": 2, "info": 1}

        results.sort(key=lambda x: (severity_score.get(x.severity, 0), x.confidence), reverse=True)

        # Deduplicate by issue type (keep highest confidence)
        seen_types = set()
        unique = []
        for r in results:
            if r.issue_type not in seen_types:
                unique.append(r)
                seen_types.add(r.issue_type)

        return unique
