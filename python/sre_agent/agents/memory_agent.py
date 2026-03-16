import logging
from typing import Any, Dict, List, Optional

import numpy as np

from .base import AnalysisResult, BaseAgent

logger = logging.getLogger(__name__)

# Try importing torch, handle gracefully if missing
try:
    import torch
    import torch.nn as nn

    TORCH_AVAILABLE = True
except ImportError:
    TORCH_AVAILABLE = False
    logger.warning("PyTorch not available, MemoryAgent will use heuristic fallback.")

if TORCH_AVAILABLE:

    class AutoEncoder(nn.Module):
        def __init__(self, input_dim=1):
            super(AutoEncoder, self).__init__()
            self.encoder = nn.Sequential(nn.Linear(input_dim, 4), nn.ReLU(), nn.Linear(4, 2))
            self.decoder = nn.Sequential(nn.Linear(2, 4), nn.ReLU(), nn.Linear(4, input_dim))

        def forward(self, x):
            encoded = self.encoder(x)
            decoded = self.decoder(encoded)
            return decoded


class MemoryAgent(BaseAgent):
    """Agent focused on detecting memory anomalies using PyTorch."""

    def __init__(self):
        super().__init__("memory_agent")
        self.metric_name = "system.memory.usage"
        self.threshold = 90.0

        if TORCH_AVAILABLE:
            self.model = AutoEncoder()
            self.model.eval()  # Inference mode (assuming pre-trained)
            self.criterion = nn.MSELoss()

    def analyze(
        self, metrics: List[Dict[str, Any]], logs: List[Dict[str, Any]]
    ) -> Optional[AnalysisResult]:
        # Filter for memory metrics
        mem_values = [m["value"] for m in metrics if m["name"] == self.metric_name]

        if not mem_values:
            return None

        current_val = mem_values[-1]  # Take latest

        # 1. PyTorch-based Anomaly Detection
        if TORCH_AVAILABLE:
            try:
                input_tensor = torch.tensor([[current_val / 100.0]], dtype=torch.float32)
                with torch.no_grad():
                    output_tensor = self.model(input_tensor)
                    loss = self.criterion(output_tensor, input_tensor).item()

                # High reconstruction error -> Anomaly
                if loss > 0.1:
                    return AnalysisResult(
                        issue_detected=True,
                        issue_type="memory_anomaly",
                        severity="warning",
                        confidence=min(0.5 + loss, 0.95),
                        root_cause="Memory usage pattern deviates significantly from normal baseline.",
                        remediation="Investigate unexpected memory change.",
                        metadata={"reconstruction_error": loss},
                    )
            except Exception as e:
                logger.error(f"Inference failed: {e}")

        # 2. Heuristic Fallback (High Usage)
        if current_val > self.threshold:
            return AnalysisResult(
                issue_detected=True,
                issue_type="memory_pressure",
                severity="critical",
                confidence=0.95,
                root_cause=f"Memory usage {current_val:.1f}% exceeds threshold {self.threshold}%",
                remediation="Check for memory leaks. Restart heaviest process.",
                metadata={"current_usage": current_val},
            )

        # 3. Leak Detection (Simple Trend)
        if len(mem_values) >= 3:
            # Check if strictly increasing
            if all(x < y for x, y in zip(mem_values, mem_values[1:])):
                return AnalysisResult(
                    issue_detected=True,
                    issue_type="memory_leak_candidate",
                    severity="warning",
                    confidence=0.70,
                    root_cause="Memory usage is consistently increasing.",
                    remediation="Profile application memory usage.",
                    metadata={"trend": "increasing"},
                )

        return None
