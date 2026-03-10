#ifndef AI_SRE_AGENT_PROBE_CORE_SUBPROCESS_H_
#define AI_SRE_AGENT_PROBE_CORE_SUBPROCESS_H_

#include <string>
#include <vector>

namespace probe_core {

constexpr int kDefaultExternalCommandTimeoutMs = 1500;

std::string RunCommandCapture(const std::vector<std::string>& argv, int timeout_ms);

}  // namespace probe_core

#endif  // AI_SRE_AGENT_PROBE_CORE_SUBPROCESS_H_
