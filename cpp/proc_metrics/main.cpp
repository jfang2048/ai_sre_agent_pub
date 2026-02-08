#include <algorithm>
#include <fstream>
#include <iostream>
#include <sstream>
#include <string>
#include <vector>

namespace {

constexpr const char* kProcStatPath = "/proc/stat";
constexpr const char* kProcMeminfoPath = "/proc/meminfo";

struct Metric {
  std::string name;
  double value;
};

std::string escapeJson(const std::string& input) {
  std::string output;
  output.reserve(input.size());
  for (const char ch : input) {
    if (ch == '"') {
      output += "\\\"";
    } else if (ch == '\\') {
      output += "\\\\";
    } else {
      output.push_back(ch);
    }
  }
  return output;
}

bool readCpuMetrics(std::vector<Metric>& out) {
  std::ifstream file(kProcStatPath);
  if (!file.is_open()) {
    return false;
  }

  std::string line;
  if (!std::getline(file, line)) {
    return false;
  }

  std::istringstream iss(line);
  std::string cpu;
  long long user = 0;
  long long nice = 0;
  long long system = 0;
  long long idle = 0;
  long long iowait = 0;
  long long irq = 0;
  long long softirq = 0;
  long long steal = 0;

  if (!(iss >> cpu >> user >> nice >> system >> idle >> iowait >> irq >> softirq >> steal) ||
      cpu != "cpu") {
    return false;
  }

  out.push_back({"ext_cpu_user_jiffies", static_cast<double>(user + nice)});
  out.push_back({"ext_cpu_system_jiffies", static_cast<double>(system + irq + softirq)});
  out.push_back({"ext_cpu_idle_jiffies", static_cast<double>(idle + iowait)});
  out.push_back({"ext_cpu_steal_jiffies", static_cast<double>(steal)});
  return true;
}

bool readMemoryMetrics(std::vector<Metric>& out) {
  std::ifstream file(kProcMeminfoPath);
  if (!file.is_open()) {
    return false;
  }

  long long total = 0;
  long long free = 0;
  long long buffers = 0;
  long long cached = 0;
  long long reclaimable = 0;

  std::string line;
  while (std::getline(file, line)) {
    std::istringstream iss(line);
    std::string key;
    long long value = 0;
    std::string unit;
    if (!(iss >> key >> value >> unit)) {
      continue;
    }

    if (key == "MemTotal:") {
      total = value;
    } else if (key == "MemFree:") {
      free = value;
    } else if (key == "Buffers:") {
      buffers = value;
    } else if (key == "Cached:") {
      cached = value;
    } else if (key == "SReclaimable:") {
      reclaimable = value;
    }
  }

  if (total <= 0) {
    return false;
  }

  const long long used = std::max(0LL, total - free - buffers - cached - reclaimable);
  out.push_back({"ext_memory_total_kib", static_cast<double>(total)});
  out.push_back({"ext_memory_used_kib", static_cast<double>(used)});
  return true;
}

void emitJson(const std::vector<Metric>& metrics) {
  std::cout << "{\"metrics\":[";
  for (size_t i = 0; i < metrics.size(); ++i) {
    const auto& metric = metrics[i];
    std::cout << "{\"name\":\"" << escapeJson(metric.name) << "\",\"value\":" << metric.value << "}";
    if (i + 1 < metrics.size()) {
      std::cout << ',';
    }
  }
  std::cout << "]}\n";
}

}  // namespace

int main() {
  std::vector<Metric> metrics;
  metrics.reserve(8);

  const bool cpuOk = readCpuMetrics(metrics);
  const bool memOk = readMemoryMetrics(metrics);
  emitJson(metrics);

  if (!cpuOk && !memOk) {
    return 1;
  }
  return 0;
}
