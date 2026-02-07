import logging
import numpy as np
from typing import List, Dict, Any

logger = logging.getLogger(__name__)

TORCH_AVAILABLE = False
try:
    import torch
    import torch.nn as nn
    TORCH_AVAILABLE = True
except ImportError:
    logger.warning("PyTorch not installed. Anomaly detection will run in fallback mode.")

class LSTMPredictor(nn.Module if TORCH_AVAILABLE else object):
    def __init__(self, input_dim=1, hidden_dim=32, num_layers=1):
        if not TORCH_AVAILABLE: return
        super(LSTMPredictor, self).__init__()
        self.lstm = nn.LSTM(input_dim, hidden_dim, num_layers, batch_first=True)
        self.linear = nn.Linear(hidden_dim, 1)

    def forward(self, x):
        if not TORCH_AVAILABLE: return None
        out, _ = self.lstm(x)
        return self.linear(out[:, -1, :])

class AnomalyDetector:
    """
    Detects anomalies using a time-series model (LSTM) or statistical fallback.
    """
    def __init__(self):
        self.model = LSTMPredictor() if TORCH_AVAILABLE else None
        self.history = {} # Store history for stateful detection

    def detect(self, metric_name: str, values: List[float]) -> Dict[str, Any]:
        """
        Returns prediction and anomaly score.
        """
        if not values:
            return {"is_anomaly": False, "score": 0.0}

        # Fallback: Statistical Z-Score
        if not TORCH_AVAILABLE or len(values) < 10:
            mean = np.mean(values)
            std = np.std(values)
            current = values[-1]
            
            if std == 0:
                score = 0.0
            else:
                score = abs(current - mean) / std

            return {
                "is_anomaly": score > 3.0,
                "score": score,
                "method": "z_score_fallback"
            }

        # LSTM Logic (Simplified Inference)
        # Preprocessing: Scale to 0-1 (MinMax using recent window)
        min_val = min(values)
        max_val = max(values)
        if max_val == min_val:
            return {"is_anomaly": False, "score": 0.0}
            
        scaled = [(v - min_val) / (max_val - min_val) for v in values]
        
        # Prepare tensor [batch, seq_len, features]
        input_seq = torch.tensor(scaled[:-1], dtype=torch.float32).view(1, -1, 1)
        
        with torch.no_grad():
            self.model.eval()
            prediction_scaled = self.model(input_seq).item()
            
        prediction = prediction_scaled * (max_val - min_val) + min_val
        actual = values[-1]
        
        # Anomaly Score: Error relative to range
        error = abs(actual - prediction)
        range_span = max_val - min_val
        score = error / range_span if range_span > 0 else 0
        
        return {
            "is_anomaly": score > 0.25, # >25% deviation from prediction
            "score": score,
            "predicted": prediction,
            "actual": actual,
            "method": "lstm_inference"
        }
