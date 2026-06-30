#ifndef AI_SRE_AGENT_PROBE_CORE_PROCESS_KERNEL_COLLECTOR_H_
#define AI_SRE_AGENT_PROBE_CORE_PROCESS_KERNEL_COLLECTOR_H_

#include <cstdint>
#include <string>
#include <unordered_map>
#include <vector>

namespace probe_core {

struct ProcessKernelSample {
  int pid = 0;
  std::string name;
  double cpu_percent = 0.0;
  uint64_t rss_bytes = 0;
  double io_read_bps = 0.0;
  double io_write_bps = 0.0;
  uint64_t voluntary_ctx = 0;
  uint64_t nonvoluntary_ctx = 0;
  uint64_t minor_faults = 0;
  uint64_t major_faults = 0;
  double sched_run_seconds_total = 0.0;
  double sched_wait_seconds_total = 0.0;
  double sched_wait_ratio = 0.0;
  double block_io_delay_seconds_total = 0.0;
  double block_io_delay_seconds_per_second = 0.0;
  uint64_t pss_bytes = 0;
  uint64_t net_connections = 0;
  uint64_t net_tx_queue_bytes = 0;
  uint64_t net_rx_queue_bytes = 0;
  bool used_taskstats = false;
  bool used_proc_fallback = false;
};

struct ProcessKernelCollectorOptions {
  int topk = 20;
  int watchlist_limit = 256;
  long clock_ticks = 100;
  int online_cpus = 1;
};

class ProcessKernelCollector {
 public:
  explicit ProcessKernelCollector(ProcessKernelCollectorOptions options);
  ~ProcessKernelCollector();

  void noteEBPFActivity(int pid, const std::string& comm);
  void noteEBPFResourceSnapshot(int pid, uint64_t cpu_user_ms, uint64_t cpu_sys_ms,
                                uint64_t rss_bytes);
  void reconcileFromProc();
  bool available() const;
  const std::string& failureReason() const;

  std::vector<ProcessKernelSample> collect(double elapsed_seconds,
                                           bool lightweight_enrichment);

 private:
  struct Impl;
  Impl* impl_;
};

}  // namespace probe_core

#endif  // AI_SRE_AGENT_PROBE_CORE_PROCESS_KERNEL_COLLECTOR_H_
