"""Production agent runtime components.

This package provides explicit orchestration primitives:
- planning
- context retrieval
- tool execution
- memory
- reasoning
- Haystack pipeline execution
"""

from .config import RuntimeConfig
from .orchestrator import Orchestrator
from .haystack_runtime import is_haystack_available

__all__ = ["RuntimeConfig", "Orchestrator", "is_haystack_available"]
