// Package classifier provides ML-powered issue classification.
//
// The classifier uses a layered approach:
// 1. Rule-based classification (fast, deterministic)
// 2. Statistical pattern matching
// 3. ML model inference (via Python service or API)
//
// This design allows graceful degradation if the ML service is unavailable.
package classifier

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ai/queue"
	"go.uber.org/zap"
)

// IssueCategory represents categories of issues
type IssueCategory string

const (
	CategoryUnknown            IssueCategory = "unknown"
	CategoryCPUSaturation      IssueCategory = "cpu_saturation"
	CategoryMemoryPressure     IssueCategory = "memory_pressure"
	CategoryDiskIOBottleneck   IssueCategory = "disk_io_bottleneck"
	CategoryNetworkSaturation  IssueCategory = "network_saturation"
	CategoryApplicationError   IssueCategory = "application_error"
	CategoryResourceLeak       IssueCategory = "resource_leak"
	CategoryCascadingFailure   IssueCategory = "cascading_failure"
	CategoryCapacityIssue      IssueCategory = "capacity_issue"
	CategoryConfigurationError IssueCategory = "configuration_error"
	CategoryExternalDependency IssueCategory = "external_dependency"
)

// Severity levels
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityError    Severity = "error"
	SeverityCritical Severity = "critical"
)

// Classification represents a classified issue
type Classification struct {
	Category       IssueCategory `json:"category"`
	Severity       Severity      `json:"severity"`
	Confidence     float64       `json:"confidence"`
	Description    string        `json:"description"`
	Factors        []string      `json:"factors"`
	RelatedMetrics []string      `json:"related_metrics"`
	Method         string        `json:"method"` // "rules", "ml", "hybrid"
	Timestamp      time.Time     `json:"timestamp"`
}

// Classifier provides issue classification
type Classifier struct {
	logger     *zap.Logger
	mlClient   MLClient
	rules      []ClassificationRule
	thresholds map[string]ThresholdConfig
}

// MLClient interface for ML model inference
type MLClient interface {
	// Classify uses ML model to classify data
	Classify(ctx context.Context, data *queue.DataPoint) ([]Classification, error)

	// IsAvailable checks if the ML service is available
	IsAvailable(ctx context.Context) bool
}

// Config holds classifier configuration
type Config struct {
	EnableML        bool                       `yaml:"enable_ml"`
	MLServiceAddr   string                     `yaml:"ml_service_addr"`
	FallbackToRules bool                       `yaml:"fallback_to_rules"`
	Thresholds      map[string]ThresholdConfig `yaml:"thresholds"`
}

// ThresholdConfig defines thresholds for metrics
type ThresholdConfig struct {
	Warning  float64 `yaml:"warning"`
	Error    float64 `yaml:"error"`
	Critical float64 `yaml:"critical"`
}

// DefaultConfig returns default classifier configuration
func DefaultConfig() Config {
	return Config{
		EnableML:        true,
		MLServiceAddr:   "localhost:50051",
		FallbackToRules: true,
		Thresholds:      defaultThresholds(),
	}
}

func defaultThresholds() map[string]ThresholdConfig {
	return map[string]ThresholdConfig{
		"system.cpu.usage":              {Warning: 70, Error: 85, Critical: 95},
		"system.memory.usage":           {Warning: 75, Error: 90, Critical: 97},
		"system.disk.usage":             {Warning: 80, Error: 90, Critical: 95},
		"system.disk.io.utilization":    {Warning: 70, Error: 85, Critical: 95},
		"system.load.1m":                {Warning: 4, Error: 8, Critical: 16},
		"system.network.rx.utilization": {Warning: 70, Error: 85, Critical: 95},
		"system.network.tx.utilization": {Warning: 70, Error: 85, Critical: 95},
	}
}

// New creates a new classifier
func New(cfg Config, logger *zap.Logger) *Classifier {
	if logger == nil {
		logger, _ = zap.NewDevelopment()
	}

	c := &Classifier{
		logger:     logger.With(zap.String("component", "classifier")),
		thresholds: cfg.Thresholds,
		rules:      buildDefaultRules(),
	}

	if c.thresholds == nil {
		c.thresholds = defaultThresholds()
	}

	return c
}

// SetMLClient sets the ML client for model inference
func (c *Classifier) SetMLClient(client MLClient) {
	c.mlClient = client
}

// Classify classifies a data point
func (c *Classifier) Classify(ctx context.Context, data *queue.DataPoint) ([]Classification, error) {
	var classifications []Classification

	// Step 1: Run rule-based classification (always runs)
	ruleResults := c.classifyByRules(data)
	classifications = append(classifications, ruleResults...)

	// Step 2: Run ML classification if available
	if c.mlClient != nil && c.mlClient.IsAvailable(ctx) {
		mlResults, err := c.mlClient.Classify(ctx, data)
		if err != nil {
			c.logger.Warn("ML classification failed, using rules only",
				zap.Error(err))
		} else {
			// Merge ML results (prefer higher confidence)
			classifications = mergeClassifications(classifications, mlResults)
		}
	}

	// Step 3: Check for patterns in logs
	logResults := c.classifyByLogs(data.Logs)
	classifications = append(classifications, logResults...)

	// Deduplicate and sort by severity/confidence
	classifications = deduplicateAndSort(classifications)

	return classifications, nil
}

// classifyByRules applies rule-based classification
func (c *Classifier) classifyByRules(data *queue.DataPoint) []Classification {
	var results []Classification
	metrics := metricsToMap(data.Metrics)

	for _, rule := range c.rules {
		if classification := rule.Evaluate(metrics); classification != nil {
			classification.Method = "rules"
			classification.Timestamp = time.Now()
			results = append(results, *classification)
		}
	}

	return results
}

// classifyByLogs classifies based on log patterns
func (c *Classifier) classifyByLogs(logs []queue.LogEntry) []Classification {
	var results []Classification

	for _, log := range logs {
		msg := strings.ToLower(log.Message)

		// OOMKilled detection
		if strings.Contains(msg, "oomkilled") || strings.Contains(msg, "out of memory") {
			results = append(results, Classification{
				Category:    CategoryMemoryPressure,
				Severity:    SeverityCritical,
				Confidence:  0.95,
				Description: "Process was killed due to out-of-memory condition",
				Factors:     []string{"OOMKilled event detected in logs"},
				Method:      "rules",
				Timestamp:   time.Now(),
			})
		}

		// Disk full detection
		if strings.Contains(msg, "no space left") || strings.Contains(msg, "disk full") {
			results = append(results, Classification{
				Category:    CategoryDiskIOBottleneck,
				Severity:    SeverityCritical,
				Confidence:  0.95,
				Description: "Disk space exhausted",
				Factors:     []string{"Disk full error detected in logs"},
				Method:      "rules",
				Timestamp:   time.Now(),
			})
		}

		// Connection refused/timeout
		if strings.Contains(msg, "connection refused") || strings.Contains(msg, "connection timed out") {
			results = append(results, Classification{
				Category:    CategoryExternalDependency,
				Severity:    SeverityError,
				Confidence:  0.85,
				Description: "External service connectivity issue",
				Factors:     []string{"Connection error detected in logs"},
				Method:      "rules",
				Timestamp:   time.Now(),
			})
		}

		// Crash/segfault detection
		if strings.Contains(msg, "segmentation fault") || strings.Contains(msg, "core dumped") {
			results = append(results, Classification{
				Category:    CategoryApplicationError,
				Severity:    SeverityCritical,
				Confidence:  0.95,
				Description: "Application crash detected",
				Factors:     []string{"Segmentation fault or core dump in logs"},
				Method:      "rules",
				Timestamp:   time.Now(),
			})
		}
	}

	return results
}

// ============================================================================
// Classification Rules
// ============================================================================

// ClassificationRule defines a classification rule
type ClassificationRule struct {
	Name        string
	Description string
	Evaluate    func(metrics map[string]float64) *Classification
}

func buildDefaultRules() []ClassificationRule {
	return []ClassificationRule{
		{
			Name:        "high_cpu",
			Description: "Detect CPU saturation",
			Evaluate: func(m map[string]float64) *Classification {
				cpu := m["system.cpu.usage"]
				load := m["system.load.1m"]

				if cpu > 90 {
					return &Classification{
						Category:       CategoryCPUSaturation,
						Severity:       SeverityCritical,
						Confidence:     0.90,
						Description:    fmt.Sprintf("Critical CPU saturation at %.1f%%", cpu),
						Factors:        []string{"CPU usage exceeds 90%"},
						RelatedMetrics: []string{"system.cpu.usage", "system.load.1m"},
					}
				} else if cpu > 80 && load > 4 {
					return &Classification{
						Category:       CategoryCPUSaturation,
						Severity:       SeverityWarning,
						Confidence:     0.75,
						Description:    fmt.Sprintf("High CPU at %.1f%% with elevated load %.2f", cpu, load),
						Factors:        []string{"High CPU combined with high load average"},
						RelatedMetrics: []string{"system.cpu.usage", "system.load.1m"},
					}
				}
				return nil
			},
		},
		{
			Name:        "memory_pressure",
			Description: "Detect memory pressure",
			Evaluate: func(m map[string]float64) *Classification {
				mem := m["system.memory.usage"]
				swap := m["system.swap.usage"]

				if mem > 95 {
					return &Classification{
						Category:       CategoryMemoryPressure,
						Severity:       SeverityCritical,
						Confidence:     0.92,
						Description:    fmt.Sprintf("Critical memory pressure at %.1f%%", mem),
						Factors:        []string{"Memory usage exceeds 95%"},
						RelatedMetrics: []string{"system.memory.usage", "system.memory.available"},
					}
				} else if mem > 85 && swap > 50 {
					return &Classification{
						Category:       CategoryMemoryPressure,
						Severity:       SeverityError,
						Confidence:     0.80,
						Description:    fmt.Sprintf("Memory pressure with swap usage: mem=%.1f%%, swap=%.1f%%", mem, swap),
						Factors:        []string{"High memory with significant swap usage"},
						RelatedMetrics: []string{"system.memory.usage", "system.swap.usage"},
					}
				} else if mem > 80 {
					return &Classification{
						Category:       CategoryMemoryPressure,
						Severity:       SeverityWarning,
						Confidence:     0.70,
						Description:    fmt.Sprintf("Memory usage elevated at %.1f%%", mem),
						Factors:        []string{"Memory usage above warning threshold"},
						RelatedMetrics: []string{"system.memory.usage"},
					}
				}
				return nil
			},
		},
		{
			Name:        "disk_io_bottleneck",
			Description: "Detect disk I/O bottleneck",
			Evaluate: func(m map[string]float64) *Classification {
				ioUtil := m["system.disk.io.utilization"]
				diskUsage := m["system.disk.usage"]
				ioWait := m["system.cpu.iowait"]

				if ioUtil > 90 || ioWait > 30 {
					return &Classification{
						Category:       CategoryDiskIOBottleneck,
						Severity:       SeverityCritical,
						Confidence:     0.85,
						Description:    fmt.Sprintf("Critical disk I/O bottleneck: util=%.1f%%, iowait=%.1f%%", ioUtil, ioWait),
						Factors:        []string{"Disk utilization or I/O wait extremely high"},
						RelatedMetrics: []string{"system.disk.io.utilization", "system.cpu.iowait"},
					}
				} else if diskUsage > 90 {
					return &Classification{
						Category:       CategoryCapacityIssue,
						Severity:       SeverityCritical,
						Confidence:     0.90,
						Description:    fmt.Sprintf("Disk capacity critical at %.1f%%", diskUsage),
						Factors:        []string{"Disk usage exceeds 90%"},
						RelatedMetrics: []string{"system.disk.usage"},
					}
				}
				return nil
			},
		},
		{
			Name:        "network_saturation",
			Description: "Detect network saturation",
			Evaluate: func(m map[string]float64) *Classification {
				rxUtil := m["system.network.rx.utilization"]
				txUtil := m["system.network.tx.utilization"]
				errors := m["system.net.rx_errors"] + m["system.net.tx_errors"]

				if rxUtil > 90 || txUtil > 90 {
					return &Classification{
						Category:       CategoryNetworkSaturation,
						Severity:       SeverityCritical,
						Confidence:     0.85,
						Description:    fmt.Sprintf("Network saturation: rx=%.1f%%, tx=%.1f%%", rxUtil, txUtil),
						Factors:        []string{"Network utilization extremely high"},
						RelatedMetrics: []string{"system.network.rx.utilization", "system.network.tx.utilization"},
					}
				} else if errors > 100 {
					return &Classification{
						Category:       CategoryNetworkSaturation,
						Severity:       SeverityWarning,
						Confidence:     0.70,
						Description:    fmt.Sprintf("Network errors detected: %.0f errors", errors),
						Factors:        []string{"High number of network errors"},
						RelatedMetrics: []string{"system.net.rx_errors", "system.net.tx_errors"},
					}
				}
				return nil
			},
		},
		{
			Name:        "resource_leak",
			Description: "Detect potential resource leaks",
			Evaluate: func(m map[string]float64) *Classification {
				fdUsage := m["system.fd.allocated"]
				fdMax := m["system.fd.maximum"]

				if fdMax > 0 {
					fdPercent := (fdUsage / fdMax) * 100
					if fdPercent > 80 {
						return &Classification{
							Category:       CategoryResourceLeak,
							Severity:       SeverityWarning,
							Confidence:     0.65,
							Description:    fmt.Sprintf("File descriptor usage high: %.1f%% of max", fdPercent),
							Factors:        []string{"High file descriptor usage may indicate leak"},
							RelatedMetrics: []string{"system.fd.allocated", "system.fd.maximum"},
						}
					}
				}
				return nil
			},
		},
	}
}

// ============================================================================
// Helpers
// ============================================================================

func metricsToMap(metrics []queue.MetricData) map[string]float64 {
	m := make(map[string]float64)
	for _, metric := range metrics {
		m[metric.Name] = metric.Value
	}
	return m
}

func mergeClassifications(rules, ml []Classification) []Classification {
	// Use map to deduplicate by category
	byCategory := make(map[IssueCategory]Classification)

	for _, c := range rules {
		byCategory[c.Category] = c
	}

	for _, c := range ml {
		if existing, ok := byCategory[c.Category]; ok {
			// Keep higher confidence, mark as hybrid
			if c.Confidence > existing.Confidence {
				c.Method = "hybrid"
				byCategory[c.Category] = c
			}
		} else {
			byCategory[c.Category] = c
		}
	}

	var result []Classification
	for _, c := range byCategory {
		result = append(result, c)
	}
	return result
}

func deduplicateAndSort(classifications []Classification) []Classification {
	// Simple sort by severity (critical > error > warning > info)
	severityOrder := map[Severity]int{
		SeverityCritical: 0,
		SeverityError:    1,
		SeverityWarning:  2,
		SeverityInfo:     3,
	}

	// Sort (simple bubble sort for small slices)
	for i := 0; i < len(classifications); i++ {
		for j := i + 1; j < len(classifications); j++ {
			if severityOrder[classifications[j].Severity] < severityOrder[classifications[i].Severity] {
				classifications[i], classifications[j] = classifications[j], classifications[i]
			}
		}
	}

	return classifications
}
