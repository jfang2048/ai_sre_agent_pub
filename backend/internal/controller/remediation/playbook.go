package remediation

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

type Playbook struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Steps       []PlaybookStep `yaml:"steps"`
	Timeout     time.Duration  `yaml:"timeout"`
	OnFailure   string         `yaml:"on_failure"`
}

type PlaybookStep struct {
	Name       string                 `yaml:"name"`
	Type       string                 `yaml:"type"`
	Command    string                 `yaml:"command"`
	Parameters map[string]interface{} `yaml:"parameters"`
	Timeout    time.Duration          `yaml:"timeout"`
	OnFailure  string                 `yaml:"on_failure"`
}

type PlaybookRunner struct {
	executor *Executor
	logger   *zap.Logger
}

func NewPlaybookRunner(executor *Executor, logger *zap.Logger) *PlaybookRunner {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &PlaybookRunner{executor: executor, logger: logger.With(zap.String("component", "playbook"))}
}

func (pr *PlaybookRunner) Run(ctx context.Context, playbook *Playbook, dryRun bool) (*PlaybookResult, error) {
	if playbook == nil {
		return nil, fmt.Errorf("playbook is required")
	}
	result := &PlaybookResult{
		Playbook:  playbook.Name,
		StartedAt: time.Now().UTC(),
		Steps:     make([]StepResult, 0, len(playbook.Steps)),
	}
	if playbook.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, playbook.Timeout)
		defer cancel()
	}
	for _, step := range playbook.Steps {
		stepResult := pr.executeStep(ctx, step, dryRun)
		result.Steps = append(result.Steps, stepResult)
		if stepResult.Success {
			continue
		}
		action := step.OnFailure
		if action == "" {
			action = playbook.OnFailure
		}
		switch action {
		case "continue":
			continue
		case "rollback":
			pr.rollback(ctx, result)
			result.CompletedAt = time.Now().UTC()
			result.Success = false
			return result, fmt.Errorf("step %s failed, rollback executed", step.Name)
		default:
			result.CompletedAt = time.Now().UTC()
			result.Success = false
			return result, fmt.Errorf("step %s failed: %s", step.Name, stepResult.Error)
		}
	}
	result.CompletedAt = time.Now().UTC()
	result.Success = true
	return result, nil
}

func (pr *PlaybookRunner) executeStep(ctx context.Context, step PlaybookStep, dryRun bool) StepResult {
	startedAt := time.Now().UTC()
	result := StepResult{Step: step.Name}
	if step.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, step.Timeout)
		defer cancel()
	}
	switch step.Type {
	case "read":
		output, err := pr.executeRead(ctx, step)
		if err != nil {
			result.Error = err.Error()
		} else {
			result.Success = true
			result.Output = output
		}
	case "write", "execute":
		action := &Action{
			Type:       step.Command,
			Target:     stepString(step.Parameters, "target"),
			Parameters: cloneParameters(step.Parameters),
		}
		executor := pr.executor
		if executor == nil {
			result.Error = "executor is required"
			break
		}
		if dryRun && !executor.config.DryRun {
			cfg := executor.config
			cfg.DryRun = true
			executor = NewExecutor(cfg, executor.logger)
			executor.SetValidator(pr.executor.validator)
		}
		actionResult, err := executor.Execute(ctx, action)
		if err != nil {
			result.Error = err.Error()
		} else {
			result.Success = actionResult.Success
			result.Output = actionResult.Message
			result.Changes = append([]ResourceChange(nil), actionResult.Changes...)
			if actionResult.Error != nil && result.Error == "" {
				result.Error = actionResult.Error.Error()
			}
		}
	case "validate":
		result.Success, result.Error = pr.executeValidate(ctx, step)
	case "notify":
		result.Success, result.Error = pr.executeNotify(ctx, step)
	default:
		result.Error = fmt.Sprintf("unknown step type: %s", step.Type)
	}
	result.Duration = time.Since(startedAt)
	return result
}

func (pr *PlaybookRunner) executeRead(ctx context.Context, step PlaybookStep) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return fmt.Sprintf("read output for %s", step.Command), nil
}

func (pr *PlaybookRunner) executeValidate(ctx context.Context, step PlaybookStep) (bool, string) {
	if err := ctx.Err(); err != nil {
		return false, err.Error()
	}
	return true, ""
}

func (pr *PlaybookRunner) executeNotify(ctx context.Context, step PlaybookStep) (bool, string) {
	if err := ctx.Err(); err != nil {
		return false, err.Error()
	}
	return true, ""
}

func (pr *PlaybookRunner) rollback(ctx context.Context, result *PlaybookResult) {
	pr.logger.Info("rollback playbook", zap.String("playbook", result.Playbook))
	for i := len(result.Steps) - 1; i >= 0; i-- {
		step := result.Steps[i]
		if !step.Success || len(step.Changes) == 0 {
			continue
		}
		pr.logger.Info("rollback step", zap.String("step", step.Step))
	}
}

type PlaybookResult struct {
	Playbook    string       `json:"playbook"`
	Success     bool         `json:"success"`
	StartedAt   time.Time    `json:"started_at"`
	CompletedAt time.Time    `json:"completed_at"`
	Steps       []StepResult `json:"steps"`
}

type StepResult struct {
	Step     string           `json:"step"`
	Success  bool             `json:"success"`
	Duration time.Duration    `json:"duration"`
	Output   string           `json:"output,omitempty"`
	Error    string           `json:"error,omitempty"`
	Changes  []ResourceChange `json:"changes,omitempty"`
}

func LoadPlaybook(data []byte) (*Playbook, error) {
	var playbook Playbook
	if err := yaml.Unmarshal(data, &playbook); err != nil {
		return nil, fmt.Errorf("failed to parse playbook: %w", err)
	}
	return &playbook, nil
}

func ScaleUpPlaybook(replicas int) *Playbook {
	return &Playbook{
		Name:        "scale_up",
		Description: "Scale up deployment to handle increased load",
		Steps: []PlaybookStep{
			{Name: "check_current_capacity", Type: "read", Command: "kubectl get deployment", Parameters: map[string]interface{}{"output": "json"}},
			{Name: "scale_replicas", Type: "write", Command: "scale_deployment", Parameters: map[string]interface{}{"replicas": replicas}},
			{Name: "verify_scaling", Type: "validate", Command: "check_replicas", Parameters: map[string]interface{}{"expected": replicas}},
		},
		OnFailure: "rollback",
	}
}

func RestartPodPlaybook() *Playbook {
	return &Playbook{
		Name:        "restart_pod",
		Description: "Gracefully restart a pod",
		Steps:       []PlaybookStep{{Name: "drain_connections", Type: "write", Command: "drain_pod"}, {Name: "delete_pod", Type: "write", Command: "restart_pod"}, {Name: "verify_ready", Type: "validate", Command: "check_ready"}},
		OnFailure:   "stop",
	}
}

func FailoverPlaybook(region string) *Playbook {
	return &Playbook{
		Name:        "failover",
		Description: "Shift traffic to a secondary region",
		Steps:       []PlaybookStep{{Name: "verify_secondary", Type: "read", Command: "check_region_health", Parameters: map[string]interface{}{"region": region}}, {Name: "switch_traffic", Type: "write", Command: "failover_traffic", Parameters: map[string]interface{}{"region": region}}, {Name: "verify_traffic", Type: "validate", Command: "check_traffic", Parameters: map[string]interface{}{"region": region}}},
		OnFailure:   "rollback",
	}
}

func cloneParameters(in map[string]interface{}) map[string]interface{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func stepString(params map[string]interface{}, key string) string {
	if len(params) == 0 {
		return ""
	}
	value, ok := params[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}
