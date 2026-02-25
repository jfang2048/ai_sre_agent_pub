package predictor

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/brain/llm"
	"github.com/jfang2048/ai_sre_agent_pub/pkg/proto"
	metricspb "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
	"go.uber.org/zap"
)

// Predictor makes predictions about future system state
type Predictor struct {
	llmClient LLMClient
	logger    *zap.Logger
}

// LLMClient is the LLM client interface
type LLMClient interface {
	Analyze(ctx context.Context, data *llm.AnalysisData) (*llm.AnalysisResult, error)
}

// Config configures the predictor
type Config struct {
	HistoryWindow     time.Duration
	PredictionHorizon time.Duration
	MinConfidence     float64
}

// NewPredictor creates a new predictor
func NewPredictor(llmClient LLMClient, logger *zap.Logger) *Predictor {
	return &Predictor{
		llmClient: llmClient,
		logger:    logger.With(zap.String("component", "predictor")),
	}
}

// SLOPrediction is an SLO violation prediction
type SLOPrediction struct {
	SLOName          string    `json:"slo_name"`
	WillViolate      bool      `json:"will_violate"`
	Confidence       float64   `json:"confidence"`
	PredictedValue   float64   `json:"predicted_value"`
	ViolationTime    time.Time `json:"violation_time,omitempty"`
	Reasoning        string    `json:"reasoning"`
	SuggestedActions []string  `json:"suggested_actions"`
}

// AnomalyPrediction is an anomaly prediction
type AnomalyPrediction struct {
	MetricName      string    `json:"metric_name,omitempty"`
	WillBeAnomalous bool      `json:"will_be_anomalous"`
	Confidence      float64   `json:"confidence"`
	Reason          string    `json:"reason"`
	PredictedValues []float64 `json:"predicted_values,omitempty"`
}

// PredictSLOViolation predicts if an SLO will be violated
func (p *Predictor) PredictSLOViolation(ctx context.Context, slo *proto.SLO, history []*metricspb.Metric) (*SLOPrediction, error) {
	p.logger.Info("predicting SLO violation",
		zap.String("slo", slo.Name),
		zap.Int("history_points", len(history)))

	// Get target value based on SLO type
	targetValue := p.getSLOTargetValue(slo)

	// Extract values from history
	values := extractMetricValues(history)
	if len(values) < 2 {
		return &SLOPrediction{
			SLOName:     slo.Name,
			WillViolate: false,
			Confidence:  0.0,
			Reasoning:   "Insufficient data for prediction",
		}, nil
	}

	// Calculate trend
	trend := calculateTrend(values)
	currentValue := values[len(values)-1]

	// Predict when threshold will be crossed
	prediction := &SLOPrediction{
		SLOName:        slo.Name,
		PredictedValue: currentValue + trend*10, // 10 steps ahead
		Reasoning:      "Statistical trend analysis",
	}

	// Check if violation is likely based on SLO type
	switch slo.Type {
	case proto.SLOType_SLO_TYPE_AVAILABILITY:
		// Higher is better
		if trend < 0 {
			stepsToViolation := int((currentValue - targetValue) / -trend)
			if stepsToViolation > 0 && stepsToViolation < 100 {
				prediction.WillViolate = true
				prediction.Confidence = math.Min(0.9, 0.5+float64(len(values))*0.05)
				prediction.ViolationTime = time.Now().Add(time.Duration(stepsToViolation) * time.Minute)
			}
		}
	case proto.SLOType_SLO_TYPE_LATENCY:
		// Lower is better
		if trend > 0 {
			stepsToViolation := int((targetValue - currentValue) / trend)
			if stepsToViolation > 0 && stepsToViolation < 100 {
				prediction.WillViolate = true
				prediction.Confidence = math.Min(0.9, 0.5+float64(len(values))*0.05)
				prediction.ViolationTime = time.Now().Add(time.Duration(stepsToViolation) * time.Minute)
			}
		}
	case proto.SLOType_SLO_TYPE_THROUGHPUT:
		// Higher is better
		if trend < 0 {
			stepsToViolation := int((currentValue - targetValue) / -trend)
			if stepsToViolation > 0 && stepsToViolation < 100 {
				prediction.WillViolate = true
				prediction.Confidence = math.Min(0.9, 0.5+float64(len(values))*0.05)
				prediction.ViolationTime = time.Now().Add(time.Duration(stepsToViolation) * time.Minute)
			}
		}
	}

	// Get LLM-enhanced prediction if available
	if p.llmClient != nil {
		llmPrediction, err := p.llmSLOPrediction(ctx, slo, history)
		if err == nil {
			return p.combinePredictions(prediction, llmPrediction), nil
		}
	}

	return prediction, nil
}

// getSLOTargetValue extracts the target value from SLO based on its type
func (p *Predictor) getSLOTargetValue(slo *proto.SLO) float64 {
	switch slo.Type {
	case proto.SLOType_SLO_TYPE_AVAILABILITY:
		if target, ok := slo.Target.(*proto.SLO_AvailabilityTarget); ok {
			return target.AvailabilityTarget
		}
	case proto.SLOType_SLO_TYPE_LATENCY:
		if target, ok := slo.Target.(*proto.SLO_LatencyTarget); ok && target.LatencyTarget != nil {
			return target.LatencyTarget.GetValue()
		}
	case proto.SLOType_SLO_TYPE_THROUGHPUT:
		if target, ok := slo.Target.(*proto.SLO_ThroughputTarget); ok && target.ThroughputTarget != nil {
			return target.ThroughputTarget.GetRps()
		}
	case proto.SLOType_SLO_TYPE_RESOURCE:
		if target, ok := slo.Target.(*proto.SLO_ResourceTarget); ok && target.ResourceTarget != nil {
			return target.ResourceTarget.GetCpuPercent()
		}
	case proto.SLOType_SLO_TYPE_CUSTOM:
		if target, ok := slo.Target.(*proto.SLO_CustomTarget); ok {
			return target.CustomTarget
		}
	}
	return 0.0
}

// llmSLOPrediction gets LLM-based prediction
func (p *Predictor) llmSLOPrediction(ctx context.Context, slo *proto.SLO, history []*metricspb.Metric) (*SLOPrediction, error) {
	data := &llm.AnalysisData{
		Metrics: history,
		SLOs:    []*proto.SLO{slo},
		Context: p.buildSLOContext(slo, history),
	}

	result, err := p.llmClient.Analyze(ctx, data)
	if err != nil {
		return nil, err
	}

	prediction := &SLOPrediction{
		SLOName:          slo.Name,
		WillViolate:      false,
		Confidence:       result.Confidence,
		Reasoning:        result.Summary,
		SuggestedActions: result.Recommendations,
	}

	for _, pred := range result.Predictions {
		if pred.Type == "slo_violation" {
			prediction.WillViolate = pred.WillHappen
			prediction.Confidence = pred.Confidence
			prediction.ViolationTime = pred.TimeHorizon
		}
	}

	return prediction, nil
}

// combinePredictions combines statistical and LLM predictions
func (p *Predictor) combinePredictions(stat, llm *SLOPrediction) *SLOPrediction {
	// Weight the predictions
	statWeight := 0.5
	llmWeight := 0.5

	if llm.Confidence > 0.8 {
		llmWeight = 0.7
		statWeight = 0.3
	} else if llm.Confidence < 0.3 {
		llmWeight = 0.2
		statWeight = 0.8
	}

	combinedWillViolate := (stat.WillViolate && statWeight > 0.3) || (llm.WillViolate && llmWeight > 0.3)
	combinedConfidence := stat.Confidence*statWeight + llm.Confidence*llmWeight

	result := &SLOPrediction{
		SLOName:          stat.SLOName,
		WillViolate:      combinedWillViolate,
		Confidence:       combinedConfidence,
		PredictedValue:   stat.PredictedValue,
		Reasoning:        "Combined statistical and LLM analysis",
		SuggestedActions: llm.SuggestedActions,
	}

	// Use earlier violation time
	if stat.WillViolate && llm.WillViolate {
		if stat.ViolationTime.Before(llm.ViolationTime) {
			result.ViolationTime = stat.ViolationTime
		} else {
			result.ViolationTime = llm.ViolationTime
		}
	} else if stat.WillViolate {
		result.ViolationTime = stat.ViolationTime
	} else if llm.WillViolate {
		result.ViolationTime = llm.ViolationTime
	}

	return result
}

// PredictAnomaly predicts if an anomaly will occur
func (p *Predictor) PredictAnomaly(ctx context.Context, metrics []*metricspb.Metric) (*AnomalyPrediction, error) {
	predictions := make([]*AnomalyPrediction, 0)

	for _, metric := range metrics {
		values := extractMetricValuesFromMetric(metric)
		if len(values) < 10 {
			continue
		}

		// Calculate anomaly score
		anomalyScore := calculateAnomalyScore(values)

		if anomalyScore > 0.7 {
			predictions = append(predictions, &AnomalyPrediction{
				MetricName:      metric.Name,
				WillBeAnomalous: true,
				Confidence:      anomalyScore,
				Reason:          "Statistical anomaly detected",
			})
		}
	}

	if len(predictions) == 0 {
		return &AnomalyPrediction{
			WillBeAnomalous: false,
			Confidence:      0.0,
		}, nil
	}

	// Return highest confidence prediction
	result := predictions[0]
	for _, pred := range predictions {
		if pred.Confidence > result.Confidence {
			result = pred
		}
	}

	return result, nil
}

// buildSLOContext builds context for SLO prediction
func (p *Predictor) buildSLOContext(slo *proto.SLO, history []*metricspb.Metric) string {
	return fmt.Sprintf("SLO: %s, Type: %v, History points: %d",
		slo.Name, slo.Type, len(history))
}

// Helper functions

func extractMetricValues(metrics []*metricspb.Metric) []float64 {
	values := make([]float64, 0)
	for _, m := range metrics {
		for _, p := range m.Points {
			values = append(values, p.Value)
		}
	}
	return values
}

func extractMetricValuesFromMetric(metric *metricspb.Metric) []float64 {
	values := make([]float64, len(metric.Points))
	for i, p := range metric.Points {
		values[i] = p.Value
	}
	return values
}

func calculateTrend(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}

	// Simple linear regression
	n := float64(len(values))
	sumX := 0.0
	sumY := 0.0
	sumXY := 0.0
	sumX2 := 0.0

	for i, y := range values {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	slope := (n*sumXY - sumX*sumY) / (n*sumX2 - sumX*sumX)
	return slope
}

func calculateAnomalyScore(values []float64) float64 {
	// Calculate mean and standard deviation
	n := float64(len(values))
	mean := 0.0
	for _, v := range values {
		mean += v
	}
	mean /= n

	variance := 0.0
	for _, v := range values {
		diff := v - mean
		variance += diff * diff
	}
	variance /= n
	stdDev := math.Sqrt(variance)

	// Calculate z-score of last value
	lastValue := values[len(values)-1]
	zScore := math.Abs((lastValue - mean) / stdDev)

	// Convert to anomaly score (0-1)
	anomalyScore := math.Min(1.0, zScore/3.0)
	return anomalyScore
}
