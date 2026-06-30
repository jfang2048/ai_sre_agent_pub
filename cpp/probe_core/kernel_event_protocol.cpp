#include "kernel_event_protocol.h"

#include <cerrno>
#include <algorithm>
#include <cctype>
#include <cstdlib>
#include <cstring>
#include <string>

namespace probe_core {
namespace {

constexpr uint32_t kKernelEventMagic = 0x41535245U;  // "ERSA"
constexpr uint16_t kKernelEventVersion = 1;

enum class KernelEventRecordKind : uint16_t {
  kRuntimeEvent = 1,
};

struct KernelEventRecordV1 {
  uint32_t magic;
  uint16_t version;
  uint16_t kind;
  uint32_t size;
  int64_t timestamp_unix_nano;
  int32_t pid;
  uint32_t reserved;
  uint64_t bytes;
  uint64_t latency_ns;
  uint64_t cpu_user_ms;
  uint64_t cpu_sys_ms;
  uint64_t rss_bytes;
  char category[16];
  char type[24];
};

static_assert(sizeof(KernelEventRecordV1) == 112,
              "KernelEventRecordV1 size must stay stable");

std::string trimCopy(std::string value) {
  value.erase(value.begin(),
              std::find_if(value.begin(), value.end(), [](unsigned char c) {
                return !std::isspace(c);
              }));
  value.erase(std::find_if(value.rbegin(), value.rend(), [](unsigned char c) {
                return !std::isspace(c);
              }).base(),
              value.end());
  return value;
}

uint64_t parseU64(const std::string& s) {
  if (s.empty()) return 0;
  char* end = nullptr;
  errno = 0;
  const unsigned long long v = strtoull(s.c_str(), &end, 10);
  if (errno != 0 || end == s.c_str()) return 0;
  return static_cast<uint64_t>(v);
}

bool extractJSONString(std::string_view payload, std::string_view key, std::string* out) {
  const std::string needle = "\"" + std::string(key) + "\"";
  size_t pos = payload.find(needle);
  if (pos == std::string_view::npos) return false;
  pos = payload.find(':', pos + needle.size());
  if (pos == std::string_view::npos) return false;
  pos++;
  while (pos < payload.size() &&
         std::isspace(static_cast<unsigned char>(payload[pos]))) pos++;
  if (pos >= payload.size() || payload[pos] != '"') return false;
  pos++;
  std::string value;
  value.reserve(32);
  while (pos < payload.size()) {
    const char c = payload[pos++];
    if (c == '"') {
      *out = value;
      return true;
    }
    if (c == '\\') {
      if (pos < payload.size()) {
        value.push_back(payload[pos++]);
      }
      continue;
    }
    value.push_back(c);
  }
  return false;
}

bool extractJSONUint64(std::string_view payload, std::string_view key, uint64_t* out) {
  const std::string needle = "\"" + std::string(key) + "\"";
  size_t pos = payload.find(needle);
  if (pos == std::string_view::npos) return false;
  pos = payload.find(':', pos + needle.size());
  if (pos == std::string_view::npos) return false;
  pos++;
  while (pos < payload.size() &&
         std::isspace(static_cast<unsigned char>(payload[pos]))) pos++;
  if (pos >= payload.size()) return false;
  const size_t start = pos;
  while (pos < payload.size() && payload[pos] >= '0' && payload[pos] <= '9') pos++;
  if (start == pos) return false;
  *out = parseU64(std::string(payload.substr(start, pos - start)));
  return true;
}

bool extractJSONInt(std::string_view payload, std::string_view key, int* out) {
  const std::string needle = "\"" + std::string(key) + "\"";
  size_t pos = payload.find(needle);
  if (pos == std::string_view::npos) return false;
  pos = payload.find(':', pos + needle.size());
  if (pos == std::string_view::npos) return false;
  pos++;
  while (pos < payload.size() &&
         std::isspace(static_cast<unsigned char>(payload[pos]))) pos++;
  if (pos >= payload.size()) return false;
  bool negative = false;
  if (payload[pos] == '-') {
    negative = true;
    pos++;
  }
  const size_t start = pos;
  while (pos < payload.size() && payload[pos] >= '0' && payload[pos] <= '9') pos++;
  if (start == pos) return false;
  int value = atoi(std::string(payload.substr(start, pos - start)).c_str());
  *out = negative ? -value : value;
  return true;
}

bool parseBinaryRecord(std::string_view payload, ParsedEBPFEvent* out) {
  if (payload.size() < sizeof(KernelEventRecordV1) || out == nullptr) {
    return false;
  }
  KernelEventRecordV1 wire{};
  memcpy(&wire, payload.data(), sizeof(wire));
  if (wire.magic != kKernelEventMagic || wire.version != kKernelEventVersion ||
      wire.kind != static_cast<uint16_t>(KernelEventRecordKind::kRuntimeEvent) ||
      wire.size != sizeof(KernelEventRecordV1)) {
    return false;
  }

  ParsedEBPFEvent event;
  event.pid = wire.pid;
  event.bytes = wire.bytes;
  event.latency_ns = wire.latency_ns;
  event.cpu_user_ms = wire.cpu_user_ms;
  event.cpu_sys_ms = wire.cpu_sys_ms;
  event.rss_bytes = wire.rss_bytes;
  event.has_resource_snapshot =
      wire.cpu_user_ms > 0 || wire.cpu_sys_ms > 0 || wire.rss_bytes > 0;
  event.category =
      trimCopy(std::string(wire.category, strnlen(wire.category, sizeof(wire.category))));
  event.type = trimCopy(std::string(wire.type, strnlen(wire.type, sizeof(wire.type))));
  if (event.category.empty()) event.category = "unknown";
  if (event.type.empty()) event.type = "event";
  *out = std::move(event);
  return true;
}

bool parseJSONRecord(std::string_view payload, ParsedEBPFEvent* out) {
  if (payload.empty() || out == nullptr) return false;
  ParsedEBPFEvent event;
  if (!extractJSONString(payload, "category", &event.category)) {
    event.category = "unknown";
  }
  if (!extractJSONString(payload, "type", &event.type)) {
    event.type = "event";
  }
  (void)extractJSONInt(payload, "pid", &event.pid);
  (void)extractJSONUint64(payload, "bytes", &event.bytes);
  (void)extractJSONUint64(payload, "latency_ns", &event.latency_ns);
  (void)extractJSONUint64(payload, "cpu_user_ms", &event.cpu_user_ms);
  (void)extractJSONUint64(payload, "cpu_sys_ms", &event.cpu_sys_ms);
  (void)extractJSONUint64(payload, "rss_bytes", &event.rss_bytes);
  event.has_resource_snapshot =
      event.cpu_user_ms > 0 || event.cpu_sys_ms > 0 || event.rss_bytes > 0;
  *out = std::move(event);
  return true;
}

}  // namespace

bool parseKernelEventPayload(std::string_view payload, ParsedEBPFEvent* out) {
  if (parseBinaryRecord(payload, out)) {
    return true;
  }
  return parseJSONRecord(payload, out);
}

}  // namespace probe_core
