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
	UtilSMPercent       float64 `json:"util_sm_percent,omitempty"`
	UtilMemPercent      float64 `json:"util_mem_percent,omitempty"`
	UtilEncPercent      float64 `json:"util_enc_percent,omitempty"`
	UtilDecPercent      float64 `json:"util_dec_percent,omitempty"`
	UtilJpegPercent     float64 `json:"util_jpeg_percent,omitempty"`
	UtilOFAPercent      float64 `json:"util_ofa_percent,omitempty"`
	PowerState          float64 `json:"power_state,omitempty"`
	MemTotalMiB         float64 `json:"mem_total_mib,omitempty"`
	MemUsedMiB          float64 `json:"mem_used_mib,omitempty"`
	MemFreeMiB          float64 `json:"mem_free_mib,omitempty"`
	MemReservedMiB      float64 `json:"mem_reserved_mib,omitempty"`
	Bar1MemTotalMiB     float64 `json:"bar1_mem_total_mib,omitempty"`
	Bar1MemUsedMiB      float64 `json:"bar1_mem_used_mib,omitempty"`
	Bar1MemFreeMiB      float64 `json:"bar1_mem_free_mib,omitempty"`
	TempC               float64 `json:"temp_c,omitempty"`
	TempMemC            float64 `json:"temp_mem_c,omitempty"`
	PowerDrawW          float64 `json:"power_draw_w,omitempty"`
	PowerLimitW         float64 `json:"power_limit_w,omitempty"`
	PCIEGen             float64 `json:"pcie_gen,omitempty"`
	PCIEWidth           float64 `json:"pcie_width,omitempty"`
	PCIEGenMax          float64 `json:"pcie_gen_max,omitempty"`
	PCIEWidthMax        float64 `json:"pcie_width_max,omitempty"`
	PCIERxMBs           float64 `json:"pcie_rx_mb_s,omitempty"`
	PCIETxMBs           float64 `json:"pcie_tx_mb_s,omitempty"`
	PCIETheoreticalMBs  float64 `json:"pcie_theoretical_mb_s,omitempty"`
	PCIEBandwidthMaxMBs float64 `json:"pcie_bandwidth_max_mb_s,omitempty"`
	PCIERxUtilPercent   float64 `json:"pcie_rx_util_percent,omitempty"`
	PCIETxUtilPercent   float64 `json:"pcie_tx_util_percent,omitempty"`
	PCIELinkUtilPercent float64 `json:"pcie_link_util_percent,omitempty"`

	ThrottleActive                  float64 `json:"throttle_active,omitempty"`
	ThrottleThermal                 float64 `json:"throttle_thermal_active,omitempty"`
	ThrottlePower                   float64 `json:"throttle_power_active,omitempty"`
	ProcessCount                    float64 `json:"process_count,omitempty"`
	ContextCount                    float64 `json:"context_count,omitempty"`
	XidErrorsTotal                  float64 `json:"xid_errors_total,omitempty"`
	UVMFaultsTotal                  float64 `json:"uvm_faults_total,omitempty"`
	ResetEventsTotal                float64 `json:"reset_events_total,omitempty"`
	ReliabilityEventsTotal          float64 `json:"reliability_events_total,omitempty"`
	ECCSingleBitErrorsTotal         float64 `json:"ecc_single_bit_errors_total,omitempty"`
	ECCDoubleBitErrorsTotal         float64 `json:"ecc_double_bit_errors_total,omitempty"`
	ECCVolatileSingleBitErrorsTotal float64 `json:"ecc_volatile_single_bit_errors_total,omitempty"`
	ECCVolatileDoubleBitErrorsTotal float64 `json:"ecc_volatile_double_bit_errors_total,omitempty"`
	RetiredPagesSingleBitTotal      float64 `json:"retired_pages_single_bit_total,omitempty"`
	RetiredPagesDoubleBitTotal      float64 `json:"retired_pages_double_bit_total,omitempty"`
	RetiredPagesPending             float64 `json:"retired_pages_pending,omitempty"`
	RemappedRowsCorrectableTotal    float64 `json:"remapped_rows_correctable_total,omitempty"`
	RemappedRowsUncorrectableTotal  float64 `json:"remapped_rows_uncorrectable_total,omitempty"`
	RemappedRowsPending             float64 `json:"remapped_rows_pending,omitempty"`
	ResetRequired                   float64 `json:"reset_required,omitempty"`
	ResetRecommended                float64 `json:"reset_recommended,omitempty"`
	MigEnabled                      float64 `json:"mig_enabled,omitempty"`
	MigPending                      float64 `json:"mig_pending,omitempty"`
	PersistenceModeOn               float64 `json:"persistence_mode_on,omitempty"`
	NVLinkLinks                     float64 `json:"nvlink_links,omitempty"`
	KernelHotspotPeakSMUtilPercent  float64 `json:"kernel_hotspot_peak_sm_util_percent,omitempty"`
	KernelActiveContexts            float64 `json:"kernel_active_contexts,omitempty"`

	Processes []Process `json:"processes,omitempty"`
}

type Process struct {
	PID           string  `json:"pid"`
	Name          string  `json:"name,omitempty"`
	MemMiB        float64 `json:"mem_mib,omitempty"`
	UtilSMPct     float64 `json:"util_sm_percent,omitempty"`
	UtilMemPct    float64 `json:"util_mem_percent,omitempty"`
	UtilEncPct    float64 `json:"util_enc_percent,omitempty"`
	UtilDecPct    float64 `json:"util_dec_percent,omitempty"`
	ContextActive float64 `json:"context_active,omitempty"`
	ContextType   string  `json:"context_type,omitempty"`
}

// Event is a normalized GPU runtime event record derived from counters/signals.
type Event struct {
	Timestamp   time.Time `json:"timestamp"`
	CollectorID string    `json:"collector_id,omitempty"`
	Hostname    string    `json:"hostname,omitempty"`
	GPUIndex    string    `json:"gpu_index,omitempty"`
	EventType   string    `json:"event_type"`
	Severity    string    `json:"severity"`
	Code        string    `json:"code,omitempty"`
	Count       float64   `json:"count,omitempty"`
	Source      string    `json:"source,omitempty"`
	Message     string    `json:"message,omitempty"`
}

// MetricPoint is a compact timestamped scalar used by timeline APIs.
type MetricPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// Node is GPU state for one collector node.
type Node struct {
	CollectorID string            `json:"collector_id"`
	Hostname    string            `json:"hostname"`
	Labels      map[string]string `json:"labels,omitempty"`

	LastSeen time.Time `json:"last_seen"`

	GPUCount int               `json:"gpu_count"`
	GPUs     map[string]Device `json:"gpus"` // key: gpu_index

	RecentEvents []Event `json:"recent_events,omitempty"`
}
