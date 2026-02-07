package gpuobs

import "time"

// Device is a single GPU in a node.
type Device struct {
	GPUIndex      string            `json:"gpu_index"`
	UUID          string            `json:"uuid,omitempty"`
	Name          string            `json:"name,omitempty"`
	DriverVersion string            `json:"driver_version,omitempty"`
	CUDAVersion   string            `json:"cuda_version,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`

	LastSeen time.Time `json:"last_seen"`

	// Scalar metrics used for scheduling / balancing.
	UtilSMPercent     float64 `json:"util_sm_percent,omitempty"`
	UtilMemPercent    float64 `json:"util_mem_percent,omitempty"`
	MemTotalMiB       float64 `json:"mem_total_mib,omitempty"`
	MemUsedMiB        float64 `json:"mem_used_mib,omitempty"`
	MemFreeMiB        float64 `json:"mem_free_mib,omitempty"`
	TempC             float64 `json:"temp_c,omitempty"`
	TempMemC          float64 `json:"temp_mem_c,omitempty"`
	PowerDrawW        float64 `json:"power_draw_w,omitempty"`
	PowerLimitW       float64 `json:"power_limit_w,omitempty"`
	PCIERxMBs         float64 `json:"pcie_rx_mb_s,omitempty"`
	PCIETxMBs         float64 `json:"pcie_tx_mb_s,omitempty"`
	ThrottleActive    float64 `json:"throttle_active,omitempty"`
	ThrottleThermal   float64 `json:"throttle_thermal_active,omitempty"`
	ThrottlePower     float64 `json:"throttle_power_active,omitempty"`
	ProcessCount      float64 `json:"process_count,omitempty"`
	ContextCount      float64 `json:"context_count,omitempty"`
	XidErrorsTotal    float64 `json:"xid_errors_total,omitempty"`
	MigEnabled        float64 `json:"mig_enabled,omitempty"`
	MigPending        float64 `json:"mig_pending,omitempty"`
	PersistenceModeOn float64 `json:"persistence_mode_on,omitempty"`

	Processes []Process `json:"processes,omitempty"`
}

type Process struct {
	PID        string  `json:"pid"`
	Name       string  `json:"name,omitempty"`
	MemMiB     float64 `json:"mem_mib,omitempty"`
	UtilSMPct  float64 `json:"util_sm_percent,omitempty"`
	UtilMemPct float64 `json:"util_mem_percent,omitempty"`
}

// Node is GPU state for one collector node.
type Node struct {
	CollectorID string            `json:"collector_id"`
	Hostname    string            `json:"hostname"`
	Labels      map[string]string `json:"labels,omitempty"`

	LastSeen time.Time `json:"last_seen"`

	GPUCount int               `json:"gpu_count"`
	GPUs     map[string]Device `json:"gpus"` // key: gpu_index
}
