#include <chrono>
#include <fstream>
#include <iostream>
#include <sstream>
#include <string>
#include <vector>

namespace {
struct Metric {
  std::string name;
  double value;
};

bool readCpu(std::vector<Metric>& out) {
  std::ifstream f("/proc/stat");
  if (!f.is_open()) return false;
  std::string line;
  if (!std::getline(f, line)) return false;
  std::istringstream iss(line);
  std::string cpu;
  long long user = 0, nice = 0, system = 0, idle = 0, iowait = 0, irq = 0,
            softirq = 0, steal = 0;
  iss >> cpu >> user >> nice >> system >> idle >> iowait >> irq >> softirq >>
      steal;
  out.push_back({"ext_cpu_user_jiffies", static_cast<double>(user + nice)});
  out.push_back({"ext_cpu_system_jiffies", static_cast<double>(system + irq + softirq)});
  out.push_back({"ext_cpu_idle_jiffies", static_cast<double>(idle + iowait)});
  out.push_back({"ext_cpu_steal_jiffies", static_cast<double>(steal)});
  return true;
}

bool readMem(std::vector<Metric>& out) {
  std::ifstream f("/proc/meminfo");
  if (!f.is_open()) return false;
  std::string key, unit;
  long long total = 0, free = 0, buffers = 0, cached = 0, sreclaim = 0;
  while (f >> key) {
    long long val = 0;
    f >> val >> unit;
    if (key == "MemTotal:") total = val;
    else if (key == "MemFree:") free = val;
    else if (key == "Buffers:") buffers = val;
    else if (key == "Cached:") cached = val;
    else if (key == "SReclaimable:") sreclaim = val;
  }
  long long used = total - free - buffers - cached - sreclaim;
  out.push_back({"ext_memory_total_kib", static_cast<double>(total)});
  out.push_back({"ext_memory_used_kib", static_cast<double>(used)});
  return true;
}

void emitJson(const std::vector<Metric>& metrics) {
  std::cout << "{\"metrics\":[";
  for (size_t i = 0; i < metrics.size(); ++i) {
    const auto& m = metrics[i];
    std::cout << "{\"name\":\"" << m.name << "\",\"value\":" << m.value << "}";
    if (i + 1 < metrics.size()) std::cout << ",";
  }
  std::cout << "]}\n";
}
}  // namespace

int main() {
  std::vector<Metric> metrics;
  readCpu(metrics);
  readMem(metrics);
  emitJson(metrics);
  return 0;
}
