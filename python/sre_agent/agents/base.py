from abc import ABC, abstractmethod
from typing import Dict, List, Any, Optional
from dataclasses import dataclass

@dataclass
class AnalysisResult:
    issue_detected: bool
    issue_type: str = ""
    severity: str = "info"
    confidence: float = 0.0
    root_cause: str = ""
    remediation: str = ""
    metadata: Dict[str, Any] = None

class BaseAgent(ABC):
    """Abstract base class for domain-specific AI agents."""
    
    def __init__(self, name: str):
        self.name = name

    @abstractmethod
    def analyze(self, metrics: List[Dict[str, Any]], logs: List[Dict[str, Any]]) -> Optional[AnalysisResult]:
        """
        Analyze the incoming data stream and return a result if an issue is detected.
        
        Args:
            metrics: List of metric dicts {'name': ..., 'value': ...}
            logs: List of log dicts {'message': ..., 'level': ...}
        
        Returns:
            AnalysisResult or None
        """
        pass
