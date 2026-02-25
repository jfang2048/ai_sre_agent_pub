"""Simple prediction primitives used by unit tests and baseline analyses."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Dict, Sequence, Tuple

import numpy as np


@dataclass
class BaselinePredictor:
    """Baseline predictor using mean/std from historical observations."""

    mean: float = 0.0
    std: float = 0.0
    trained: bool = False

    def train(self, values: Sequence[float]) -> None:
        if len(values) == 0:
            self.mean = 0.0
            self.std = 0.0
            self.trained = False
            return

        arr = np.asarray(values, dtype=float)
        self.mean = float(np.mean(arr))
        self.std = float(np.std(arr))
        self.trained = True

    def predict(self, next_value: float) -> Tuple[bool, float]:
        """Return (is_expected, confidence) for the next observed value."""

        if not self.trained:
            return False, 0.0

        if self.std == 0:
            confidence = 1.0 if float(next_value) == self.mean else 0.0
            return confidence > 0.5, confidence

        z_score = abs((float(next_value) - self.mean) / self.std)
        confidence = max(0.0, min(1.0, 1.0 - (z_score / 3.0)))
        return confidence > 0.5, confidence


def predict_slo_violation(history: Sequence[float], slo_target: float) -> Dict[str, float | bool | None]:
    """Predict whether the trend suggests crossing below an SLO target."""

    if len(history) == 0:
        return {
            "will_violate": False,
            "confidence": 0.0,
            "predicted_next": None,
            "slope": 0.0,
        }

    arr = np.asarray(history, dtype=float)
    if len(arr) == 1:
        predicted_next = float(arr[0])
        return {
            "will_violate": predicted_next < slo_target,
            "confidence": 0.5,
            "predicted_next": predicted_next,
            "slope": 0.0,
        }

    x = np.arange(len(arr), dtype=float)
    slope, intercept = np.polyfit(x, arr, 1)
    predicted_next = float(slope * len(arr) + intercept)

    current = float(arr[-1])
    will_violate = current < slo_target or predicted_next < slo_target

    # Confidence increases with stable downward trend and proximity to target.
    distance = abs(predicted_next - slo_target)
    normalized_distance = min(1.0, distance / max(abs(slo_target), 1e-6))
    trend_strength = min(1.0, abs(float(slope)) * 1000.0)
    confidence = max(0.5, min(0.99, 1.0 - normalized_distance * 0.5 + trend_strength * 0.25))

    return {
        "will_violate": bool(will_violate),
        "confidence": float(confidence),
        "predicted_next": predicted_next,
        "slope": float(slope),
    }

