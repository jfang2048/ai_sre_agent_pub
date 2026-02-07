import logging
import json
from typing import List, Dict, Any, Optional
from .base import BaseAgent, AnalysisResult

logger = logging.getLogger(__name__)

# Try importing LangChain
try:
    from langchain.prompts import PromptTemplate
    from langchain.llms.fake import FakeListLLM 
    # In prod: from langchain.chat_models import ChatOpenAI
    LANGCHAIN_AVAILABLE = True
except ImportError:
    LANGCHAIN_AVAILABLE = False
    logger.warning("LangChain not available, ErrorAgent will use heuristic fallback.")

class ErrorAgent(BaseAgent):
    """Agent focused on analyzing error logs using LLM (LangChain)."""
    
    def __init__(self):
        super().__init__("error_agent")
        
        if LANGCHAIN_AVAILABLE:
            self.llm = FakeListLLM(responses=[
                '{"root_cause": "Database connection refused due to firewall", "remediation": "Update security group rules"}'
            ])
            self.prompt = PromptTemplate(
                input_variables=["log_message"],
                template="Analyze this log error: {log_message}. \nReturn JSON with root_cause and remediation."
            )

    def analyze(self, metrics: List[Dict[str, Any]], logs: List[Dict[str, Any]]) -> Optional[AnalysisResult]:
        # Filter for error logs
        error_logs = [l for l in logs if l.get('level', '').lower() in ['error', 'critical', 'fatal']]
        
        if not error_logs:
            return None

        # Analyze the most recent error
        latest_error = error_logs[-1]
        msg = latest_error.get('message', '')

        if LANGCHAIN_AVAILABLE:
            try:
                # 1. Format prompt
                _ = self.prompt.format(log_message=msg)
                
                # 2. Run LLM (Simulated)
                response = self.llm.predict(msg)
                
                # 3. Parse JSON
                try:
                    analysis = json.loads(response)
                    return AnalysisResult(
                        issue_detected=True,
                        issue_type="log_error_analysis",
                        severity="error",
                        confidence=0.85,
                        root_cause=analysis.get("root_cause", "Unknown"),
                        remediation=analysis.get("remediation", "Check logs"),
                        metadata={"log_message": msg}
                    )
                except json.JSONDecodeError:
                    pass
            except Exception as e:
                logger.error(f"LangChain analysis failed: {e}")

        # Fallback
        if "connection refused" in msg.lower():
            return AnalysisResult(
                issue_detected=True,
                issue_type="connection_failure",
                severity="error",
                confidence=0.9,
                root_cause="Target service is unreachable.",
                remediation="Check network connectivity and service status.",
                metadata={"error": msg}
            )

        return AnalysisResult(
            issue_detected=True,
            issue_type="application_error",
            severity="error",
            confidence=0.6,
            root_cause="Application logged an error.",
            remediation="Investigate application logs.",
            metadata={"error": msg}
        )
