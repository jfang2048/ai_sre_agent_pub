#ifndef AI_SRE_AGENT_PROBE_CORE_GPU_NVML_H_
#define AI_SRE_AGENT_PROBE_CORE_GPU_NVML_H_

#include <string>
#include <vector>

namespace probe_core {

struct GPUDeviceSample {
  std::string index;
  std::string uuid;
  std::string name;
  std::string driver_version;
  std::string pci_bus_id;

  double util_sm_percent = -1.0;
  double util_mem_percent = -1.0;
  double memory_total_mib = -1.0;
  double memory_used_mib = -1.0;
  double memory_free_mib = -1.0;
  double memory_pressure_percent = -1.0;
  double temperature_celsius = -1.0;
  double power_draw_watts = -1.0;
  double power_limit_watts = -1.0;
  double clock_graphics_mhz = -1.0;
  double clock_sm_mhz = -1.0;
  double clock_memory_mhz = -1.0;
  double clock_video_mhz = -1.0;
  double fan_speed_percent = -1.0;
  double pcie_gen = -1.0;
  double pcie_width = -1.0;
  double pcie_gen_max = -1.0;
  double pcie_width_max = -1.0;
  double pcie_rx_mb_s = -1.0;
  double pcie_tx_mb_s = -1.0;
  double bar1_total_mib = -1.0;
  double bar1_used_mib = -1.0;
  double bar1_free_mib = -1.0;
  double ecc_single_bit_errors_total = -1.0;
  double ecc_double_bit_errors_total = -1.0;
  double process_count = -1.0;
  double context_count = -1.0;
};

struct GPUProcessSample {
  std::string gpu_index;
  std::string gpu_uuid;
  std::string pid;
  double memory_used_mib = -1.0;
  double memory_util_percent = -1.0;
  double context_active = 1.0;
};

struct GPUNVMLSamples {
  std::vector<GPUDeviceSample> devices;
  std::vector<GPUProcessSample> processes;
};

// CollectNVMLSamples dynamically loads NVML at runtime and returns device/process
// metrics when the NVIDIA management library is present. It returns false when
// the runtime is unavailable or no samples can be collected.
bool CollectNVMLSamples(GPUNVMLSamples* out);

}  // namespace probe_core

#endif  // AI_SRE_AGENT_PROBE_CORE_GPU_NVML_H_
