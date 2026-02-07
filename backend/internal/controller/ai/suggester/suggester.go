// Package suggester provides remediation suggestions for classified issues.
//
// The suggester uses:
// 1. Rule-based logic for known issues (fast, reliable)
// 2. ML predictions for complex scenarios (via Python service)
// 3. Human-readable explanations for operators
package suggester

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ai/classifier"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ai/queue"
	"go.uber.org/zap"
)

// RemediationType represents types of remediation actions
type RemediationType string

const (
	RemediationRestart       RemediationType = "restart"
	RemediationScale         RemediationType = "scale"
	RemediationRollback      RemediationType = "rollback"
	RemediationConfiguration RemediationType = "configuration"
	RemediationResourceLimit RemediationType = "resource_limit"
	RemediationManual        RemediationType = "manual"
	RemediationKillProcess   RemediationType = "kill_process"
	RemediationCleanup       RemediationType = "cleanup"
)

// Suggestion represents a remediation suggestion
type Suggestion struct {
	Type            RemediationType `json:"type"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	Confidence      float64         `json:"confidence"`
	RiskLevel       string          `json:"risk_level"` // low, medium, high
	Impact          string          `json:"impact"`
	Steps           []ActionStep    `json:"steps"`
	ExpectedOutcome string          `json:"expected_outcome"`
	EstimatedTime   time.Duration   `json:"estimated_time"`
	Reasoning       string          `json:"reasoning"`
	Automated       bool            `json:"automated"` // Can be auto-executed
}

// ActionStep represents a step in remediation
type ActionStep struct {
	Order            int               `json:"order"`
	Action           string            `json:"action"`
	Target           string            `json:"target"`
	Parameters       map[string]string `json:"parameters,omitempty"`
	RequiresApproval bool              `json:"requires_approval"`
	Reversible       bool              `json:"reversible"`
	Command          string            `json:"command,omitempty"` // CLI command if applicable
}

// Explanation provides human-readable issue explanation
type Explanation struct {
	Summary      string   `json:"summary"`       // One-line summary
	WhatHappened string   `json:"what_happened"` // Detailed what
	WhyHappened  string   `json:"why_happened"`  // Root cause
	Impact       string   `json:"impact"`        // Business impact
	NextSteps    string   `json:"next_steps"`    // What to do
	RelatedDocs  []string `json:"related_docs"`  // Documentation links
}

// Suggester provides remediation suggestions
type Suggester struct {
	logger   *zap.Logger
	mlClient MLClient
	rulesDB  map[classifier.IssueCategory][]SuggestionRule
}

// MLClient interface for ML-based suggestions
type MLClient interface {
	Suggest(ctx context.Context, classification classifier.Classification, data *queue.DataPoint) ([]Suggestion, error)
	Explain(ctx context.Context, classification classifier.Classification, data *queue.DataPoint) (*Explanation, error)
	IsAvailable(ctx context.Context) bool
}

// SuggestionRule defines a rule for generating suggestions
type SuggestionRule struct {
	Name                string
	Condition           func(c classifier.Classification, data *queue.DataPoint) bool
	GenerateSuggestion  func(c classifier.Classification, data *queue.DataPoint) *Suggestion
	GenerateExplanation func(c classifier.Classification, data *queue.DataPoint) *Explanation
}

// New creates a new suggester
func New(logger *zap.Logger) *Suggester {
	if logger == nil {
		logger, _ = zap.NewDevelopment()
	}

	s := &Suggester{
		logger:  logger.With(zap.String("component", "suggester")),
		rulesDB: buildSuggestionRules(),
	}

	return s
}

// SetMLClient sets the ML client for advanced suggestions
func (s *Suggester) SetMLClient(client MLClient) {
	s.mlClient = client
}

// Suggest generates suggestions for a classification
func (s *Suggester) Suggest(ctx context.Context, c classifier.Classification, data *queue.DataPoint) ([]Suggestion, error) {
	var suggestions []Suggestion

	// Step 1: Apply rule-based suggestions
	if rules, ok := s.rulesDB[c.Category]; ok {
		for _, rule := range rules {
			if rule.Condition(c, data) {
				if suggestion := rule.GenerateSuggestion(c, data); suggestion != nil {
					suggestions = append(suggestions, *suggestion)
				}
			}
		}
	}

	// Step 2: Get ML suggestions if available
	if s.mlClient != nil && s.mlClient.IsAvailable(ctx) {
		mlSuggestions, err := s.mlClient.Suggest(ctx, c, data)
		if err != nil {
			s.logger.Warn("ML suggestions failed", zap.Error(err))
		} else {
			suggestions = append(suggestions, mlSuggestions...)
		}
	}

	// Sort by confidence
	sortByConfidence(suggestions)

	return suggestions, nil
}

// Explain generates a human-readable explanation
func (s *Suggester) Explain(ctx context.Context, c classifier.Classification, data *queue.DataPoint) (*Explanation, error) {
	// Try ML explanation first
	if s.mlClient != nil && s.mlClient.IsAvailable(ctx) {
		explanation, err := s.mlClient.Explain(ctx, c, data)
		if err == nil {
			return explanation, nil
		}
		s.logger.Warn("ML explanation failed, using rules", zap.Error(err))
	}

	// Fall back to rule-based explanation
	return s.explainByRules(c, data), nil
}

// explainByRules generates explanation using rules
func (s *Suggester) explainByRules(c classifier.Classification, data *queue.DataPoint) *Explanation {
	metrics := metricsToMap(data.Metrics)

	switch c.Category {
	case classifier.CategoryMemoryPressure:
		memUsage := metrics["system.memory.usage"]
		return &Explanation{
			Summary:      fmt.Sprintf("Memory at %.0f%% - system under memory pressure", memUsage),
			WhatHappened: fmt.Sprintf("Memory usage has reached %.1f%%, which is above the critical threshold.", memUsage),
			WhyHappened:  "Possible causes: memory leak in application, insufficient memory allocation, or unexpected workload increase.",
			Impact:       "High memory usage can cause OOM kills, application crashes, and degraded performance.",
			NextSteps:    "Identify top memory consumers with 'top' or 'htop'. Consider killing idle processes or scaling pods.",
		}

	case classifier.CategoryCPUSaturation:
		cpuUsage := metrics["system.cpu.usage"]
		load := metrics["system.load.1m"]
		return &Explanation{
			Summary:      fmt.Sprintf("CPU at %.0f%% with load %.1f - compute saturation", cpuUsage, load),
			WhatHappened: fmt.Sprintf("CPU usage is %.1f%% with system load at %.2f.", cpuUsage, load),
			WhyHappened:  "Possible causes: runaway process, insufficient CPU capacity, or inefficient code.",
			Impact:       "High CPU causes slow response times, request timeouts, and degraded user experience.",
			NextSteps:    "Check process CPU usage with 'top -o %CPU'. Look for runaway processes or consider scaling.",
		}

	case classifier.CategoryDiskIOBottleneck:
		diskUtil := metrics["system.disk.io.utilization"]
		iowait := metrics["system.cpu.iowait"]
		return &Explanation{
			Summary:      fmt.Sprintf("Disk I/O bottleneck - utilization %.0f%%, iowait %.0f%%", diskUtil, iowait),
			WhatHappened: fmt.Sprintf("Disk I/O is saturated at %.1f%% utilization with %.1f%% CPU iowait.", diskUtil, iowait),
			WhyHappened:  "Possible causes: heavy read/write workload, slow storage, or insufficient I/O capacity.",
			Impact:       "I/O bottleneck causes slow application response, database timeouts, and log write delays.",
			NextSteps:    "Use 'iostat -x 1' to identify busy disks. Check for runaway I/O processes with 'iotop'.",
		}

	case classifier.CategoryNetworkSaturation:
		return &Explanation{
			Summary:      "Network saturated - bandwidth limit reached",
			WhatHappened: "Network interface utilization has exceeded normal levels.",
			WhyHappened:  "Possible causes: traffic spike, DDoS attack, or application generating excessive traffic.",
			Impact:       "Network saturation causes packet loss, connection timeouts, and degraded service quality.",
			NextSteps:    "Check network traffic with 'iftop' or 'nethogs'. Identify top bandwidth consumers.",
		}

	case classifier.CategoryApplicationError:
		return &Explanation{
			Summary:      "Application error detected - possible crash",
			WhatHappened: "Application has encountered a critical error or crash.",
			WhyHappened:  "Possible causes: bug in code, resource exhaustion, or dependency failure.",
			Impact:       "Application errors cause service disruption and potential data loss.",
			NextSteps:    "Check application logs for stack traces. Review recent deployments for regressions.",
		}

	default:
		return &Explanation{
			Summary:      fmt.Sprintf("Issue detected: %s", c.Description),
			WhatHappened: c.Description,
			WhyHappened:  strings.Join(c.Factors, "; "),
			Impact:       "Impact depends on the specific issue severity.",
			NextSteps:    "Investigate the related metrics and logs for more details.",
		}
	}
}

// ============================================================================
// Suggestion Rules
// ============================================================================

func buildSuggestionRules() map[classifier.IssueCategory][]SuggestionRule {
	rules := make(map[classifier.IssueCategory][]SuggestionRule)

	// Memory pressure rules
	rules[classifier.CategoryMemoryPressure] = []SuggestionRule{
		{
			Name: "oom_killed_recovery",
			Condition: func(c classifier.Classification, data *queue.DataPoint) bool {
				for _, log := range data.Logs {
					if strings.Contains(strings.ToLower(log.Message), "oomkilled") {
						return true
					}
				}
				return false
			},
			GenerateSuggestion: func(c classifier.Classification, data *queue.DataPoint) *Suggestion {
				return &Suggestion{
					Type:        RemediationResourceLimit,
					Title:       "Increase Memory Limits",
					Description: "Process was OOMKilled. Increase memory limits to prevent recurrence.",
					Confidence:  0.90,
					RiskLevel:   "low",
					Impact:      "Prevents OOM kills; may increase resource costs",
					Steps: []ActionStep{
						{Order: 1, Action: "identify_process", Target: "oomkilled_process", Reversible: true},
						{Order: 2, Action: "increase_memory_limit", Target: "container", Parameters: map[string]string{"factor": "1.5"}, RequiresApproval: true, Reversible: true},
						{Order: 3, Action: "restart", Target: "pod", RequiresApproval: true, Reversible: true},
					},
					ExpectedOutcome: "Process runs with increased memory allocation",
					EstimatedTime:   5 * time.Minute,
					Reasoning:       "OOMKilled indicates memory limit was exceeded. Increasing limit prevents termination.",
					Automated:       false,
				}
			},
		},
		{
			Name: "kill_idle_processes",
			Condition: func(c classifier.Classification, data *queue.DataPoint) bool {
				metrics := metricsToMap(data.Metrics)
				return metrics["system.memory.usage"] > 90
			},
			GenerateSuggestion: func(c classifier.Classification, data *queue.DataPoint) *Suggestion {
				return &Suggestion{
					Type:        RemediationKillProcess,
					Title:       "Kill Idle or Low-Priority Processes",
					Description: "Memory critical. Consider terminating non-essential processes.",
					Confidence:  0.75,
					RiskLevel:   "medium",
					Impact:      "Frees memory immediately; may affect background tasks",
					Steps: []ActionStep{
						{Order: 1, Action: "list_processes", Target: "system", Command: "ps aux --sort=-%mem | head -20"},
						{Order: 2, Action: "identify_idle", Target: "processes", RequiresApproval: true},
						{Order: 3, Action: "kill", Target: "selected_processes", RequiresApproval: true, Reversible: false},
					},
					ExpectedOutcome: "Memory usage reduced below critical threshold",
					EstimatedTime:   2 * time.Minute,
					Reasoning:       "Immediate memory relief by terminating non-critical processes.",
					Automated:       false,
				}
			},
		},
	}

	// CPU saturation rules
	rules[classifier.CategoryCPUSaturation] = []SuggestionRule{
		{
			Name: "scale_horizontally",
			Condition: func(c classifier.Classification, data *queue.DataPoint) bool {
				return c.Severity == classifier.SeverityCritical || c.Severity == classifier.SeverityError
			},
			GenerateSuggestion: func(c classifier.Classification, data *queue.DataPoint) *Suggestion {
				return &Suggestion{
					Type:        RemediationScale,
					Title:       "Scale Horizontally",
					Description: "CPU saturation detected. Consider scaling out to distribute load.",
					Confidence:  0.80,
					RiskLevel:   "low",
					Impact:      "Distributes load; increases infrastructure cost",
					Steps: []ActionStep{
						{Order: 1, Action: "verify_autoscaler", Target: "deployment"},
						{Order: 2, Action: "scale_replicas", Target: "deployment", Parameters: map[string]string{"increment": "2"}, RequiresApproval: true, Reversible: true},
						{Order: 3, Action: "monitor", Target: "metrics", Parameters: map[string]string{"duration": "5m"}},
					},
					ExpectedOutcome: "Load distributed across more replicas; CPU per instance reduced",
					EstimatedTime:   5 * time.Minute,
					Reasoning:       "Horizontal scaling spreads load across more instances.",
					Automated:       false,
				}
			},
		},
		{
			Name: "identify_runaway_process",
			Condition: func(c classifier.Classification, data *queue.DataPoint) bool {
				metrics := metricsToMap(data.Metrics)
				return metrics["system.cpu.usage"] > 90
			},
			GenerateSuggestion: func(c classifier.Classification, data *queue.DataPoint) *Suggestion {
				return &Suggestion{
					Type:        RemediationKillProcess,
					Title:       "Identify and Stop Runaway Process",
					Description: "Extreme CPU usage may indicate a runaway process.",
					Confidence:  0.70,
					RiskLevel:   "medium",
					Impact:      "May interrupt legitimate workload if misidentified",
					Steps: []ActionStep{
						{Order: 1, Action: "identify", Target: "top_cpu_process", Command: "top -b -n 1 -o %CPU | head -20"},
						{Order: 2, Action: "analyze", Target: "process", RequiresApproval: false},
						{Order: 3, Action: "kill", Target: "runaway_process", RequiresApproval: true, Reversible: false},
					},
					ExpectedOutcome: "CPU usage returns to normal levels",
					EstimatedTime:   3 * time.Minute,
					Reasoning:       "Runaway processes consume disproportionate CPU; terminating them restores normalcy.",
					Automated:       false,
				}
			},
		},
	}

	// Disk I/O rules
	rules[classifier.CategoryDiskIOBottleneck] = []SuggestionRule{
		{
			Name: "cleanup_disk",
			Condition: func(c classifier.Classification, data *queue.DataPoint) bool {
				metrics := metricsToMap(data.Metrics)
				return metrics["system.disk.usage"] > 85
			},
			GenerateSuggestion: func(c classifier.Classification, data *queue.DataPoint) *Suggestion {
				return &Suggestion{
					Type:        RemediationCleanup,
					Title:       "Clean Up Disk Space",
					Description: "Disk usage is high. Clean up logs, temp files, or old data.",
					Confidence:  0.85,
					RiskLevel:   "low",
					Impact:      "Frees disk space; may remove old logs",
					Steps: []ActionStep{
						{Order: 1, Action: "analyze_disk", Target: "filesystem", Command: "du -sh /* 2>/dev/null | sort -rh | head -10"},
						{Order: 2, Action: "cleanup_logs", Target: "/var/log", Command: "journalctl --vacuum-time=7d", RequiresApproval: true, Reversible: false},
						{Order: 3, Action: "cleanup_temp", Target: "/tmp", RequiresApproval: true, Reversible: false},
					},
					ExpectedOutcome: "Disk usage reduced below critical threshold",
					EstimatedTime:   10 * time.Minute,
					Reasoning:       "Removing old logs and temporary files frees disk space quickly.",
					Automated:       false,
				}
			},
		},
	}

	// Capacity issues
	rules[classifier.CategoryCapacityIssue] = []SuggestionRule{
		{
			Name: "scale_resources",
			Condition: func(c classifier.Classification, data *queue.DataPoint) bool {
				return true // Always suggest for capacity issues
			},
			GenerateSuggestion: func(c classifier.Classification, data *queue.DataPoint) *Suggestion {
				return &Suggestion{
					Type:        RemediationScale,
					Title:       "Increase Resource Capacity",
					Description: "System capacity is insufficient. Scale resources to meet demand.",
					Confidence:  0.80,
					RiskLevel:   "low",
					Impact:      "Increases available capacity; increases cost",
					Steps: []ActionStep{
						{Order: 1, Action: "analyze_usage", Target: "resources"},
						{Order: 2, Action: "plan_scaling", Target: "infrastructure", RequiresApproval: true},
						{Order: 3, Action: "apply_changes", Target: "infrastructure", RequiresApproval: true, Reversible: true},
					},
					ExpectedOutcome: "Capacity meets demand with headroom for growth",
					EstimatedTime:   30 * time.Minute,
					Reasoning:       "Capacity issues require infrastructure scaling.",
					Automated:       false,
				}
			},
		},
	}

	return rules
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

func sortByConfidence(suggestions []Suggestion) {
	// Simple bubble sort for small slices
	for i := 0; i < len(suggestions); i++ {
		for j := i + 1; j < len(suggestions); j++ {
			if suggestions[j].Confidence > suggestions[i].Confidence {
				suggestions[i], suggestions[j] = suggestions[j], suggestions[i]
			}
		}
	}
}
