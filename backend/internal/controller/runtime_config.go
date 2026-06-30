package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/inventory"
)

var runtimeReloadableFields = []string{
	"agent.policy_file",
	"ingest.history_samples_per_node",
	"ingest.node_retention",
	"inventory.enabled",
	"inventory.heartbeat_ttl",
	"inventory.targets_file",
}

// RuntimeConfigReloadReport records what changed during a hot reload attempt.
type RuntimeConfigReloadReport struct {
	Source          string    `json:"source"`
	Applied         []string  `json:"applied,omitempty"`
	RestartRequired []string  `json:"restart_required,omitempty"`
	Warnings        []string  `json:"warnings,omitempty"`
	Reloadable      []string  `json:"reloadable"`
	Timestamp       time.Time `json:"timestamp"`
}

// SetRuntimeConfigReloader registers a process-level config reload hook used by the API and signal watchers.
func (c *Controller) SetRuntimeConfigReloader(fn func(context.Context, string) (RuntimeConfigReloadReport, error)) {
	if c == nil {
		return
	}
	c.configReloadMu.Lock()
	c.configReloader = fn
	c.configReloadMu.Unlock()
}

func (c *Controller) configReloadStatus() RuntimeConfigReloadReport {
	if c == nil {
		return RuntimeConfigReloadReport{Reloadable: append([]string(nil), runtimeReloadableFields...)}
	}
	c.configReloadMu.RLock()
	defer c.configReloadMu.RUnlock()
	report := c.lastConfigReload
	if len(report.Reloadable) == 0 {
		report.Reloadable = append([]string(nil), runtimeReloadableFields...)
	}
	return report
}

func (c *Controller) triggerRuntimeConfigReload(ctx context.Context, source string) (RuntimeConfigReloadReport, error) {
	if c == nil {
		return RuntimeConfigReloadReport{}, fmt.Errorf("controller unavailable")
	}
	c.configReloadMu.RLock()
	reloader := c.configReloader
	c.configReloadMu.RUnlock()
	if reloader == nil {
		return RuntimeConfigReloadReport{}, fmt.Errorf("runtime config reload not configured")
	}
	return reloader(ctx, strings.TrimSpace(source))
}

// ApplyRuntimeConfig applies only the controller settings that are explicitly safe to reload in place.
func (c *Controller) ApplyRuntimeConfig(next Config) (RuntimeConfigReloadReport, error) {
	return c.applyRuntimeConfig(next, "")
}

// ApplyRuntimeConfigWithSource applies runtime-safe config changes and records the trigger source.
func (c *Controller) ApplyRuntimeConfigWithSource(next Config, source string) (RuntimeConfigReloadReport, error) {
	return c.applyRuntimeConfig(next, source)
}

func (c *Controller) applyRuntimeConfig(next Config, source string) (RuntimeConfigReloadReport, error) {
	report := RuntimeConfigReloadReport{
		Source:     strings.TrimSpace(source),
		Applied:    []string{},
		Reloadable: append([]string(nil), runtimeReloadableFields...),
		Timestamp:  time.Now().UTC(),
	}
	if c == nil {
		return report, fmt.Errorf("controller unavailable")
	}

	current := c.config
	report.RestartRequired = append(report.RestartRequired, diffRestartRequiredFields(current, next)...)

	if c.ingestStore != nil && (current.Ingest.NodeRetention != next.Ingest.NodeRetention || current.Ingest.HistorySamplesPerNode != next.Ingest.HistorySamplesPerNode) {
		if err := c.ingestStore.SetRetention(next.Ingest.NodeRetention, next.Ingest.HistorySamplesPerNode); err != nil {
			report.Warnings = append(report.Warnings, err.Error())
			c.storeRuntimeReloadReport(report, err)
			return report, err
		}
		report.Applied = append(report.Applied, "ingest.node_retention", "ingest.history_samples_per_node")
		current.Ingest.NodeRetention = next.Ingest.NodeRetention
		current.Ingest.HistorySamplesPerNode = next.Ingest.HistorySamplesPerNode
	}

	if c.inventoryManager != nil {
		staticTargets := buildRuntimeInventoryTargets(current.Nodes, next.Inventory.StaticTargets)
		c.inventoryManager.Reload(next.Inventory, staticTargets)
		report.Applied = append(report.Applied, "inventory.enabled", "inventory.heartbeat_ttl", "inventory.targets_file")
		current.Inventory = next.Inventory
		current.Inventory.StaticTargets = append([]inventory.StaticProbe(nil), next.Inventory.StaticTargets...)
	} else if inventoryConfigChanged(current.Inventory, next.Inventory) {
		report.RestartRequired = append(report.RestartRequired, "inventory")
	}

	if c.agentService != nil {
		if err := c.agentService.ReloadPlaybooks(next.Agent.PolicyFile); err != nil {
			report.Warnings = append(report.Warnings, err.Error())
			c.storeRuntimeReloadReport(report, err)
			return report, err
		}
		report.Applied = append(report.Applied, "agent.policy_file")
		current.Agent.PolicyFile = next.Agent.PolicyFile
	} else if strings.TrimSpace(next.Agent.PolicyFile) != strings.TrimSpace(current.Agent.PolicyFile) {
		report.RestartRequired = append(report.RestartRequired, "agent.policy_file")
	}

	report.Applied = uniqueSortedStrings(report.Applied)
	report.RestartRequired = uniqueSortedStrings(report.RestartRequired)
	report.Warnings = uniqueSortedStrings(report.Warnings)
	c.config = current
	c.storeRuntimeReloadReport(report, nil)
	return report, nil
}

func (c *Controller) storeRuntimeReloadReport(report RuntimeConfigReloadReport, err error) {
	if c == nil {
		return
	}
	report.Source = firstNonEmpty(strings.TrimSpace(report.Source), "internal")
	report.Applied = uniqueSortedStrings(report.Applied)
	report.RestartRequired = uniqueSortedStrings(report.RestartRequired)
	report.Warnings = uniqueSortedStrings(report.Warnings)
	if len(report.Reloadable) == 0 {
		report.Reloadable = append([]string(nil), runtimeReloadableFields...)
	}

	c.configReloadMu.Lock()
	c.lastConfigReload = report
	c.configReloadMu.Unlock()

	status := "success"
	output := "runtime config reload applied"
	if err != nil {
		status = "failed"
		output = err.Error()
	} else if len(report.RestartRequired) > 0 {
		status = "partial"
		output = "runtime config reload applied with restart-required fields left unchanged"
	}
	evidence := make([]string, 0, len(report.Applied)+len(report.RestartRequired)+len(report.Warnings))
	for _, item := range report.Applied {
		evidence = append(evidence, "applied:"+item)
	}
	for _, item := range report.RestartRequired {
		evidence = append(evidence, "restart_required:"+item)
	}
	for _, item := range report.Warnings {
		evidence = append(evidence, "warning:"+item)
	}
	c.appendInternalControllerAudit(ControllerAuditRecord{
		Actor:      "controller",
		Action:     "config.reload",
		Resource:   "controller.runtime_config",
		Status:     status,
		Input:      map[string]string{"source": report.Source},
		Output:     output,
		Evidence:   evidence,
		OccurredAt: report.Timestamp,
	})
}

func (c *Controller) handleControllerConfigReload(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet, http.MethodPost) {
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, c.configReloadStatus())
		return
	}
	if !c.requireActiveController(w) {
		return
	}
	report, err := c.triggerRuntimeConfigReload(r.Context(), "api")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":  err.Error(),
			"report": report,
		})
		return
	}
	writeJSON(w, report)
}

func diffRestartRequiredFields(current, next Config) []string {
	var out []string
	appendIfChanged := func(name string, equal bool) {
		if !equal {
			out = append(out, name)
		}
	}

	appendIfChanged("listen", current.ListenAddr == next.ListenAddr)
	appendIfChanged("grpc_listen", current.GRPCListenAddr == next.GRPCListenAddr)
	appendIfChanged("scrape_interval", current.ScrapeInterval == next.ScrapeInterval)
	appendIfChanged("scrape_timeout", current.ScrapeTimeout == next.ScrapeTimeout)
	appendIfChanged("nodes", reflect.DeepEqual(current.Nodes, next.Nodes))
	appendIfChanged("web_path", current.WebPath == next.WebPath)
	appendIfChanged("log_level", current.LogLevel == next.LogLevel)
	appendIfChanged("auth", reflect.DeepEqual(current.Auth, next.Auth))
	appendIfChanged("analysis", reflect.DeepEqual(current.Analysis, next.Analysis))
	appendIfChanged("checks", reflect.DeepEqual(current.Checks, next.Checks))
	appendIfChanged("ingest.max_nodes", current.Ingest.MaxNodes == next.Ingest.MaxNodes)
	appendIfChanged("ingest.persistence", reflect.DeepEqual(current.Ingest.Persistence, next.Ingest.Persistence))
	appendIfChanged("tsdb", reflect.DeepEqual(current.TSDB, next.TSDB))
	appendIfChanged("orchestration", reflect.DeepEqual(current.Orchestration, next.Orchestration))
	appendIfChanged("kubernetes", reflect.DeepEqual(current.Kubernetes, next.Kubernetes))
	appendIfChanged("gpu", reflect.DeepEqual(current.GPU, next.GPU))
	appendIfChanged("incidents", reflect.DeepEqual(current.Incidents, next.Incidents))
	appendIfChanged("ha", reflect.DeepEqual(current.HA, next.HA))

	agentCurrent := current.Agent
	agentNext := next.Agent
	agentCurrent.PolicyFile = ""
	agentNext.PolicyFile = ""
	appendIfChanged("agent.runtime", reflect.DeepEqual(agentCurrent, agentNext))
	return uniqueSortedStrings(out)
}

func inventoryConfigChanged(current, next inventory.Config) bool {
	return current.Enabled != next.Enabled ||
		current.HeartbeatTTL != next.HeartbeatTTL ||
		strings.TrimSpace(current.TargetsFile) != strings.TrimSpace(next.TargetsFile) ||
		!reflect.DeepEqual(current.StaticTargets, next.StaticTargets)
}

func buildRuntimeInventoryTargets(nodes []NodeConfig, static []inventory.StaticProbe) []inventory.StaticProbe {
	out := make([]inventory.StaticProbe, 0, len(nodes)+len(static))
	for _, node := range nodes {
		id := strings.TrimSpace(node.Name)
		if id == "" {
			id = strings.TrimSpace(node.Address)
		}
		if id == "" {
			continue
		}
		out = append(out, inventory.StaticProbe{
			ID:      id,
			Name:    strings.TrimSpace(node.Name),
			Address: strings.TrimSpace(node.Address),
			Enabled: true,
			Labels:  node.Labels,
		})
	}
	out = append(out, static...)
	return out
}

func uniqueSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}
