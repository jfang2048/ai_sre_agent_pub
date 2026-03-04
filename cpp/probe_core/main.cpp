#include <arpa/inet.h>
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <linux/ethtool.h>
#include <linux/if_link.h>
#include <linux/netlink.h>
#include <linux/perf_event.h>
#include <linux/rtnetlink.h>
#include <net/if.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <poll.h>
#include <sys/ioctl.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <sys/sysinfo.h>
#include <sys/types.h>
#include <sys/un.h>
#include <sys/wait.h>
#include <unistd.h>
#include <zlib.h>

#include <algorithm>
#include <atomic>
#include <cctype>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <deque>
#include <filesystem>
#include <fstream>
#include <functional>
#include <iomanip>
#include <iostream>
#include <mutex>
#include <optional>
#include <regex>
#include <set>
#include <sstream>
#include <string>
#include <thread>
#include <unordered_map>
#include <unordered_set>
#include <utility>
#include <vector>
#include <string_view>

#include "probeipc/v1/probeipc.pb.h"

namespace fs = std::filesystem;
using Clock = std::chrono::steady_clock;

namespace {

constexpr const char* kProcPath = "/proc";
constexpr const char* kProcStatPath = "/proc/stat";
constexpr const char* kProcMeminfoPath = "/proc/meminfo";
constexpr const char* kProcVMStatPath = "/proc/vmstat";
constexpr const char* kProcNetDevPath = "/proc/net/dev";
constexpr const char* kProcNetSNMPPath = "/proc/net/snmp";
constexpr const char* kProcNetSoftnetPath = "/proc/net/softnet_stat";
constexpr const char* kProcDiskstatsPath = "/proc/diskstats";
constexpr const char* kPressureMemPath = "/proc/pressure/memory";
constexpr const char* kPressureCPUPath = "/proc/pressure/cpu";
constexpr const char* kPressureIOPath = "/proc/pressure/io";
constexpr const char* kProcSelfCgroupPath = "/proc/self/cgroup";
constexpr const char* kCgroupRootPath = "/sys/fs/cgroup";

long pageSizeBytes() {
  static const long kPageSize = std::max<long>(sysconf(_SC_PAGESIZE), 4096);
  return kPageSize;
}

enum class HostCollectionMode { kAuto, kKernel, kProc };

struct Options {
  bool once = false;
  bool list_collectors = false;
  int interval_ms = 1000;
  int max_interval_ms = 5000;
  int process_interval_samples = 2;
  int host_proc_fallback_interval_samples = 10;
  int pressure_interval_samples = 3;
  int netlink_interval_samples = 2;
  int backoff_queue_high_percent = 75;
  int backoff_queue_low_percent = 20;
  int topk = 20;
  int window_samples = 6;
  int queue_depth = 16;
  int gpu_interval_samples = 5;
  bool gzip = false;
  HostCollectionMode host_mode = HostCollectionMode::kAuto;
  std::string ebpf_socket_path;
  std::unordered_set<std::string> collectors;
};

using MetricLabel = std::pair<std::string, std::string>;
using MetricLabels = std::vector<MetricLabel>;

struct MetricPoint {
  std::string name;
  double value = 0.0;
  MetricLabels labels;
};

struct ProcCounters {
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
  std::string name;
};

struct ProcRow {
  int pid = 0;
  std::string name;
  double cpu_percent = 0;
  uint64_t rss_bytes = 0;
  double io_read_bps = 0;
  double io_write_bps = 0;
  uint64_t voluntary_ctx = 0;
  uint64_t nonvoluntary_ctx = 0;
  uint64_t minor_faults = 0;
  uint64_t major_faults = 0;
  double sched_run_seconds_total = 0;
  double sched_wait_seconds_total = 0;
  double sched_wait_ratio = 0;
  double block_io_delay_seconds_total = 0;
  double block_io_delay_seconds_per_second = 0;
  uint64_t pss_bytes = 0;
  uint64_t net_connections = 0;
  uint64_t net_tx_queue_bytes = 0;
  uint64_t net_rx_queue_bytes = 0;
};

struct CPUSnapshot {
  uint64_t total = 0;
  uint64_t idle = 0;
  uint64_t user = 0;
  uint64_t system = 0;
  uint64_t iowait = 0;
  uint64_t irq = 0;
  uint64_t softirq = 0;
  uint64_t steal = 0;
  uint64_t ctxt_total = 0;
  uint64_t running = 0;
  uint64_t blocked = 0;
};

struct DiskSnapshot {
  uint64_t reads_completed = 0;
  uint64_t sectors_read = 0;
  uint64_t read_ms = 0;
  uint64_t writes_completed = 0;
  uint64_t sectors_written = 0;
  uint64_t write_ms = 0;
  uint64_t io_in_progress = 0;
  uint64_t io_ms = 0;
  uint64_t weighted_io_ms = 0;
};

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

struct RDMAPortSnapshot {
  uint64_t xmit_words = 0;
  uint64_t rcv_words = 0;
  uint64_t tx_packets = 0;
  uint64_t error_events = 0;
  uint64_t congestion_events = 0;
  uint64_t pfc_pause_frames = 0;
  uint64_t ecn_marked_packets = 0;
  double link_rate_gbps = 0.0;
};

struct SocketQueue {
  uint64_t tx_queue = 0;
  uint64_t rx_queue = 0;
};

struct NetlinkLinkData {
  std::unordered_map<std::string, NetSnapshot> stats;
  std::unordered_map<std::string, uint64_t> tx_queue_len;
  bool ok = false;
};

// ScopedFD keeps file-descriptor ownership explicit and local.
class ScopedFD {
 public:
  ScopedFD() = default;
  explicit ScopedFD(int fd) : fd_(fd) {}
  ~ScopedFD() { reset(); }

  ScopedFD(const ScopedFD&) = delete;
  ScopedFD& operator=(const ScopedFD&) = delete;

  ScopedFD(ScopedFD&& other) noexcept : fd_(other.release()) {}
  ScopedFD& operator=(ScopedFD&& other) noexcept {
    if (this != &other) {
      reset(other.release());
    }
    return *this;
  }

  bool valid() const { return fd_ >= 0; }
  int get() const { return fd_; }

  int release() {
    const int out = fd_;
    fd_ = -1;
    return out;
  }

  void reset(int next = -1) {
    if (fd_ >= 0) {
      close(fd_);
    }
    fd_ = next;
  }

 private:
  int fd_ = -1;
};

// ScopedDir keeps DIR* ownership local and deterministic.
class ScopedDir {
 public:
  ScopedDir() = default;
  explicit ScopedDir(DIR* dir) : dir_(dir) {}
  ~ScopedDir() { reset(); }

  ScopedDir(const ScopedDir&) = delete;
  ScopedDir& operator=(const ScopedDir&) = delete;

  ScopedDir(ScopedDir&& other) noexcept : dir_(other.release()) {}
  ScopedDir& operator=(ScopedDir&& other) noexcept {
    if (this != &other) {
      reset(other.release());
    }
    return *this;
  }

  bool valid() const { return dir_ != nullptr; }
  DIR* get() const { return dir_; }

  DIR* release() {
    DIR* out = dir_;
    dir_ = nullptr;
    return out;
  }

  void reset(DIR* next = nullptr) {
    if (dir_ != nullptr) {
      closedir(dir_);
    }
    dir_ = next;
  }

 private:
  DIR* dir_ = nullptr;
};

std::atomic<bool> g_shutdown_requested(false);

void handleSignal(int) { g_shutdown_requested.store(true); }

int64_t nowUnixNanos() {
  return std::chrono::duration_cast<std::chrono::nanoseconds>(
             std::chrono::system_clock::now().time_since_epoch())
      .count();
}

int64_t nowMonotonicNanos() {
  timespec ts{};
  clock_gettime(CLOCK_MONOTONIC, &ts);
  return static_cast<int64_t>(ts.tv_sec) * 1000000000LL + static_cast<int64_t>(ts.tv_nsec);
}

bool isNumeric(const std::string& s) {
  if (s.empty()) return false;
  for (char c : s) {
    if (c < '0' || c > '9') return false;
  }
  return true;
}

std::string trim(const std::string& in) {
  size_t start = 0;
  while (start < in.size() && std::isspace(static_cast<unsigned char>(in[start]))) start++;
  size_t end = in.size();
  while (end > start && std::isspace(static_cast<unsigned char>(in[end - 1]))) end--;
  return in.substr(start, end - start);
}

std::string toLower(std::string in) {
  std::transform(in.begin(), in.end(), in.begin(),
                 [](unsigned char c) { return static_cast<char>(std::tolower(c)); });
  return in;
}

std::vector<std::string> splitWS(const std::string& line) {
  std::istringstream iss(line);
  std::vector<std::string> out;
  std::string item;
  while (iss >> item) out.push_back(item);
  return out;
}

uint64_t parseU64(const std::string& s) {
  if (s.empty()) return 0;
  char* end = nullptr;
  errno = 0;
  unsigned long long v = strtoull(s.c_str(), &end, 10);
  if (errno != 0 || end == s.c_str()) return 0;
  return static_cast<uint64_t>(v);
}

double parseDouble(const std::string& s) {
  if (s.empty()) return 0.0;
  char* end = nullptr;
  errno = 0;
  double v = strtod(s.c_str(), &end);
  if (errno != 0 || end == s.c_str()) return 0.0;
  return v;
}

struct ParsedEBPFEvent {
  std::string category;
  std::string type;
  int pid = -1;
  uint64_t bytes = 0;
  uint64_t latency_ns = 0;
};

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

std::string ebpfEventKey(const std::string& category, const std::string& type) {
  const std::string c = category.empty() ? "unknown" : category;
  const std::string t = type.empty() ? "event" : type;
  return c + "|" + t;
}

bool parseEBPFEventPayload(std::string_view payload, ParsedEBPFEvent* out) {
  if (out == nullptr || payload.empty()) return false;
  ParsedEBPFEvent evt;
  if (!extractJSONString(payload, "category", &evt.category)) {
    evt.category = "unknown";
  }
  if (!extractJSONString(payload, "type", &evt.type)) {
    evt.type = "event";
  }
  (void)extractJSONInt(payload, "pid", &evt.pid);
  (void)extractJSONUint64(payload, "bytes", &evt.bytes);
  (void)extractJSONUint64(payload, "latency_ns", &evt.latency_ns);
  *out = std::move(evt);
  return true;
}

bool readFile(const std::string& path, std::string* out) {
  std::ifstream f(path);
  if (!f.is_open()) return false;
  std::ostringstream ss;
  ss << f.rdbuf();
  *out = ss.str();
  return true;
}

void addMetric(std::vector<MetricPoint>* metrics, const std::string& name, double value,
               MetricLabels labels = {}) {
  metrics->push_back(MetricPoint{name, value, std::move(labels)});
}

bool startsWith(const std::string& s, const std::string& prefix) {
  return s.size() >= prefix.size() && s.compare(0, prefix.size(), prefix) == 0;
}

std::string hostModeToString(HostCollectionMode mode) {
  switch (mode) {
    case HostCollectionMode::kKernel:
      return "kernel";
    case HostCollectionMode::kProc:
      return "proc";
    case HostCollectionMode::kAuto:
    default:
      return "auto";
  }
}

std::optional<HostCollectionMode> parseHostMode(const std::string& raw) {
  const std::string mode = toLower(trim(raw));
  if (mode == "auto") return HostCollectionMode::kAuto;
  if (mode == "kernel") return HostCollectionMode::kKernel;
  if (mode == "proc") return HostCollectionMode::kProc;
  return std::nullopt;
}

void collectPressureStream(std::istream& in, const std::string& metricPrefix,
                           const std::string& resource, std::vector<MetricPoint>* metrics) {
  std::string line;
  while (std::getline(in, line)) {
    auto fields = splitWS(line);
    if (fields.size() < 5) continue;
    const std::string scope = fields[0];
    double avg10 = 0.0, avg60 = 0.0, avg300 = 0.0, total = 0.0;
    for (size_t i = 1; i < fields.size(); ++i) {
      if (startsWith(fields[i], "avg10=")) avg10 = parseDouble(fields[i].substr(strlen("avg10=")));
      if (startsWith(fields[i], "avg60=")) avg60 = parseDouble(fields[i].substr(strlen("avg60=")));
      if (startsWith(fields[i], "avg300=")) avg300 = parseDouble(fields[i].substr(strlen("avg300=")));
      if (startsWith(fields[i], "total=")) total = parseDouble(fields[i].substr(strlen("total=")));
    }
    addMetric(metrics, metricPrefix + resource + "_" + scope + "_avg10", avg10);
    addMetric(metrics, metricPrefix + resource + "_" + scope + "_avg60", avg60);
    addMetric(metrics, metricPrefix + resource + "_" + scope + "_avg300", avg300);
    addMetric(metrics, metricPrefix + resource + "_" + scope + "_total", total);
  }
}

bool collectPressureFile(const std::string& path, const std::string& metricPrefix,
                         const std::string& resource, std::vector<MetricPoint>* metrics) {
  std::ifstream f(path);
  if (!f.is_open()) return false;
  collectPressureStream(f, metricPrefix, resource, metrics);
  return true;
}

std::string detectCgroupPath() {
  std::ifstream f(kProcSelfCgroupPath);
  if (!f.is_open()) return kCgroupRootPath;
  std::string line;
  while (std::getline(f, line)) {
    const size_t first = line.find(':');
    if (first == std::string::npos) continue;
    const size_t second = line.find(':', first + 1);
    if (second == std::string::npos) continue;
    const std::string controllers = line.substr(first + 1, second - first - 1);
    std::string rel = line.substr(second + 1);
    if (controllers.empty() || line.rfind("0::", 0) == 0) {
      rel = trim(rel);
      if (rel.empty() || rel == "/") return kCgroupRootPath;
      if (rel.front() != '/') rel.insert(rel.begin(), '/');
      return std::string(kCgroupRootPath) + rel;
    }
  }
  return kCgroupRootPath;
}

bool collectSysinfoHost(std::vector<MetricPoint>* metrics) {
  struct sysinfo info {};
  if (sysinfo(&info) != 0) return false;

  addMetric(metrics, "probe_core_loadavg_1m", static_cast<double>(info.loads[0]) / 65536.0);
  addMetric(metrics, "probe_core_loadavg_5m", static_cast<double>(info.loads[1]) / 65536.0);
  addMetric(metrics, "probe_core_loadavg_15m", static_cast<double>(info.loads[2]) / 65536.0);

  const uint64_t unit = info.mem_unit == 0 ? 1 : static_cast<uint64_t>(info.mem_unit);
  const uint64_t total = static_cast<uint64_t>(info.totalram) * unit;
  const uint64_t free = static_cast<uint64_t>(info.freeram) * unit;
  const uint64_t buffers = static_cast<uint64_t>(info.bufferram) * unit;
  const uint64_t available = free + buffers;
  const uint64_t used = total > available ? total - available : 0;

  if (total > 0) {
    addMetric(metrics, "probe_core_memory_total_bytes", static_cast<double>(total));
    addMetric(metrics, "probe_core_memory_available_bytes", static_cast<double>(available));
    addMetric(metrics, "probe_core_memory_used_bytes", static_cast<double>(used));
    addMetric(metrics, "probe_core_memory_used_percent",
              (static_cast<double>(used) / static_cast<double>(total)) * 100.0);
  }
  addMetric(metrics, "probe_core_memory_buffers_bytes", static_cast<double>(buffers));
  addMetric(metrics, "probe_core_swap_total_bytes", static_cast<double>(info.totalswap) * unit);
  addMetric(metrics, "probe_core_swap_free_bytes", static_cast<double>(info.freeswap) * unit);
  addMetric(metrics, "probe_core_sysinfo_processes", static_cast<double>(info.procs));
  return true;
}

std::optional<CPUSnapshot> readCPUSnapshot() {
  std::ifstream f(kProcStatPath);
  if (!f.is_open()) return std::nullopt;
  CPUSnapshot snap{};
  std::string line;
  while (std::getline(f, line)) {
    if (startsWith(line, "cpu ")) {
      auto fields = splitWS(line);
      if (fields.size() < 8) return std::nullopt;
      snap.user = parseU64(fields[1]) + parseU64(fields[2]);
      snap.system = parseU64(fields[3]);
      snap.idle = parseU64(fields[4]);
      snap.iowait = parseU64(fields[5]);
      snap.irq = parseU64(fields[6]);
      snap.softirq = parseU64(fields[7]);
      if (fields.size() > 8) snap.steal = parseU64(fields[8]);
      snap.total = snap.user + snap.system + snap.idle + snap.iowait + snap.irq + snap.softirq +
                   snap.steal;
      continue;
    }
    if (startsWith(line, "ctxt ")) {
      auto fields = splitWS(line);
      if (fields.size() >= 2) snap.ctxt_total = parseU64(fields[1]);
      continue;
    }
    if (startsWith(line, "procs_running ")) {
      auto fields = splitWS(line);
      if (fields.size() >= 2) snap.running = parseU64(fields[1]);
      continue;
    }
    if (startsWith(line, "procs_blocked ")) {
      auto fields = splitWS(line);
      if (fields.size() >= 2) snap.blocked = parseU64(fields[1]);
      continue;
    }
  }
  return snap;
}

void collectLoadAvg(std::vector<MetricPoint>* metrics) {
  std::ifstream f("/proc/loadavg");
  if (!f.is_open()) return;
  double l1 = 0, l5 = 0, l15 = 0;
  if (!(f >> l1 >> l5 >> l15)) return;
  addMetric(metrics, "probe_core_loadavg_1m", l1);
  addMetric(metrics, "probe_core_loadavg_5m", l5);
  addMetric(metrics, "probe_core_loadavg_15m", l15);
}

void collectMem(std::vector<MetricPoint>* metrics) {
  std::ifstream f(kProcMeminfoPath);
  if (!f.is_open()) return;
  std::unordered_map<std::string, uint64_t> kv;
  std::string line;
  while (std::getline(f, line)) {
    auto fields = splitWS(line);
    if (fields.size() < 2) continue;
    std::string key = fields[0];
    if (!key.empty() && key.back() == ':') key.pop_back();
    kv[key] = parseU64(fields[1]) * 1024ULL;
  }

  const uint64_t total = kv["MemTotal"];
  const uint64_t available = kv["MemAvailable"];
  if (total > 0) {
    addMetric(metrics, "probe_core_memory_total_bytes", static_cast<double>(total));
    addMetric(metrics, "probe_core_memory_available_bytes", static_cast<double>(available));
    addMetric(metrics, "probe_core_memory_used_bytes", static_cast<double>(total - available));
    addMetric(metrics, "probe_core_memory_used_percent",
              (static_cast<double>(total - available) / static_cast<double>(total)) * 100.0);
  }
  addMetric(metrics, "probe_core_memory_cached_bytes", static_cast<double>(kv["Cached"]));
  addMetric(metrics, "probe_core_memory_buffers_bytes", static_cast<double>(kv["Buffers"]));
  addMetric(metrics, "probe_core_swap_total_bytes", static_cast<double>(kv["SwapTotal"]));
  addMetric(metrics, "probe_core_swap_free_bytes", static_cast<double>(kv["SwapFree"]));
}

void collectVMStat(std::vector<MetricPoint>* metrics) {
  std::ifstream f(kProcVMStatPath);
  if (!f.is_open()) return;
  std::string key;
  uint64_t value = 0;
  while (f >> key >> value) {
    if (key == "pgfault") addMetric(metrics, "probe_core_vm_pgfault_total", static_cast<double>(value));
    if (key == "pgmajfault")
      addMetric(metrics, "probe_core_vm_pgmajfault_total", static_cast<double>(value));
    if (key == "pgscan_kswapd")
      addMetric(metrics, "probe_core_vm_pgscan_kswapd_total", static_cast<double>(value));
    if (key == "pgsteal_kswapd")
      addMetric(metrics, "probe_core_vm_pgsteal_kswapd_total", static_cast<double>(value));
  }
}

std::unordered_map<std::string, DiskSnapshot> readDiskstats() {
  std::unordered_map<std::string, DiskSnapshot> out;
  std::ifstream f(kProcDiskstatsPath);
  if (!f.is_open()) return out;
  std::string line;
  while (std::getline(f, line)) {
    auto fields = splitWS(line);
    if (fields.size() < 14) continue;
    const std::string dev = fields[2];
    if (startsWith(dev, "loop") || startsWith(dev, "ram")) continue;
    DiskSnapshot s{};
    s.reads_completed = parseU64(fields[3]);
    s.sectors_read = parseU64(fields[5]);
    s.read_ms = parseU64(fields[6]);
    s.writes_completed = parseU64(fields[7]);
    s.sectors_written = parseU64(fields[9]);
    s.write_ms = parseU64(fields[10]);
    s.io_in_progress = parseU64(fields[11]);
    s.io_ms = parseU64(fields[12]);
    s.weighted_io_ms = parseU64(fields[13]);
    out.emplace(dev, s);
  }
  return out;
}

std::unordered_map<std::string, DiskSnapshot> readDiskstatsSysfs(bool* ok) {
  if (ok != nullptr) *ok = false;
  std::unordered_map<std::string, DiskSnapshot> out;
  const fs::path root("/sys/block");
  std::error_code ec;
  if (!fs::exists(root, ec) || !fs::is_directory(root, ec)) {
    return out;
  }

  for (fs::directory_iterator it(root, fs::directory_options::skip_permission_denied, ec), end;
       it != end; it.increment(ec)) {
    if (ec) {
      ec.clear();
      continue;
    }
    const std::string dev = it->path().filename().string();
    if (startsWith(dev, "loop") || startsWith(dev, "ram")) continue;

    std::ifstream stat(it->path() / "stat");
    if (!stat.is_open()) continue;
    std::vector<std::string> fields;
    std::string token;
    while (stat >> token) fields.push_back(token);
    if (fields.size() < 11) continue;

    DiskSnapshot s{};
    s.reads_completed = parseU64(fields[0]);
    s.sectors_read = parseU64(fields[2]);
    s.read_ms = parseU64(fields[3]);
    s.writes_completed = parseU64(fields[4]);
    s.sectors_written = parseU64(fields[6]);
    s.write_ms = parseU64(fields[7]);
    s.io_in_progress = parseU64(fields[8]);
    s.io_ms = parseU64(fields[9]);
    s.weighted_io_ms = parseU64(fields[10]);
    out.emplace(dev, s);
  }

  if (ok != nullptr) *ok = !out.empty();
  return out;
}

uint64_t readU64File(const std::string& path) {
  std::ifstream f(path);
  if (!f.is_open()) return 0;
  std::string s;
  f >> s;
  return parseU64(s);
}

bool readU64FileMaybe(const std::string& path, uint64_t* value) {
  if (value == nullptr) return false;
  std::ifstream f(path);
  if (!f.is_open()) return false;
  std::string text;
  if (!(f >> text)) return false;
  *value = parseU64(text);
  return true;
}

bool readTokenFile(const std::string& path, std::string* token) {
  if (token == nullptr) return false;
  std::ifstream f(path);
  if (!f.is_open()) return false;
  std::string out;
  if (!(f >> out)) return false;
  *token = out;
  return true;
}

bool readCgroupLimitFile(const std::string& path, uint64_t* value, bool* unlimited) {
  if (value == nullptr || unlimited == nullptr) return false;
  std::string token;
  if (!readTokenFile(path, &token)) return false;
  token = trim(token);
  if (token == "max") {
    *value = 0;
    *unlimited = true;
    return true;
  }
  *value = parseU64(token);
  *unlimited = false;
  return true;
}

struct CgroupCPUStat {
  uint64_t usage_usec = 0;
  uint64_t user_usec = 0;
  uint64_t system_usec = 0;
  uint64_t nr_periods = 0;
  uint64_t nr_throttled = 0;
  uint64_t throttled_usec = 0;
  bool has_data = false;
};

bool readCgroupCPUStat(const std::string& path, CgroupCPUStat* stat) {
  if (stat == nullptr) return false;
  std::ifstream f(path);
  if (!f.is_open()) return false;
  std::string key;
  uint64_t value = 0;
  while (f >> key >> value) {
    if (key == "usage_usec") stat->usage_usec = value;
    if (key == "user_usec") stat->user_usec = value;
    if (key == "system_usec") stat->system_usec = value;
    if (key == "nr_periods") stat->nr_periods = value;
    if (key == "nr_throttled") stat->nr_throttled = value;
    if (key == "throttled_usec") stat->throttled_usec = value;
    stat->has_data = true;
  }
  return stat->has_data;
}

uint64_t counterDelta(uint64_t current, uint64_t previous) {
  if (current >= previous) {
    return current - previous;
  }
  return current;
}

bool readRDMAStateFile(const std::string& path, uint64_t* value) {
  if (value == nullptr) return false;
  std::string text;
  if (!readFile(path, &text)) return false;
  text = trim(text);
  if (text.empty()) return false;
  const size_t sep = text.find(':');
  const std::string head = sep == std::string::npos ? text : text.substr(0, sep);
  const uint64_t parsed = parseU64(trim(head));
  if (parsed == 0 && trim(head) != "0") return false;
  *value = parsed;
  return true;
}

bool readRDMALinkRateGbps(const std::string& path, double* value) {
  if (value == nullptr) return false;
  std::ifstream f(path);
  if (!f.is_open()) return false;
  std::string rate_token;
  f >> rate_token;
  if (rate_token.empty()) return false;
  const double rate = parseDouble(rate_token);
  if (rate <= 0.0) return false;
  *value = rate;
  return true;
}

bool isRDMACongestionCounter(const std::string& name) {
  if (name.empty()) return false;
  std::string lower;
  lower.reserve(name.size());
  for (char c : name) {
    lower.push_back(static_cast<char>(std::tolower(static_cast<unsigned char>(c))));
  }
  static const std::vector<std::string> keywords = {"cnp", "ecn", "cong", "pfc", "buffer"};
  for (const auto& keyword : keywords) {
    if (lower.find(keyword) != std::string::npos) {
      return true;
    }
  }
  return false;
}

NetlinkLinkData readNetlinkLinkData() {
  NetlinkLinkData out;
  ScopedFD fd(socket(AF_NETLINK, SOCK_RAW, NETLINK_ROUTE));
  if (!fd.valid()) return out;

  struct {
    nlmsghdr nlh;
    ifinfomsg ifm;
  } req {};
  req.nlh.nlmsg_len = NLMSG_LENGTH(sizeof(ifinfomsg));
  req.nlh.nlmsg_type = RTM_GETLINK;
  req.nlh.nlmsg_flags = NLM_F_REQUEST | NLM_F_DUMP;
  req.nlh.nlmsg_seq = 1;
  req.ifm.ifi_family = AF_UNSPEC;

  sockaddr_nl addr {};
  addr.nl_family = AF_NETLINK;
  if (sendto(fd.get(), &req, req.nlh.nlmsg_len, 0, reinterpret_cast<sockaddr*>(&addr),
             sizeof(addr)) < 0) {
    return out;
  }

  char buf[8192];
  while (true) {
    const ssize_t nread = recv(fd.get(), buf, sizeof(buf), 0);
    if (nread <= 0) break;

    ssize_t rem = nread;
    for (nlmsghdr* nh = reinterpret_cast<nlmsghdr*>(buf); NLMSG_OK(nh, rem);
         nh = NLMSG_NEXT(nh, rem)) {
      if (nh->nlmsg_type == NLMSG_DONE) {
        out.ok = true;
        return out;
      }
      if (nh->nlmsg_type == NLMSG_ERROR) {
        return NetlinkLinkData{};
      }
      if (nh->nlmsg_type != RTM_NEWLINK) continue;

      auto* ifi = reinterpret_cast<ifinfomsg*>(NLMSG_DATA(nh));
      (void)ifi;
      int len = IFLA_PAYLOAD(nh);
      std::string ifname;
      NetSnapshot net{};
      bool have_stats = false;
      uint64_t txqlen = 0;
      bool have_txqlen = false;

      for (rtattr* attr = IFLA_RTA(ifi); RTA_OK(attr, len); attr = RTA_NEXT(attr, len)) {
        if (attr->rta_type == IFLA_IFNAME) {
          ifname = reinterpret_cast<const char*>(RTA_DATA(attr));
          continue;
        }
        if (attr->rta_type == IFLA_TXQLEN && RTA_PAYLOAD(attr) >= sizeof(uint32_t)) {
          uint32_t raw = 0;
          memcpy(&raw, RTA_DATA(attr), sizeof(raw));
          txqlen = raw;
          have_txqlen = true;
          continue;
        }
        if (attr->rta_type == IFLA_STATS64 && RTA_PAYLOAD(attr) >= sizeof(rtnl_link_stats64)) {
          rtnl_link_stats64 stats {};
          memcpy(&stats, RTA_DATA(attr), sizeof(stats));
          net.rx_bytes = stats.rx_bytes;
          net.rx_packets = stats.rx_packets;
          net.rx_errs = stats.rx_errors;
          net.rx_drop = stats.rx_dropped;
          net.tx_bytes = stats.tx_bytes;
          net.tx_packets = stats.tx_packets;
          net.tx_errs = stats.tx_errors;
          net.tx_drop = stats.tx_dropped;
          have_stats = true;
          continue;
        }
        if (attr->rta_type == IFLA_STATS && RTA_PAYLOAD(attr) >= sizeof(rtnl_link_stats) && !have_stats) {
          rtnl_link_stats stats {};
          memcpy(&stats, RTA_DATA(attr), sizeof(stats));
          net.rx_bytes = stats.rx_bytes;
          net.rx_packets = stats.rx_packets;
          net.rx_errs = stats.rx_errors;
          net.rx_drop = stats.rx_dropped;
          net.tx_bytes = stats.tx_bytes;
          net.tx_packets = stats.tx_packets;
          net.tx_errs = stats.tx_errors;
          net.tx_drop = stats.tx_dropped;
          have_stats = true;
        }
      }

      if (ifname.empty()) continue;
      if (have_stats) {
        out.stats[ifname] = net;
      }
      if (have_txqlen) {
        out.tx_queue_len[ifname] = txqlen;
      }
    }
  }

  out.ok = !out.stats.empty() || !out.tx_queue_len.empty();
  return out;
}

std::unordered_map<std::string, NetSnapshot> readNetDev() {
  std::unordered_map<std::string, NetSnapshot> out;
  std::ifstream f(kProcNetDevPath);
  if (!f.is_open()) return out;
  std::string line;
  int line_no = 0;
  while (std::getline(f, line)) {
    line_no++;
    if (line_no <= 2) continue;
    auto colon = line.find(':');
    if (colon == std::string::npos) continue;
    std::string iface = trim(line.substr(0, colon));
    std::string rest = line.substr(colon + 1);
    auto fields = splitWS(rest);
    if (fields.size() < 16) continue;
    NetSnapshot s{};
    s.rx_bytes = parseU64(fields[0]);
    s.rx_packets = parseU64(fields[1]);
    s.rx_errs = parseU64(fields[2]);
    s.rx_drop = parseU64(fields[3]);
    s.tx_bytes = parseU64(fields[8]);
    s.tx_packets = parseU64(fields[9]);
    s.tx_errs = parseU64(fields[10]);
    s.tx_drop = parseU64(fields[11]);
    out.emplace(iface, s);
  }
  return out;
}

uint64_t parseTcpRetransSegs() {
  std::ifstream f(kProcNetSNMPPath);
  if (!f.is_open()) return 0;
  std::string headers;
  std::string values;
  while (std::getline(f, headers)) {
    if (!startsWith(headers, "Tcp:")) continue;
    if (!std::getline(f, values)) break;
    if (!startsWith(values, "Tcp:")) continue;
    auto h = splitWS(headers);
    auto v = splitWS(values);
    size_t n = std::min(h.size(), v.size());
    for (size_t i = 1; i < n; ++i) {
      if (h[i] == "RetransSegs") return parseU64(v[i]);
    }
  }
  return 0;
}

std::unordered_map<std::string, SocketQueue> readSocketQueueMap(const std::string& path) {
  std::unordered_map<std::string, SocketQueue> out;
  std::ifstream f(path);
  if (!f.is_open()) return out;
  std::string line;
  int line_no = 0;
  while (std::getline(f, line)) {
    line_no++;
    if (line_no <= 1) continue;
    auto fields = splitWS(trim(line));
    if (fields.size() < 10) continue;
    const std::string txrx = fields[4];
    const std::string inode = fields[9];
    auto sep = txrx.find(':');
    if (sep == std::string::npos) continue;
    const std::string tx_hex = txrx.substr(0, sep);
    const std::string rx_hex = txrx.substr(sep + 1);
    SocketQueue q{};
    q.tx_queue = strtoull(tx_hex.c_str(), nullptr, 16);
    q.rx_queue = strtoull(rx_hex.c_str(), nullptr, 16);
    out[inode] = q;
  }
  return out;
}

std::unordered_set<std::string> listSocketInodesForPid(int pid) {
  std::unordered_set<std::string> inodes;
  std::string fd_path = std::string(kProcPath) + "/" + std::to_string(pid) + "/fd";
  ScopedDir dir(opendir(fd_path.c_str()));
  if (!dir.valid()) return inodes;
  dirent* ent = nullptr;
  char link_buf[512];
  while ((ent = readdir(dir.get())) != nullptr) {
    if (ent->d_name[0] == '.') continue;
    std::string link_path = fd_path + "/" + ent->d_name;
    ssize_t n = readlink(link_path.c_str(), link_buf, sizeof(link_buf) - 1);
    if (n <= 0) continue;
    link_buf[n] = '\0';
    std::string target(link_buf);
    if (!startsWith(target, "socket:[")) continue;
    auto start = target.find('[');
    auto end = target.find(']');
    if (start == std::string::npos || end == std::string::npos || end <= start + 1) continue;
    inodes.insert(target.substr(start + 1, end - start - 1));
  }
  return inodes;
}

uint64_t readPssBytes(int pid) {
  std::ifstream f(std::string(kProcPath) + "/" + std::to_string(pid) + "/smaps_rollup");
  if (!f.is_open()) return 0;
  std::string line;
  while (std::getline(f, line)) {
    if (!startsWith(line, "Pss:")) continue;
    auto fields = splitWS(line);
    if (fields.size() < 2) return 0;
    return parseU64(fields[1]) * 1024ULL;
  }
  return 0;
}

bool parseProcStat(int pid, ProcCounters* out) {
  std::string path = std::string(kProcPath) + "/" + std::to_string(pid) + "/stat";
  std::string line;
  if (!readFile(path, &line)) return false;
  const auto l = line.find('(');
  const auto r = line.rfind(')');
  if (l == std::string::npos || r == std::string::npos || r <= l) return false;

  out->name = line.substr(l + 1, r - l - 1);
  std::string rest = line.substr(r + 1);
  auto fields = splitWS(rest);
  if (fields.size() < 22) return false;

  out->minor_faults = parseU64(fields[7]);   // minflt (field #10)
  out->major_faults = parseU64(fields[9]);   // majflt (field #12)
  const uint64_t utime = parseU64(fields[11]);  // field #14
  const uint64_t stime = parseU64(fields[12]);  // field #15
  out->cpu_total = utime + stime;
  const uint64_t rss_pages = parseU64(fields[21]);  // field #24
  out->rss_bytes = rss_pages * static_cast<uint64_t>(pageSizeBytes());
  if (fields.size() > 39) {  // delayacct_blkio_ticks (field #42)
    out->blkio_delay_ticks = parseU64(fields[39]);
  }
  return true;
}

void parseProcStatusCtx(int pid, ProcCounters* out) {
  std::ifstream f(std::string(kProcPath) + "/" + std::to_string(pid) + "/status");
  if (!f.is_open()) return;
  std::string line;
  while (std::getline(f, line)) {
    if (startsWith(line, "voluntary_ctxt_switches:")) {
      auto fields = splitWS(line);
      if (fields.size() >= 2) out->voluntary_ctx = parseU64(fields[1]);
      continue;
    }
    if (startsWith(line, "nonvoluntary_ctxt_switches:")) {
      auto fields = splitWS(line);
      if (fields.size() >= 2) out->nonvoluntary_ctx = parseU64(fields[1]);
      continue;
    }
  }
}

void parseProcSchedstat(int pid, ProcCounters* out) {
  std::ifstream f(std::string(kProcPath) + "/" + std::to_string(pid) + "/schedstat");
  if (!f.is_open()) return;
  uint64_t run_ns = 0;
  uint64_t wait_ns = 0;
  if (!(f >> run_ns >> wait_ns)) {
    return;
  }
  out->sched_run_ns = run_ns;
  out->sched_wait_ns = wait_ns;
}

void parseProcIO(int pid, ProcCounters* out) {
  std::ifstream f(std::string(kProcPath) + "/" + std::to_string(pid) + "/io");
  if (!f.is_open()) return;
  std::string line;
  while (std::getline(f, line)) {
    if (startsWith(line, "read_bytes:")) {
      auto fields = splitWS(line);
      if (fields.size() >= 2) out->read_bytes = parseU64(fields[1]);
      continue;
    }
    if (startsWith(line, "write_bytes:")) {
      auto fields = splitWS(line);
      if (fields.size() >= 2) out->write_bytes = parseU64(fields[1]);
      continue;
    }
  }
}

std::string runCommandCapture(const std::vector<std::string>& argv) {
  if (argv.empty() || argv[0].empty()) return {};

  int pipe_fds[2] = {-1, -1};
#ifdef O_CLOEXEC
  if (pipe2(pipe_fds, O_CLOEXEC) != 0) return {};
#else
  if (pipe(pipe_fds) != 0) return {};
#endif
  ScopedFD read_end(pipe_fds[0]);
  ScopedFD write_end(pipe_fds[1]);

  const pid_t pid = fork();
  if (pid < 0) return {};

  if (pid == 0) {
    if (dup2(write_end.get(), STDOUT_FILENO) < 0) _exit(127);
    ScopedFD devnull(open("/dev/null", O_WRONLY | O_CLOEXEC));
    if (devnull.valid()) {
      if (dup2(devnull.get(), STDERR_FILENO) < 0) _exit(127);
    }

    read_end.reset();
    write_end.reset();

    std::vector<char*> exec_argv;
    exec_argv.reserve(argv.size() + 1);
    for (const auto& arg : argv) {
      exec_argv.push_back(const_cast<char*>(arg.c_str()));
    }
    exec_argv.push_back(nullptr);

    execvp(exec_argv[0], exec_argv.data());
    _exit(127);
  }

  write_end.reset();

  std::string out;
  char buf[4096];
  while (true) {
    const ssize_t n = read(read_end.get(), buf, sizeof(buf));
    if (n > 0) {
      out.append(buf, static_cast<size_t>(n));
      continue;
    }
    if (n < 0 && errno == EINTR) {
      continue;
    }
    break;
  }
  read_end.reset();

  int status = 0;
  while (waitpid(pid, &status, 0) < 0) {
    if (errno == EINTR) continue;
    return {};
  }
  if (!WIFEXITED(status) || WEXITSTATUS(status) != 0) {
    return {};
  }
  return out;
}

std::vector<std::string> splitCSVLine(const std::string& in) {
  std::vector<std::string> out;
  std::string current;
  std::istringstream ss(in);
  while (std::getline(ss, current, ',')) out.push_back(trim(current));
  return out;
}

const std::vector<std::string>& collectorOrder() {
  static const std::vector<std::string> kCollectors = {
      "host", "disk", "network", "rdma", "netlink",
      "ethtool", "perf", "ebpf", "gpu", "process"};
  return kCollectors;
}

const std::unordered_set<std::string>& collectorSet() {
  static const std::unordered_set<std::string> kCollectors(collectorOrder().begin(),
                                                            collectorOrder().end());
  return kCollectors;
}

bool collectorEnabled(const Options& opts, const std::string& name) {
  if (opts.collectors.empty()) {
    return true;
  }
  return opts.collectors.find(name) != opts.collectors.end();
}

bool parseCollectorsArg(const std::string& raw, Options* opts) {
  if (opts == nullptr) {
    return false;
  }
  opts->collectors.clear();
  for (const auto& item : splitCSVLine(raw)) {
    const std::string collector = toLower(trim(item));
    if (collector.empty()) {
      continue;
    }
    if (collector == "all") {
      opts->collectors.clear();
      return true;
    }
    if (collectorSet().find(collector) == collectorSet().end()) {
      std::cerr << "unknown collector module: " << collector << "\n";
      return false;
    }
    opts->collectors.insert(collector);
  }
  if (opts->collectors.empty()) {
    std::cerr << "collector module list is empty\n";
    return false;
  }
  if (opts->collectors.find("process") != opts->collectors.end() &&
      opts->collectors.find("host") == opts->collectors.end()) {
    // Keep host enabled for process mode to preserve complete scheduler/context signal set.
    opts->collectors.insert("host");
  }
  return true;
}

bool gzipCompress(const std::string& input, std::string* output) {
  z_stream zs{};
  if (deflateInit2(&zs, Z_BEST_SPEED, Z_DEFLATED, 15 + 16, 8, Z_DEFAULT_STRATEGY) != Z_OK) {
    return false;
  }
  zs.next_in = reinterpret_cast<Bytef*>(const_cast<char*>(input.data()));
  zs.avail_in = static_cast<uInt>(input.size());

  constexpr size_t kChunk = 16384;
  std::string out;
  char tmp[kChunk];
  int ret = Z_OK;
  while (ret == Z_OK) {
    zs.next_out = reinterpret_cast<Bytef*>(tmp);
    zs.avail_out = kChunk;
    ret = deflate(&zs, Z_FINISH);
    if (out.size() < zs.total_out) {
      out.append(tmp, zs.total_out - out.size());
    }
  }
  deflateEnd(&zs);
  if (ret != Z_STREAM_END) return false;
  *output = std::move(out);
  return true;
}

long perfEventOpen(struct perf_event_attr* hw_event, pid_t pid, int cpu, int group_fd,
                   unsigned long flags) {
  return syscall(__NR_perf_event_open, hw_event, pid, cpu, group_fd, flags);
}

class ProbeCoreRuntime {
 public:
  explicit ProbeCoreRuntime(Options options)
      : opts_(std::move(options)),
        last_collect_(Clock::now()),
        last_gpu_collect_seq_(0),
        sample_seq_(0),
        dropped_frames_(0),
        ebpf_sock_fd_(-1),
        perf_last_ctx_switch_(0),
        perf_last_task_clock_ns_(0),
        perf_last_page_faults_(0),
        perf_last_major_page_faults_(0),
        netlink_cache_seq_(0),
        last_netlink_refresh_seq_(0),
        last_process_collect_seq_(0),
        last_proc_host_refresh_seq_(0),
        last_psi_refresh_seq_(0),
        last_cgroup_refresh_seq_(0),
        clock_ticks_(std::max<long>(sysconf(_SC_CLK_TCK), 1)),
        online_cpus_(std::max<int>(1, static_cast<int>(sysconf(_SC_NPROCESSORS_ONLN)))),
        base_interval_ms_(std::max(100, opts_.interval_ms)),
        effective_interval_ms_(std::max(100, opts_.interval_ms)),
        sampling_backoff_events_(0),
        cgroup_path_(detectCgroupPath()) {
    if (opts_.max_interval_ms < base_interval_ms_) {
      opts_.max_interval_ms = base_interval_ms_;
    }
    setupPerf();
    if (collectorEnabled(opts_, "ebpf")) {
      setupEBPFSocket();
    }
  }

  ~ProbeCoreRuntime() {
    stopEBPFReceiver();
    if (ebpf_sock_fd_ >= 0) close(ebpf_sock_fd_);
    closePerfCounters(perf_ctx_switch_fds_);
    closePerfCounters(perf_task_clock_fds_);
    closePerfCounters(perf_page_fault_fds_);
    closePerfCounters(perf_major_page_fault_fds_);
  }

  void runOnce() {
    auto frame = collectFrame();
    writeFrame(frame);
  }

  void runStream() {
    writer_thread_ = std::thread([this]() { writerLoop(); });
    collector_thread_ = std::thread([this]() { collectorLoop(); });

    collector_thread_.join();
    {
      std::lock_guard<std::mutex> lock(queue_mu_);
      stopping_ = true;
    }
    queue_cv_.notify_all();
    writer_thread_.join();
  }

 private:
  struct PerfCounterSnapshot {
    bool has_context_switches = false;
    bool has_task_clock = false;
    bool has_page_faults = false;
    bool has_major_page_faults = false;

    uint64_t context_switches = 0;
    uint64_t task_clock_ns = 0;
    uint64_t page_faults = 0;
    uint64_t major_page_faults = 0;
  };

  Options opts_;
  Clock::time_point last_collect_;
  uint32_t last_gpu_collect_seq_;
  uint32_t sample_seq_;
  std::atomic<uint32_t> dropped_frames_;
  bool stopping_ = false;
  std::mutex queue_mu_;
  std::condition_variable queue_cv_;
  std::deque<std::string> frame_queue_;
  std::thread collector_thread_;
  std::thread writer_thread_;
  std::thread ebpf_receiver_thread_;

  CPUSnapshot last_cpu_{};
  bool has_last_cpu_ = false;
  std::unordered_map<std::string, DiskSnapshot> last_disk_;
  std::unordered_map<std::string, NetSnapshot> last_net_;
  std::unordered_map<std::string, RDMAPortSnapshot> last_rdma_ports_;
  uint64_t last_retrans_ = 0;
  std::unordered_map<int, ProcCounters> last_proc_;
  std::mutex ebpf_mu_;
  std::atomic<uint64_t> total_ebpf_events_{0};
  std::atomic<uint64_t> total_ebpf_bytes_{0};
  std::unordered_map<std::string, uint64_t> ebpf_event_counts_;
  std::unordered_map<std::string, uint64_t> ebpf_event_bytes_;
  std::unordered_map<std::string, uint64_t> ebpf_event_latency_sum_ns_;
  std::unordered_map<std::string, uint64_t> ebpf_event_latency_count_;
  std::unordered_map<std::string, uint64_t> ebpf_category_counts_;
  std::unordered_map<int, uint64_t> ebpf_pid_event_counts_;
  std::unordered_map<int, uint64_t> ebpf_pid_bytes_;
  std::unordered_map<int, uint64_t> ebpf_pid_latency_sum_ns_;
  std::unordered_map<int, uint64_t> ebpf_pid_latency_count_;
  std::unordered_map<std::string, uint64_t> last_ebpf_event_counts_;
  std::unordered_map<std::string, uint64_t> last_ebpf_event_bytes_;
  std::unordered_map<int, uint64_t> last_ebpf_pid_event_counts_;
  std::unordered_map<int, uint64_t> last_ebpf_pid_bytes_;
  int64_t last_ebpf_emit_mono_ns_ = 0;
  std::vector<MetricPoint> cached_gpu_metrics_;
  std::vector<MetricPoint> cached_host_proc_metrics_;
  std::vector<MetricPoint> cached_psi_metrics_;
  std::vector<MetricPoint> cached_cgroup_metrics_;
  std::vector<MetricPoint> cached_ethtool_metrics_;
  std::vector<ProcRow> cached_process_rows_;
  NetlinkLinkData netlink_cache_;
  std::unordered_map<std::string, uint64_t> disk_queue_capacity_cache_;

  int ebpf_sock_fd_;
  std::vector<int> perf_ctx_switch_fds_;
  std::vector<int> perf_task_clock_fds_;
  std::vector<int> perf_page_fault_fds_;
  std::vector<int> perf_major_page_fault_fds_;
  uint64_t perf_last_ctx_switch_;
  uint64_t perf_last_task_clock_ns_;
  uint64_t perf_last_page_faults_;
  uint64_t perf_last_major_page_faults_;
  uint64_t perf_snapshot_seq_ = 0;
  PerfCounterSnapshot perf_snapshot_;
  uint64_t netlink_cache_seq_;
  uint32_t last_netlink_refresh_seq_;
  uint32_t last_process_collect_seq_;
  uint32_t last_proc_host_refresh_seq_;
  uint32_t last_psi_refresh_seq_;
  uint32_t last_cgroup_refresh_seq_;
  uint32_t last_ethtool_collect_seq_ = 0;
  uint32_t last_disk_queue_capacity_refresh_seq_ = 0;
  const long clock_ticks_;
  int online_cpus_;
  int base_interval_ms_;
  int effective_interval_ms_;
  uint64_t sampling_backoff_events_;
  std::string cgroup_path_;
  std::atomic<bool> ebpf_receiver_running_{false};

  void setupEBPFSocket() {
    if (opts_.ebpf_socket_path.empty()) return;
    ScopedFD sock(socket(AF_UNIX, SOCK_DGRAM, 0));
    if (!sock.valid()) return;

    sockaddr_un addr{};
    addr.sun_family = AF_UNIX;
    if (opts_.ebpf_socket_path.size() >= sizeof(addr.sun_path)) {
      return;
    }
    strncpy(addr.sun_path, opts_.ebpf_socket_path.c_str(), sizeof(addr.sun_path) - 1);
    if (connect(sock.get(), reinterpret_cast<sockaddr*>(&addr), sizeof(addr)) < 0) {
      return;
    }
    const int flags = fcntl(sock.get(), F_GETFL, 0);
    if (flags >= 0) {
      (void)fcntl(sock.get(), F_SETFL, flags | O_NONBLOCK);
    }
    ebpf_sock_fd_ = sock.release();
    startEBPFReceiver();
  }

  void startEBPFReceiver() {
    if (ebpf_sock_fd_ < 0) return;
    if (ebpf_receiver_running_.exchange(true)) return;
    ebpf_receiver_thread_ = std::thread([this]() { ebpfReceiverLoop(); });
  }

  void stopEBPFReceiver() {
    if (!ebpf_receiver_running_.exchange(false)) return;
    if (ebpf_sock_fd_ >= 0) {
      shutdown(ebpf_sock_fd_, SHUT_RD);
    }
    if (ebpf_receiver_thread_.joinable()) {
      ebpf_receiver_thread_.join();
    }
  }

  void ebpfReceiverLoop() {
    char buf[65536];
    while (ebpf_receiver_running_.load(std::memory_order_relaxed) && ebpf_sock_fd_ >= 0) {
      pollfd pfd {};
      pfd.fd = ebpf_sock_fd_;
      pfd.events = POLLIN;
      const int rc = poll(&pfd, 1, 5000);
      if (rc < 0) {
        if (errno == EINTR) continue;
        break;
      }
      if (rc == 0) {
        continue;
      }
      if ((pfd.revents & (POLLERR | POLLHUP | POLLNVAL)) != 0) {
        break;
      }
      if ((pfd.revents & POLLIN) == 0) {
        continue;
      }

      uint64_t event_count = 0;
      uint64_t byte_count = 0;
      while (true) {
        const ssize_t n = recv(ebpf_sock_fd_, buf, sizeof(buf), 0);
        if (n > 0) {
          ParsedEBPFEvent evt;
          if (parseEBPFEventPayload(std::string_view(buf, static_cast<size_t>(n)), &evt)) {
            recordEBPFEvent(evt);
          }
          event_count++;
          byte_count += static_cast<uint64_t>(n);
          continue;
        }
        if (n < 0 && errno == EINTR) {
          continue;
        }
        if (n < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) {
          break;
        }
        if (n == 0) {
          ebpf_receiver_running_.store(false, std::memory_order_relaxed);
        }
        break;
      }
      if (event_count > 0) {
        total_ebpf_events_.fetch_add(event_count, std::memory_order_relaxed);
      }
      if (byte_count > 0) {
        total_ebpf_bytes_.fetch_add(byte_count, std::memory_order_relaxed);
      }
      if (!ebpf_receiver_running_.load(std::memory_order_relaxed)) {
        break;
      }
    }
  }

  void recordEBPFEvent(const ParsedEBPFEvent& evt) {
    const std::string category = evt.category.empty() ? "unknown" : toLower(evt.category);
    const std::string type = evt.type.empty() ? "event" : toLower(evt.type);
    const std::string key = ebpfEventKey(category, type);

    std::lock_guard<std::mutex> lock(ebpf_mu_);
    ebpf_event_counts_[key]++;
    ebpf_category_counts_[category]++;
    if (evt.bytes > 0) {
      ebpf_event_bytes_[key] += evt.bytes;
    }
    if (evt.latency_ns > 0) {
      ebpf_event_latency_sum_ns_[key] += evt.latency_ns;
      ebpf_event_latency_count_[key]++;
    }
    if (evt.pid > 0) {
      ebpf_pid_event_counts_[evt.pid]++;
      if (evt.bytes > 0) {
        ebpf_pid_bytes_[evt.pid] += evt.bytes;
      }
      if (evt.latency_ns > 0) {
        ebpf_pid_latency_sum_ns_[evt.pid] += evt.latency_ns;
        ebpf_pid_latency_count_[evt.pid]++;
      }
    }
  }

  static void closePerfCounters(std::vector<int>& fds) {
    for (int fd : fds) {
      if (fd >= 0) close(fd);
    }
    fds.clear();
  }

  static std::vector<int> openPerfCountersForAllCPUs(uint64_t config) {
    std::vector<int> fds;
    const int cpus = std::max<int>(1, static_cast<int>(sysconf(_SC_NPROCESSORS_ONLN)));
    fds.reserve(cpus);
    for (int cpu = 0; cpu < cpus; ++cpu) {
      struct perf_event_attr pe {};
      pe.type = PERF_TYPE_SOFTWARE;
      pe.size = sizeof(struct perf_event_attr);
      pe.config = config;
      pe.disabled = 0;
      pe.exclude_hv = 1;
      pe.exclude_guest = 1;
      pe.read_format = 0;
      const int fd = static_cast<int>(perfEventOpen(&pe, -1, cpu, -1, PERF_FLAG_FD_CLOEXEC));
      if (fd >= 0) {
        fds.push_back(fd);
      }
    }
    return fds;
  }

  static bool readPerfCounterTotal(const std::vector<int>& fds, uint64_t* total) {
    if (total == nullptr || fds.empty()) return false;
    uint64_t sum = 0;
    bool any = false;
    for (int fd : fds) {
      uint64_t value = 0;
      if (read(fd, &value, sizeof(value)) != sizeof(value)) continue;
      sum += value;
      any = true;
    }
    if (!any) return false;
    *total = sum;
    return true;
  }

  void setupPerf() {
    perf_ctx_switch_fds_ = openPerfCountersForAllCPUs(PERF_COUNT_SW_CONTEXT_SWITCHES);
    perf_task_clock_fds_ = openPerfCountersForAllCPUs(PERF_COUNT_SW_TASK_CLOCK);
    perf_page_fault_fds_ = openPerfCountersForAllCPUs(PERF_COUNT_SW_PAGE_FAULTS);
    perf_major_page_fault_fds_ = openPerfCountersForAllCPUs(PERF_COUNT_SW_PAGE_FAULTS_MAJ);
    readPerfCounterTotal(perf_ctx_switch_fds_, &perf_last_ctx_switch_);
    readPerfCounterTotal(perf_task_clock_fds_, &perf_last_task_clock_ns_);
    readPerfCounterTotal(perf_page_fault_fds_, &perf_last_page_faults_);
    readPerfCounterTotal(perf_major_page_fault_fds_, &perf_last_major_page_faults_);
    perf_snapshot_seq_ = 0;
    perf_snapshot_ = {};
  }

  const PerfCounterSnapshot& getPerfCounterSnapshot() {
    if (perf_snapshot_seq_ == sample_seq_) {
      return perf_snapshot_;
    }

    PerfCounterSnapshot snapshot;
    snapshot.has_context_switches =
        readPerfCounterTotal(perf_ctx_switch_fds_, &snapshot.context_switches);
    snapshot.has_task_clock = readPerfCounterTotal(perf_task_clock_fds_, &snapshot.task_clock_ns);
    snapshot.has_page_faults = readPerfCounterTotal(perf_page_fault_fds_, &snapshot.page_faults);
    snapshot.has_major_page_faults =
        readPerfCounterTotal(perf_major_page_fault_fds_, &snapshot.major_page_faults);

    perf_snapshot_ = snapshot;
    perf_snapshot_seq_ = sample_seq_;
    return perf_snapshot_;
  }

  static std::string compressionToString(bool gzip) { return gzip ? "gzip" : "none"; }

  void collectorLoop() {
    while (!g_shutdown_requested.load()) {
      auto start = Clock::now();
      enqueueFrame(collectFrame());
      if (base_interval_ms_ <= 0) continue;
      const auto collect_elapsed =
          std::chrono::duration_cast<std::chrono::milliseconds>(Clock::now() - start);
      applyBackoff(collect_elapsed.count());
      const int64_t remaining = static_cast<int64_t>(effective_interval_ms_) - collect_elapsed.count();
      if (remaining > 0) {
        std::this_thread::sleep_for(std::chrono::milliseconds(remaining));
      }
    }
  }

  void applyBackoff(int64_t collect_ms) {
    const double high = std::clamp(static_cast<double>(opts_.backoff_queue_high_percent) / 100.0,
                                   0.10, 0.99);
    const double low = std::clamp(static_cast<double>(opts_.backoff_queue_low_percent) / 100.0,
                                  0.0, high - 0.05);
    double queue_ratio = 0.0;
    {
      std::lock_guard<std::mutex> lock(queue_mu_);
      if (opts_.queue_depth > 0) {
        queue_ratio = static_cast<double>(frame_queue_.size()) / static_cast<double>(opts_.queue_depth);
      }
    }

    const int max_interval = std::max(base_interval_ms_, opts_.max_interval_ms);
    const bool overloaded =
        queue_ratio >= high || collect_ms >= static_cast<int64_t>(effective_interval_ms_ * 0.8);
    if (overloaded) {
      const int grown =
          std::min(max_interval, std::max(base_interval_ms_, effective_interval_ms_ + effective_interval_ms_ / 4));
      if (grown > effective_interval_ms_) {
        effective_interval_ms_ = grown;
        sampling_backoff_events_++;
      }
      return;
    }

    if (effective_interval_ms_ > base_interval_ms_ && queue_ratio <= low &&
        collect_ms <= static_cast<int64_t>(effective_interval_ms_ * 0.45)) {
      effective_interval_ms_ =
          std::max(base_interval_ms_, effective_interval_ms_ - std::max(1, effective_interval_ms_ / 10));
    }
  }

  void writerLoop() {
    while (true) {
      std::string frame;
      {
        std::unique_lock<std::mutex> lock(queue_mu_);
        queue_cv_.wait(lock, [this]() { return stopping_ || !frame_queue_.empty(); });
        if (frame_queue_.empty() && stopping_) return;
        if (frame_queue_.empty()) continue;
        frame = std::move(frame_queue_.front());
        frame_queue_.pop_front();
      }
      writeFrame(frame);
    }
  }

  void enqueueFrame(std::string frame) {
    std::lock_guard<std::mutex> lock(queue_mu_);
    while (static_cast<int>(frame_queue_.size()) >= opts_.queue_depth && !frame_queue_.empty()) {
      frame_queue_.pop_front();
      dropped_frames_.fetch_add(1, std::memory_order_relaxed);
    }
    frame_queue_.push_back(std::move(frame));
    queue_cv_.notify_one();
  }

  void writeFrame(const std::string& frame) {
    if (frame.empty()) return;
    const uint32_t n = static_cast<uint32_t>(frame.size());
    char len[4];
    len[0] = static_cast<char>(n & 0xff);
    len[1] = static_cast<char>((n >> 8) & 0xff);
    len[2] = static_cast<char>((n >> 16) & 0xff);
    len[3] = static_cast<char>((n >> 24) & 0xff);
    std::cout.write(len, sizeof(len));
    std::cout.write(frame.data(), static_cast<std::streamsize>(frame.size()));
    std::cout.flush();
  }

  std::string collectFrame() {
    sample_seq_++;
    auto batch = collectBatch();

    std::string payload;
    if (!batch.SerializeToString(&payload)) {
      return {};
    }
    std::string encoded_payload;
    probeipc::v1::Compression compression = probeipc::v1::COMPRESSION_NONE;
    if (opts_.gzip) {
      std::string compressed;
      if (gzipCompress(payload, &compressed)) {
        encoded_payload.swap(compressed);
        compression = probeipc::v1::COMPRESSION_GZIP;
      } else {
        encoded_payload.swap(payload);
      }
    } else {
      encoded_payload.swap(payload);
    }

    probeipc::v1::FrameEnvelope env;
    env.set_compression(compression);
    env.mutable_payload()->swap(encoded_payload);
    const std::string& envelope_payload = env.payload();
    env.set_payload_crc32(crc32(0, reinterpret_cast<const Bytef*>(envelope_payload.data()),
                                static_cast<uInt>(envelope_payload.size())));

    std::string frame;
    env.SerializeToString(&frame);
    return frame;
  }

  bool shouldRefresh(uint32_t last_refresh_seq, int interval_samples) const {
    const uint32_t period = static_cast<uint32_t>(std::max(1, interval_samples));
    if (last_refresh_seq == 0) return true;
    return (sample_seq_ - last_refresh_seq) >= period;
  }

  void appendCachedMetrics(const std::vector<MetricPoint>& cached, std::vector<MetricPoint>* metrics) {
    for (const auto& metric : cached) {
      metrics->push_back(metric);
    }
  }

  const NetlinkLinkData& getNetlinkSnapshot() {
    if (netlink_cache_seq_ == sample_seq_) return netlink_cache_;
    if (netlink_cache_.stats.empty() ||
        shouldRefresh(last_netlink_refresh_seq_, opts_.netlink_interval_samples)) {
      netlink_cache_ = readNetlinkLinkData();
      last_netlink_refresh_seq_ = sample_seq_;
    }
    netlink_cache_seq_ = sample_seq_;
    return netlink_cache_;
  }

  void refreshProcHostSupplementCache() {
    std::vector<MetricPoint> supplement;
    supplement.reserve(32);

    std::ifstream meminfo(kProcMeminfoPath);
    if (meminfo.is_open()) {
      std::string line;
      while (std::getline(meminfo, line)) {
        auto fields = splitWS(line);
        if (fields.size() < 2) continue;
        if (startsWith(fields[0], "Cached:")) {
          addMetric(&supplement, "probe_core_memory_cached_bytes",
                    static_cast<double>(parseU64(fields[1]) * 1024ULL));
        }
        if (startsWith(fields[0], "Buffers:")) {
          addMetric(&supplement, "probe_core_memory_buffers_bytes",
                    static_cast<double>(parseU64(fields[1]) * 1024ULL));
        }
      }
    }

    collectVMStat(&supplement);
    auto cpu = readCPUSnapshot();
    if (cpu.has_value()) {
      const auto& snap = cpu.value();
      addMetric(&supplement, "probe_core_cpu_user_jiffies", static_cast<double>(snap.user));
      addMetric(&supplement, "probe_core_cpu_system_jiffies", static_cast<double>(snap.system));
      addMetric(&supplement, "probe_core_cpu_idle_jiffies", static_cast<double>(snap.idle + snap.iowait));
      addMetric(&supplement, "probe_core_sched_running_tasks", static_cast<double>(snap.running));
      addMetric(&supplement, "probe_core_sched_blocked_tasks", static_cast<double>(snap.blocked));
    }
    cached_host_proc_metrics_ = std::move(supplement);
  }

  void refreshPressureCache() {
    std::vector<MetricPoint> pressure;
    pressure.reserve(64);
    collectPressureFile(kPressureMemPath, "probe_core_pressure_", "memory", &pressure);
    collectPressureFile(kPressureCPUPath, "probe_core_pressure_", "cpu", &pressure);
    collectPressureFile(kPressureIOPath, "probe_core_pressure_", "io", &pressure);

    const std::string cpu_pressure = cgroup_path_ + "/cpu.pressure";
    const std::string mem_pressure = cgroup_path_ + "/memory.pressure";
    const std::string io_pressure = cgroup_path_ + "/io.pressure";
    collectPressureFile(cpu_pressure, "probe_core_cgroup_pressure_", "cpu", &pressure);
    collectPressureFile(mem_pressure, "probe_core_cgroup_pressure_", "memory", &pressure);
    collectPressureFile(io_pressure, "probe_core_cgroup_pressure_", "io", &pressure);
    cached_psi_metrics_ = std::move(pressure);
  }

  void refreshCgroupMetricsCache() {
    std::vector<MetricPoint> cgroup;
    cgroup.reserve(64);
    collectCgroupMetrics(&cgroup);
    cached_cgroup_metrics_ = std::move(cgroup);
  }

  void collectCgroupMetrics(std::vector<MetricPoint>* metrics) {
    if (cgroup_path_.empty()) return;
    addMetric(metrics, "probe_core_cgroup_detected", 1.0, {{"path", cgroup_path_}});

    uint64_t mem_current = 0;
    if (readU64FileMaybe(cgroup_path_ + "/memory.current", &mem_current)) {
      addMetric(metrics, "probe_core_cgroup_memory_current_bytes", static_cast<double>(mem_current));
    }

    uint64_t memory_limit = 0;
    bool memory_unlimited = false;
    if (readCgroupLimitFile(cgroup_path_ + "/memory.max", &memory_limit, &memory_unlimited)) {
      if (memory_unlimited) {
        addMetric(metrics, "probe_core_cgroup_memory_limit_unlimited", 1.0);
      } else {
        addMetric(metrics, "probe_core_cgroup_memory_limit_bytes", static_cast<double>(memory_limit));
      }
    }

    uint64_t swap_current = 0;
    if (readU64FileMaybe(cgroup_path_ + "/memory.swap.current", &swap_current)) {
      addMetric(metrics, "probe_core_cgroup_swap_current_bytes", static_cast<double>(swap_current));
    }

    uint64_t swap_limit = 0;
    bool swap_unlimited = false;
    if (readCgroupLimitFile(cgroup_path_ + "/memory.swap.max", &swap_limit, &swap_unlimited)) {
      if (swap_unlimited) {
        addMetric(metrics, "probe_core_cgroup_swap_limit_unlimited", 1.0);
      } else {
        addMetric(metrics, "probe_core_cgroup_swap_limit_bytes", static_cast<double>(swap_limit));
      }
    }

    CgroupCPUStat cpu_stat{};
    if (readCgroupCPUStat(cgroup_path_ + "/cpu.stat", &cpu_stat)) {
      addMetric(metrics, "probe_core_cgroup_cpu_usage_seconds_total", cpu_stat.usage_usec / 1e6);
      addMetric(metrics, "probe_core_cgroup_cpu_user_seconds_total", cpu_stat.user_usec / 1e6);
      addMetric(metrics, "probe_core_cgroup_cpu_system_seconds_total",
                cpu_stat.system_usec / 1e6);
      addMetric(metrics, "probe_core_cgroup_cpu_periods_total",
                static_cast<double>(cpu_stat.nr_periods));
      addMetric(metrics, "probe_core_cgroup_cpu_throttled_periods_total",
                static_cast<double>(cpu_stat.nr_throttled));
      addMetric(metrics, "probe_core_cgroup_cpu_throttled_seconds_total",
                cpu_stat.throttled_usec / 1e6);
      if (cpu_stat.nr_periods > 0) {
        addMetric(metrics, "probe_core_cgroup_cpu_throttled_ratio",
                  std::clamp(static_cast<double>(cpu_stat.nr_throttled) /
                                 static_cast<double>(cpu_stat.nr_periods),
                             0.0, 1.0));
      }
    }

    std::ifstream io_stat(cgroup_path_ + "/io.stat");
    if (io_stat.is_open()) {
      uint64_t rbytes = 0;
      uint64_t wbytes = 0;
      uint64_t rios = 0;
      uint64_t wios = 0;
      std::string line;
      while (std::getline(io_stat, line)) {
        auto fields = splitWS(line);
        for (size_t i = 1; i < fields.size(); ++i) {
          const auto pos = fields[i].find('=');
          if (pos == std::string::npos) continue;
          const std::string key = fields[i].substr(0, pos);
          const uint64_t val = parseU64(fields[i].substr(pos + 1));
          if (key == "rbytes") rbytes += val;
          if (key == "wbytes") wbytes += val;
          if (key == "rios") rios += val;
          if (key == "wios") wios += val;
        }
      }
      addMetric(metrics, "probe_core_cgroup_io_read_bytes_total", static_cast<double>(rbytes));
      addMetric(metrics, "probe_core_cgroup_io_write_bytes_total", static_cast<double>(wbytes));
      addMetric(metrics, "probe_core_cgroup_io_read_ops_total", static_cast<double>(rios));
      addMetric(metrics, "probe_core_cgroup_io_write_ops_total", static_cast<double>(wios));
    }
  }

  bool collectKernelHostMetrics(double elapsed, std::vector<MetricPoint>* metrics) {
    const bool sysinfo_ok = collectSysinfoHost(metrics);
    const PerfCounterSnapshot& perf = getPerfCounterSnapshot();

    bool cpu_ok = false;
    if (perf.has_task_clock) {
      cpu_ok = true;
      addMetric(metrics, "probe_core_cpu_task_clock_seconds_total", perf.task_clock_ns / 1e9);
      if (perf.task_clock_ns >= perf_last_task_clock_ns_ && perf_last_task_clock_ns_ > 0 &&
          elapsed > 0.0) {
        const double delta_ns = static_cast<double>(perf.task_clock_ns - perf_last_task_clock_ns_);
        const double busy_seconds = delta_ns / 1e9;
        const double capacity_seconds =
            elapsed * static_cast<double>(std::max(1, online_cpus_));
        if (capacity_seconds > 0.0) {
          addMetric(metrics, "probe_core_cpu_usage_percent",
                    std::clamp((busy_seconds / capacity_seconds) * 100.0, 0.0, 100.0));
        }
      }
      perf_last_task_clock_ns_ = perf.task_clock_ns;
    }

    if (perf.has_context_switches) {
      addMetric(metrics, "probe_core_sched_context_switches_total",
                static_cast<double>(perf.context_switches));
    }

    if (perf.has_page_faults) {
      addMetric(metrics, "probe_core_vm_pgfault_total", static_cast<double>(perf.page_faults));
    }

    if (perf.has_major_page_faults) {
      addMetric(metrics, "probe_core_vm_pgmajfault_total",
                static_cast<double>(perf.major_page_faults));
    }

    addMetric(metrics, "probe_core_host_kernel_primary_available", (sysinfo_ok && cpu_ok) ? 1.0 : 0.0);
    return sysinfo_ok && cpu_ok;
  }

  void collectProcHostFallback(double elapsed, std::vector<MetricPoint>* metrics) {
    collectLoadAvg(metrics);
    collectMem(metrics);
    collectVMStat(metrics);
    collectPressureFile(kPressureMemPath, "probe_core_pressure_", "memory", metrics);
    collectPressureFile(kPressureCPUPath, "probe_core_pressure_", "cpu", metrics);
    collectPressureFile(kPressureIOPath, "probe_core_pressure_", "io", metrics);

    auto cur = readCPUSnapshot();
    if (!cur.has_value()) return;
    const auto& cpu = cur.value();
    addMetric(metrics, "probe_core_cpu_user_jiffies", static_cast<double>(cpu.user));
    addMetric(metrics, "probe_core_cpu_system_jiffies", static_cast<double>(cpu.system));
    addMetric(metrics, "probe_core_cpu_idle_jiffies", static_cast<double>(cpu.idle + cpu.iowait));
    addMetric(metrics, "probe_core_sched_context_switches_total", static_cast<double>(cpu.ctxt_total));
    addMetric(metrics, "probe_core_sched_running_tasks", static_cast<double>(cpu.running));
    addMetric(metrics, "probe_core_sched_blocked_tasks", static_cast<double>(cpu.blocked));
    if (has_last_cpu_ && cpu.total > last_cpu_.total && elapsed > 0.0) {
      const double delta_total = static_cast<double>(cpu.total - last_cpu_.total);
      const double delta_idle =
          static_cast<double>((cpu.idle + cpu.iowait) - (last_cpu_.idle + last_cpu_.iowait));
      if (delta_total > 0.0) {
        const double usage =
            std::clamp((delta_total - delta_idle) / delta_total * 100.0, 0.0, 100.0);
        addMetric(metrics, "probe_core_cpu_usage_percent", usage);
        addMetric(metrics, "probe_core_cpu_jiffies_delta_per_sec", delta_total / elapsed);
      }
    }
    last_cpu_ = cpu;
    has_last_cpu_ = true;
  }

  void collectCoreHostMetrics(double elapsed, std::vector<MetricPoint>* metrics) {
    const bool force_proc = opts_.host_mode == HostCollectionMode::kProc;
    bool kernel_ok = false;
    if (!force_proc) {
      kernel_ok = collectKernelHostMetrics(elapsed, metrics);
    }

    if (kernel_ok) {
      if (shouldRefresh(last_proc_host_refresh_seq_, opts_.host_proc_fallback_interval_samples) ||
          cached_host_proc_metrics_.empty()) {
        refreshProcHostSupplementCache();
        last_proc_host_refresh_seq_ = sample_seq_;
      }
      if (shouldRefresh(last_psi_refresh_seq_, opts_.pressure_interval_samples) ||
          cached_psi_metrics_.empty()) {
        refreshPressureCache();
        last_psi_refresh_seq_ = sample_seq_;
      }
      if (shouldRefresh(last_cgroup_refresh_seq_, opts_.pressure_interval_samples) ||
          cached_cgroup_metrics_.empty()) {
        refreshCgroupMetricsCache();
        last_cgroup_refresh_seq_ = sample_seq_;
      }
      appendCachedMetrics(cached_host_proc_metrics_, metrics);
      appendCachedMetrics(cached_psi_metrics_, metrics);
      appendCachedMetrics(cached_cgroup_metrics_, metrics);
      addMetric(metrics, "probe_core_host_collection_source", 1.0, {{"source", "kernel"}});
      addMetric(metrics, "probe_core_host_proc_refresh_interval_samples",
                static_cast<double>(std::max(1, opts_.host_proc_fallback_interval_samples)));
      addMetric(metrics, "probe_core_host_proc_refresh_age_samples",
                static_cast<double>(sample_seq_ - last_proc_host_refresh_seq_));
      addMetric(metrics, "probe_core_cgroup_refresh_interval_samples",
                static_cast<double>(std::max(1, opts_.pressure_interval_samples)));
      addMetric(metrics, "probe_core_cgroup_refresh_age_samples",
                static_cast<double>(sample_seq_ - last_cgroup_refresh_seq_));
      return;
    }

    collectProcHostFallback(elapsed, metrics);
    if (shouldRefresh(last_cgroup_refresh_seq_, opts_.pressure_interval_samples) ||
        cached_cgroup_metrics_.empty()) {
      refreshCgroupMetricsCache();
      last_cgroup_refresh_seq_ = sample_seq_;
    }
    appendCachedMetrics(cached_cgroup_metrics_, metrics);
    addMetric(metrics, "probe_core_host_collection_source", 1.0, {{"source", "proc"}});
    addMetric(metrics, "probe_core_cgroup_refresh_interval_samples",
              static_cast<double>(std::max(1, opts_.pressure_interval_samples)));
    addMetric(metrics, "probe_core_cgroup_refresh_age_samples",
              static_cast<double>(sample_seq_ - last_cgroup_refresh_seq_));
  }

  void refreshDiskQueueCapacityCache(const std::unordered_map<std::string, DiskSnapshot>& disks) {
    for (const auto& [dev, _] : disks) {
      const uint64_t nr_req = readU64File("/sys/block/" + dev + "/queue/nr_requests");
      if (nr_req > 0) {
        disk_queue_capacity_cache_[dev] = nr_req;
      }
    }
    last_disk_queue_capacity_refresh_seq_ = sample_seq_;
  }

  void collectDiskMetrics(double elapsed, std::vector<MetricPoint>* metrics) {
    bool used_sysfs = false;
    auto cur = readDiskstatsSysfs(&used_sysfs);
    if (!used_sysfs || cur.empty()) {
      cur = readDiskstats();
    }
    addMetric(metrics, "probe_core_disk_collection_source", 1.0,
              {{"source", (used_sysfs && !cur.empty()) ? "sysfs" : "proc"}});
    if (shouldRefresh(last_disk_queue_capacity_refresh_seq_, 30) ||
        disk_queue_capacity_cache_.empty()) {
      refreshDiskQueueCapacityCache(cur);
    }

    for (const auto& [dev, snap] : cur) {
      const auto it = last_disk_.find(dev);
      if (it == last_disk_.end()) continue;
      const auto& prev = it->second;
      const double read_bytes =
          static_cast<double>(counterDelta(snap.sectors_read, prev.sectors_read)) * 512.0;
      const double write_bytes =
          static_cast<double>(counterDelta(snap.sectors_written, prev.sectors_written)) * 512.0;
      const double read_ops =
          static_cast<double>(counterDelta(snap.reads_completed, prev.reads_completed));
      const double write_ops =
          static_cast<double>(counterDelta(snap.writes_completed, prev.writes_completed));
      const double read_ms = static_cast<double>(counterDelta(snap.read_ms, prev.read_ms));
      const double write_ms = static_cast<double>(counterDelta(snap.write_ms, prev.write_ms));
      const double await_ms =
          (read_ops + write_ops) > 0.0 ? (read_ms + write_ms) / (read_ops + write_ops) : 0.0;

      addMetric(metrics, "probe_core_disk_read_bytes_per_sec", read_bytes / elapsed, {{"device", dev}});
      addMetric(metrics, "probe_core_disk_write_bytes_per_sec", write_bytes / elapsed, {{"device", dev}});
      addMetric(metrics, "probe_core_disk_reads_per_sec", read_ops / elapsed, {{"device", dev}});
      addMetric(metrics, "probe_core_disk_writes_per_sec", write_ops / elapsed, {{"device", dev}});
      addMetric(metrics, "probe_core_disk_await_ms", await_ms, {{"device", dev}});
      addMetric(metrics, "probe_core_disk_queue_depth", static_cast<double>(snap.io_in_progress),
                {{"device", dev}});

      const auto cap_it = disk_queue_capacity_cache_.find(dev);
      if (cap_it != disk_queue_capacity_cache_.end() && cap_it->second > 0) {
        addMetric(metrics, "probe_core_disk_queue_capacity",
                  static_cast<double>(cap_it->second), {{"device", dev}});
      }
    }
    last_disk_ = std::move(cur);
  }

  void collectNetworkMetrics(double elapsed, std::vector<MetricPoint>* metrics) {
    const auto& netlink = getNetlinkSnapshot();
    auto cur = netlink.stats;
    const bool use_netlink = netlink.ok && !cur.empty();
    if (!use_netlink) {
      cur = readNetDev();
    }
    addMetric(metrics, "probe_core_network_collection_source", 1.0,
              {{"source", use_netlink ? "netlink" : "proc"}});

    for (const auto& [iface, snap] : cur) {
      const auto it = last_net_.find(iface);
      if (it == last_net_.end()) continue;
      const auto& prev = it->second;
      addMetric(metrics, "probe_core_network_rx_bytes_per_sec",
                static_cast<double>(counterDelta(snap.rx_bytes, prev.rx_bytes)) / elapsed,
                {{"iface", iface}});
      addMetric(metrics, "probe_core_network_tx_bytes_per_sec",
                static_cast<double>(counterDelta(snap.tx_bytes, prev.tx_bytes)) / elapsed,
                {{"iface", iface}});
      addMetric(metrics, "probe_core_network_rx_packets_per_sec",
                static_cast<double>(counterDelta(snap.rx_packets, prev.rx_packets)) / elapsed,
                {{"iface", iface}});
      addMetric(metrics, "probe_core_network_tx_packets_per_sec",
                static_cast<double>(counterDelta(snap.tx_packets, prev.tx_packets)) / elapsed,
                {{"iface", iface}});
      addMetric(metrics, "probe_core_network_rx_drops_total", static_cast<double>(snap.rx_drop),
                {{"iface", iface}});
      addMetric(metrics, "probe_core_network_tx_drops_total", static_cast<double>(snap.tx_drop),
                {{"iface", iface}});
      addMetric(metrics, "probe_core_network_rx_errors_total", static_cast<double>(snap.rx_errs),
                {{"iface", iface}});
      addMetric(metrics, "probe_core_network_tx_errors_total", static_cast<double>(snap.tx_errs),
                {{"iface", iface}});
    }
    last_net_ = std::move(cur);

    const uint64_t retrans = parseTcpRetransSegs();
    addMetric(metrics, "probe_core_network_tcp_retransmissions_total", static_cast<double>(retrans));
    if (retrans >= last_retrans_ && elapsed > 0) {
      addMetric(metrics, "probe_core_network_tcp_retransmissions_per_sec",
                static_cast<double>(retrans - last_retrans_) / elapsed);
    }
    last_retrans_ = retrans;

    std::ifstream softnet(kProcNetSoftnetPath);
    if (softnet.is_open()) {
      std::string line;
      uint64_t dropped = 0;
      uint64_t squeezed = 0;
      while (std::getline(softnet, line)) {
        auto fields = splitWS(line);
        if (fields.size() < 3) continue;
        dropped += strtoull(fields[1].c_str(), nullptr, 16);
        squeezed += strtoull(fields[2].c_str(), nullptr, 16);
      }
      addMetric(metrics, "probe_core_network_softnet_dropped_total", static_cast<double>(dropped));
      addMetric(metrics, "probe_core_network_softnet_squeezed_total", static_cast<double>(squeezed));
    }
  }

  probeipc::v1::ProbeBatch collectBatch() {
    probeipc::v1::ProbeBatch batch;
    batch.set_collected_at_unix_nano(nowUnixNanos());
    batch.set_monotonic_unix_nano(nowMonotonicNanos());
    batch.set_window_samples(static_cast<uint32_t>(opts_.window_samples));
    batch.set_sequence(sample_seq_);
    batch.set_dropped_due_backpressure(dropped_frames_.load(std::memory_order_relaxed));

    std::vector<MetricPoint> metrics;
    metrics.reserve(1200);

    auto now = Clock::now();
    double elapsed = std::chrono::duration<double>(now - last_collect_).count();
    if (elapsed <= 0) elapsed = 1.0;
    last_collect_ = now;

    netlink_cache_seq_ = 0;
    if (collectorEnabled(opts_, "host")) {
      collectCoreHostMetrics(elapsed, &metrics);
    }
    if (collectorEnabled(opts_, "disk")) {
      collectDiskMetrics(elapsed, &metrics);
    }
    if (collectorEnabled(opts_, "network")) {
      collectNetworkMetrics(elapsed, &metrics);
    }
    if (collectorEnabled(opts_, "rdma")) {
      collectRDMAMetrics(elapsed, &metrics);
    }
    if (collectorEnabled(opts_, "netlink")) {
      collectNetlinkMetrics(&metrics);
    }
    if (collectorEnabled(opts_, "ethtool")) {
      collectEthtoolMetrics(&metrics);
    }
    if (collectorEnabled(opts_, "perf")) {
      collectPerfMetrics(&metrics);
    }
    if (collectorEnabled(opts_, "ebpf")) {
      collectEBPFMetrics(&metrics);
    }
    if (collectorEnabled(opts_, "gpu")) {
      collectGPUMetrics(&metrics);
    }

    std::vector<ProcRow> processes;
    if (collectorEnabled(opts_, "process")) {
      const bool should_collect = shouldRefresh(last_process_collect_seq_, opts_.process_interval_samples) ||
                                  cached_process_rows_.empty();
      if (should_collect) {
        cached_process_rows_ = collectProcessMetrics(elapsed, &metrics);
        last_process_collect_seq_ = sample_seq_;
      } else {
        for (const auto& row : cached_process_rows_) {
          emitProcessRowMetrics(row, &metrics);
        }
        addMetric(&metrics, "probe_core_process_sampling_skipped", 1.0);
      }
      processes = cached_process_rows_;
      addMetric(&metrics, "probe_core_process_sampling_interval_samples",
                static_cast<double>(std::max(1, opts_.process_interval_samples)));
      addMetric(&metrics, "probe_core_process_sampling_age_samples",
                static_cast<double>(sample_seq_ - last_process_collect_seq_));
    }

    addMetric(&metrics, "probe_core_sampling_base_interval_ms", static_cast<double>(base_interval_ms_));
    addMetric(&metrics, "probe_core_sampling_effective_interval_ms",
              static_cast<double>(effective_interval_ms_));
    addMetric(&metrics, "probe_core_sampling_backoff_events_total",
              static_cast<double>(sampling_backoff_events_));
    addMetric(&metrics, "probe_core_sampling_backoff_queue_high_percent",
              static_cast<double>(opts_.backoff_queue_high_percent));
    addMetric(&metrics, "probe_core_sampling_backoff_queue_low_percent",
              static_cast<double>(opts_.backoff_queue_low_percent));
    addMetric(&metrics, "probe_core_netlink_refresh_interval_samples",
              static_cast<double>(std::max(1, opts_.netlink_interval_samples)));
    addMetric(&metrics, "probe_core_netlink_refresh_age_samples",
              static_cast<double>(sample_seq_ - last_netlink_refresh_seq_));
    addMetric(&metrics, "probe_core_host_mode", 1.0, {{"mode", hostModeToString(opts_.host_mode)}});

    addMetric(&metrics, "probe_core_backpressure_dropped_batches_total",
              static_cast<double>(dropped_frames_.load(std::memory_order_relaxed)));
    {
      std::lock_guard<std::mutex> lock(queue_mu_);
      addMetric(&metrics, "probe_core_backpressure_queue_depth",
                static_cast<double>(frame_queue_.size()));
      addMetric(&metrics, "probe_core_backpressure_queue_capacity",
                static_cast<double>(opts_.queue_depth));
    }
    addMetric(&metrics, "probe_core_ipc_compression_enabled", opts_.gzip ? 1.0 : 0.0,
              {{"compression", compressionToString(opts_.gzip)}});
    for (const auto& module : collectorOrder()) {
      addMetric(&metrics, "probe_core_collector_module_enabled",
                collectorEnabled(opts_, module) ? 1.0 : 0.0, {{"module", module}});
    }

    for (const auto& m : metrics) {
      auto* out = batch.add_metrics();
      out->set_name(m.name);
      out->set_value(m.value);
      for (const auto& [k, v] : m.labels) {
        auto* label = out->add_labels();
        label->set_key(k);
        label->set_value(v);
      }
    }
    for (const auto& p : processes) {
      auto* out = batch.add_processes();
      out->set_pid(p.pid);
      out->set_name(p.name);
      out->set_cpu_percent(p.cpu_percent);
      out->set_rss_bytes(p.rss_bytes);
      out->set_io_read_bps(p.io_read_bps);
      out->set_io_write_bps(p.io_write_bps);
      out->set_voluntary_ctx_switches(p.voluntary_ctx);
      out->set_nonvoluntary_ctx_switches(p.nonvoluntary_ctx);
      out->set_minor_faults(p.minor_faults);
      out->set_major_faults(p.major_faults);
      out->set_pss_bytes(p.pss_bytes);
      out->set_net_tx_queue_bytes(p.net_tx_queue_bytes);
      out->set_net_rx_queue_bytes(p.net_rx_queue_bytes);
    }
    return batch;
  }

  void collectRDMAMetrics(double elapsed, std::vector<MetricPoint>* metrics) {
    const fs::path root("/sys/class/infiniband");
    std::error_code ec;
    if (!fs::exists(root, ec) || !fs::is_directory(root, ec)) {
      return;
    }

    std::unordered_map<std::string, RDMAPortSnapshot> current;
    int port_count = 0;
    double total_error_rate = 0.0;
    double total_congestion_rate = 0.0;
    double total_pfc_pause_rate = 0.0;
    uint64_t total_tx_packet_delta = 0;
    uint64_t total_ecn_marked_delta = 0;

    for (fs::directory_iterator dev_it(root, fs::directory_options::skip_permission_denied, ec), end;
         dev_it != end; dev_it.increment(ec)) {
      if (ec) {
        ec.clear();
        continue;
      }
      if (!dev_it->is_directory(ec)) {
        ec.clear();
        continue;
      }
      const std::string dev_name = dev_it->path().filename().string();
      const fs::path ports_path = dev_it->path() / "ports";
      if (!fs::exists(ports_path, ec) || !fs::is_directory(ports_path, ec)) {
        ec.clear();
        continue;
      }

      for (fs::directory_iterator port_it(ports_path, fs::directory_options::skip_permission_denied, ec), pend;
           port_it != pend; port_it.increment(ec)) {
        if (ec) {
          ec.clear();
          continue;
        }
        if (!port_it->is_directory(ec)) {
          ec.clear();
          continue;
        }
        const std::string port = port_it->path().filename().string();
        const std::string key = dev_name + ":" + port;
        port_count++;
        MetricLabels labels = {{"device", dev_name}, {"port", port}};

        const fs::path port_base = port_it->path();
        const fs::path counter_base = port_base / "counters";

        uint64_t state = 0;
        if (readRDMAStateFile((port_base / "state").string(), &state)) {
          addMetric(metrics, "probe_core_rdma_port_state", static_cast<double>(state), labels);
        }
        if (readRDMAStateFile((port_base / "phys_state").string(), &state)) {
          addMetric(metrics, "probe_core_rdma_port_phys_state", static_cast<double>(state), labels);
        }

        RDMAPortSnapshot snap{};
        if (readRDMALinkRateGbps((port_base / "rate").string(), &snap.link_rate_gbps)) {
          addMetric(metrics, "probe_core_rdma_port_link_rate_gbps", snap.link_rate_gbps, labels);
        }

        const std::unordered_map<std::string, std::string> counter_metrics = {
            {"port_xmit_data", "probe_core_rdma_port_transmit_words_total"},
            {"port_rcv_data", "probe_core_rdma_port_receive_words_total"},
            {"port_xmit_packets", "probe_core_rdma_port_transmit_packets_total"},
            {"port_rcv_packets", "probe_core_rdma_port_receive_packets_total"},
            {"port_xmit_discards", "probe_core_rdma_port_transmit_discards_total"},
            {"port_rcv_errors", "probe_core_rdma_port_receive_errors_total"},
            {"symbol_error", "probe_core_rdma_port_symbol_errors_total"},
            {"link_downed", "probe_core_rdma_port_link_downed_total"},
            {"link_error_recovery", "probe_core_rdma_port_link_recovery_total"},
            {"port_rcv_remote_physical_errors",
             "probe_core_rdma_port_remote_physical_errors_total"},
            {"port_rcv_constraint_errors",
             "probe_core_rdma_port_receive_constraint_errors_total"},
            {"port_xmit_constraint_errors",
             "probe_core_rdma_port_transmit_constraint_errors_total"},
        };
        for (const auto& [source, metric_name] : counter_metrics) {
          uint64_t value = 0;
          if (!readU64FileMaybe((counter_base / source).string(), &value)) {
            continue;
          }
          addMetric(metrics, metric_name, static_cast<double>(value), labels);
          if (source == "port_xmit_data") snap.xmit_words = value;
          if (source == "port_rcv_data") snap.rcv_words = value;
          if (source == "port_xmit_packets") snap.tx_packets = value;
          if (source == "port_xmit_discards" || source == "port_rcv_errors" ||
              source == "symbol_error" || source == "link_downed" ||
              source == "link_error_recovery" || source == "port_rcv_remote_physical_errors" ||
              source == "port_rcv_constraint_errors" || source == "port_xmit_constraint_errors") {
            snap.error_events += value;
          }
        }

        const fs::path hw_counters_path = port_base / "hw_counters";
        if (fs::exists(hw_counters_path, ec) && fs::is_directory(hw_counters_path, ec)) {
          for (fs::directory_iterator hw_it(hw_counters_path, fs::directory_options::skip_permission_denied, ec),
               hend;
               hw_it != hend; hw_it.increment(ec)) {
            if (ec) {
              ec.clear();
              continue;
            }
            if (!hw_it->is_regular_file(ec)) {
              ec.clear();
              continue;
            }
            const std::string counter_name = hw_it->path().filename().string();
            if (!isRDMACongestionCounter(counter_name)) {
              continue;
            }
            uint64_t value = 0;
            if (!readU64FileMaybe(hw_it->path().string(), &value)) {
              continue;
            }
            snap.congestion_events += value;

            MetricLabels hw_labels = labels;
            hw_labels.emplace_back("counter", counter_name);
            addMetric(metrics, "probe_core_rdma_port_congestion_counter_total",
                      static_cast<double>(value), std::move(hw_labels));

            std::string counter_lower = counter_name;
            std::transform(counter_lower.begin(), counter_lower.end(), counter_lower.begin(),
                           [](unsigned char c) { return static_cast<char>(std::tolower(c)); });
            if (counter_lower.find("pfc") != std::string::npos) {
              snap.pfc_pause_frames += value;
            }
            if (counter_lower.find("ecn") != std::string::npos) {
              snap.ecn_marked_packets += value;
            }
          }
        } else {
          ec.clear();
        }

        current[key] = snap;

        const auto prev_it = last_rdma_ports_.find(key);
        if (prev_it == last_rdma_ports_.end() || elapsed <= 0.0) {
          continue;
        }
        const auto& prev = prev_it->second;
        const double tx_bps = (static_cast<double>(counterDelta(snap.xmit_words, prev.xmit_words)) *
                               4.0) /
                              elapsed;
        const double rx_bps =
            (static_cast<double>(counterDelta(snap.rcv_words, prev.rcv_words)) * 4.0) / elapsed;
        const double error_rate =
            static_cast<double>(counterDelta(snap.error_events, prev.error_events)) / elapsed;
        const double congestion_rate =
            static_cast<double>(counterDelta(snap.congestion_events, prev.congestion_events)) /
            elapsed;
        const double pfc_rate =
            static_cast<double>(counterDelta(snap.pfc_pause_frames, prev.pfc_pause_frames)) /
            elapsed;
        const uint64_t tx_packet_delta = counterDelta(snap.tx_packets, prev.tx_packets);
        const uint64_t ecn_marked_delta =
            counterDelta(snap.ecn_marked_packets, prev.ecn_marked_packets);

        addMetric(metrics, "probe_core_rdma_port_transmit_bytes_per_second", tx_bps, labels);
        addMetric(metrics, "probe_core_rdma_port_receive_bytes_per_second", rx_bps, labels);
        addMetric(metrics, "probe_core_rdma_port_errors_per_second", error_rate, labels);
        addMetric(metrics, "probe_core_rdma_port_congestion_events_per_second", congestion_rate,
                  labels);
        addMetric(metrics, "probe_core_rdma_port_pfc_pause_frames_per_second", pfc_rate, labels);

        if (tx_packet_delta > 0) {
          const double ecn_ratio = std::clamp(
              static_cast<double>(ecn_marked_delta) / static_cast<double>(tx_packet_delta), 0.0,
              1.0);
          addMetric(metrics, "probe_core_rdma_port_ecn_marked_ratio", ecn_ratio, labels);
          total_tx_packet_delta += tx_packet_delta;
          total_ecn_marked_delta += ecn_marked_delta;
        }
        if (snap.link_rate_gbps > 0) {
          const double link_bps = snap.link_rate_gbps * 1'000'000'000.0;
          const double utilization = std::clamp(((tx_bps + rx_bps) * 8.0 / link_bps) * 100.0, 0.0,
                                                100.0);
          addMetric(metrics, "probe_core_rdma_port_utilization_percent", utilization, labels);
        }

        total_error_rate += error_rate;
        total_congestion_rate += congestion_rate;
        total_pfc_pause_rate += pfc_rate;
      }
    }

    if (port_count > 0) {
      addMetric(metrics, "probe_core_rdma_ports", static_cast<double>(port_count));
      addMetric(metrics, "probe_core_rdma_errors_per_second", total_error_rate);
      addMetric(metrics, "probe_core_rdma_congestion_events_per_second", total_congestion_rate);
      addMetric(metrics, "probe_core_rdma_pfc_pause_frames_per_second", total_pfc_pause_rate);
      if (total_tx_packet_delta > 0) {
        const double ecn_ratio = std::clamp(
            static_cast<double>(total_ecn_marked_delta) / static_cast<double>(total_tx_packet_delta),
            0.0, 1.0);
        addMetric(metrics, "probe_core_rdma_ecn_marked_ratio", ecn_ratio);
      }
    }

    last_rdma_ports_ = std::move(current);
  }

  void collectNetlinkMetrics(std::vector<MetricPoint>* metrics) {
    const auto& snapshot = getNetlinkSnapshot();
    if (!snapshot.ok) return;
    for (const auto& [iface, txqlen] : snapshot.tx_queue_len) {
      addMetric(metrics, "probe_core_netlink_tx_queue_len", static_cast<double>(txqlen),
                {{"iface", iface}});
    }
  }

  void refreshEthtoolMetricsCache() {
    cached_ethtool_metrics_.clear();
    last_ethtool_collect_seq_ = sample_seq_;

    ScopedFD fd(socket(AF_INET, SOCK_DGRAM, 0));
    if (!fd.valid()) return;

    auto net = getNetlinkSnapshot().stats;
    if (net.empty()) {
      net = readNetDev();
    }
    for (const auto& [iface, _] : net) {
      ifreq ifr {};
      strncpy(ifr.ifr_name, iface.c_str(), IFNAMSIZ - 1);

      ethtool_value eval {};
      eval.cmd = ETHTOOL_GLINK;
      ifr.ifr_data = reinterpret_cast<char*>(&eval);
      if (ioctl(fd.get(), SIOCETHTOOL, &ifr) == 0) {
        addMetric(&cached_ethtool_metrics_, "probe_core_network_link_up",
                  static_cast<double>(eval.data), {{"iface", iface}});
      }

#ifdef ETHTOOL_GSET
      ethtool_cmd ecmd {};
      ecmd.cmd = ETHTOOL_GSET;
      ifr.ifr_data = reinterpret_cast<char*>(&ecmd);
      if (ioctl(fd.get(), SIOCETHTOOL, &ifr) == 0) {
#ifdef ethtool_cmd_speed
        addMetric(&cached_ethtool_metrics_, "probe_core_network_speed_mbps",
                  static_cast<double>(ethtool_cmd_speed(&ecmd)), {{"iface", iface}});
#endif
      }
#endif
    }
  }

  void collectEthtoolMetrics(std::vector<MetricPoint>* metrics) {
    if (shouldRefresh(last_ethtool_collect_seq_, 10)) {
      refreshEthtoolMetricsCache();
    }
    appendCachedMetrics(cached_ethtool_metrics_, metrics);
  }

  void collectPerfMetrics(std::vector<MetricPoint>* metrics) {
    const PerfCounterSnapshot& perf = getPerfCounterSnapshot();
    addMetric(metrics, "probe_core_perf_context_switches_available",
              perf.has_context_switches ? 1.0 : 0.0);

    if (perf.has_context_switches) {
      addMetric(metrics, "probe_core_perf_context_switches_total",
                static_cast<double>(perf.context_switches));
      if (perf.context_switches >= perf_last_ctx_switch_) {
        addMetric(metrics, "probe_core_perf_context_switches_delta",
                  static_cast<double>(perf.context_switches - perf_last_ctx_switch_));
      }
      perf_last_ctx_switch_ = perf.context_switches;
    }

    if (perf.has_task_clock) {
      addMetric(metrics, "probe_core_perf_task_clock_seconds_total", perf.task_clock_ns / 1e9);
    }

    if (perf.has_page_faults) {
      addMetric(metrics, "probe_core_perf_page_faults_total",
                static_cast<double>(perf.page_faults));
      if (perf.page_faults >= perf_last_page_faults_) {
        addMetric(metrics, "probe_core_perf_page_faults_delta",
                  static_cast<double>(perf.page_faults - perf_last_page_faults_));
      }
      perf_last_page_faults_ = perf.page_faults;
    }

    if (perf.has_major_page_faults) {
      addMetric(metrics, "probe_core_perf_major_page_faults_total",
                static_cast<double>(perf.major_page_faults));
      if (perf.major_page_faults >= perf_last_major_page_faults_) {
        addMetric(metrics, "probe_core_perf_major_page_faults_delta",
                  static_cast<double>(perf.major_page_faults - perf_last_major_page_faults_));
      }
      perf_last_major_page_faults_ = perf.major_page_faults;
    }
  }

  void collectEBPFMetrics(std::vector<MetricPoint>* metrics) {
    if (ebpf_sock_fd_ < 0) {
      addMetric(metrics, "probe_core_ebpf_socket_connected", 0);
      return;
    }
    addMetric(metrics, "probe_core_ebpf_socket_connected", 1);
    addMetric(metrics, "probe_core_ebpf_receiver_running",
              ebpf_receiver_running_.load(std::memory_order_relaxed) ? 1.0 : 0.0);

    std::unordered_map<std::string, uint64_t> counts;
    std::unordered_map<std::string, uint64_t> bytes;
    std::unordered_map<std::string, uint64_t> lat_sum_ns;
    std::unordered_map<std::string, uint64_t> lat_count;
    std::unordered_map<std::string, uint64_t> category_counts;
    std::unordered_map<int, uint64_t> pid_counts;
    std::unordered_map<int, uint64_t> pid_bytes;
    std::unordered_map<int, uint64_t> pid_lat_sum_ns;
    std::unordered_map<int, uint64_t> pid_lat_count;
    std::unordered_map<std::string, uint64_t> prev_counts;
    std::unordered_map<std::string, uint64_t> prev_bytes;
    std::unordered_map<int, uint64_t> prev_pid_counts;
    std::unordered_map<int, uint64_t> prev_pid_bytes;
    int64_t prev_emit_mono_ns = 0;

    {
      std::lock_guard<std::mutex> lock(ebpf_mu_);
      counts = ebpf_event_counts_;
      bytes = ebpf_event_bytes_;
      lat_sum_ns = ebpf_event_latency_sum_ns_;
      lat_count = ebpf_event_latency_count_;
      category_counts = ebpf_category_counts_;
      pid_counts = ebpf_pid_event_counts_;
      pid_bytes = ebpf_pid_bytes_;
      pid_lat_sum_ns = ebpf_pid_latency_sum_ns_;
      pid_lat_count = ebpf_pid_latency_count_;
      prev_counts = last_ebpf_event_counts_;
      prev_bytes = last_ebpf_event_bytes_;
      prev_pid_counts = last_ebpf_pid_event_counts_;
      prev_pid_bytes = last_ebpf_pid_bytes_;
      prev_emit_mono_ns = last_ebpf_emit_mono_ns_;
      last_ebpf_event_counts_ = counts;
      last_ebpf_event_bytes_ = bytes;
      last_ebpf_pid_event_counts_ = pid_counts;
      last_ebpf_pid_bytes_ = pid_bytes;
      last_ebpf_emit_mono_ns_ = nowMonotonicNanos();
    }

    const uint64_t total_events = total_ebpf_events_.load(std::memory_order_relaxed);
    const uint64_t total_bytes = total_ebpf_bytes_.load(std::memory_order_relaxed);
    addMetric(metrics, "probe_core_ebpf_events_total", static_cast<double>(total_events));
    addMetric(metrics, "probe_core_ebpf_bytes_total", static_cast<double>(total_bytes));

    double window_seconds = static_cast<double>(std::max(100, effective_interval_ms_)) / 1000.0;
    if (prev_emit_mono_ns > 0) {
      const int64_t delta_ns = nowMonotonicNanos() - prev_emit_mono_ns;
      if (delta_ns > 0 && delta_ns < 120000000000LL) {
        window_seconds = static_cast<double>(delta_ns) / 1e9;
      }
    }
    if (window_seconds <= 0) {
      window_seconds = 1.0;
    }

    std::unordered_map<std::string, double> category_rates;
    std::unordered_map<std::string, double> category_bytes_rates;

    for (const auto& [key, count] : counts) {
      std::string category = "unknown";
      std::string type = key;
      const size_t sep = key.find('|');
      if (sep != std::string::npos) {
        category = key.substr(0, sep);
        type = key.substr(sep + 1);
      }
      MetricLabels labels = {{"category", category}, {"type", type}};
      addMetric(metrics, "probe_core_ebpf_events_total", static_cast<double>(count), labels);
      const uint64_t prev = prev_counts.count(key) ? prev_counts[key] : 0;
      const double rate = static_cast<double>(counterDelta(count, prev)) / window_seconds;
      addMetric(metrics, "probe_core_ebpf_events_rate", rate, labels);
      category_rates[category] += rate;

      if (bytes.count(key) > 0) {
        const uint64_t b = bytes[key];
        addMetric(metrics, "probe_core_ebpf_events_bytes_total", static_cast<double>(b), labels);
        const uint64_t prev_b = prev_bytes.count(key) ? prev_bytes[key] : 0;
        const double bytes_rate = static_cast<double>(counterDelta(b, prev_b)) / window_seconds;
        addMetric(metrics, "probe_core_ebpf_events_bytes_rate", bytes_rate, labels);
        category_bytes_rates[category] += bytes_rate;
      }

      const uint64_t sum_ns = lat_sum_ns.count(key) ? lat_sum_ns[key] : 0;
      const uint64_t cnt = lat_count.count(key) ? lat_count[key] : 0;
      if (cnt > 0 && sum_ns > 0) {
        addMetric(metrics, "probe_core_ebpf_events_latency_seconds_sum",
                  static_cast<double>(sum_ns) / 1e9, labels);
        addMetric(metrics, "probe_core_ebpf_events_latency_seconds_count",
                  static_cast<double>(cnt), labels);
        addMetric(metrics, "probe_core_ebpf_events_latency_seconds_avg",
                  (static_cast<double>(sum_ns) / static_cast<double>(cnt)) / 1e9, labels);
      }

      if (category == "syscall" && cnt > 0 && sum_ns > 0) {
        addMetric(metrics, "probe_core_ebpf_syscall_latency_seconds_avg",
                  (static_cast<double>(sum_ns) / static_cast<double>(cnt)) / 1e9, labels);
      }
      if (category == "io" && cnt > 0 && sum_ns > 0) {
        addMetric(metrics, "probe_core_ebpf_io_latency_seconds_avg",
                  (static_cast<double>(sum_ns) / static_cast<double>(cnt)) / 1e9, labels);
      }
      if (category == "sched") {
        addMetric(metrics, "probe_core_ebpf_sched_contention_events_per_second", rate, labels);
      }
    }

    for (const auto& [category, count] : category_counts) {
      MetricLabels labels = {{"category", category}};
      addMetric(metrics, "probe_core_ebpf_category_events_total", static_cast<double>(count), labels);
      if (category_rates.count(category) > 0) {
        addMetric(metrics, "probe_core_ebpf_category_events_rate", category_rates[category], labels);
      }
      if (category_bytes_rates.count(category) > 0) {
        addMetric(metrics, "probe_core_ebpf_category_bytes_per_second",
                  category_bytes_rates[category], labels);
      }
      if (category == "net" && category_bytes_rates.count(category) > 0) {
        addMetric(metrics, "probe_core_ebpf_network_flow_bytes_per_second",
                  category_bytes_rates[category],
                  {{"category", category}, {"scope", "all"}});
      }
    }

    std::vector<std::pair<int, uint64_t>> top_pids;
    top_pids.reserve(pid_counts.size());
    for (const auto& kv : pid_counts) {
      top_pids.emplace_back(kv.first, kv.second);
    }
    std::sort(top_pids.begin(), top_pids.end(),
              [](const auto& left, const auto& right) { return left.second > right.second; });
    if (top_pids.size() > 16) {
      top_pids.resize(16);
    }

    for (const auto& [pid, count] : top_pids) {
      const std::string pid_label = std::to_string(pid);
      MetricLabels labels = {{"pid", pid_label}};
      addMetric(metrics, "probe_core_ebpf_process_events_total", static_cast<double>(count), labels);
      const uint64_t prev = prev_pid_counts.count(pid) ? prev_pid_counts[pid] : 0;
      addMetric(metrics, "probe_core_ebpf_process_events_rate",
                static_cast<double>(counterDelta(count, prev)) / window_seconds, labels);

      if (pid_bytes.count(pid) > 0) {
        const uint64_t bytes_total = pid_bytes[pid];
        addMetric(metrics, "probe_core_ebpf_process_bytes_total",
                  static_cast<double>(bytes_total), labels);
        const uint64_t prev_total = prev_pid_bytes.count(pid) ? prev_pid_bytes[pid] : 0;
        addMetric(metrics, "probe_core_ebpf_process_bytes_rate",
                  static_cast<double>(counterDelta(bytes_total, prev_total)) / window_seconds, labels);
      }
      if (pid_lat_sum_ns.count(pid) > 0 && pid_lat_count.count(pid) > 0 &&
          pid_lat_count[pid] > 0) {
        addMetric(metrics, "probe_core_ebpf_process_latency_seconds_avg",
                  (static_cast<double>(pid_lat_sum_ns[pid]) / static_cast<double>(pid_lat_count[pid])) /
                      1e9,
                  labels);
      }
    }
  }

  void collectGPUMetrics(std::vector<MetricPoint>* metrics) {
    if (opts_.gpu_interval_samples <= 0) opts_.gpu_interval_samples = 5;
    const bool refresh = cached_gpu_metrics_.empty() ||
                         (sample_seq_ - last_gpu_collect_seq_) >= static_cast<uint32_t>(opts_.gpu_interval_samples);
    if (refresh) {
      cached_gpu_metrics_.clear();
      const std::string gpu_csv = runCommandCapture(
          {"nvidia-smi",
           "--query-gpu=index,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw",
           "--format=csv,noheader,nounits"});
      if (!gpu_csv.empty()) {
        std::istringstream ss(gpu_csv);
        std::string line;
        while (std::getline(ss, line)) {
          auto cols = splitCSVLine(line);
          if (cols.size() < 6) continue;
          const std::string gpu = cols[0];
          cached_gpu_metrics_.push_back(
              {"probe_core_gpu_utilization_percent", parseDouble(cols[1]), {{"gpu", gpu}}});
          cached_gpu_metrics_.push_back(
              {"probe_core_gpu_memory_used_mb", parseDouble(cols[2]), {{"gpu", gpu}}});
          cached_gpu_metrics_.push_back(
              {"probe_core_gpu_memory_total_mb", parseDouble(cols[3]), {{"gpu", gpu}}});
          cached_gpu_metrics_.push_back(
              {"probe_core_gpu_temperature_c", parseDouble(cols[4]), {{"gpu", gpu}}});
          cached_gpu_metrics_.push_back(
              {"probe_core_gpu_power_watts", parseDouble(cols[5]), {{"gpu", gpu}}});
        }
      }

      const std::string proc_csv = runCommandCapture(
          {"nvidia-smi", "--query-compute-apps=pid,gpu_uuid,used_memory",
           "--format=csv,noheader,nounits"});
      if (!proc_csv.empty()) {
        std::istringstream ss(proc_csv);
        std::string line;
        while (std::getline(ss, line)) {
          auto cols = splitCSVLine(line);
          if (cols.size() < 3) continue;
          cached_gpu_metrics_.push_back({"probe_core_gpu_process_memory_used_mb", parseDouble(cols[2]),
                                         {{"pid", cols[0]}, {"gpu_uuid", cols[1]}}});
        }
      }

      cached_gpu_metrics_.push_back({"probe_core_gpu_probe_success",
                                     cached_gpu_metrics_.empty() ? 0.0 : 1.0, {}});
      last_gpu_collect_seq_ = sample_seq_;
    }
    for (const auto& m : cached_gpu_metrics_) metrics->push_back(m);
  }

  void emitProcessRowMetrics(const ProcRow& row, std::vector<MetricPoint>* metrics) {
    const std::string pid = std::to_string(row.pid);
    MetricLabels labels = {{"pid", pid}, {"process", row.name}};
    addMetric(metrics, "probe_core_process_cpu_percent", row.cpu_percent, labels);
    addMetric(metrics, "probe_core_process_rss_bytes", static_cast<double>(row.rss_bytes), labels);
    addMetric(metrics, "probe_core_process_io_read_bps", row.io_read_bps, labels);
    addMetric(metrics, "probe_core_process_io_write_bps", row.io_write_bps, labels);
    addMetric(metrics, "probe_core_process_context_switches_total",
              static_cast<double>(row.voluntary_ctx),
              {{"pid", pid}, {"process", row.name}, {"type", "voluntary"}});
    addMetric(metrics, "probe_core_process_context_switches_total",
              static_cast<double>(row.nonvoluntary_ctx),
              {{"pid", pid}, {"process", row.name}, {"type", "nonvoluntary"}});
    addMetric(metrics, "probe_core_process_page_faults_total", static_cast<double>(row.minor_faults),
              {{"pid", pid}, {"process", row.name}, {"type", "minor"}});
    addMetric(metrics, "probe_core_process_page_faults_total", static_cast<double>(row.major_faults),
              {{"pid", pid}, {"process", row.name}, {"type", "major"}});
    addMetric(metrics, "probe_core_process_pss_bytes", static_cast<double>(row.pss_bytes), labels);
    addMetric(metrics, "probe_core_process_sched_run_seconds_total", row.sched_run_seconds_total,
              labels);
    addMetric(metrics, "probe_core_process_sched_wait_seconds_total", row.sched_wait_seconds_total,
              labels);
    addMetric(metrics, "probe_core_process_sched_wait_ratio", row.sched_wait_ratio, labels);
    addMetric(metrics, "probe_core_process_block_io_delay_seconds_total",
              row.block_io_delay_seconds_total, labels);
    addMetric(metrics, "probe_core_process_block_io_delay_seconds_per_second",
              row.block_io_delay_seconds_per_second, labels);
    addMetric(metrics, "probe_core_process_socket_connections",
              static_cast<double>(row.net_connections), labels);
    addMetric(metrics, "probe_core_process_socket_tx_queue_bytes",
              static_cast<double>(row.net_tx_queue_bytes), labels);
    addMetric(metrics, "probe_core_process_socket_rx_queue_bytes",
              static_cast<double>(row.net_rx_queue_bytes), labels);
  }

  std::vector<ProcRow> collectProcessMetrics(double elapsed, std::vector<MetricPoint>* metrics) {
    std::unordered_map<int, ProcCounters> cur;
    cur.reserve(1024);

    ScopedDir proc(opendir(kProcPath));
    if (!proc.valid()) return {};
    dirent* ent = nullptr;
    while ((ent = readdir(proc.get())) != nullptr) {
      if (!isNumeric(ent->d_name)) continue;
      const int pid = atoi(ent->d_name);
      if (pid <= 0) continue;
      ProcCounters s;
      if (!parseProcStat(pid, &s)) continue;
      parseProcStatusCtx(pid, &s);
      parseProcSchedstat(pid, &s);
      parseProcIO(pid, &s);
      cur.emplace(pid, std::move(s));
    }

    const double elapsed_safe = elapsed > 0.0 ? elapsed : 1.0;
    const double host_delta =
        elapsed_safe * static_cast<double>(std::max<long>(clock_ticks_, 1)) *
        static_cast<double>(std::max(1, online_cpus_));
    const long clock_ticks = std::max<long>(clock_ticks_, 1);
    std::vector<ProcRow> rows;
    rows.reserve(cur.size());
    for (const auto& [pid, now] : cur) {
      const auto it = last_proc_.find(pid);
      if (it == last_proc_.end()) continue;
      const auto& prev = it->second;
      ProcRow row;
      row.pid = pid;
      row.name = now.name;
      const uint64_t cpu_delta = counterDelta(now.cpu_total, prev.cpu_total);
      row.cpu_percent = static_cast<double>(cpu_delta) / host_delta * 100.0;
      row.rss_bytes = now.rss_bytes;
      row.io_read_bps =
          static_cast<double>(counterDelta(now.read_bytes, prev.read_bytes)) / elapsed_safe;
      row.io_write_bps =
          static_cast<double>(counterDelta(now.write_bytes, prev.write_bytes)) / elapsed_safe;
      row.voluntary_ctx = now.voluntary_ctx;
      row.nonvoluntary_ctx = now.nonvoluntary_ctx;
      row.minor_faults = now.minor_faults;
      row.major_faults = now.major_faults;
      row.sched_run_seconds_total = static_cast<double>(now.sched_run_ns) / 1e9;
      row.sched_wait_seconds_total = static_cast<double>(now.sched_wait_ns) / 1e9;
      const double run_delta_seconds =
          static_cast<double>(counterDelta(now.sched_run_ns, prev.sched_run_ns)) / 1e9;
      const double wait_delta_seconds =
          static_cast<double>(counterDelta(now.sched_wait_ns, prev.sched_wait_ns)) / 1e9;
      if (run_delta_seconds > 0 || wait_delta_seconds > 0) {
        row.sched_wait_ratio = wait_delta_seconds / std::max(run_delta_seconds, 1e-6);
      }
      row.block_io_delay_seconds_total =
          static_cast<double>(now.blkio_delay_ticks) / static_cast<double>(clock_ticks);
      const double blkio_delta_seconds =
          static_cast<double>(counterDelta(now.blkio_delay_ticks, prev.blkio_delay_ticks)) /
          static_cast<double>(clock_ticks);
      row.block_io_delay_seconds_per_second = blkio_delta_seconds / elapsed_safe;
      rows.push_back(row);
    }

    std::sort(rows.begin(), rows.end(), [](const ProcRow& a, const ProcRow& b) {
      return a.cpu_percent > b.cpu_percent;
    });
    if (static_cast<int>(rows.size()) > opts_.topk) rows.resize(opts_.topk);

    auto inode_map = readSocketQueueMap("/proc/net/tcp");
    auto inode_map6 = readSocketQueueMap("/proc/net/tcp6");
    inode_map.insert(inode_map6.begin(), inode_map6.end());

    for (auto& row : rows) {
      row.pss_bytes = readPssBytes(row.pid);
      const auto inodes = listSocketInodesForPid(row.pid);
      for (const auto& inode : inodes) {
        auto it = inode_map.find(inode);
        if (it == inode_map.end()) continue;
        row.net_connections++;
        row.net_tx_queue_bytes += it->second.tx_queue;
        row.net_rx_queue_bytes += it->second.rx_queue;
      }
      emitProcessRowMetrics(row, metrics);
    }

    last_proc_ = std::move(cur);
    return rows;
  }
};

std::string collectorModulesCSV() {
  std::ostringstream out;
  const auto& modules = collectorOrder();
  for (size_t i = 0; i < modules.size(); ++i) {
    if (i > 0) out << ",";
    out << modules[i];
  }
  return out.str();
}

void printCollectors(std::ostream& out) {
  for (const auto& module : collectorOrder()) {
    out << module << "\n";
  }
}

void printUsage() {
  const std::string modules = collectorModulesCSV();
  std::cerr << "Usage: sre-probe-core [options]\n"
            << "  --once                       collect one frame and exit\n"
            << "  --list-collectors            print supported collector modules and exit\n"
            << "  --interval-ms <n>           collection interval in ms (default 1000)\n"
            << "  --max-interval-ms <n>       upper bound for adaptive sampling backoff (default 5000)\n"
            << "  --topk <n>                  top processes per batch (default 20)\n"
            << "  --window-samples <n>        sliding-window sample count metadata (default 6)\n"
            << "  --queue-depth <n>           max pending IPC frames before dropping oldest (default 16)\n"
            << "  --compression <none|gzip>   frame payload compression (default none)\n"
            << "  --host-mode <auto|kernel|proc> host collector mode (default auto)\n"
            << "  --process-interval-samples <n> process collector cadence in samples (default 2)\n"
            << "  --host-proc-fallback-interval-samples <n> /proc host supplement cadence (default 10)\n"
            << "  --pressure-interval-samples <n> PSI refresh cadence in samples (default 3)\n"
            << "  --netlink-interval-samples <n> netlink refresh cadence in samples (default 2)\n"
            << "  --backoff-queue-high-percent <n> increase interval when queue ratio >= n (default 75)\n"
            << "  --backoff-queue-low-percent <n>  decrease interval when queue ratio <= n (default 20)\n"
            << "  --gpu-interval-samples <n>  GPU polling frequency in samples (default 5)\n"
            << "  --ebpf-socket <path>        optional unix datagram socket for eBPF event stream\n"
            << "  --collectors <csv>          run only selected modules; default is all\n"
            << "                              modules: " << modules << "\n";
}

bool parseOptions(int argc, char** argv, Options* opts) {
  for (int i = 1; i < argc; ++i) {
    const std::string arg = argv[i];
    auto requireValue = [&](const std::string& name) -> std::string {
      if (i + 1 >= argc) {
        std::cerr << "missing value for " << name << "\n";
        return "";
      }
      return argv[++i];
    };

    if (arg == "--once") {
      opts->once = true;
    } else if (arg == "--list-collectors") {
      opts->list_collectors = true;
    } else if (arg == "--interval-ms") {
      const auto v = requireValue(arg);
      if (v.empty()) return false;
      opts->interval_ms = std::max(100, atoi(v.c_str()));
    } else if (arg == "--max-interval-ms") {
      const auto v = requireValue(arg);
      if (v.empty()) return false;
      opts->max_interval_ms = std::max(100, atoi(v.c_str()));
    } else if (arg == "--topk") {
      const auto v = requireValue(arg);
      if (v.empty()) return false;
      opts->topk = std::max(1, atoi(v.c_str()));
    } else if (arg == "--window-samples") {
      const auto v = requireValue(arg);
      if (v.empty()) return false;
      opts->window_samples = std::max(1, atoi(v.c_str()));
    } else if (arg == "--queue-depth") {
      const auto v = requireValue(arg);
      if (v.empty()) return false;
      opts->queue_depth = std::max(1, atoi(v.c_str()));
    } else if (arg == "--compression") {
      const auto v = requireValue(arg);
      if (v.empty()) return false;
      if (v == "gzip") {
        opts->gzip = true;
      } else if (v == "none") {
        opts->gzip = false;
      } else {
        std::cerr << "unknown compression: " << v << "\n";
        return false;
      }
    } else if (arg == "--ebpf-socket") {
      opts->ebpf_socket_path = requireValue(arg);
      if (opts->ebpf_socket_path.empty()) return false;
    } else if (arg == "--gpu-interval-samples") {
      const auto v = requireValue(arg);
      if (v.empty()) return false;
      opts->gpu_interval_samples = std::max(1, atoi(v.c_str()));
    } else if (arg == "--host-mode") {
      const auto v = requireValue(arg);
      if (v.empty()) return false;
      const auto mode = parseHostMode(v);
      if (!mode.has_value()) {
        std::cerr << "unknown host mode: " << v << "\n";
        return false;
      }
      opts->host_mode = mode.value();
    } else if (arg == "--process-interval-samples") {
      const auto v = requireValue(arg);
      if (v.empty()) return false;
      opts->process_interval_samples = std::max(1, atoi(v.c_str()));
    } else if (arg == "--host-proc-fallback-interval-samples") {
      const auto v = requireValue(arg);
      if (v.empty()) return false;
      opts->host_proc_fallback_interval_samples = std::max(1, atoi(v.c_str()));
    } else if (arg == "--pressure-interval-samples") {
      const auto v = requireValue(arg);
      if (v.empty()) return false;
      opts->pressure_interval_samples = std::max(1, atoi(v.c_str()));
    } else if (arg == "--netlink-interval-samples") {
      const auto v = requireValue(arg);
      if (v.empty()) return false;
      opts->netlink_interval_samples = std::max(1, atoi(v.c_str()));
    } else if (arg == "--backoff-queue-high-percent") {
      const auto v = requireValue(arg);
      if (v.empty()) return false;
      opts->backoff_queue_high_percent = std::clamp(atoi(v.c_str()), 5, 99);
    } else if (arg == "--backoff-queue-low-percent") {
      const auto v = requireValue(arg);
      if (v.empty()) return false;
      opts->backoff_queue_low_percent = std::clamp(atoi(v.c_str()), 0, 90);
    } else if (arg == "--collectors") {
      const auto v = requireValue(arg);
      if (v.empty()) return false;
      if (!parseCollectorsArg(v, opts)) {
        return false;
      }
    } else if (arg == "--help" || arg == "-h") {
      printUsage();
      exit(0);
    } else {
      std::cerr << "unknown argument: " << arg << "\n";
      return false;
    }
  }
  if (opts->backoff_queue_low_percent >= opts->backoff_queue_high_percent) {
    opts->backoff_queue_low_percent = std::max(0, opts->backoff_queue_high_percent - 5);
  }
  if (opts->max_interval_ms < opts->interval_ms) {
    opts->max_interval_ms = opts->interval_ms;
  }
  return true;
}

}  // namespace

int main(int argc, char** argv) {
  GOOGLE_PROTOBUF_VERIFY_VERSION;
  signal(SIGINT, handleSignal);
  signal(SIGTERM, handleSignal);

  Options opts;
  if (!parseOptions(argc, argv, &opts)) {
    printUsage();
    return 1;
  }
  if (opts.list_collectors) {
    printCollectors(std::cout);
    return 0;
  }

  ProbeCoreRuntime runtime(opts);
  if (opts.once) {
    runtime.runOnce();
  } else {
    runtime.runStream();
  }
  google::protobuf::ShutdownProtobufLibrary();
  return 0;
}
