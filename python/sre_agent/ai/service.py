"""gRPC server for AI analysis service.

This server exposes the AI analysis capabilities to the Go controller.
It handles:
- Issue classification requests
- Suggestion generation
- Explanation requests
- Model training

Start with: python -m sre_agent.ai.service --port 50052
"""

import os
import json
import logging
import argparse
from concurrent import futures
from typing import Dict, Any, List, Optional

import grpc

from sre_agent.ai.classifier import (
    IssueClassifier,
    ExplanationGenerator,
    MetricData,
    LogEntry,
    IssueCategory,
    Severity,
    create_ai_service,
)
from sre_agent.pipeline import AnalysisPipeline

logger = logging.getLogger(__name__)


class AIServicer:
    """gRPC servicer for AI analysis."""

    def __init__(
        self,
        classifier: IssueClassifier,
        explainer: ExplanationGenerator,
        llm_client=None
    ):
        """Initialize the servicer.
        
        Args:
            classifier: Issue classifier
            explainer: Explanation generator
            llm_client: Optional LLM client for advanced analysis
        """
        self.classifier = classifier
        self.explainer = explainer
        self.llm_client = llm_client
        
        # Pluggable Agents
        self.pipeline = AnalysisPipeline()

    def ClassifyIssue(self, request, context) -> Dict[str, Any]:
        """Classify issues based on metrics and logs.
        
        This is a simplified implementation that can be extended
        with proper protobuf message handling.
        """
        logger.info(f"Classification request for node: {request.get('node_name', 'unknown')}")

        # Convert request to internal format
        metrics = [
            MetricData(
                name=m.get('name', ''),
                value=m.get('value', 0),
                timestamp=m.get('timestamp', ''),
                labels=m.get('labels', {})
            )
            for m in request.get('metrics', [])
        ]

        logs = [
            LogEntry(
                message=log.get('message', ''),
                level=log.get('level', 'info'),
                timestamp=log.get('timestamp', ''),
                source=log.get('source', ''),
                labels=log.get('labels', {})
            )
            for log in request.get('logs', [])
        ]

        # Classify using standard classifier
        classifications = self.classifier.classify(metrics, logs)

        # Run Pipeline
        raw_metrics = [{'name': m.name, 'value': m.value, 'timestamp': m.timestamp, 'labels': m.labels} for m in metrics]
        raw_logs = [{'message': l.message, 'level': l.level, 'timestamp': l.timestamp, 'source': l.source, 'labels': l.labels} for l in logs]

        pipeline_results = self.pipeline.run(request.get('node_name', 'unknown'), raw_metrics, raw_logs)

        for result in pipeline_results:
            # Convert AnalysisResult to Classification
            sev = Severity.WARNING
            if result.severity == 'critical': sev = Severity.CRITICAL
            elif result.severity == 'error': sev = Severity.ERROR
            
            # Map categories
            cat = IssueCategory.UNKNOWN
            if "memory" in result.issue_type: cat = IssueCategory.MEMORY_PRESSURE
            elif "error" in result.issue_type: cat = IssueCategory.APPLICATION_ERROR

            metadata = result.metadata or {}
            request_id = str(metadata.get("request_id", "")).strip()
            provider = str(metadata.get("provider", "")).strip()
            plan_version = str(metadata.get("plan_version", "haystack-runtime-plan-v1")).strip()

            factors = [result.issue_type, result.remediation]
            if request_id:
                factors.append(f"request_id:{request_id}")
            if provider:
                factors.append(f"provider:{provider}")

            related_metrics = []
            evidence = metadata.get("evidence", [])
            if isinstance(evidence, list):
                related_metrics = [str(item)[:160] for item in evidence[:8]]
            
            from sre_agent.ai.classifier import Classification
            classifications.append(Classification(
                category=cat,
                severity=sev,
                confidence=result.confidence,
                description=result.root_cause,
                factors=factors,
                related_metrics=related_metrics,
                method=f"pipeline:{result.issue_type}:{plan_version}"
            ))

        # Convert to response format
        return {
            'classifications': [
                {
                    'category': c.category.value,
                    'severity': c.severity.value,
                    'confidence': c.confidence,
                    'description': c.description,
                    'factors': c.factors,
                    'related_metrics': c.related_metrics,
                    'method': c.method,
                }
                for c in classifications
            ],
            'analysis_id': f"ai-{hash(json.dumps(request))}"
        }

    def SuggestFix(self, request, context) -> Dict[str, Any]:
        """Generate remediation suggestions.
        
        Uses rule-based suggestions with optional ML enhancement.
        """
        classification = request.get('classification', {})
        category = IssueCategory(classification.get('category', 'unknown'))
        severity = Severity(classification.get('severity', 'warning'))

        suggestions = self._generate_suggestions(category, severity, request)

        return {
            'suggestions': suggestions,
            'analysis_id': request.get('analysis_id', '')
        }

    def ExplainIssue(self, request, context) -> Dict[str, Any]:
        """Generate human-readable explanation."""
        classification = request.get('classification', {})
        metrics = {m.get('name', ''): m.get('value', 0) for m in request.get('metrics', [])}
        audience = request.get('audience', 'operator')

        # Create classification object
        category = IssueCategory(classification.get('category', 'unknown'))
        severity = Severity(classification.get('severity', 'warning'))

        from sre_agent.ai.classifier import Classification
        c = Classification(
            category=category,
            severity=severity,
            confidence=classification.get('confidence', 0.5),
            description=classification.get('description', ''),
            factors=classification.get('factors', []),
            related_metrics=classification.get('related_metrics', []),
            method=classification.get('method', 'rules')
        )

        explanation = self.explainer.explain(c, metrics, audience)

        return {
            'summary': explanation.summary,
            'what_happened': explanation.what_happened,
            'why_happened': explanation.why_happened,
            'impact': explanation.impact,
            'next_steps': explanation.next_steps,
        }

    def TrainModel(self, request, context) -> Dict[str, Any]:
        """Train the classifier on historical data."""
        training_data = request.get('training_data', [])

        if not training_data:
            return {'success': False, 'message': 'No training data provided'}

        # Convert to internal format
        formatted_data = []
        for item in training_data:
            metrics = {m.get('name', ''): m.get('value', 0) for m in item.get('metrics', [])}
            category = IssueCategory(item.get('label', 'unknown'))
            formatted_data.append((metrics, category))

        try:
            self.classifier.train(formatted_data)
            return {
                'success': True,
                'message': f'Model trained on {len(training_data)} samples',
                'model_version': '1.0.0',
            }
        except Exception as e:
            logger.error(f"Training failed: {e}")
            return {'success': False, 'message': str(e)}

    def _generate_suggestions(
        self,
        category: IssueCategory,
        severity: Severity,
        request: Dict[str, Any]
    ) -> List[Dict[str, Any]]:
        """Generate suggestions based on category."""
        suggestions = []

        if category == IssueCategory.MEMORY_PRESSURE:
            # Check if OOMKilled
            logs = request.get('logs', [])
            is_oom = any('oomkilled' in log.get('message', '').lower() for log in logs)

            if is_oom:
                suggestions.append({
                    'type': 'resource_limit',
                    'title': 'Increase Memory Limits',
                    'description': 'Process was OOMKilled. Increase memory limits to prevent recurrence.',
                    'confidence': 0.90,
                    'risk_level': 'low',
                    'steps': [
                        {'order': 1, 'action': 'identify_process', 'target': 'oomkilled_process'},
                        {'order': 2, 'action': 'increase_memory_limit', 'target': 'container', 'parameters': {'factor': '1.5'}},
                        {'order': 3, 'action': 'restart', 'target': 'pod'},
                    ],
                    'reasoning': 'OOMKilled indicates memory limit exceeded. Increasing limit prevents termination.'
                })

            suggestions.append({
                'type': 'kill_process',
                'title': 'Kill Idle Processes',
                'description': 'Terminate non-essential processes to free memory.',
                'confidence': 0.75,
                'risk_level': 'medium',
                'steps': [
                    {'order': 1, 'action': 'list_processes', 'command': 'ps aux --sort=-%mem | head -20'},
                    {'order': 2, 'action': 'identify_idle', 'requires_approval': True},
                    {'order': 3, 'action': 'kill', 'requires_approval': True},
                ],
                'reasoning': 'Immediate memory relief by terminating non-critical processes.'
            })

        elif category == IssueCategory.CPU_SATURATION:
            suggestions.append({
                'type': 'scale',
                'title': 'Scale Horizontally',
                'description': 'Distribute load across more replicas.',
                'confidence': 0.80,
                'risk_level': 'low',
                'steps': [
                    {'order': 1, 'action': 'verify_autoscaler', 'target': 'deployment'},
                    {'order': 2, 'action': 'scale_replicas', 'parameters': {'increment': '2'}},
                    {'order': 3, 'action': 'monitor', 'parameters': {'duration': '5m'}},
                ],
                'reasoning': 'Horizontal scaling spreads load across instances.'
            })

            suggestions.append({
                'type': 'kill_process',
                'title': 'Stop Runaway Process',
                'description': 'Identify and terminate CPU-heavy processes.',
                'confidence': 0.70,
                'risk_level': 'medium',
                'steps': [
                    {'order': 1, 'action': 'identify', 'command': 'top -b -n 1 -o %CPU | head -20'},
                    {'order': 2, 'action': 'analyze', 'requires_approval': False},
                    {'order': 3, 'action': 'kill', 'requires_approval': True},
                ],
                'reasoning': 'Runaway processes consume CPU; terminating restores normalcy.'
            })

        elif category == IssueCategory.DISK_IO_BOTTLENECK:
            suggestions.append({
                'type': 'cleanup',
                'title': 'Clean Up Disk',
                'description': 'Remove old logs and temp files.',
                'confidence': 0.85,
                'risk_level': 'low',
                'steps': [
                    {'order': 1, 'action': 'analyze', 'command': 'du -sh /* 2>/dev/null | sort -rh | head -10'},
                    {'order': 2, 'action': 'cleanup_logs', 'command': 'journalctl --vacuum-time=7d'},
                    {'order': 3, 'action': 'cleanup_temp', 'target': '/tmp'},
                ],
                'reasoning': 'Removing old files frees disk space quickly.'
            })

        elif category == IssueCategory.CAPACITY_ISSUE:
            suggestions.append({
                'type': 'scale',
                'title': 'Increase Capacity',
                'description': 'Scale resources to meet demand.',
                'confidence': 0.80,
                'risk_level': 'low',
                'steps': [
                    {'order': 1, 'action': 'analyze_usage'},
                    {'order': 2, 'action': 'plan_scaling', 'requires_approval': True},
                    {'order': 3, 'action': 'apply_changes', 'requires_approval': True},
                ],
                'reasoning': 'Capacity issues require infrastructure scaling.'
            })

        # Default suggestion
        if not suggestions:
            suggestions.append({
                'type': 'manual',
                'title': 'Manual Investigation Required',
                'description': 'Investigate the issue manually.',
                'confidence': 0.50,
                'risk_level': 'low',
                'steps': [
                    {'order': 1, 'action': 'review_logs'},
                    {'order': 2, 'action': 'check_metrics'},
                    {'order': 3, 'action': 'contact_on_call'},
                ],
                'reasoning': 'Issue requires human investigation.'
            })

        return suggestions


class JSONRPCHandler:
    """Simple JSON-RPC handler as an alternative to full gRPC.
    
    This can be used for testing or in environments where gRPC is not available.
    """

    def __init__(self, servicer: AIServicer):
        self.servicer = servicer

    def handle(self, request_json: str) -> str:
        """Handle a JSON-RPC request."""
        request = json.loads(request_json)
        method = request.get('method', '')
        params = request.get('params', {})
        request_id = request.get('id', 1)

        result = None
        error = None

        try:
            if method == 'classify':
                result = self.servicer.ClassifyIssue(params, None)
            elif method == 'suggest':
                result = self.servicer.SuggestFix(params, None)
            elif method == 'explain':
                result = self.servicer.ExplainIssue(params, None)
            elif method == 'train':
                result = self.servicer.TrainModel(params, None)
            else:
                error = {'code': -32601, 'message': f'Method not found: {method}'}
        except Exception as e:
            error = {'code': -32603, 'message': str(e)}

        response = {'jsonrpc': '2.0', 'id': request_id}
        if error:
            response['error'] = error
        else:
            response['result'] = result

        return json.dumps(response)


def serve(port: int = 50052, model_path: Optional[str] = None):
    """Start the AI service.
    
    Args:
        port: Port to listen on
        model_path: Path to saved model
    """
    # Create AI components
    classifier, explainer = create_ai_service(model_path)
    servicer = AIServicer(classifier, explainer)

    # For now, use HTTP/JSON-RPC as a simple alternative
    # Full gRPC implementation would use the generated proto stubs
    from http.server import HTTPServer, BaseHTTPRequestHandler

    handler = JSONRPCHandler(servicer)

    class RequestHandler(BaseHTTPRequestHandler):
        def do_POST(self):
            content_length = int(self.headers['Content-Length'])
            body = self.rfile.read(content_length).decode('utf-8')

            response = handler.handle(body)

            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(response.encode('utf-8'))

        def log_message(self, format, *args):
            logger.info(f"{self.address_string()} - {format % args}")

    server = HTTPServer(('0.0.0.0', port), RequestHandler)
    logger.info(f"AI Service listening on port {port}")
    server.serve_forever()


def main():
    """Main entry point."""
    parser = argparse.ArgumentParser(description='AI Analysis Service')
    parser.add_argument('--port', type=int, default=50052, help='Port to listen on')
    parser.add_argument('--model', type=str, default=None, help='Path to saved model')
    parser.add_argument('--log-level', type=str, default='INFO', help='Log level')

    args = parser.parse_args()

    logging.basicConfig(
        level=getattr(logging, args.log_level.upper()),
        format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
    )

    serve(args.port, args.model)


if __name__ == '__main__':
    main()
