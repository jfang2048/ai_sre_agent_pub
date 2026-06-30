#ifndef AI_SRE_AGENT_PROBE_CORE_PROCFS_FALLBACK_COLLECTOR_H_
#define AI_SRE_AGENT_PROBE_CORE_PROCFS_FALLBACK_COLLECTOR_H_

#include <cstdint>
#include <string>
#include <unordered_map>
#include <unordered_set>

namespace probe_core {

struct ProcFallbackSnapshot {
  int pid = 0;
  std::string name;
  uint64_t cpu_total = 0;
  uint64_t rss_bytes = 0;
  uint64_t read_bytes = 0;
  uint64_t write_bytes = 0;
  uint64_t voluntary_ctx = 0;
  uint64_t nonvoluntary_ctx = 0;
  uint64_t minor_faults = 0;
  uint64_t major_faults = 0;
  uint64_t sched_run_ns = 0;
  uint64_t sched_wait_ns = 0;
  uint64_t blkio_delay_ticks = 0;
};

std::unordered_map<int, ProcFallbackSnapshot> collectProcProcessSnapshots();
std::unordered_set<uint64_t> listSocketInodesForPidFallback(int pid);
uint64_t readPssBytesFallback(int pid);
std::string readProcessCommFallback(int pid);

}  // namespace probe_core

#endif  // AI_SRE_AGENT_PROBE_CORE_PROCFS_FALLBACK_COLLECTOR_H_
