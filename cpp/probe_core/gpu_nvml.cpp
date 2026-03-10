#include "gpu_nvml.h"

#include <dlfcn.h>

#include <algorithm>
#include <array>
#include <cstdint>
#include <cstring>
#include <string>
#include <vector>

namespace probe_core {
namespace {

using nvmlReturn_t = int;
using nvmlDevice_t = struct nvmlDevice_st*;

struct nvmlUtilization_t {
  unsigned int gpu;
  unsigned int memory;
};

struct nvmlMemory_t {
  unsigned long long total;
  unsigned long long free;
  unsigned long long used;
};

struct nvmlBAR1Memory_t {
  unsigned long long bar1Total;
  unsigned long long bar1Free;
  unsigned long long bar1Used;
};

struct nvmlPciInfo_t {
  char busIdLegacy[16];
  unsigned int domain;
  unsigned int bus;
  unsigned int device;
  unsigned int pciDeviceId;
  unsigned int pciSubSystemId;
  char busId[32];
};

struct nvmlProcessInfo_v1_t {
  unsigned int pid;
  unsigned long long usedGpuMemory;
};

struct nvmlProcessInfo_v2_t {
  unsigned int pid;
  unsigned long long usedGpuMemory;
  unsigned int gpuInstanceId;
  unsigned int computeInstanceId;
};

constexpr nvmlReturn_t kNVMLSuccess = 0;
constexpr nvmlReturn_t kNVMLInsufficientSize = 7;
constexpr unsigned long long kNVMLValueNotAvailableU64 = ~0ULL;

constexpr unsigned int kNVMLTemperatureGPU = 0;
constexpr unsigned int kNVMLClockGraphics = 0;
constexpr unsigned int kNVMLClockSM = 1;
constexpr unsigned int kNVMLClockMem = 2;
constexpr unsigned int kNVMLClockVideo = 3;
constexpr unsigned int kNVMLPcieUtilTxBytes = 0;
constexpr unsigned int kNVMLPcieUtilRxBytes = 1;
constexpr unsigned int kNVMLMemoryErrorCorrected = 0;
constexpr unsigned int kNVMLMemoryErrorUncorrected = 1;
constexpr unsigned int kNVMLAggregateECC = 1;

class ScopedLibrary {
 public:
  ScopedLibrary() = default;
  explicit ScopedLibrary(void* handle) : handle_(handle) {}
  ~ScopedLibrary() {
    if (handle_ != nullptr) {
      dlclose(handle_);
    }
  }

  ScopedLibrary(const ScopedLibrary&) = delete;
  ScopedLibrary& operator=(const ScopedLibrary&) = delete;

  ScopedLibrary(ScopedLibrary&& other) noexcept : handle_(other.release()) {}
  ScopedLibrary& operator=(ScopedLibrary&& other) noexcept {
    if (this != &other) {
      reset(other.release());
    }
    return *this;
  }

  void* get() const { return handle_; }
  explicit operator bool() const { return handle_ != nullptr; }

  void* release() {
    void* out = handle_;
    handle_ = nullptr;
    return out;
  }

  void reset(void* next = nullptr) {
    if (handle_ != nullptr) {
      dlclose(handle_);
    }
    handle_ = next;
  }

 private:
  void* handle_ = nullptr;
};

template <typename Fn>
Fn loadSymbol(void* handle, std::initializer_list<const char*> names) {
  for (const char* name : names) {
    if (name == nullptr) continue;
    void* sym = dlsym(handle, name);
    if (sym != nullptr) {
      return reinterpret_cast<Fn>(sym);
    }
  }
  return nullptr;
}

double bytesToMiB(unsigned long long bytes) {
  if (bytes == kNVMLValueNotAvailableU64) return -1.0;
  return static_cast<double>(bytes) / (1024.0 * 1024.0);
}

double clampPercent(double value) {
  if (value < 0.0) return -1.0;
  if (value > 100.0) return 100.0;
  return value;
}

struct NVMLApi {
  ScopedLibrary lib;

  nvmlReturn_t (*init_v2)() = nullptr;
  nvmlReturn_t (*shutdown)() = nullptr;
  nvmlReturn_t (*system_get_driver_version)(char*, unsigned int) = nullptr;
  nvmlReturn_t (*device_get_count_v2)(unsigned int*) = nullptr;
  nvmlReturn_t (*device_get_handle_by_index_v2)(unsigned int, nvmlDevice_t*) = nullptr;
  nvmlReturn_t (*device_get_uuid)(nvmlDevice_t, char*, unsigned int) = nullptr;
  nvmlReturn_t (*device_get_name)(nvmlDevice_t, char*, unsigned int) = nullptr;
  nvmlReturn_t (*device_get_pci_info_v3)(nvmlDevice_t, nvmlPciInfo_t*) = nullptr;
  nvmlReturn_t (*device_get_utilization_rates)(nvmlDevice_t, nvmlUtilization_t*) = nullptr;
  nvmlReturn_t (*device_get_memory_info)(nvmlDevice_t, nvmlMemory_t*) = nullptr;
  nvmlReturn_t (*device_get_bar1_memory_info)(nvmlDevice_t, nvmlBAR1Memory_t*) = nullptr;
  nvmlReturn_t (*device_get_temperature)(nvmlDevice_t, unsigned int, unsigned int*) = nullptr;
  nvmlReturn_t (*device_get_power_usage)(nvmlDevice_t, unsigned int*) = nullptr;
  nvmlReturn_t (*device_get_power_limit)(nvmlDevice_t, unsigned int*) = nullptr;
  nvmlReturn_t (*device_get_clock_info)(nvmlDevice_t, unsigned int, unsigned int*) = nullptr;
  nvmlReturn_t (*device_get_fan_speed)(nvmlDevice_t, unsigned int*) = nullptr;
  nvmlReturn_t (*device_get_curr_pcie_gen)(nvmlDevice_t, unsigned int*) = nullptr;
  nvmlReturn_t (*device_get_curr_pcie_width)(nvmlDevice_t, unsigned int*) = nullptr;
  nvmlReturn_t (*device_get_max_pcie_gen)(nvmlDevice_t, unsigned int*) = nullptr;
  nvmlReturn_t (*device_get_max_pcie_width)(nvmlDevice_t, unsigned int*) = nullptr;
  nvmlReturn_t (*device_get_pcie_throughput)(nvmlDevice_t, unsigned int, unsigned int*) = nullptr;
  nvmlReturn_t (*device_get_total_ecc_errors)(nvmlDevice_t, unsigned int, unsigned int,
                                              unsigned long long*) = nullptr;
  nvmlReturn_t (*device_get_compute_running_processes_v1)(nvmlDevice_t, unsigned int*,
                                                          nvmlProcessInfo_v1_t*) = nullptr;
  nvmlReturn_t (*device_get_compute_running_processes_v2)(nvmlDevice_t, unsigned int*,
                                                          nvmlProcessInfo_v2_t*) = nullptr;
};

bool loadNVMLApi(NVMLApi* api) {
  if (api == nullptr) return false;
  const std::array<const char*, 2> libs = {"libnvidia-ml.so.1", "libnvidia-ml.so"};
  for (const char* name : libs) {
    api->lib.reset(dlopen(name, RTLD_LAZY | RTLD_LOCAL));
    if (api->lib) {
      break;
    }
  }
  if (!api->lib) {
    return false;
  }

  void* handle = api->lib.get();
  api->init_v2 = loadSymbol<nvmlReturn_t (*)()>(handle, {"nvmlInit_v2", "nvmlInit"});
  api->shutdown = loadSymbol<nvmlReturn_t (*)()>(handle, {"nvmlShutdown"});
  api->system_get_driver_version =
      loadSymbol<nvmlReturn_t (*)(char*, unsigned int)>(handle, {"nvmlSystemGetDriverVersion"});
  api->device_get_count_v2 =
      loadSymbol<nvmlReturn_t (*)(unsigned int*)>(handle, {"nvmlDeviceGetCount_v2",
                                                           "nvmlDeviceGetCount"});
  api->device_get_handle_by_index_v2 =
      loadSymbol<nvmlReturn_t (*)(unsigned int, nvmlDevice_t*)>(
          handle, {"nvmlDeviceGetHandleByIndex_v2", "nvmlDeviceGetHandleByIndex"});
  api->device_get_uuid = loadSymbol<nvmlReturn_t (*)(nvmlDevice_t, char*, unsigned int)>(
      handle, {"nvmlDeviceGetUUID"});
  api->device_get_name = loadSymbol<nvmlReturn_t (*)(nvmlDevice_t, char*, unsigned int)>(
      handle, {"nvmlDeviceGetName"});
  api->device_get_pci_info_v3 =
      loadSymbol<nvmlReturn_t (*)(nvmlDevice_t, nvmlPciInfo_t*)>(
          handle, {"nvmlDeviceGetPciInfo_v3", "nvmlDeviceGetPciInfo"});
  api->device_get_utilization_rates =
      loadSymbol<nvmlReturn_t (*)(nvmlDevice_t, nvmlUtilization_t*)>(
          handle, {"nvmlDeviceGetUtilizationRates"});
  api->device_get_memory_info = loadSymbol<nvmlReturn_t (*)(nvmlDevice_t, nvmlMemory_t*)>(
      handle, {"nvmlDeviceGetMemoryInfo_v2", "nvmlDeviceGetMemoryInfo"});
  api->device_get_bar1_memory_info =
      loadSymbol<nvmlReturn_t (*)(nvmlDevice_t, nvmlBAR1Memory_t*)>(
          handle, {"nvmlDeviceGetBAR1MemoryInfo_v2", "nvmlDeviceGetBAR1MemoryInfo"});
  api->device_get_temperature =
      loadSymbol<nvmlReturn_t (*)(nvmlDevice_t, unsigned int, unsigned int*)>(
          handle, {"nvmlDeviceGetTemperature"});
  api->device_get_power_usage =
      loadSymbol<nvmlReturn_t (*)(nvmlDevice_t, unsigned int*)>(handle,
                                                                {"nvmlDeviceGetPowerUsage"});
  api->device_get_power_limit =
      loadSymbol<nvmlReturn_t (*)(nvmlDevice_t, unsigned int*)>(
          handle, {"nvmlDeviceGetEnforcedPowerLimit", "nvmlDeviceGetPowerManagementLimit"});
  api->device_get_clock_info =
      loadSymbol<nvmlReturn_t (*)(nvmlDevice_t, unsigned int, unsigned int*)>(
          handle, {"nvmlDeviceGetClockInfo"});
  api->device_get_fan_speed =
      loadSymbol<nvmlReturn_t (*)(nvmlDevice_t, unsigned int*)>(handle,
                                                                {"nvmlDeviceGetFanSpeed"});
  api->device_get_curr_pcie_gen =
      loadSymbol<nvmlReturn_t (*)(nvmlDevice_t, unsigned int*)>(
          handle, {"nvmlDeviceGetCurrPcieLinkGeneration"});
  api->device_get_curr_pcie_width =
      loadSymbol<nvmlReturn_t (*)(nvmlDevice_t, unsigned int*)>(
          handle, {"nvmlDeviceGetCurrPcieLinkWidth"});
  api->device_get_max_pcie_gen =
      loadSymbol<nvmlReturn_t (*)(nvmlDevice_t, unsigned int*)>(
          handle, {"nvmlDeviceGetMaxPcieLinkGeneration"});
  api->device_get_max_pcie_width =
      loadSymbol<nvmlReturn_t (*)(nvmlDevice_t, unsigned int*)>(
          handle, {"nvmlDeviceGetMaxPcieLinkWidth"});
  api->device_get_pcie_throughput =
      loadSymbol<nvmlReturn_t (*)(nvmlDevice_t, unsigned int, unsigned int*)>(
          handle, {"nvmlDeviceGetPcieThroughput"});
  api->device_get_total_ecc_errors =
      loadSymbol<nvmlReturn_t (*)(nvmlDevice_t, unsigned int, unsigned int, unsigned long long*)>(
          handle, {"nvmlDeviceGetTotalEccErrors"});
  api->device_get_compute_running_processes_v2 =
      loadSymbol<nvmlReturn_t (*)(nvmlDevice_t, unsigned int*, nvmlProcessInfo_v2_t*)>(
          handle, {"nvmlDeviceGetComputeRunningProcesses_v3",
                   "nvmlDeviceGetComputeRunningProcesses_v2"});
  api->device_get_compute_running_processes_v1 =
      loadSymbol<nvmlReturn_t (*)(nvmlDevice_t, unsigned int*, nvmlProcessInfo_v1_t*)>(
          handle, {"nvmlDeviceGetComputeRunningProcesses"});

  return api->init_v2 != nullptr && api->shutdown != nullptr &&
         api->device_get_count_v2 != nullptr && api->device_get_handle_by_index_v2 != nullptr;
}

template <typename T>
bool readUnsignedMetric(nvmlReturn_t (*fn)(nvmlDevice_t, T*), nvmlDevice_t device, double* out) {
  if (fn == nullptr || out == nullptr) return false;
  T value{};
  if (fn(device, &value) != kNVMLSuccess) return false;
  *out = static_cast<double>(value);
  return true;
}

template <typename T>
bool readClockMetric(nvmlReturn_t (*fn)(nvmlDevice_t, unsigned int, T*), nvmlDevice_t device,
                     unsigned int clock_type, double* out) {
  if (fn == nullptr || out == nullptr) return false;
  T value{};
  if (fn(device, clock_type, &value) != kNVMLSuccess) return false;
  *out = static_cast<double>(value);
  return true;
}

bool appendNVMLProcesses(const NVMLApi& api, nvmlDevice_t device, double device_total_mib,
                         const std::string& gpu_index, const std::string& gpu_uuid,
                         std::vector<GPUProcessSample>* out, double* process_count) {
  if (out == nullptr || process_count == nullptr) return false;
  *process_count = 0.0;

  if (api.device_get_compute_running_processes_v2 != nullptr) {
    unsigned int count = 0;
    nvmlReturn_t rc = api.device_get_compute_running_processes_v2(device, &count, nullptr);
    if (rc == kNVMLInsufficientSize && count > 0) {
      std::vector<nvmlProcessInfo_v2_t> infos(count);
      rc = api.device_get_compute_running_processes_v2(device, &count, infos.data());
      if (rc == kNVMLSuccess) {
        *process_count = static_cast<double>(count);
        for (unsigned int i = 0; i < count; ++i) {
          GPUProcessSample sample;
          sample.gpu_index = gpu_index;
          sample.gpu_uuid = gpu_uuid;
          sample.pid = std::to_string(infos[i].pid);
          sample.memory_used_mib = bytesToMiB(infos[i].usedGpuMemory);
          if (sample.memory_used_mib >= 0.0 && device_total_mib > 0.0) {
            sample.memory_util_percent = clampPercent((sample.memory_used_mib / device_total_mib) * 100.0);
          }
          out->push_back(std::move(sample));
        }
        return true;
      }
    } else if (rc == kNVMLSuccess) {
      return true;
    }
  }

  if (api.device_get_compute_running_processes_v1 == nullptr) {
    return false;
  }

  unsigned int count = 0;
  nvmlReturn_t rc = api.device_get_compute_running_processes_v1(device, &count, nullptr);
  if (rc != kNVMLInsufficientSize || count == 0) {
    return rc == kNVMLSuccess;
  }

  std::vector<nvmlProcessInfo_v1_t> infos(count);
  rc = api.device_get_compute_running_processes_v1(device, &count, infos.data());
  if (rc != kNVMLSuccess) {
    return false;
  }
  *process_count = static_cast<double>(count);
  for (unsigned int i = 0; i < count; ++i) {
    GPUProcessSample sample;
    sample.gpu_index = gpu_index;
    sample.gpu_uuid = gpu_uuid;
    sample.pid = std::to_string(infos[i].pid);
    sample.memory_used_mib = bytesToMiB(infos[i].usedGpuMemory);
    if (sample.memory_used_mib >= 0.0 && device_total_mib > 0.0) {
      sample.memory_util_percent = clampPercent((sample.memory_used_mib / device_total_mib) * 100.0);
    }
    out->push_back(std::move(sample));
  }
  return true;
}

}  // namespace

bool CollectNVMLSamples(GPUNVMLSamples* out) {
  if (out == nullptr) return false;
  out->devices.clear();
  out->processes.clear();

  NVMLApi api;
  if (!loadNVMLApi(&api)) {
    return false;
  }
  if (api.init_v2() != kNVMLSuccess) {
    return false;
  }

  const auto shutdown = [&api]() { api.shutdown(); };

  unsigned int count = 0;
  if (api.device_get_count_v2(&count) != kNVMLSuccess || count == 0) {
    shutdown();
    return false;
  }

  std::string driver_version;
  if (api.system_get_driver_version != nullptr) {
    char buf[96] = {0};
    if (api.system_get_driver_version(buf, sizeof(buf)) == kNVMLSuccess) {
      driver_version = buf;
    }
  }

  out->devices.reserve(count);
  for (unsigned int index = 0; index < count; ++index) {
    nvmlDevice_t device = nullptr;
    if (api.device_get_handle_by_index_v2(index, &device) != kNVMLSuccess || device == nullptr) {
      continue;
    }

    GPUDeviceSample sample;
    sample.index = std::to_string(index);
    sample.driver_version = driver_version;

    if (api.device_get_uuid != nullptr) {
      char uuid[96] = {0};
      if (api.device_get_uuid(device, uuid, sizeof(uuid)) == kNVMLSuccess) {
        sample.uuid = uuid;
      }
    }
    if (api.device_get_name != nullptr) {
      char name[96] = {0};
      if (api.device_get_name(device, name, sizeof(name)) == kNVMLSuccess) {
        sample.name = name;
      }
    }
    if (api.device_get_pci_info_v3 != nullptr) {
      nvmlPciInfo_t pci{};
      if (api.device_get_pci_info_v3(device, &pci) == kNVMLSuccess) {
        sample.pci_bus_id = pci.busId[0] != '\0' ? pci.busId : pci.busIdLegacy;
      }
    }

    if (api.device_get_utilization_rates != nullptr) {
      nvmlUtilization_t util{};
      if (api.device_get_utilization_rates(device, &util) == kNVMLSuccess) {
        sample.util_sm_percent = static_cast<double>(util.gpu);
        sample.util_mem_percent = static_cast<double>(util.memory);
      }
    }

    if (api.device_get_memory_info != nullptr) {
      nvmlMemory_t mem{};
      if (api.device_get_memory_info(device, &mem) == kNVMLSuccess) {
        sample.memory_total_mib = bytesToMiB(mem.total);
        sample.memory_used_mib = bytesToMiB(mem.used);
        sample.memory_free_mib = bytesToMiB(mem.free);
        if (sample.memory_total_mib > 0.0 && sample.memory_used_mib >= 0.0) {
          sample.memory_pressure_percent =
              clampPercent((sample.memory_used_mib / sample.memory_total_mib) * 100.0);
        }
      }
    }

    if (api.device_get_bar1_memory_info != nullptr) {
      nvmlBAR1Memory_t bar1{};
      if (api.device_get_bar1_memory_info(device, &bar1) == kNVMLSuccess) {
        sample.bar1_total_mib = bytesToMiB(bar1.bar1Total);
        sample.bar1_used_mib = bytesToMiB(bar1.bar1Used);
        sample.bar1_free_mib = bytesToMiB(bar1.bar1Free);
      }
    }

    if (api.device_get_temperature != nullptr) {
      unsigned int value = 0;
      if (api.device_get_temperature(device, kNVMLTemperatureGPU, &value) == kNVMLSuccess) {
        sample.temperature_celsius = static_cast<double>(value);
      }
    }
    if (api.device_get_power_usage != nullptr) {
      unsigned int value = 0;
      if (api.device_get_power_usage(device, &value) == kNVMLSuccess) {
        sample.power_draw_watts = static_cast<double>(value) / 1000.0;
      }
    }
    if (api.device_get_power_limit != nullptr) {
      unsigned int value = 0;
      if (api.device_get_power_limit(device, &value) == kNVMLSuccess) {
        sample.power_limit_watts = static_cast<double>(value) / 1000.0;
      }
    }

    readClockMetric(api.device_get_clock_info, device, kNVMLClockGraphics,
                    &sample.clock_graphics_mhz);
    readClockMetric(api.device_get_clock_info, device, kNVMLClockSM, &sample.clock_sm_mhz);
    readClockMetric(api.device_get_clock_info, device, kNVMLClockMem, &sample.clock_memory_mhz);
    readClockMetric(api.device_get_clock_info, device, kNVMLClockVideo, &sample.clock_video_mhz);
    readUnsignedMetric(api.device_get_fan_speed, device, &sample.fan_speed_percent);
    readUnsignedMetric(api.device_get_curr_pcie_gen, device, &sample.pcie_gen);
    readUnsignedMetric(api.device_get_curr_pcie_width, device, &sample.pcie_width);
    readUnsignedMetric(api.device_get_max_pcie_gen, device, &sample.pcie_gen_max);
    readUnsignedMetric(api.device_get_max_pcie_width, device, &sample.pcie_width_max);

    if (api.device_get_pcie_throughput != nullptr) {
      unsigned int value = 0;
      if (api.device_get_pcie_throughput(device, kNVMLPcieUtilRxBytes, &value) == kNVMLSuccess) {
        sample.pcie_rx_mb_s = static_cast<double>(value) / 1024.0;
      }
      if (api.device_get_pcie_throughput(device, kNVMLPcieUtilTxBytes, &value) == kNVMLSuccess) {
        sample.pcie_tx_mb_s = static_cast<double>(value) / 1024.0;
      }
    }

    if (api.device_get_total_ecc_errors != nullptr) {
      unsigned long long value = 0;
      if (api.device_get_total_ecc_errors(device, kNVMLMemoryErrorCorrected, kNVMLAggregateECC,
                                          &value) == kNVMLSuccess) {
        sample.ecc_single_bit_errors_total = static_cast<double>(value);
      }
      if (api.device_get_total_ecc_errors(device, kNVMLMemoryErrorUncorrected, kNVMLAggregateECC,
                                          &value) == kNVMLSuccess) {
        sample.ecc_double_bit_errors_total = static_cast<double>(value);
      }
    }

    appendNVMLProcesses(api, device, sample.memory_total_mib, sample.index, sample.uuid,
                        &out->processes, &sample.process_count);
    sample.context_count = sample.process_count;

    out->devices.push_back(std::move(sample));
  }

  shutdown();
  return !out->devices.empty();
}

}  // namespace probe_core
