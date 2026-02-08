"""AI analysis package for SRE Agent.

This package provides ML-powered analysis capabilities:
- Issue classification using trained models
- Anomaly detection (Isolation Forest)
- Suggestion generation
- Natural language explanations

Components:
- classifier: Issue classification and anomaly detection
- service: gRPC/HTTP service for Controller integration

Example usage:

    from sre_agent.ai import create_ai_service
    
    classifier, explainer = create_ai_service()
    
    metrics = [MetricData(name="cpu.usage", value=95.0, timestamp="...")]
    classifications = classifier.classify(metrics)
    
    for c in classifications:
        explanation = explainer.explain(c, {"cpu.usage": 95.0})
        print(explanation.summary)
"""

from sre_agent.ai.classifier import (
    IssueClassifier,
    ExplanationGenerator,
    MetricData,
    LogEntry,
    Classification,
    Suggestion,
    Explanation,
    IssueCategory,
    Severity,
    create_ai_service,
)

__all__ = [
    "IssueClassifier",
    "ExplanationGenerator",
    "MetricData",
    "LogEntry",
    "Classification",
    "Suggestion",
    "Explanation",
    "IssueCategory",
    "Severity",
    "create_ai_service",
]
