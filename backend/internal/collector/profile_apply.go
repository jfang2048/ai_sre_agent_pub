package collector

import (
	"strings"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"google.golang.org/protobuf/proto"
)

func (c *Collector) ApplyRuntimeProfile(profile RuntimeProfile) RuntimeProfileStatus {
	if c == nil {
		return RuntimeProfileStatus{State: "disabled"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.profileRuntime == nil {
		c.profileRuntime = newRuntimeProfileRuntime()
	}
	status := c.profileRuntime.apply(profile)
	if profile.SamplingInterval > 0 {
		c.currentInterval = clampDuration(profile.SamplingInterval, c.cfg.MinCollectionInterval, c.cfg.MaxCollectionInterval)
	}
	return status
}

func (c *Collector) runtimeProfileStatusSnapshot() RuntimeProfileStatus {
	if c == nil || c.profileRuntime == nil {
		return RuntimeProfileStatus{State: "inactive"}
	}
	return c.profileRuntime.snapshot()
}

func (c *Collector) maybeExpireRuntimeProfileLocked(now time.Time) {
	if c == nil || c.profileRuntime == nil {
		return
	}
	if !c.profileRuntime.clearIfExpired() {
		return
	}
	c.currentInterval = clampDuration(c.cfg.CollectionInterval, c.cfg.MinCollectionInterval, c.cfg.MaxCollectionInterval)
	c.logger.Info("collector runtime profile reverted")
}

func applyRuntimeProfileToConfig(base Config, status RuntimeProfileStatus) Config {
	if status.State != "active" {
		return base
	}
	cfg := base
	if status.SamplingInterval > 0 {
		cfg.CollectionInterval = clampDuration(status.SamplingInterval, cfg.MinCollectionInterval, cfg.MaxCollectionInterval)
	}
	if status.ProcessTopK > 0 {
		cfg.TopK = status.ProcessTopK
	}
	if len(status.AllowedModules) > 0 {
		cfg.ProbeCore.Collectors = append([]string(nil), status.AllowedModules...)
	}
	return cfg
}

func applyRuntimeProfileToProcesses(processes []*telemetryv1.ProcessSample, status RuntimeProfileStatus) []*telemetryv1.ProcessSample {
	out := cloneProcessSamples(processes)
	if status.State != "active" || status.ProcessTopK <= 0 || len(out) <= status.ProcessTopK {
		return out
	}
	return out[:status.ProcessTopK]
}

func applyRuntimeProfileToLogs(logs []*telemetryv1.LogFingerprint, status RuntimeProfileStatus) []*telemetryv1.LogFingerprint {
	out := cloneLogFingerprints(logs)
	if status.State != "active" {
		return out
	}
	if len(status.EventFilters) > 0 {
		filtered := make([]*telemetryv1.LogFingerprint, 0, len(out))
		for _, item := range out {
			line := strings.ToLower(strings.TrimSpace(item.Example))
			if line == "" {
				line = strings.ToLower(strings.TrimSpace(item.Fingerprint))
			}
			for _, filter := range status.EventFilters {
				if strings.Contains(line, strings.ToLower(strings.TrimSpace(filter))) {
					filtered = append(filtered, item)
					break
				}
			}
		}
		out = filtered
	}
	if status.LogBudget > 0 && len(out) > status.LogBudget {
		out = out[:status.LogBudget]
	}
	return out
}

func cloneCollectorInfo(info *telemetryv1.CollectorInfo) *telemetryv1.CollectorInfo {
	if info == nil {
		return nil
	}
	return proto.Clone(info).(*telemetryv1.CollectorInfo)
}

func appendCollectorInfoRuntimeProfileLabels(info *telemetryv1.CollectorInfo, status RuntimeProfileStatus) {
	if info == nil || status.State == "" || status.State == "inactive" {
		return
	}
	labels := map[string]string{
		"runtime_profile_state": status.State,
	}
	if status.ProfileID != "" {
		labels["runtime_profile_id"] = status.ProfileID
	}
	if status.SceneFamily != "" {
		labels["runtime_profile_scene"] = status.SceneFamily
	}
	if status.GPUDetailMode != "" {
		labels["runtime_profile_gpu_mode"] = status.GPUDetailMode
	}
	if len(status.AllowedModules) > 0 {
		labels["runtime_profile_modules"] = strings.Join(status.AllowedModules, ",")
	}
	if !status.ExpiresAt.IsZero() {
		labels["runtime_profile_expires_at"] = status.ExpiresAt.UTC().Format(time.RFC3339)
	}
	info.Labels = append(info.Labels, buildLabels(labels)...)
}

func compactStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}
