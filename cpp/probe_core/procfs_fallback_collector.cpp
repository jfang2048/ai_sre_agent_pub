#include "procfs_fallback_collector.h"

#include <dirent.h>
#include <errno.h>

#include <cctype>
#include <cstdlib>
#include <fstream>
#include <sstream>
#include <string>
#include <vector>

#include "posix_raii.h"

namespace probe_core {
namespace {

constexpr const char* kProcPath = "/proc";

long pageSizeBytes() {
  static const long kPageSize = std::max<long>(sysconf(_SC_PAGESIZE), 4096);
  return kPageSize;
}

bool isNumeric(const std::string& s) {
  if (s.empty()) return false;
  for (char c : s) {
    if (c < '0' || c > '9') return false;
  }
  return true;
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
  const unsigned long long v = strtoull(s.c_str(), &end, 10);
  if (errno != 0 || end == s.c_str()) return 0;
  return static_cast<uint64_t>(v);
}

bool readFile(const std::string& path, std::string* out) {
  std::ifstream f(path);
  if (!f.is_open()) return false;
  std::ostringstream ss;
  ss << f.rdbuf();
  *out = ss.str();
  return true;
}

bool parseProcStat(int pid, ProcFallbackSnapshot* out) {
  if (out == nullptr) return false;
  std::string path = std::string(kProcPath) + "/" + std::to_string(pid) + "/stat";
  std::string line;
  if (!readFile(path, &line)) return false;
  const auto l = line.find('(');
  const auto r = line.rfind(')');
  if (l == std::string::npos || r == std::string::npos || r <= l) return false;

  out->pid = pid;
  out->name = line.substr(l + 1, r - l - 1);
  const std::string rest = line.substr(r + 1);
  const auto fields = splitWS(rest);
  if (fields.size() < 22) return false;

  out->minor_faults = parseU64(fields[7]);
  out->major_faults = parseU64(fields[9]);
  const uint64_t utime = parseU64(fields[11]);
  const uint64_t stime = parseU64(fields[12]);
  out->cpu_total = utime + stime;
  const uint64_t rss_pages = parseU64(fields[21]);
  out->rss_bytes = rss_pages * static_cast<uint64_t>(pageSizeBytes());
  if (fields.size() > 39) {
    out->blkio_delay_ticks = parseU64(fields[39]);
  }
  return true;
}

void parseProcStatusCtx(int pid, ProcFallbackSnapshot* out) {
  std::ifstream f(std::string(kProcPath) + "/" + std::to_string(pid) + "/status");
  if (!f.is_open()) return;
  std::string line;
  while (std::getline(f, line)) {
    if (line.rfind("voluntary_ctxt_switches:", 0) == 0) {
      const auto fields = splitWS(line);
      if (fields.size() >= 2) out->voluntary_ctx = parseU64(fields[1]);
      continue;
    }
    if (line.rfind("nonvoluntary_ctxt_switches:", 0) == 0) {
      const auto fields = splitWS(line);
      if (fields.size() >= 2) out->nonvoluntary_ctx = parseU64(fields[1]);
      continue;
    }
  }
}

void parseProcSchedstat(int pid, ProcFallbackSnapshot* out) {
  std::ifstream f(std::string(kProcPath) + "/" + std::to_string(pid) + "/schedstat");
  if (!f.is_open()) return;
  uint64_t run_ns = 0;
  uint64_t wait_ns = 0;
  if (!(f >> run_ns >> wait_ns)) return;
  out->sched_run_ns = run_ns;
  out->sched_wait_ns = wait_ns;
}

void parseProcIO(int pid, ProcFallbackSnapshot* out) {
  std::ifstream f(std::string(kProcPath) + "/" + std::to_string(pid) + "/io");
  if (!f.is_open()) return;
  std::string line;
  while (std::getline(f, line)) {
    if (line.rfind("read_bytes:", 0) == 0) {
      const auto fields = splitWS(line);
      if (fields.size() >= 2) out->read_bytes = parseU64(fields[1]);
      continue;
    }
    if (line.rfind("write_bytes:", 0) == 0) {
      const auto fields = splitWS(line);
      if (fields.size() >= 2) out->write_bytes = parseU64(fields[1]);
      continue;
    }
  }
}

}  // namespace

std::unordered_map<int, ProcFallbackSnapshot> collectProcProcessSnapshots() {
  std::unordered_map<int, ProcFallbackSnapshot> out;
  ScopedDir proc(opendir(kProcPath));
  if (!proc.valid()) return out;

  dirent* ent = nullptr;
  while ((ent = readdir(proc.get())) != nullptr) {
    if (!isNumeric(ent->d_name)) continue;
    const int pid = atoi(ent->d_name);
    if (pid <= 0) continue;
    ProcFallbackSnapshot snapshot;
    if (!parseProcStat(pid, &snapshot)) continue;
    parseProcStatusCtx(pid, &snapshot);
    parseProcSchedstat(pid, &snapshot);
    parseProcIO(pid, &snapshot);
    out.emplace(pid, std::move(snapshot));
  }
  return out;
}

std::unordered_set<uint64_t> listSocketInodesForPidFallback(int pid) {
  std::unordered_set<uint64_t> inodes;
  const std::string fd_path = std::string(kProcPath) + "/" + std::to_string(pid) + "/fd";
  ScopedDir dir(opendir(fd_path.c_str()));
  if (!dir.valid()) return inodes;

  dirent* ent = nullptr;
  char link_buf[512];
  while ((ent = readdir(dir.get())) != nullptr) {
    if (ent->d_name[0] == '.') continue;
    const std::string link_path = fd_path + "/" + ent->d_name;
    const ssize_t n = readlink(link_path.c_str(), link_buf, sizeof(link_buf) - 1);
    if (n <= 0) continue;
    link_buf[n] = '\0';
    const std::string target(link_buf);
    if (target.rfind("socket:[", 0) != 0) continue;
    const auto start = target.find('[');
    const auto end = target.find(']');
    if (start == std::string::npos || end == std::string::npos || end <= start + 1) continue;
    inodes.insert(parseU64(target.substr(start + 1, end - start - 1)));
  }
  return inodes;
}

uint64_t readPssBytesFallback(int pid) {
  std::ifstream f(std::string(kProcPath) + "/" + std::to_string(pid) + "/smaps_rollup");
  if (!f.is_open()) return 0;
  std::string line;
  while (std::getline(f, line)) {
    if (line.rfind("Pss:", 0) != 0) continue;
    const auto fields = splitWS(line);
    if (fields.size() < 2) return 0;
    return parseU64(fields[1]) * 1024ULL;
  }
  return 0;
}

std::string readProcessCommFallback(int pid) {
  std::string raw;
  if (!readFile(std::string(kProcPath) + "/" + std::to_string(pid) + "/comm", &raw)) {
    return {};
  }
  std::string out = raw;
  while (!out.empty() && (out.back() == '\n' || out.back() == '\r')) {
    out.pop_back();
  }
  return out;
}

}  // namespace probe_core
