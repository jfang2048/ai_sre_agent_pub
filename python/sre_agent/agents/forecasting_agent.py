import logging
import time
from typing import List, Dict, Any, Optional
from collections import deque
from .base import BaseAgent, AnalysisResult

logger = logging.getLogger(__name__)

# Try importing sklearn
try:
    from sklearn.linear_model import LinearRegression
    import numpy as np
    SKLEARN_AVAILABLE = True
except ImportError:
    SKLEARN_AVAILABLE = False
    logger.warning("scikit-learn not available, ForecastingAgent will be disabled.")

class ForecastingAgent(BaseAgent):
    """Agent focused on predictive analysis using Linear Regression."""
    
    def __init__(self, window_size=50):
        super().__init__("forecasting_agent")
        self.window_size = window_size
        # State: {metric_name: deque([(timestamp, value), ...])}
        self.history = {}
        
        # Targets for prediction
        self.targets = {
            "system.disk.usage": {"limit": 100.0, "name": "Disk Space"},
            "system.memory.usage": {"limit": 100.0, "name": "Memory"},
        }

    def analyze(self, metrics: List[Dict[str, Any]], logs: List[Dict[str, Any]]) -> Optional[AnalysisResult]:
        if not SKLEARN_AVAILABLE:
            return None

        issues = []
        
        # 1. Update History
        for m in metrics:
            name = m.get('name')
            if name in self.targets:
                if name not in self.history:
                    self.history[name] = deque(maxlen=self.window_size)
                
                # Parse timestamp if string, else use current time if missing
                ts_raw = m.get('timestamp')
                # Simplified timestamp handling: use monotonic counter or time.time()
                # For regression, relative time is strictly better if scrapes are periodic
                ts = time.time() 
                val = float(m.get('value', 0))
                
                self.history[name].append((ts, val))

        # 2. Run Forecasting
        for name, config in self.targets.items():
            data = self.history.get(name)
            if not data or len(data) < 10: # Need enough points
                continue
            
            # Prepare data
            timestamps = np.array([x[0] for x in data]).reshape(-1, 1)
            values = np.array([x[1] for x in data])
            
            # Relative time for numerical stability
            timestamps_rel = timestamps - timestamps[0]
            
            # Fit model
            model = LinearRegression()
            model.fit(timestamps_rel, values)
            
            slope = model.coef_[0]
            
            # Only care about increasing trends
            if slope <= 0:
                continue
                
            # Predict Time to Limit
            # limit = slope * t + intercept => t = (limit - intercept) / slope
            limit = config['limit']
            intercept = model.intercept_
            
            if intercept >= limit:
                # Already full, handled by reactive agents
                continue
                
            seconds_to_limit = (limit - intercept) / slope
            hours_to_limit = seconds_to_limit / 3600.0
            
            # Alert if running out < 4 hours (Urgent) or < 24 hours (Warning)
            if 0 < hours_to_limit < 4:
                return AnalysisResult(
                    issue_detected=True,
                    issue_type="resource_exhaustion_imminent",
                    severity="critical",
                    confidence=0.90,
                    root_cause=f"{config['name']} predicted to fill in {hours_to_limit:.1f} hours based on current trend.",
                    remediation=f"Proactively free up {config['name']} or scale resources immediately.",
                    metadata={"hours_to_full": hours_to_limit, "slope": slope}
                )
            
            elif 0 < hours_to_limit < 24:
                # We return the first valid prediction for now
                return AnalysisResult(
                    issue_detected=True,
                    issue_type="resource_exhaustion_forecast",
                    severity="warning",
                    confidence=0.75,
                    root_cause=f"{config['name']} predicted to fill in {hours_to_limit:.1f} hours.",
                    remediation=f"Plan to add capacity or cleanup {config['name']} soon.",
                    metadata={"hours_to_full": hours_to_limit, "slope": slope}
                )
                
        return None
