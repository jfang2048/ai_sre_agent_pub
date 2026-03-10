package collector

import (
	"os"
	"strconv"
	"strings"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"golang.org/x/sys/unix"
)

const (
	runtimeModeAuto      = "auto"
	runtimeModeHost      = "host"
	runtimeModeNamespace = "namespace"
	runtimeModeLimited   = "limited"
)

type collectorRuntimeInspection struct {
	RequestedMode       string
	AppliedMode         string
	ProbeCoreHostMode   string
	Containerized       bool
	HostPIDNamespace    bool
	ProcVisible         bool
	CgroupVisible       bool
	KernelMountsVisible bool
	BPFFSVisible        bool
	CAPBPF              bool
	CAPSysAdmin         bool
	CanUseEBPF          bool
	Degraded            bool
	Reasons             []string
}

type runtimeInspectorDeps struct {
	stat     func(string) (os.FileInfo, error)
	readFile func(string) ([]byte, error)
	readlink func(string) (string, error)
}

func defaultRuntimeInspectorDeps() runtimeInspectorDeps {
	return runtimeInspectorDeps{
		stat:     os.Stat,
		readFile: os.ReadFile,
		readlink: os.Readlink,
	}
}

func normalizeCollectorRuntimeMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", runtimeModeAuto:
		return runtimeModeAuto
	case runtimeModeHost:
		return runtimeModeHost
	case runtimeModeNamespace:
		return runtimeModeNamespace
	case runtimeModeLimited:
		return runtimeModeLimited
	default:
		return ""
	}
}

func detectCollectorRuntimeInspection(cfg Config) collectorRuntimeInspection {
	return detectCollectorRuntimeInspectionWithDeps(cfg, defaultRuntimeInspectorDeps())
}

func detectCollectorRuntimeInspectionWithDeps(cfg Config, deps runtimeInspectorDeps) collectorRuntimeInspection {
	requested := normalizeCollectorRuntimeMode(cfg.RuntimeMode)
	if requested == "" {
		requested = runtimeModeAuto
	}
	inspection := collectorRuntimeInspection{
		RequestedMode:     requested,
		AppliedMode:       runtimeModeLimited,
		ProbeCoreHostMode: "proc",
		ProcVisible:       pathAccessible(deps, "/proc/stat") && pathAccessible(deps, "/proc/meminfo"),
		CgroupVisible:     pathAccessible(deps, "/sys/fs/cgroup"),
		KernelMountsVisible: pathAccessible(deps, "/sys/kernel") ||
			pathAccessible(deps, "/sys/kernel/debug") ||
			pathAccessible(deps, "/lib/modules"),
		BPFFSVisible: pathAccessible(deps, "/sys/fs/bpf"),
	}
	inspection.HostPIDNamespace = sameNamespace(deps, "/proc/self/ns/pid", "/proc/1/ns/pid")
	inspection.Containerized = detectsContainerRuntime(deps)
	inspection.CAPBPF = hasEffectiveCapability(deps, unix.CAP_BPF)
	inspection.CAPSysAdmin = hasEffectiveCapability(deps, unix.CAP_SYS_ADMIN)
	inspection.CanUseEBPF = inspection.BPFFSVisible && (inspection.CAPBPF || inspection.CAPSysAdmin)

	hostCapable := inspection.ProcVisible && inspection.HostPIDNamespace && inspection.KernelMountsVisible
	namespaceCapable := inspection.ProcVisible && (inspection.HostPIDNamespace || inspection.CgroupVisible || inspection.Containerized)

	switch requested {
	case runtimeModeHost:
		switch {
		case hostCapable:
			inspection.AppliedMode = runtimeModeHost
		case namespaceCapable:
			inspection.AppliedMode = runtimeModeNamespace
			inspection.Reasons = append(inspection.Reasons, "requested_host_mode_unavailable")
		default:
			inspection.AppliedMode = runtimeModeLimited
			inspection.Reasons = append(inspection.Reasons, "requested_host_mode_unavailable")
		}
	case runtimeModeNamespace:
		if namespaceCapable {
			inspection.AppliedMode = runtimeModeNamespace
		} else {
			inspection.AppliedMode = runtimeModeLimited
			inspection.Reasons = append(inspection.Reasons, "requested_namespace_mode_unavailable")
		}
	case runtimeModeLimited:
		inspection.AppliedMode = runtimeModeLimited
	default:
		switch {
		case hostCapable:
			inspection.AppliedMode = runtimeModeHost
		case namespaceCapable:
			inspection.AppliedMode = runtimeModeNamespace
		default:
			inspection.AppliedMode = runtimeModeLimited
		}
	}

	if inspection.Containerized {
		inspection.Reasons = append(inspection.Reasons, "containerized_runtime")
	}
	if !inspection.HostPIDNamespace {
		inspection.Reasons = append(inspection.Reasons, "host_pid_namespace_unavailable")
	}
	if !inspection.KernelMountsVisible {
		inspection.Reasons = append(inspection.Reasons, "kernel_mounts_restricted")
	}
	if !inspection.CanUseEBPF {
		if !inspection.BPFFSVisible {
			inspection.Reasons = append(inspection.Reasons, "bpf_filesystem_unavailable")
		}
		if !inspection.CAPBPF && !inspection.CAPSysAdmin {
			inspection.Reasons = append(inspection.Reasons, "bpf_capability_unavailable")
		}
	}
	if !inspection.ProcVisible {
		inspection.Reasons = append(inspection.Reasons, "proc_visibility_restricted")
	}

	inspection.Reasons = dedupeStrings(inspection.Reasons)
	if inspection.AppliedMode == runtimeModeHost {
		inspection.ProbeCoreHostMode = "kernel"
	}
	inspection.Degraded = inspection.AppliedMode != runtimeModeHost || !inspection.CanUseEBPF
	return inspection
}

func applyProbeCoreHostMode(args []string, inspection collectorRuntimeInspection) []string {
	out := append([]string(nil), args...)
	desired := strings.TrimSpace(inspection.ProbeCoreHostMode)
	if desired == "" {
		desired = "proc"
	}
	for idx := 0; idx < len(out); idx++ {
		if out[idx] != "--host-mode" {
			continue
		}
		if idx+1 >= len(out) {
			return append(out, desired)
		}
		if strings.EqualFold(strings.TrimSpace(out[idx+1]), "auto") {
			out[idx+1] = desired
		}
		return out
	}
	return append(out, "--host-mode", desired)
}

func pathAccessible(deps runtimeInspectorDeps, path string) bool {
	if deps.stat == nil {
		return false
	}
	_, err := deps.stat(path)
	return err == nil
}

func sameNamespace(deps runtimeInspectorDeps, left, right string) bool {
	if deps.readlink == nil {
		return false
	}
	leftRef, err := deps.readlink(left)
	if err != nil {
		return false
	}
	rightRef, err := deps.readlink(right)
	if err != nil {
		return false
	}
	return leftRef == rightRef && strings.TrimSpace(leftRef) != ""
}

func detectsContainerRuntime(deps runtimeInspectorDeps) bool {
	if pathAccessible(deps, "/.dockerenv") {
		return true
	}
	if deps.readFile == nil {
		return false
	}
	for _, path := range []string{"/proc/1/cgroup", "/proc/self/cgroup"} {
		raw, err := deps.readFile(path)
		if err != nil {
			continue
		}
		text := strings.ToLower(string(raw))
		if strings.Contains(text, "docker") || strings.Contains(text, "kubepods") || strings.Contains(text, "containerd") || strings.Contains(text, "podman") {
			return true
		}
	}
	return false
}

func hasEffectiveCapability(deps runtimeInspectorDeps, capability int) bool {
	if deps.readFile == nil || capability < 0 || capability >= 64 {
		return false
	}
	raw, err := deps.readFile("/proc/self/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return false
		}
		value, err := strconv.ParseUint(strings.TrimSpace(fields[1]), 16, 64)
		if err != nil {
			return false
		}
		return value&(uint64(1)<<uint(capability)) != 0
	}
	return false
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func appendCollectorInfoRuntimeLabels(info *telemetryv1.CollectorInfo, inspection collectorRuntimeInspection) {
	if info == nil {
		return
	}
	info.Labels = append(info.Labels,
		&telemetryv1.Label{Key: "runtime_mode", Value: inspection.AppliedMode},
		&telemetryv1.Label{Key: "runtime_mode_requested", Value: inspection.RequestedMode},
		&telemetryv1.Label{Key: "runtime_probe_core_host_mode", Value: inspection.ProbeCoreHostMode},
	)
	if inspection.Containerized {
		info.Labels = append(info.Labels, &telemetryv1.Label{Key: "runtime_containerized", Value: "true"})
	}
}
