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

type seriesProfile struct {
	Baseline         float64
	RecentMean       float64
	Current          float64
	Trend            float64
	Acceleration     float64
	Volatility       float64
	RelativeChange   float64
	DirectionalSteps int
	Unstable         bool
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
	profile := analyzeSeries(values)
	stepDuration := estimateSampleStep(history)
	stepsAhead := predictionSteps(stepDuration)

	// Predict when threshold will be crossed
	prediction := &SLOPrediction{
		SLOName:          slo.Name,
		PredictedValue:   currentValue + trend*float64(stepsAhead),
		Confidence:       predictionConfidence(profile, len(values)),
		Reasoning:        buildPredictionReasoning(slo, profile),
		SuggestedActions: suggestedActionsForSLO(slo, profile),
	}

	// Check if violation is likely based on SLO type
	switch slo.Type {
	case proto.SLOType_SLO_TYPE_AVAILABILITY:
		// Higher is better
		if trend < 0 && !profile.Unstable {
			stepsToViolation := int((currentValue - targetValue) / -trend)
			if stepsToViolation > 0 && stepsToViolation < 100 {
				prediction.WillViolate = true
				prediction.Confidence = math.Max(prediction.Confidence, predictionConfidence(profile, len(values)))
				prediction.ViolationTime = time.Now().Add(time.Duration(stepsToViolation) * stepDuration)
			}
		}
	case proto.SLOType_SLO_TYPE_LATENCY:
		// Lower is better
		if trend > 0 && !profile.Unstable {
			stepsToViolation := int((targetValue - currentValue) / trend)
			if stepsToViolation > 0 && stepsToViolation < 100 {
				prediction.WillViolate = true
				prediction.Confidence = math.Max(prediction.Confidence, predictionConfidence(profile, len(values)))
				prediction.ViolationTime = time.Now().Add(time.Duration(stepsToViolation) * stepDuration)
			}
		}
	case proto.SLOType_SLO_TYPE_THROUGHPUT:
		// Higher is better
		if trend < 0 && !profile.Unstable {
			stepsToViolation := int((currentValue - targetValue) / -trend)
			if stepsToViolation > 0 && stepsToViolation < 100 {
				prediction.WillViolate = true
				prediction.Confidence = math.Max(prediction.Confidence, predictionConfidence(profile, len(values)))
				prediction.ViolationTime = time.Now().Add(time.Duration(stepsToViolation) * stepDuration)
			}
		}
	}

	// Blend in the LLM prediction when a client is configured.
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
		profile := analyzeSeries(values)

		if anomalyScore > 0.7 {
			predictions = append(predictions, &AnomalyPrediction{
				MetricName:      metric.Name,
				WillBeAnomalous: true,
				Confidence:      anomalyScore,
				Reason:          anomalyReason(metric.Name, anomalyScore, profile),
				PredictedValues: extrapolateValues(values, 3),
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
	if len(values) < 2 {
		return 0
	}
	// Calculate mean and standard deviation
	window := values[:len(values)-1]
	n := float64(len(window))
	mean := 0.0
	for _, v := range window {
		mean += v
	}
	mean /= n

	variance := 0.0
	for _, v := range window {
		diff := v - mean
		variance += diff * diff
	}
	variance /= n
	stdDev := math.Sqrt(variance)

	// Calculate z-score of last value
	lastValue := values[len(values)-1]
	if stdDev < 1e-9 {
		if math.Abs(lastValue-mean) < 1e-9 {
			return 0
		}
		return 1
	}
	zScore := math.Abs((lastValue - mean) / stdDev)
	prev := values[len(values)-2]
	jumpScore := 0.0
	if math.Abs(prev) > 1e-9 {
		jumpScore = math.Abs((lastValue - prev) / math.Abs(prev))
	}

	// Convert to anomaly score (0-1)
	anomalyScore := math.Min(1.0, math.Max(zScore/3.0, jumpScore))
	return anomalyScore
}

func analyzeSeries(values []float64) seriesProfile {
	if len(values) == 0 {
		return seriesProfile{}
	}
	current := values[len(values)-1]
	if len(values) == 1 {
		return seriesProfile{Baseline: current, RecentMean: current, Current: current}
	}

	baselineWindow := values[:len(values)-1]
	if len(baselineWindow) > 0 && len(baselineWindow) > 8 {
		baselineWindow = baselineWindow[:len(baselineWindow)-len(baselineWindow)/4]
	}
	recentWindow := values[maxInt(0, len(values)-maxInt(3, len(values)/3)):]

	baseline := mean(baselineWindow)
	recentMean := mean(recentWindow)
	trend := calculateTrend(values)
	acceleration := trend - calculateTrend(baselineWindow)
	volatility := coefficientOfVariation(baselineWindow)
	relativeChange := 0.0
	if math.Abs(baseline) > 1e-9 {
		relativeChange = (current - baseline) / math.Abs(baseline)
	} else if math.Abs(current) > 1e-9 {
		relativeChange = 1
	}

	directionalSteps := 0
	signChanges := 0
	lastSign := 0
	epsilon := math.Max(math.Abs(baseline)*0.01, 0.01)
	for idx := 1; idx < len(values); idx++ {
		delta := values[idx] - values[idx-1]
		sign := 0
		switch {
		case delta > epsilon:
			sign = 1
			directionalSteps++
		case delta < -epsilon:
			sign = -1
			directionalSteps++
		}
		if sign != 0 && lastSign != 0 && sign != lastSign {
			signChanges++
		}
		if sign != 0 {
			lastSign = sign
		}
	}

	return seriesProfile{
		Baseline:         baseline,
		RecentMean:       recentMean,
		Current:          current,
		Trend:            trend,
		Acceleration:     acceleration,
		Volatility:       volatility,
		RelativeChange:   relativeChange,
		DirectionalSteps: directionalSteps,
		Unstable:         volatility >= 0.25 || signChanges >= maxInt(2, len(values)/3),
	}
}

func buildPredictionReasoning(slo *proto.SLO, profile seriesProfile) string {
	direction := "stable"
	switch {
	case profile.Trend > 0:
		direction = "rising"
	case profile.Trend < 0:
		direction = "falling"
	}
	switch {
	case profile.Unstable:
		return fmt.Sprintf("Signal is %s but noisy relative to baseline; prediction confidence is reduced until the trend stabilizes.", direction)
	case math.Abs(profile.RelativeChange) >= 0.15:
		return fmt.Sprintf("Recent samples show a steady %s drift away from baseline (delta %.1f%%), which is more operationally meaningful than a single spike.", direction, profile.RelativeChange*100)
	default:
		return fmt.Sprintf("Recent samples are %s with modest variance, so the prediction is driven by baseline deviation rather than one outlier.", direction)
	}
}

func suggestedActionsForSLO(slo *proto.SLO, profile seriesProfile) []string {
	switch slo.Type {
	case proto.SLOType_SLO_TYPE_LATENCY:
		return []string{
			"inspect correlated saturation signals before raising timeout budgets",
			"check whether retries, queue growth, or retransmits are amplifying tail latency",
		}
	case proto.SLOType_SLO_TYPE_THROUGHPUT:
		return []string{
			"verify the workload is not feeder-starved by CPU, storage, or network contention",
			"compare recent throughput against queue depth and backpressure indicators",
		}
	case proto.SLOType_SLO_TYPE_AVAILABILITY:
		return []string{
			"inspect recent error bursts and dependency health before shifting traffic",
			"confirm whether the degradation is transient or steadily worsening",
		}
	default:
		if profile.Unstable {
			return []string{"hold changes until the signal stabilizes enough to separate noise from real degradation"}
		}
		return []string{"inspect the top contributing signals around the same window before taking corrective action"}
	}
}

func predictionConfidence(profile seriesProfile, sampleCount int) float64 {
	confidence := 0.35
	confidence += math.Min(0.2, math.Abs(profile.RelativeChange))
	confidence += math.Min(0.2, float64(sampleCount)/40.0)
	confidence += math.Min(0.15, math.Abs(profile.Trend)/math.Max(math.Abs(profile.Baseline), 1))
	confidence -= math.Min(0.2, profile.Volatility*0.5)
	if profile.Unstable {
		confidence -= 0.15
	}
	if confidence < 0.05 {
		return 0.05
	}
	if confidence > 0.95 {
		return 0.95
	}
	return confidence
}

func estimateSampleStep(history []*metricspb.Metric) time.Duration {
	best := time.Duration(0)
	for _, metric := range history {
		for idx := 1; idx < len(metric.Points); idx++ {
			prev := metric.Points[idx-1].GetTimestamp()
			curr := metric.Points[idx].GetTimestamp()
			if prev == nil || curr == nil {
				continue
			}
			delta := curr.AsTime().Sub(prev.AsTime())
			if delta <= 0 {
				continue
			}
			if best == 0 || delta < best {
				best = delta
			}
		}
	}
	if best <= 0 {
		return time.Minute
	}
	return best
}

func predictionSteps(step time.Duration) int {
	if step <= 0 {
		return 10
	}
	horizon := 10 * step
	steps := int(horizon / step)
	if steps < 3 {
		return 3
	}
	return steps
}

func anomalyReason(metric string, score float64, profile seriesProfile) string {
	switch {
	case profile.Unstable && math.Abs(profile.RelativeChange) >= 0.1:
		return fmt.Sprintf("%s is becoming unstable and diverging from baseline (score %.2f)", metric, score)
	case profile.Trend > 0:
		return fmt.Sprintf("%s is rising away from baseline fast enough to look operationally significant (score %.2f)", metric, score)
	case profile.Trend < 0:
		return fmt.Sprintf("%s is dropping away from baseline fast enough to look operationally significant (score %.2f)", metric, score)
	default:
		return fmt.Sprintf("%s shows a statistically unusual jump relative to its recent baseline (score %.2f)", metric, score)
	}
}

func extrapolateValues(values []float64, horizon int) []float64 {
	if len(values) == 0 || horizon <= 0 {
		return nil
	}
	trend := calculateTrend(values)
	current := values[len(values)-1]
	out := make([]float64, 0, horizon)
	for step := 1; step <= horizon; step++ {
		out = append(out, current+trend*float64(step))
	}
	return out
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func coefficientOfVariation(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	avg := mean(values)
	variance := 0.0
	for _, value := range values {
		diff := value - avg
		variance += diff * diff
	}
	stddev := math.Sqrt(variance / float64(len(values)))
	return stddev / math.Max(math.Abs(avg), 1.0)
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
