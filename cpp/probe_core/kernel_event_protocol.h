#ifndef AI_SRE_AGENT_PROBE_CORE_KERNEL_EVENT_PROTOCOL_H_
#define AI_SRE_AGENT_PROBE_CORE_KERNEL_EVENT_PROTOCOL_H_

#include <cstdint>
#include <string>
#include <string_view>

namespace probe_core {

struct ParsedEBPFEvent {
  std::string category;
  std::string type;
  int pid = -1;
  uint64_t bytes = 0;
  uint64_t latency_ns = 0;
  uint64_t cpu_user_ms = 0;
  uint64_t cpu_sys_ms = 0;
  uint64_t rss_bytes = 0;
  bool has_resource_snapshot = false;
};

bool parseKernelEventPayload(std::string_view payload, ParsedEBPFEvent* out);

}  // namespace probe_core

#endif  // AI_SRE_AGENT_PROBE_CORE_KERNEL_EVENT_PROTOCOL_H_
