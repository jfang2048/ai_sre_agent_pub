"""gRPC server for Python SRE agent services."""

import logging
from concurrent import futures
from typing import Any, Dict

import grpc

# Import generated protobuf modules
import proto.agent_pb2 as agent_proto
import proto.agent_pb2_grpc as agent_grpc
import proto.metrics_pb2 as metrics_proto
import proto.metrics_pb2_grpc as metrics_grpc
import proto.prediction_pb2 as prediction_proto
import proto.prediction_pb2_grpc as prediction_grpc

logger = logging.getLogger(__name__)


class PredictionService(prediction_proto.PredictionServiceServicer):
    """Service for ML predictions."""

    def __init__(self, analyzer: Any):
        """Initialize the prediction service.

        Args:
            analyzer: Anomaly detector and forecaster
        """
        self.analyzer = analyzer

    def PredictSLOViolation(
        self,
        request: prediction_proto.PredictSLORequest,
        context: grpc.ServicerContext,
    ) -> prediction_proto.PredictionResponse:
        """Predict if an SLO violation will occur."""
        logger.info(f"SLO prediction request for {request.slo_name}")

        # Get historical metrics
        # In production, would query from metric store

        # Run prediction
        from sre_agent.analysis.forecast import predict_slo_violation

        result = predict_slo_violation(
            current_sli=0.995,  # Would come from metrics
            slo_target=0.999,
            trend=0.0001,
            window_minutes=60,
        )

        response = prediction_proto.PredictionResponse()
        response.will_violate = result["will_violate"]
        response.confidence = result["confidence"]

        if result.get("predicted_value"):
            response.predicted_value = result["predicted_value"]

        if result.get("time_to_violation_minutes"):
            response.predicted_violation_time.seconds = int(
                result["time_to_violation_minutes"] * 60
            )

        return response

    def ForecastMetrics(
        self,
        request: prediction_proto.ForecastRequest,
        context: grpc.ServicerContext,
    ) -> prediction_proto.ForecastResponse:
        """Forecast metric values."""
        logger.info(f"Forecast request for {request.metric_name}")

        from sre_agent.analysis.forecast import linear_trend_forecast

        # Simulated historical data
        values = [100.0, 102.0, 101.0, 103.0, 105.0, 104.0, 106.0]

        forecasts = []
        for _ in range(request.steps):
            forecast = linear_trend_forecast(values, len(forecasts) + 1)
            forecasts.append(forecast)

        response = prediction_proto.ForecastResponse()
        response.forecast.extend([prediction_proto.DataPoint(value=f) for f in forecasts])

        return response


class AnalysisService(agent_proto.AgentServiceServicer):
    """Service for LLM-based analysis."""

    def __init__(self, llm_client: Any):
        """Initialize the analysis service.

        Args:
            llm_client: LLM client for analysis
        """
        self.llm_client = llm_client

    def Analyze(
        self,
        request: agent_proto.AnalysisRequest,
        context: grpc.ServicerContext,
    ) -> agent_proto.AnalysisResponse:
        """Analyze metrics and logs."""
        logger.info("Analysis request received")

        # Convert proto to Python structures
        metrics = [
            {"name": m.name, "value": m.points[0].value if m.points else 0} for m in request.metrics
        ]

        # Run analysis
        result = self.llm_client.analyze(
            metrics=metrics,
            logs=list(request.logs),
            alerts=[{"name": a.name, "severity": a.severity} for a in request.alerts],
            context=request.context,
        )

        response = agent_proto.AnalysisResponse()
        response.summary = result.get("summary", "")
        response.confidence = result.get("confidence", 0.5)

        # Add predictions
        for pred in result.get("predictions", []):
            p = response.predictions.add()
            p.type = pred.get("type", "")
            p.description = pred.get("description", "")
            p.will_happen = pred.get("will_happen", False)
            p.confidence = pred.get("confidence", 0.5)

        # Add recommendations
        response.recommendions.extend(result.get("recommendations", []))

        return response


def serve(
    port: int = 50051,
    llm_client: Any = None,
    analyzer: Any = None,
) -> grpc.Server:
    """Start the gRPC server.

    Args:
        port: Port to listen on
        llm_client: LLM client for analysis
        analyzer: Anomaly detector

    Returns:
        Started gRPC server
    """
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))

    # Register services
    prediction_svc = PredictionService(analyzer)
    prediction_grpc.add_PredictionServiceServicer_to_server(prediction_svc, server)

    analysis_svc = AnalysisService(llm_client)
    agent_grpc.add_AgentServiceServicer_to_server(analysis_svc, server)

    # Start server
    server.add_insecure_port(f"[::]:{port}")
    server.start()

    logger.info(f"Python gRPC server started on port {port}")

    return server
