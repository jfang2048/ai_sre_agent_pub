import logging
from typing import List, Dict, Any, Optional
from .base import BaseAgent, AnalysisResult

logger = logging.getLogger(__name__)

class CausalAgent(BaseAgent):
    """
    Agent that uses a Causal Graph to infer root causes.
    It models relationships between metrics (e.g., Disk IO -> CPU Wait -> Load).
    """

    def __init__(self):
        super().__init__("causal_agent")
        
        # Simple Causal Graph: Effect -> [Possible Causes]
        # This is a simplified DAG represented as an adjacency list (reverse)
        self.causal_graph = {
            "system.load": ["system.cpu.iowait", "system.cpu.system", "system.cpu.user"],
            "system.cpu.iowait": ["system.disk.io_in_progress", "system.disk.utilization"],
            "app.latency": ["system.load", "system.memory.usage", "system.net.errors"],
            "app.errors": ["app.latency", "system.memory.oom_kill", "system.disk.space"],
        }
        
        # Thresholds for "Anomalous" (simplified, usually learned)
        self.thresholds = {
            "system.load": 5.0,
            "system.cpu.iowait": 10.0, # percent
            "system.cpu.system": 80.0,
            "system.disk.utilization": 90.0,
            "system.memory.usage": 90.0,
            "system.memory.oom_kill": 1.0, # count
        }

    def analyze(self, metrics: List[Dict[str, Any]], logs: List[Dict[str, Any]]) -> Optional[AnalysisResult]:
        # 1. Detect Anomalies (Nodes in the graph that are "Active")
        active_nodes = {} # {metric_name: value}
        
        for m in metrics:
            name = m.get('name')
            val = float(m.get('value', 0))
            
            # Check explicit threshold
            if name in self.thresholds and val >= self.thresholds[name]:
                active_nodes[name] = val
            
            # Special case for load (if 1m load provided)
            if "system.load.1m" in name and val > 5.0:
                 active_nodes["system.load"] = val

        # 2. Traverse Graph to find Root Cause
        # We start from known "Symptoms" (e.g., Latency, Load) and walk backwards
        roots = []
        
        # If we have High Load, check its causes
        if "system.load" in active_nodes:
            causes = self.causal_graph.get("system.load", [])
            found_cause = False
            for cause in causes:
                if cause in active_nodes:
                    # Recursive step (simplified to 1 level here for brevity)
                    # Check if *this* cause has a deeper cause
                    deeper_causes = self.causal_graph.get(cause, [])
                    deepest_cause = cause
                    
                    for dc in deeper_causes:
                        if dc in active_nodes:
                            deepest_cause = dc
                            break
                    
                    roots.append(deepest_cause)
                    found_cause = True
            
            if not found_cause:
                roots.append("system.load") # Load is high, but no specific sub-cause found

        # Deduplicate
        roots = list(set(roots))
        
        if not roots:
            return None

        # 3. Formulate Result
        primary_cause = roots[0]
        confidence = 0.85
        
        explanation = f"Causal analysis identified '{primary_cause}' as the root driver."
        if primary_cause == "system.disk.io_in_progress":
            explanation += " High Disk I/O is causing CPU Wait, leading to System Load."
            remediation = "Check for disk-intensive processes (backup, logging) or upgrade storage."
        elif primary_cause == "system.memory.usage":
            explanation += " High Memory usage is likely causing swapping or thrashing."
            remediation = "Scale memory or investigate leaks."
        else:
            remediation = "Investigate the identified metric source."

        return AnalysisResult(
            issue_detected=True,
            issue_type="causal_correlation",
            severity="warning",
            confidence=confidence,
            root_cause=explanation,
            remediation=remediation,
            metadata={"active_nodes": list(active_nodes.keys()), "graph_path": roots}
        )
