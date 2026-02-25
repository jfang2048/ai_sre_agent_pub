package observability

import (
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

var (
	globalRegistry *prometheus.Registry
	once           sync.Once
	logger         *zap.Logger
)

// Metrics holds all application metrics
type Metrics struct {
	// Agent metrics
	Uptime             *prometheus.GaugeVec
	StartTime          prometheus.Gauge
	CollectionDuration *prometheus.HistogramVec
	CollectionErrors   *prometheus.CounterVec
	CollectionSuccess  *prometheus.CounterVec

	// Prediction metrics
	PredictionsTotal     *prometheus.CounterVec
	PredictionsCorrect   *prometheus.CounterVec
	PredictionsIncorrect *prometheus.CounterVec
	PredictionConfidence *prometheus.HistogramVec

	// Remediation metrics
	ActionsTotal   *prometheus.CounterVec
	ActionsSuccess *prometheus.CounterVec
	ActionsFailed  *prometheus.CounterVec
	ActionDuration *prometheus.HistogramVec

	// SLO metrics
	SLOStatus         *prometheus.GaugeVec
	SLOErrorBudget    *prometheus.GaugeVec
	SLOViolationCount *prometheus.CounterVec

	// LLM metrics
	LLMRequestsTotal   *prometheus.CounterVec
	LLMRequestDuration *prometheus.HistogramVec
	LLMRequestTokens   *prometheus.HistogramVec
	LLMResponseTokens  *prometheus.HistogramVec

	// System metrics
	CPUUsage       *prometheus.GaugeVec
	MemoryUsage    *prometheus.GaugeVec
	GoroutineCount prometheus.Gauge
	HeapAllocBytes prometheus.Gauge
	HeapSysBytes   prometheus.Gauge
}

var instance *Metrics
var mu sync.Mutex

// GetMetrics returns the singleton Metrics instance
func GetMetrics() *Metrics {
	mu.Lock()
	defer mu.Unlock()

	if instance == nil {
		instance = &Metrics{
			// Agent metrics
			Uptime: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "sre_agent_uptime_seconds",
					Help: "Agent uptime in seconds",
				},
				[]string{"agent_name"},
			),
			StartTime: prometheus.NewGauge(
				prometheus.GaugeOpts{
					Name: "sre_agent_start_time_seconds",
					Help: "Agent start timestamp",
				},
			),
			CollectionDuration: prometheus.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "sre_agent_collection_duration_seconds",
					Help:    "Duration of metric collection",
					Buckets: prometheus.DefBuckets,
				},
				[]string{"source"},
			),
			CollectionErrors: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "sre_agent_collection_errors_total",
					Help: "Total number of collection errors",
				},
				[]string{"source"},
			),
			CollectionSuccess: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "sre_agent_collection_success_total",
					Help: "Total number of successful collections",
				},
				[]string{"source"},
			),

			// Prediction metrics
			PredictionsTotal: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "sre_agent_predictions_total",
					Help: "Total number of predictions made",
				},
				[]string{"type"},
			),
			PredictionsCorrect: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "sre_agent_predictions_correct_total",
					Help: "Total number of correct predictions",
				},
				[]string{"type"},
			),
			PredictionsIncorrect: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "sre_agent_predictions_incorrect_total",
					Help: "Total number of incorrect predictions",
				},
				[]string{"type"},
			),
			PredictionConfidence: prometheus.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "sre_agent_prediction_confidence",
					Help:    "Confidence score of predictions",
					Buckets: []float64{0.1, 0.25, 0.5, 0.75, 0.9, 0.95, 0.99},
				},
				[]string{"type"},
			),

			// Remediation metrics
			ActionsTotal: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "sre_agent_actions_total",
					Help: "Total number of remediation actions",
				},
				[]string{"type", "status"},
			),
			ActionsSuccess: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "sre_agent_actions_success_total",
					Help: "Total number of successful actions",
				},
				[]string{"type"},
			),
			ActionsFailed: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "sre_agent_actions_failed_total",
					Help: "Total number of failed actions",
				},
				[]string{"type"},
			),
			ActionDuration: prometheus.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "sre_agent_action_duration_seconds",
					Help:    "Duration of remediation actions",
					Buckets: prometheus.DefBuckets,
				},
				[]string{"type"},
			),

			// SLO metrics
			SLOStatus: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "sre_agent_slo_status",
					Help: "Current SLO status (1=meeting, 0=violating)",
				},
				[]string{"slo_name"},
			),
			SLOErrorBudget: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "sre_agent_slo_error_budget_remaining",
					Help: "Remaining error budget percentage",
				},
				[]string{"slo_name"},
			),
			SLOViolationCount: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "sre_agent_slo_violations_total",
					Help: "Total number of SLO violations",
				},
				[]string{"slo_name"},
			),

			// LLM metrics
			LLMRequestsTotal: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "sre_agent_llm_requests_total",
					Help: "Total number of LLM requests",
				},
				[]string{"provider", "model"},
			),
			LLMRequestDuration: prometheus.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "sre_agent_llm_request_duration_seconds",
					Help:    "Duration of LLM requests",
					Buckets: prometheus.DefBuckets,
				},
				[]string{"provider", "model"},
			),
			LLMRequestTokens: prometheus.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "sre_agent_llm_request_tokens",
					Help:    "Number of tokens in LLM requests",
					Buckets: prometheus.ExponentialBuckets(100, 2, 10),
				},
				[]string{"provider", "model"},
			),
			LLMResponseTokens: prometheus.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "sre_agent_llm_response_tokens",
					Help:    "Number of tokens in LLM responses",
					Buckets: prometheus.ExponentialBuckets(100, 2, 10),
				},
				[]string{"provider", "model"},
			),

			// System metrics
			CPUUsage: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "sre_agent_cpu_usage_ratio",
					Help: "Agent CPU usage ratio",
				},
				[]string{"mode"},
			),
			MemoryUsage: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "sre_agent_memory_usage_bytes",
					Help: "Agent memory usage in bytes",
				},
				[]string{"type"},
			),
			GoroutineCount: prometheus.NewGauge(
				prometheus.GaugeOpts{
					Name: "sre_agent_goroutines",
					Help: "Number of goroutines",
				},
			),
			HeapAllocBytes: prometheus.NewGauge(
				prometheus.GaugeOpts{
					Name: "sre_agent_heap_alloc_bytes",
					Help: "Current heap allocation in bytes",
				},
			),
			HeapSysBytes: prometheus.NewGauge(
				prometheus.GaugeOpts{
					Name: "sre_agent_heap_sys_bytes",
					Help: "Current heap system memory in bytes",
				},
			),
		}

		// Register all metrics
		globalRegistry.MustRegister(
			instance.Uptime,
			instance.StartTime,
			instance.CollectionDuration,
			instance.CollectionErrors,
			instance.CollectionSuccess,
			instance.PredictionsTotal,
			instance.PredictionsCorrect,
			instance.PredictionsIncorrect,
			instance.PredictionConfidence,
			instance.ActionsTotal,
			instance.ActionsSuccess,
			instance.ActionsFailed,
			instance.ActionDuration,
			instance.SLOStatus,
			instance.SLOErrorBudget,
			instance.SLOViolationCount,
			instance.LLMRequestsTotal,
			instance.LLMRequestDuration,
			instance.LLMRequestTokens,
			instance.LLMResponseTokens,
			instance.CPUUsage,
			instance.MemoryUsage,
			instance.GoroutineCount,
			instance.HeapAllocBytes,
			instance.HeapSysBytes,
		)
	}

	return instance
}

// Init initializes the observability package
func Init(l *zap.Logger, registry *prometheus.Registry) {
	logger = l
	globalRegistry = registry
}

// StartMetricsServer starts the Prometheus metrics server
func StartMetricsServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(globalRegistry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		logger.Info("starting metrics server", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("metrics server error", zap.Error(err))
		}
	}()

	return srv
}

// RecordCollection records a metric collection operation
func RecordCollection(source string, duration time.Duration, err error) {
	m := GetMetrics()

	m.CollectionDuration.WithLabelValues(source).Observe(duration.Seconds())

	if err != nil {
		m.CollectionErrors.WithLabelValues(source).Inc()
	} else {
		m.CollectionSuccess.WithLabelValues(source).Inc()
	}
}

// RecordPrediction records a prediction
func RecordPrediction(predType string, correct bool, confidence float64) {
	m := GetMetrics()

	m.PredictionsTotal.WithLabelValues(predType).Inc()
	m.PredictionConfidence.WithLabelValues(predType).Observe(confidence)

	if correct {
		m.PredictionsCorrect.WithLabelValues(predType).Inc()
	} else {
		m.PredictionsIncorrect.WithLabelValues(predType).Inc()
	}
}

// RecordAction records a remediation action
func RecordAction(actionType string, success bool, duration time.Duration) {
	m := GetMetrics()

	m.ActionDuration.WithLabelValues(actionType).Observe(duration.Seconds())

	status := "success"
	if success {
		m.ActionsSuccess.WithLabelValues(actionType).Inc()
	} else {
		status = "failed"
		m.ActionsFailed.WithLabelValues(actionType).Inc()
	}

	m.ActionsTotal.WithLabelValues(actionType, status).Inc()
}

// UpdateSystemMetrics updates system resource metrics
func UpdateSystemMetrics(cpuRatio float64, memUsed, memSys uint64, goroutines int) {
	m := GetMetrics()

	m.CPUUsage.WithLabelValues("user").Set(cpuRatio)
	m.MemoryUsage.WithLabelValues("used").Set(float64(memUsed))
	m.MemoryUsage.WithLabelValues("system").Set(float64(memSys))
	m.GoroutineCount.Set(float64(goroutines))
}

// RecordLLMRequest records an LLM request
func RecordLLMRequest(provider, model string, duration time.Duration, requestTokens, responseTokens int) {
	m := GetMetrics()

	m.LLMRequestsTotal.WithLabelValues(provider, model).Inc()
	m.LLMRequestDuration.WithLabelValues(provider, model).Observe(duration.Seconds())
	m.LLMRequestTokens.WithLabelValues(provider, model).Observe(float64(requestTokens))
	m.LLMResponseTokens.WithLabelValues(provider, model).Observe(float64(responseTokens))
}
