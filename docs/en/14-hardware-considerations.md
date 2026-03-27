# Hardware Considerations

中文版本：[docs/zh/14-hardware-considerations.md](../zh/14-hardware-considerations.md)

This page explains how `v0.8` treats hardware as part of observability logic instead of assuming every host behaves like the same generic machine.

## Why Hardware Awareness Exists

A disk queue depth that is normal on NVMe can be a warning sign on HDD. A CPU saturation threshold on a hybrid or NUMA-heavy system may not mean the same thing as on a small symmetric host. GPU observability also depends heavily on vendor runtime support.

The collector therefore keeps a cached hardware profile and uses it to adjust:

- sub-collector sampling intervals
- interpretation thresholds
- anomaly scoring
- labels and capability metrics exposed to the controller

Without this layer, the agent would either overfit one hardware class or use thresholds so generic that they become less useful in production.

## Where Hardware Awareness Is Implemented

| Path | Responsibility |
| --- | --- |
| [`backend/internal/collector/hardware_profile.go`](../../backend/internal/collector/hardware_profile.go) | hardware discovery, cached profile, sampling profile, threshold profile, hardware metrics |
| [`backend/internal/collector/protection.go`](../../backend/internal/collector/protection.go) | anomaly scoring and host-protection decisions using hardware-aware thresholds |
| [`backend/internal/collector/hardware_warnings.go`](../../backend/internal/collector/hardware_warnings.go) | broad hardware warning metrics derived from existing signals and cached thresholds |
| [`backend/internal/collector/probe/cadence.go`](../../backend/internal/collector/probe/cadence.go) | separate slow hardware tier for the legacy Go fallback path |
| [`configs/collector.yaml`](../../configs/collector.yaml) | `hardware.refresh_interval`, `probe_core.*interval_samples`, and protection limits |
| [`cpp/probe_core/gpu_nvml.cpp`](../../cpp/probe_core/gpu_nvml.cpp) | NVIDIA NVML-first GPU collection path |
| [`docs/25-metrics-reference.md`](25-metrics-reference.md) | exported hardware-related metric families |

## Hardware Discovery Model

The collector keeps hardware discovery cached instead of rescanning `/sys` and `/proc` constantly.

Current behavior from [`hardware_profile.go`](../../backend/internal/collector/hardware_profile.go):

- a hardware cache is created at collector startup
- discovery reads from `/proc` and `/sys`
- the profile is refreshed only when `hardware.refresh_interval` has elapsed
- derived sampling and threshold profiles are recomputed only when the hardware profile changes

Default config in [`configs/collector.yaml`](../../configs/collector.yaml):

- `hardware.enabled: true`
- `hardware.refresh_interval: "6h"`

This solves a real overhead problem: topology rarely changes compared with telemetry cadence, so repeated full hardware scans are wasted work.

## What the Collector Detects Today

| Domain | Fields discovered in code | Why they matter |
| --- | --- | --- |
| CPU | architecture, vendor, model, sockets, cores, threads, NUMA nodes, hybrid-core signal | adjusts process and fallback cadence, CPU and memory thresholds |
| Storage | device count, NVMe count, SSD count, HDD count, dominant class, max queue depth | changes disk latency and queue-depth interpretation |
| Network | interface count, high-speed count, max speed, dominant type, dominant driver, RDMA capability | changes retransmit/drop expectations and netlink pacing |
| GPU | device count, vendor, driver, runtime | changes GPU sampling cadence and GPU threshold defaults |

What is not modeled as a separate hardware inventory object:

- memory DIMM topology or vendor details

Memory is still observed operationally through pressure metrics, utilization, and NUMA-related signals, but not through a dedicated memory hardware profile struct.

## How Hardware Changes Sampling

The collector derives a hardware-aware sampling profile and applies it to probe-core sub-collectors.

Examples from `deriveHardwareSamplingProfile`:

| Hardware condition | Current adjustment |
| --- | --- |
| large CPU thread count, NUMA, or hybrid CPU | slower per-process and `/proc` fallback refresh |
| very large CPU count | even slower process and pressure sampling |
| standard networking without RDMA or high-speed NICs | slower netlink refresh |
| no GPU devices | much slower GPU refresh |
| many GPU devices | slightly slower GPU refresh to bound per-cycle cost |

This does not redesign the collector architecture. It changes cadence within the existing probe-core and collector model.

## Concrete Sampling Math

The easiest way to understand the current behavior is to combine:

- [`configs/collector.yaml`](../../configs/collector.yaml)
  - `collection_interval: "5s"`
  - `probe_core.interval: "1s"`
- [`hardware_profile.go`](../../backend/internal/collector/hardware_profile.go)
  - `deriveHardwareSamplingProfile`
- [`aux_sampling.go`](../../backend/internal/collector/aux_sampling.go)
  - helper cadence for compatibility process scans, logs, and external metrics

### Example A: Small VM, no GPU, standard NIC

Illustrative profile values consistent with the current code:

- `collector_hardware_cpu_threads = 16`
- `collector_hardware_cpu_numa_nodes = 1`
- `collector_hardware_gpu_devices_total = 0`
- `collector_hardware_network_high_speed_interfaces_total = 0`
- `collector_hardware_network_rdma_capable = 0`

Likely derived cadence metrics:

| Metric | Likely value | Why |
| --- | --- | --- |
| `collector_hardware_capability_process_interval_samples` | `2` | host is not large enough to slow process refresh |
| `collector_hardware_capability_host_proc_interval_samples` | `10` | default compatibility fallback interval |
| `collector_hardware_capability_pressure_interval_samples` | `3` | default PSI cadence |
| `collector_hardware_capability_netlink_interval_samples` | `3` | slower netlink cadence on standard networking |
| `collector_hardware_capability_gpu_interval_samples` | `8` | no GPU present, so GPU refresh is heavily relaxed |

What that means operationally:

- probe-core process sampling still runs every `2s`
- compatibility `/proc` process fallback runs every `max(5s, 1s * 10) = 10s`
- compatibility thermal/NIC sysfs/IRQ/RDMA scans sit on their own slow tier at `max(6 * 5s, 30s) = 30s`
- GPU sub-collection runs every `8s`, even though the collector batch loop runs every `5s`
- log tailing stays on `max(15s, 3 * 5s) = 15s`
- external metrics stay on `max(30s, 6 * 5s) = 30s`

### Example B: 128-thread NUMA host with 4 NVIDIA GPUs

Illustrative profile values consistent with the current code:

- `collector_hardware_cpu_threads = 128`
- `collector_hardware_cpu_numa_nodes = 2`
- `collector_hardware_gpu_devices_total = 4`
- `collector_hardware_network_high_speed_interfaces_total = 1`
- `collector_hardware_network_rdma_capable = 1`

Likely derived cadence metrics:

| Metric | Likely value | Why |
| --- | --- | --- |
| `collector_hardware_capability_process_interval_samples` | `4` | large CPU count slows per-process refresh |
| `collector_hardware_capability_host_proc_interval_samples` | `16` | compatibility fallback becomes more expensive on large hosts |
| `collector_hardware_capability_pressure_interval_samples` | `4` | large CPU profile relaxes PSI cadence |
| `collector_hardware_capability_netlink_interval_samples` | `2` | high-speed or RDMA networking keeps the tighter netlink cadence |
| `collector_hardware_capability_gpu_interval_samples` | `2` | many GPUs slow GPU refresh slightly, but not as aggressively as no-GPU hosts |

Operational effect:

- probe-core process sampling runs every `4s`
- compatibility `/proc` process fallback runs every `max(5s, 1s * 16) = 16s`
- compatibility thermal/NIC sysfs/IRQ/RDMA scans still stay on the separate slow hardware tier instead of the runtime extended tier
- GPU sub-collection runs every `2s`
- if the collector enters `pressure` or `critical` mode, auxiliary helpers can stretch even further because [`effectiveAuxiliaryInterval`](../../backend/internal/collector/aux_sampling.go) prefers the slower collector cadence under pressure

## What The Slow Hardware Tier Actually Sends

The slow compatibility hardware tier in [`probe/cadence.go`](../../backend/internal/collector/probe/cadence.go) now has its own payload suppression behavior.

On a real hardware refresh, the fallback batch may include metrics such as:

```json
{
  "metrics": [
    {"name":"node_thermal_zone_temp_celsius","value":87.5},
    {"name":"node_network_interface_speed_mbps","value":25000,"labels":{"device":"eth0"}},
    {"name":"collector_compat_payload_refreshed","value":1,"labels":{"component":"hardware"}}
  ]
}
```

On the next cache-hit hardware cycle, with `suppress_cached_compat_hardware_metrics: true`, the collector can send only:

```json
{
  "metrics": [
    {"name":"collector_compat_collection_cache_hit","value":1,"labels":{"component":"hardware"}},
    {"name":"collector_compat_payload_suppressed","value":1,"labels":{"component":"hardware"}}
  ]
}
```

[`StoreMetrics`](../../backend/internal/controller/ingest/store.go) carries the previous hardware fallback values forward in that case. This is a deliberate tradeoff:

- lower steady-state batch size and fewer repeated sysfs-derived values
- preserved controller-side hardware context
- slightly less redundancy per batch, which is acceptable because the next real hardware refresh or source-path update can replace or clear the view

## Broad Hardware Diagnosis Without New Heavy Probes

The current implementation adds a broad hardware-warning layer without introducing a new invasive collector:

- `collector_hardware_warning_total`
- `collector_hardware_warning{domain="cpu|memory|disk|network|gpu",reason=...,signal=...}`

These warnings are derived from signals the collector already has plus the cached hardware threshold profile. That means:

- CPU hints come from throttling, iowait, and contention-style signals already in the batch
- memory hints come from pressure and NUMA-miss style signals
- disk hints come from latency, queue depth, and IO pressure
- network hints come from retransmits, softnet drops, errors, and RDMA congestion
- GPU hints come from throttle, memory pressure, and active-process low-utilization patterns

This is intentionally broad rather than vendor-perfect. The design goal is low-impact diagnosis: more useful hardware context for operators and downstream screening, without adding another always-on privileged probe.

On the controller side, these warnings are now treated as symptom context, not as an automatic reason to retrieve. A generic question with no matching hardware finding still skips RAG, but concrete warnings such as disk latency degradation, NIC retransmits, or GPU thermal pressure can flow into retrieval and prompt assembly once they appear in findings or anomaly hints.

## How Hardware Changes Thresholds

`deriveHardwareThresholdProfile` changes thresholds based on hardware class.

Examples from the current code:

| Hardware condition | Threshold effect |
| --- | --- |
| dominant storage class `nvme` | lower latency threshold, higher expected queue depth |
| dominant storage class `hdd` | higher acceptable latency, lower queue depth, higher IO pressure tolerance |
| hybrid CPU | lower CPU busy and critical thresholds |
| multi-NUMA CPU | lower memory-pressure threshold |
| RDMA or high-speed NICs | stricter retransmit expectation and explicit softnet-drop tolerance |
| NVIDIA GPU | lower memory-pressure threshold than the generic default |
| AMD or Intel GPU | different low-utilization thresholds |

The point is not to claim per-model perfection. It is to avoid obviously wrong "one threshold fits all hardware" behavior.

## Concrete Interpretation Examples

The threshold profile matters because the same raw metric can imply different risk on different hardware.

### Example 1: The same disk latency on NVMe and HDD

Illustrative live metrics:

```text
node_disk_request_latency_p99_seconds = 0.020
node_disk_queue_depth_total = 18
```

What the current code compares them against:

- NVMe profile:
  - `collector_hardware_threshold_disk_latency_seconds = 0.015`
  - `collector_hardware_threshold_disk_queue_depth = 24`
- HDD profile:
  - `collector_hardware_threshold_disk_latency_seconds = 0.080`
  - `collector_hardware_threshold_disk_queue_depth = 2`

Interpretation:

- on NVMe, latency is already above the expected threshold, but queue depth is still below the expected limit
- on HDD, latency is still acceptable, but queue depth is far above the expected depth

That is exactly why [`protection.go`](../../backend/internal/collector/protection.go) scores disk risk as the maximum of latency and queue-depth over-threshold checks instead of assuming one fixed rule for all storage.

### Example 2: High-speed NIC retransmits

Illustrative live metrics:

```text
node_tcp_retransmit_ratio = 0.012
node_tcp_retransmits_per_second = 0.8
node_softnet_dropped_per_second = 2
```

Current threshold behavior:

- standard NIC profile keeps `collector_hardware_threshold_network_retransmit_ratio = 0.02`
- RDMA or high-speed NIC profile lowers it to `0.01`

Interpretation:

- on a standard NIC host, this is elevated but still below the stricter alert threshold
- on a high-speed NIC or RDMA host, the same retransmit ratio is already above the expected threshold

This is why the network anomaly score in [`protection.go`](../../backend/internal/collector/protection.go) considers both retransmit ratio and softnet drops together.

### Example 3: Active GPU jobs with low SM utilization

Illustrative live metrics:

```text
node_gpu_utilization_sm_avg_percent = 20
node_gpu_process_total = 3
node_gpu_memory_used_percent = 89
node_gpu_throttle_power_any = 0
```

Current NVIDIA-oriented thresholds:

- `collector_hardware_threshold_gpu_low_util_percent = 35`
- memory pressure threshold derived in code: `85`

Interpretation:

- active GPU processes plus low SM utilization can point to feeder starvation or stall conditions
- high GPU memory pressure increases the anomaly score even when thermal or power throttle flags are still zero

That combination is why the current GPU scoring does not rely on utilization alone.

## Example Hardware Profiles

Two hosts can produce the same raw metric name and still need different interpretation.

| Example host | Likely profile effect in current code |
| --- | --- |
| small VM, no GPU, standard NIC | slower GPU refresh, simpler network expectations, generic storage defaults |
| large NUMA training host with many CPU threads and multiple GPUs | slower process and fallback cadence, stricter memory-pressure interpretation, active GPU-specific thresholds |
| NVMe-heavy box | lower disk-latency threshold but higher expected queue depth |
| HDD-heavy box | higher acceptable latency but lower expected queue depth |

That is the practical reason the collector exports both raw signals and `collector_hardware_capability_*` / `collector_hardware_threshold_*` metrics.

## Hardware Metrics Exported to the Controller

The collector emits hardware metadata and derived policy metrics such as:

- `collector_hardware_cpu_sockets`
- `collector_hardware_cpu_cores`
- `collector_hardware_cpu_threads`
- `collector_hardware_cpu_numa_nodes`
- `collector_hardware_cpu_hybrid`
- `collector_hardware_storage_devices_total`
- `collector_hardware_network_interfaces_total`
- `collector_hardware_network_high_speed_interfaces_total`
- `collector_hardware_network_max_speed_mbps`
- `collector_hardware_network_rdma_capable`
- `collector_hardware_gpu_devices_total`
- `collector_hardware_capability_*`
- `collector_hardware_threshold_*`
- `collector_hardware_*_anomaly_score`

These exist so the controller and operator can see which profile the collector actually chose, instead of guessing from the host type.

## What Operators Should Inspect On Real Hosts

When validating a new host class, check these metrics together instead of looking only at workload symptoms:

| Question | Metrics to inspect | Why |
| --- | --- | --- |
| Did hardware discovery really work? | `collector_hardware_refresh_age_seconds`, `collector_hardware_storage_profile`, `collector_hardware_network_profile`, `collector_hardware_gpu_profile` | proves the collector actually built a profile instead of falling back to generic defaults |
| Did the collector relax expensive paths on this hardware? | `collector_hardware_capability_process_interval_samples`, `collector_hardware_capability_host_proc_interval_samples`, `collector_hardware_capability_gpu_interval_samples` | shows whether large-host or no-GPU backoff is active |
| Are thresholds hardware-specific or generic? | `collector_hardware_threshold_disk_latency_seconds`, `collector_hardware_threshold_disk_queue_depth`, `collector_hardware_threshold_network_retransmit_ratio` | exposes the threshold profile directly |
| Is the host currently showing a hardware-shaped anomaly? | `collector_hardware_cpu_anomaly_score`, `collector_hardware_memory_anomaly_score`, `collector_hardware_disk_anomaly_score`, `collector_hardware_gpu_anomaly_score`, `collector_hardware_network_anomaly_score` | tells you which hardware domain is currently driving protection pressure |

If these metrics do not line up with the actual host type, the problem is usually discovery visibility or container placement, not the reasoning layer.

## GPU-Specific Reality

GPU handling is the most runtime-dependent part of hardware observability in this repo.

What the current repo supports:

- GPU inventory detection from `/sys/class/drm/card*`
- runtime hinting from `/proc/driver/nvidia/version`
- NVML-first GPU collection in probe-core (including fine-grained throttle reasons, NVLink topology, and real-time energy consumption)
- bounded fallback to `nvidia-smi` when NVML is unavailable

Practical implication:

- NVIDIA is the strongest supported GPU telemetry path today
- non-NVIDIA GPU inventory may still be detected, but rich device/process telemetry is more limited
- missing runtime libraries or device access can reduce GPU observability without breaking the rest of the collector

## Deployment Caveats

Hardware-aware logic only helps if the collector can see the host correctly.

Current repo assumptions:

- `host-observer` style deployment gives the best host namespace and kernel visibility
- `/sys` and `/proc` must be readable enough for discovery
- GPU runtime visibility depends on driver/runtime access
- eBPF behavior depends on kernel support and privileges

If these assumptions are not met:

- the collector can still run
- source markers and telemetry-quality signals are expected to show degraded or compatibility modes
- thresholds and anomaly scores may become less representative

## What Happens If Hardware Discovery Fails

The collector falls back to a conservative default profile:

- architecture from `runtime.GOARCH`
- CPU counts from `runtime.NumCPU()`
- unknown storage/network class
- no GPU by default

That keeps the collector runnable, but it removes most of the hardware specialization. The system becomes generic rather than blind.

## Example Failure Mode

If `/sys` access is restricted inside a container:

- collector still starts
- CPU counts can still fall back to `runtime.NumCPU()`
- storage and NIC class detection may become `unknown`
- GPU specialization may disappear unless runtime libraries and device access are still visible

In that case the monitoring stack still works, but its thresholds are more conservative and less hardware-specific.

## Limits and Tradeoffs

The current hardware model is intentionally pragmatic:

- it is cached to keep overhead low
- it distinguishes major hardware classes, not every vendor SKU nuance
- it improves threshold quality without pretending to be a full inventory system
- it is strongest on Linux hosts where `/sys` and `/proc` expose the needed topology

## See Also

- [Metrics and signals](13-metrics-and-signals.md)
- [Codebase map](09-codebase-map.md)
- [Core files](10-core-files.md)
- [Data flow](05-data-flow.md)
- [Configuration reference](22-configuration.md)
- [Metrics reference](25-metrics-reference.md)
