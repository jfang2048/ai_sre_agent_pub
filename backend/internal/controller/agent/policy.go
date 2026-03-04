package agent

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// PlaybookRule describes a declarative remediation or action rule.
type PlaybookRule struct {
	ID         string              `yaml:"id" json:"id"`
	Summary    string              `yaml:"summary" json:"summary"`
	Severity   string              `yaml:"severity" json:"severity"`
	Conditions []PlaybookCondition `yaml:"conditions" json:"conditions"`
	Actions    []PlaybookAction    `yaml:"actions" json:"actions"`
	Labels     map[string]string   `yaml:"labels,omitempty" json:"labels,omitempty"`
}

// PlaybookCondition is a single metric-based predicate.
type PlaybookCondition struct {
	Metric    string  `yaml:"metric" json:"metric"`
	Op        string  `yaml:"op" json:"op"` // >, >=, <, <=, ==, !=
	Threshold float64 `yaml:"threshold" json:"threshold"`
}

// PlaybookAction is a declarative action template.
type PlaybookAction struct {
	Type        string `yaml:"type" json:"type"`
	Target      string `yaml:"target" json:"target"`
	Priority    string `yaml:"priority" json:"priority"`
	Safe        bool   `yaml:"safe" json:"safe"`
	Description string `yaml:"description" json:"description"`
	RunbookURL  string `yaml:"runbook_url,omitempty" json:"runbook_url,omitempty"`
}

// SignalRules holds configurable thresholds for built-in signal analysis.
type SignalRules struct {
	CPUHighPercent        float64 `yaml:"cpu_high_percent" json:"cpu_high_percent"`
	MemoryPressureRatio   float64 `yaml:"memory_pressure_ratio" json:"memory_pressure_ratio"`
	SwapActivityMin       float64 `yaml:"swap_activity_min" json:"swap_activity_min"`
	DiskIOHigh            float64 `yaml:"disk_io_high" json:"disk_io_high"`
	NetSaturationBytesSec float64 `yaml:"net_saturation_bytes_sec" json:"net_saturation_bytes_sec"`
	GPUSMHighPercent      float64 `yaml:"gpu_sm_high_percent" json:"gpu_sm_high_percent"`
}

func evaluateCondition(cond PlaybookCondition, metrics map[string]float64) bool {
	val, ok := metrics[cond.Metric]
	if !ok {
		return false
	}
	switch cond.Op {
	case ">":
		return val > cond.Threshold
	case ">=":
		return val >= cond.Threshold
	case "<":
		return val < cond.Threshold
	case "<=":
		return val <= cond.Threshold
	case "==":
		return val == cond.Threshold
	case "!=":
		return val != cond.Threshold
	default:
		return false
	}
}

func evaluatePlaybook(rule PlaybookRule, metrics map[string]float64) bool {
	if len(rule.Conditions) == 0 {
		return false
	}
	for _, cond := range rule.Conditions {
		if !evaluateCondition(cond, metrics) {
			return false
		}
	}
	return true
}

// applyPolicies turns satisfied playbook rules into actionable decisions.
func applyPolicies(nodeName string, metrics map[string]float64, rules []PlaybookRule, now time.Time) []ActionDecision {
	out := make([]ActionDecision, 0, len(rules))
	for _, rule := range rules {
		if !evaluatePlaybook(rule, metrics) {
			continue
		}
		for i, act := range rule.Actions {
			id := fmt.Sprintf("action-%s-%d-%d", sanitizeID(rule.ID), now.UnixNano(), i)
			out = append(out, ActionDecision{
				NodeName: nodeName,
				ID:       id,
				Type:     act.Type,
				Reason:   rule.Summary,
				Priority: firstNonEmpty(act.Priority, rule.Severity, "P2"),
				Safe:     act.Safe,
				Status:   ActionStatusProposed,
				Note:     act.Description,
				Created:  now,
				Updated:  now,
			})
		}
	}
	return out
}

func sanitizeID(in string) string {
	if in == "" {
		return "unnamed"
	}
	out := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, in)
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func loadPlaybooks(path string) ([]PlaybookRule, error) {
	if path == "" {
		return nil, fmt.Errorf("empty policy file path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var wrapped struct {
		Version   string         `yaml:"version"`
		Playbooks []PlaybookRule `yaml:"playbooks"`
	}
	if err := yaml.Unmarshal(data, &wrapped); err == nil && len(wrapped.Playbooks) > 0 {
		return wrapped.Playbooks, nil
	}

	var rules []PlaybookRule
	if err := yaml.Unmarshal(data, &rules); err != nil {
		return nil, err
	}
	return rules, nil
}
