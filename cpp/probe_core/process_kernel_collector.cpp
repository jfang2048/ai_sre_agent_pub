#include "process_kernel_collector.h"

#include <linux/genetlink.h>
#include <linux/netlink.h>
#include <linux/taskstats.h>
#include <sys/socket.h>
#include <unistd.h>

#include <algorithm>
#include <cerrno>
#include <cstring>
#include <memory>
#include <unordered_set>

#include "network_kernel_collector.h"
#include "posix_raii.h"
#include "procfs_fallback_collector.h"

namespace probe_core {
namespace {

constexpr int kTaskstatsSeq = 11;
constexpr int kDefaultWatchlistLimit = 256;

struct TaskstatsSnapshot {
  int pid = 0;
  std::string comm;
  uint64_t cpu_user_usec = 0;
  uint64_t cpu_sys_usec = 0;
  uint64_t read_bytes = 0;
  uint64_t write_bytes = 0;
  uint64_t voluntary_ctx = 0;
  uint64_t nonvoluntary_ctx = 0;
  uint64_t minor_faults = 0;
  uint64_t major_faults = 0;
  uint64_t sched_run_ns = 0;
  uint64_t sched_wait_ns = 0;
  uint64_t blkio_delay_ns = 0;
  uint64_t hiwater_rss_bytes = 0;
};

struct WatchEntry {
  int pid = 0;
  std::string comm;
  uint64_t activity_hits = 0;
};

struct nlattr_view {
  const nlattr* attr = nullptr;
  int len = 0;
};

int nlaAligned(int len) { return (len + NLA_ALIGNTO - 1) & ~(NLA_ALIGNTO - 1); }

bool nlaOk(const nlattr* attr, int remaining) {
  return remaining >= static_cast<int>(sizeof(nlattr)) &&
         attr->nla_len >= sizeof(nlattr) &&
         attr->nla_len <= remaining;
}

const nlattr* nlaNext(const nlattr* attr, int* remaining) {
  const int aligned = nlaAligned(attr->nla_len);
  *remaining -= aligned;
  return reinterpret_cast<const nlattr*>(
      reinterpret_cast<const char*>(attr) + aligned);
}

uint16_t nlaType(const nlattr* attr) { return attr->nla_type & NLA_TYPE_MASK; }

template <typename T>
bool copyAttrValue(const nlattr* attr, T* out) {
  if (attr == nullptr || out == nullptr ||
      attr->nla_len < sizeof(nlattr) + sizeof(T)) {
    return false;
  }
  memcpy(out, reinterpret_cast<const char*>(attr) + sizeof(nlattr), sizeof(T));
  return true;
}

class TaskstatsClient {
 public:
  TaskstatsClient() = default;

  bool initialize(std::string* reason) {
    fd_.reset(socket(AF_NETLINK, SOCK_RAW | SOCK_CLOEXEC, NETLINK_GENERIC));
    if (!fd_.valid()) {
      if (reason != nullptr) *reason = "taskstats socket creation failed";
      return false;
    }

    sockaddr_nl addr{};
    addr.nl_family = AF_NETLINK;
    if (bind(fd_.get(), reinterpret_cast<const sockaddr*>(&addr), sizeof(addr)) < 0) {
      if (reason != nullptr) *reason = "taskstats netlink bind failed";
      fd_.reset();
      return false;
    }

    family_id_ = resolveFamilyID();
    if (family_id_ == 0) {
      if (reason != nullptr) *reason = "TASKSTATS generic-netlink family unavailable";
      fd_.reset();
      return false;
    }
    return true;
  }

  bool valid() const { return fd_.valid() && family_id_ != 0; }

  bool query(int pid, TaskstatsSnapshot* out) {
    if (!valid() || out == nullptr || pid <= 0) return false;

    struct {
      nlmsghdr nlh;
      genlmsghdr genl;
      char buf[NLA_ALIGN(sizeof(uint32_t)) + sizeof(nlattr)];
    } request{};
    request.nlh.nlmsg_len = NLMSG_LENGTH(sizeof(genlmsghdr));
    request.nlh.nlmsg_type = family_id_;
    request.nlh.nlmsg_flags = NLM_F_REQUEST;
    request.nlh.nlmsg_seq = kTaskstatsSeq;
    request.genl.cmd = TASKSTATS_CMD_GET;
    request.genl.version = 1;

    auto* attr = reinterpret_cast<nlattr*>(
        reinterpret_cast<char*>(&request) + request.nlh.nlmsg_len);
    attr->nla_type = TASKSTATS_CMD_ATTR_TGID;
    attr->nla_len = sizeof(nlattr) + sizeof(uint32_t);
    memcpy(reinterpret_cast<char*>(attr) + sizeof(nlattr), &pid, sizeof(uint32_t));
    request.nlh.nlmsg_len += nlaAligned(attr->nla_len);

    if (send(fd_.get(), &request, request.nlh.nlmsg_len, 0) < 0) {
      return false;
    }

    char buffer[8192];
    const ssize_t nread = recv(fd_.get(), buffer, sizeof(buffer), 0);
    if (nread <= 0) {
      return false;
    }

    ssize_t remaining = nread;
    for (nlmsghdr* header = reinterpret_cast<nlmsghdr*>(buffer); NLMSG_OK(header, remaining);
         header = NLMSG_NEXT(header, remaining)) {
      if (header->nlmsg_type == NLMSG_ERROR || header->nlmsg_type == NLMSG_DONE) {
        return false;
      }
      if (header->nlmsg_type != family_id_) {
        continue;
      }
      const auto* genl = reinterpret_cast<const genlmsghdr*>(NLMSG_DATA(header));
      int attr_len = header->nlmsg_len - NLMSG_LENGTH(sizeof(genlmsghdr));
      const nlattr* attr_ptr = reinterpret_cast<const nlattr*>(
          reinterpret_cast<const char*>(genl) + sizeof(genlmsghdr));
      if (!parseTaskstatsAttrs(attr_ptr, attr_len, out)) {
        continue;
      }
      return true;
    }
    return false;
  }

 private:
  uint16_t resolveFamilyID() {
    struct {
      nlmsghdr nlh;
      genlmsghdr genl;
      char buf[256];
    } request{};
    request.nlh.nlmsg_len = NLMSG_LENGTH(sizeof(genlmsghdr));
    request.nlh.nlmsg_type = GENL_ID_CTRL;
    request.nlh.nlmsg_flags = NLM_F_REQUEST;
    request.nlh.nlmsg_seq = kTaskstatsSeq;
    request.genl.cmd = CTRL_CMD_GETFAMILY;
    request.genl.version = 1;

    auto* attr = reinterpret_cast<nlattr*>(
        reinterpret_cast<char*>(&request) + request.nlh.nlmsg_len);
    const char family_name[] = TASKSTATS_GENL_NAME;
    const uint16_t raw_len = static_cast<uint16_t>(sizeof(nlattr) + sizeof(family_name));
    attr->nla_type = CTRL_ATTR_FAMILY_NAME;
    attr->nla_len = raw_len;
    memcpy(reinterpret_cast<char*>(attr) + sizeof(nlattr), family_name, sizeof(family_name));
    request.nlh.nlmsg_len += nlaAligned(raw_len);

    if (send(fd_.get(), &request, request.nlh.nlmsg_len, 0) < 0) {
      return 0;
    }

    char buffer[4096];
    const ssize_t nread = recv(fd_.get(), buffer, sizeof(buffer), 0);
    if (nread <= 0) {
      return 0;
    }

    ssize_t remaining = nread;
    for (nlmsghdr* header = reinterpret_cast<nlmsghdr*>(buffer); NLMSG_OK(header, remaining);
         header = NLMSG_NEXT(header, remaining)) {
      if (header->nlmsg_type == NLMSG_ERROR) return 0;
      const auto* genl = reinterpret_cast<const genlmsghdr*>(NLMSG_DATA(header));
      int attr_len = header->nlmsg_len - NLMSG_LENGTH(sizeof(genlmsghdr));
      const nlattr* attr = reinterpret_cast<const nlattr*>(
          reinterpret_cast<const char*>(genl) + sizeof(genlmsghdr));
      while (nlaOk(attr, attr_len)) {
        if (nlaType(attr) == CTRL_ATTR_FAMILY_ID) {
          uint16_t family_id = 0;
          if (copyAttrValue(attr, &family_id)) {
            return family_id;
          }
        }
        attr = nlaNext(attr, &attr_len);
      }
    }
    return 0;
  }

  bool parseTaskstatsAttrs(const nlattr* attr, int attr_len, TaskstatsSnapshot* out) {
    while (nlaOk(attr, attr_len)) {
      const uint16_t type = nlaType(attr);
      if (type == TASKSTATS_TYPE_AGGR_TGID || type == TASKSTATS_TYPE_AGGR_PID) {
        if (parseTaskstatsAggregate(attr, out)) {
          return true;
        }
      }
      attr = nlaNext(attr, &attr_len);
    }
    return false;
  }

  bool parseTaskstatsAggregate(const nlattr* attr, TaskstatsSnapshot* out) {
    int nested_len = attr->nla_len - sizeof(nlattr);
    auto* nested = reinterpret_cast<const nlattr*>(
        reinterpret_cast<const char*>(attr) + sizeof(nlattr));
    taskstats stats{};
    bool have_stats = false;
    uint32_t tgid = 0;

    while (nlaOk(nested, nested_len)) {
      const uint16_t type = nlaType(nested);
      if ((type == TASKSTATS_TYPE_TGID || type == TASKSTATS_TYPE_PID) &&
          nested->nla_len >= sizeof(nlattr) + sizeof(uint32_t)) {
        copyAttrValue(nested, &tgid);
      } else if (type == TASKSTATS_TYPE_STATS &&
                 nested->nla_len >= sizeof(nlattr) + sizeof(taskstats)) {
        memcpy(&stats, reinterpret_cast<const char*>(nested) + sizeof(nlattr),
               sizeof(taskstats));
        have_stats = true;
      }
      nested = nlaNext(nested, &nested_len);
    }

    if (!have_stats || tgid == 0) {
      return false;
    }
    out->pid = static_cast<int>(tgid);
    out->comm = stats.ac_comm;
    out->cpu_user_usec = stats.ac_utime;
    out->cpu_sys_usec = stats.ac_stime;
    out->read_bytes = stats.read_bytes;
    out->write_bytes = stats.write_bytes;
    out->voluntary_ctx = stats.nvcsw;
    out->nonvoluntary_ctx = stats.nivcsw;
    out->minor_faults = stats.ac_minflt;
    out->major_faults = stats.ac_majflt;
    out->sched_run_ns = stats.cpu_run_real_total;
    out->sched_wait_ns = stats.cpu_delay_total;
    out->blkio_delay_ns = stats.blkio_delay_total;
    out->hiwater_rss_bytes = stats.hiwater_rss * 1024ULL;
    return true;
  }

  ScopedFD fd_;
  uint16_t family_id_ = 0;
};

}  // namespace

struct ProcessKernelCollector::Impl {
  explicit Impl(ProcessKernelCollectorOptions init_options)
      : options(std::move(init_options)) {
    if (options.topk <= 0) options.topk = 20;
    if (options.watchlist_limit <= 0) options.watchlist_limit = kDefaultWatchlistLimit;
    if (options.online_cpus <= 0) options.online_cpus = 1;
    available = taskstats.initialize(&failure_reason);
  }

  void trimWatchlist() {
    if (watchlist.size() <= static_cast<size_t>(options.watchlist_limit)) return;
    std::vector<WatchEntry> items;
    items.reserve(watchlist.size());
    for (const auto& [_, entry] : watchlist) items.push_back(entry);
    std::sort(items.begin(), items.end(), [](const WatchEntry& a, const WatchEntry& b) {
      if (a.activity_hits != b.activity_hits) return a.activity_hits > b.activity_hits;
      return a.pid < b.pid;
    });
    items.resize(options.watchlist_limit);
    std::unordered_set<int> keep;
    for (const auto& entry : items) keep.insert(entry.pid);
    for (auto it = watchlist.begin(); it != watchlist.end();) {
      if (keep.find(it->first) == keep.end()) {
        it = watchlist.erase(it);
      } else {
        ++it;
      }
    }
  }

  ProcessKernelCollectorOptions options;
  TaskstatsClient taskstats;
  bool available = false;
  std::string failure_reason;
  std::unordered_map<int, WatchEntry> watchlist;
  std::unordered_map<int, TaskstatsSnapshot> last_taskstats;
  std::unordered_map<int, ProcFallbackSnapshot> last_proc_reconcile;
  std::unordered_map<int, ProcFallbackSnapshot> proc_enrichment_cache;
  std::unordered_map<int, TaskstatsSnapshot> ebpf_resource_cache;
};

std::vector<int> sortWatchPIDs(const std::unordered_map<int, WatchEntry>& watchlist) {
  std::vector<int> pids;
  pids.reserve(watchlist.size());
  for (const auto& [pid, _] : watchlist) pids.push_back(pid);
  std::sort(pids.begin(), pids.end(), [&](int left, int right) {
    const auto& lhs = watchlist.at(left);
    const auto& rhs = watchlist.at(right);
    if (lhs.activity_hits != rhs.activity_hits) return lhs.activity_hits > rhs.activity_hits;
    return left < right;
  });
  return pids;
}

ProcessKernelCollector::ProcessKernelCollector(ProcessKernelCollectorOptions options)
    : impl_(new Impl(std::move(options))) {}

ProcessKernelCollector::~ProcessKernelCollector() { delete impl_; }

void ProcessKernelCollector::noteEBPFActivity(int pid, const std::string& comm) {
  if (impl_ == nullptr || pid <= 0) return;
  auto& entry = impl_->watchlist[pid];
  entry.pid = pid;
  if (!comm.empty()) entry.comm = comm;
  entry.activity_hits++;
  impl_->trimWatchlist();
}

void ProcessKernelCollector::noteEBPFResourceSnapshot(int pid, uint64_t cpu_user_ms,
                                                      uint64_t cpu_sys_ms,
                                                      uint64_t rss_bytes) {
  if (impl_ == nullptr || pid <= 0) return;
  TaskstatsSnapshot snapshot;
  snapshot.pid = pid;
  snapshot.cpu_user_usec = cpu_user_ms * 1000ULL;
  snapshot.cpu_sys_usec = cpu_sys_ms * 1000ULL;
  snapshot.hiwater_rss_bytes = rss_bytes;
  impl_->ebpf_resource_cache[pid] = snapshot;
}

void ProcessKernelCollector::reconcileFromProc() {
  if (impl_ == nullptr) return;

  auto current = collectProcProcessSnapshots();
  for (const auto& [pid, snapshot] : current) {
    auto& entry = impl_->watchlist[pid];
    entry.pid = pid;
    if (!snapshot.name.empty()) entry.comm = snapshot.name;
  }

  std::vector<std::pair<int, uint64_t>> ranked;
  ranked.reserve(current.size());
  for (const auto& [pid, snapshot] : current) {
    uint64_t delta = snapshot.cpu_total;
    const auto it = impl_->last_proc_reconcile.find(pid);
    if (it != impl_->last_proc_reconcile.end() && snapshot.cpu_total >= it->second.cpu_total) {
      delta = snapshot.cpu_total - it->second.cpu_total;
    }
    ranked.emplace_back(pid, delta);
  }
  std::sort(ranked.begin(), ranked.end(),
            [](const auto& left, const auto& right) { return left.second > right.second; });

  const int limit = std::min<int>(static_cast<int>(ranked.size()), impl_->options.watchlist_limit);
  for (int idx = 0; idx < limit; ++idx) {
    auto& entry = impl_->watchlist[ranked[idx].first];
    entry.pid = ranked[idx].first;
    entry.activity_hits += std::max<uint64_t>(1, ranked[idx].second);
  }

  impl_->proc_enrichment_cache = current;
  impl_->last_proc_reconcile = std::move(current);
  impl_->trimWatchlist();
}

bool ProcessKernelCollector::available() const {
  return impl_ != nullptr && impl_->available;
}

const std::string& ProcessKernelCollector::failureReason() const {
  static const std::string kEmpty;
  return impl_ == nullptr ? kEmpty : impl_->failure_reason;
}

std::vector<ProcessKernelSample> ProcessKernelCollector::collect(double elapsed_seconds,
                                                                 bool lightweight_enrichment) {
  std::vector<ProcessKernelSample> rows;
  if (impl_ == nullptr || !impl_->available) return rows;
  if (elapsed_seconds <= 0.0) elapsed_seconds = 1.0;
  if (impl_->watchlist.empty()) {
    reconcileFromProc();
  }

  const auto socket_queues = lightweight_enrichment ? SocketQueueMap{} : readSocketQueuesByInode();
  const auto watched = sortWatchPIDs(impl_->watchlist);
  std::unordered_map<int, TaskstatsSnapshot> current;
  current.reserve(watched.size());

  for (int pid : watched) {
    TaskstatsSnapshot snapshot;
    if (!impl_->taskstats.query(pid, &snapshot)) {
      continue;
    }
    const auto ebpf_it = impl_->ebpf_resource_cache.find(pid);
    if (ebpf_it != impl_->ebpf_resource_cache.end()) {
      if (snapshot.cpu_user_usec == 0) snapshot.cpu_user_usec = ebpf_it->second.cpu_user_usec;
      if (snapshot.cpu_sys_usec == 0) snapshot.cpu_sys_usec = ebpf_it->second.cpu_sys_usec;
      if (snapshot.hiwater_rss_bytes == 0) {
        snapshot.hiwater_rss_bytes = ebpf_it->second.hiwater_rss_bytes;
      }
    }
    auto watch_it = impl_->watchlist.find(pid);
    if (snapshot.comm.empty() && watch_it != impl_->watchlist.end()) {
      snapshot.comm = watch_it->second.comm;
    }
    current.emplace(pid, std::move(snapshot));
  }

  const double capacity_usec =
      elapsed_seconds * 1'000'000.0 * static_cast<double>(impl_->options.online_cpus);
  rows.reserve(current.size());
  for (const auto& [pid, now] : current) {
    const auto prev_it = impl_->last_taskstats.find(pid);
    if (prev_it == impl_->last_taskstats.end()) continue;
    const auto& prev = prev_it->second;

    ProcessKernelSample row;
    row.pid = pid;
    row.name = !now.comm.empty() ? now.comm : readProcessCommFallback(pid);
    const uint64_t cpu_delta_usec =
        (now.cpu_user_usec >= prev.cpu_user_usec ? now.cpu_user_usec - prev.cpu_user_usec : 0) +
        (now.cpu_sys_usec >= prev.cpu_sys_usec ? now.cpu_sys_usec - prev.cpu_sys_usec : 0);
    if (capacity_usec > 0.0) {
      row.cpu_percent =
          std::clamp((static_cast<double>(cpu_delta_usec) / capacity_usec) * 100.0, 0.0, 100.0);
    }
    row.io_read_bps =
        static_cast<double>(now.read_bytes >= prev.read_bytes ? now.read_bytes - prev.read_bytes
                                                              : 0) /
        elapsed_seconds;
    row.io_write_bps =
        static_cast<double>(now.write_bytes >= prev.write_bytes ? now.write_bytes - prev.write_bytes
                                                                : 0) /
        elapsed_seconds;
    row.voluntary_ctx = now.voluntary_ctx;
    row.nonvoluntary_ctx = now.nonvoluntary_ctx;
    row.minor_faults = now.minor_faults;
    row.major_faults = now.major_faults;
    row.sched_run_seconds_total = static_cast<double>(now.sched_run_ns) / 1e9;
    row.sched_wait_seconds_total = static_cast<double>(now.sched_wait_ns) / 1e9;
    const double run_delta_seconds =
        static_cast<double>(now.sched_run_ns >= prev.sched_run_ns ? now.sched_run_ns - prev.sched_run_ns
                                                                  : 0) /
        1e9;
    const double wait_delta_seconds =
        static_cast<double>(now.sched_wait_ns >= prev.sched_wait_ns ? now.sched_wait_ns - prev.sched_wait_ns
                                                                    : 0) /
        1e9;
    if (run_delta_seconds > 0.0 || wait_delta_seconds > 0.0) {
      row.sched_wait_ratio = wait_delta_seconds / std::max(run_delta_seconds, 1e-6);
    }
    row.block_io_delay_seconds_total = static_cast<double>(now.blkio_delay_ns) / 1e9;
    row.block_io_delay_seconds_per_second =
        static_cast<double>(now.blkio_delay_ns >= prev.blkio_delay_ns ? now.blkio_delay_ns - prev.blkio_delay_ns
                                                                      : 0) /
        1e9 / elapsed_seconds;
    row.rss_bytes = now.hiwater_rss_bytes;
    row.used_taskstats = true;

    const auto proc_it = impl_->proc_enrichment_cache.find(pid);
    if (proc_it != impl_->proc_enrichment_cache.end()) {
      if (row.rss_bytes == 0) row.rss_bytes = proc_it->second.rss_bytes;
      row.used_proc_fallback = true;
    }

    rows.push_back(std::move(row));
  }

  std::sort(rows.begin(), rows.end(), [](const ProcessKernelSample& left,
                                         const ProcessKernelSample& right) {
    if (left.cpu_percent != right.cpu_percent) return left.cpu_percent > right.cpu_percent;
    return left.pid < right.pid;
  });
  if (static_cast<int>(rows.size()) > impl_->options.topk) {
    rows.resize(impl_->options.topk);
  }

  if (!lightweight_enrichment) {
    for (auto& row : rows) {
      const auto proc_it = impl_->proc_enrichment_cache.find(row.pid);
      if (proc_it != impl_->proc_enrichment_cache.end()) {
        if (row.rss_bytes == 0) row.rss_bytes = proc_it->second.rss_bytes;
      }
      row.pss_bytes = readPssBytesFallback(row.pid);
      const auto inodes = listSocketInodesForPidFallback(row.pid);
      row.net_connections = inodes.size();
      for (uint64_t inode : inodes) {
        const auto sock_it = socket_queues.find(inode);
        if (sock_it == socket_queues.end()) continue;
        row.net_tx_queue_bytes += sock_it->second.tx_queue;
        row.net_rx_queue_bytes += sock_it->second.rx_queue;
      }
      row.used_proc_fallback = true;
    }
  }

  impl_->last_taskstats = std::move(current);
  impl_->ebpf_resource_cache.clear();
  return rows;
}

}  // namespace probe_core
