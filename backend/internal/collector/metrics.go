package collector

import (
	"errors"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

type runtimePromMetrics struct {
	reportAttempts      prometheus.Counter
	reportFailures      *prometheus.CounterVec
	batchesEnqueued     prometheus.Counter
	batchesSent         prometheus.Counter
	collectionDuration  prometheus.Histogram
	collectionErrors    *prometheus.CounterVec
	collectionSuccess   *prometheus.CounterVec
	configReloads       *prometheus.CounterVec
	currentPollInterval prometheus.Gauge
	retryBackoff        prometheus.Gauge
	failureStreak       prometheus.Gauge
}

func newRuntimePromMetrics() *runtimePromMetrics {
	metrics := &runtimePromMetrics{
		reportAttempts: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "sre_collector_report_attempts_total",
			Help: "Total telemetry report attempts.",
		}),
		reportFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sre_collector_report_failures_total",
			Help: "Total telemetry report failures grouped by reason.",
		}, []string{"reason"}),
		batchesEnqueued: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "sre_collector_batches_enqueued_total",
			Help: "Total batches enqueued into local spool.",
		}),
		batchesSent: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "sre_collector_batches_sent_total",
			Help: "Total batches drained and acknowledged by controller.",
		}),
		collectionDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "sre_collector_collection_duration_seconds",
			Help:    "Duration of collect-and-send cycles.",
			Buckets: prometheus.DefBuckets,
		}),
		collectionErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sre_collector_collection_errors_total",
			Help: "Total collection errors by source.",
		}, []string{"source"}),
		collectionSuccess: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sre_collector_collection_success_total",
			Help: "Total successful collections by source.",
		}, []string{"source"}),
		configReloads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sre_collector_config_reloads_total",
			Help: "Total config reload attempts grouped by result.",
		}, []string{"result"}),
		currentPollInterval: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "sre_collector_poll_interval_seconds",
			Help: "Current adaptive polling interval in seconds.",
		}),
		retryBackoff: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "sre_collector_retry_backoff_seconds",
			Help: "Current retry backoff interval used after repeated transient failures.",
		}),
		failureStreak: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "sre_collector_failure_streak",
			Help: "Current consecutive collect/send failure streak.",
		}),
	}
	registerCollectorMetric(metrics.reportAttempts)
	registerCollectorMetric(metrics.reportFailures)
	registerCollectorMetric(metrics.batchesEnqueued)
	registerCollectorMetric(metrics.batchesSent)
	registerCollectorMetric(metrics.collectionDuration)
	registerCollectorMetric(metrics.collectionErrors)
	registerCollectorMetric(metrics.collectionSuccess)
	registerCollectorMetric(metrics.configReloads)
	registerCollectorMetric(metrics.currentPollInterval)
	registerCollectorMetric(metrics.retryBackoff)
	registerCollectorMetric(metrics.failureStreak)
	return metrics
}

func registerCollectorMetric(metric prometheus.Collector) {
	if err := prometheus.DefaultRegisterer.Register(metric); err != nil {
		var alreadyRegistered prometheus.AlreadyRegisteredError
		if errors.As(err, &alreadyRegistered) {
			return
		}
		panic(fmt.Sprintf("register collector metric: %v", err))
	}
}
