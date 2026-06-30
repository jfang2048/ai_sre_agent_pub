#ifndef AI_SRE_AGENT_PROBE_CORE_NETWORK_KERNEL_COLLECTOR_H_
#define AI_SRE_AGENT_PROBE_CORE_NETWORK_KERNEL_COLLECTOR_H_

#include <cstdint>
#include <string>
#include <unordered_map>

namespace probe_core {

struct NetSnapshot {
  uint64_t rx_bytes = 0;
  uint64_t rx_packets = 0;
  uint64_t rx_errs = 0;
  uint64_t rx_drop = 0;
  uint64_t tx_bytes = 0;
  uint64_t tx_packets = 0;
  uint64_t tx_errs = 0;
  uint64_t tx_drop = 0;
};

struct NetlinkLinkData {
  std::unordered_map<std::string, NetSnapshot> stats;
  std::unordered_map<std::string, uint64_t> tx_queue_len;
  bool ok = false;
};

struct SocketQueue {
  uint64_t tx_queue = 0;
  uint64_t rx_queue = 0;
};

using SocketQueueMap = std::unordered_map<uint64_t, SocketQueue>;

NetlinkLinkData readNetlinkLinkData();
SocketQueueMap readSocketQueuesByInode();

}  // namespace probe_core

#endif  // AI_SRE_AGENT_PROBE_CORE_NETWORK_KERNEL_COLLECTOR_H_
