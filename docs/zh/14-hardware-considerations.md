# 硬件注意事项

English version: [docs/en/14-hardware-considerations.md](../en/14-hardware-considerations.md)

本页说明 `v0.7` 如何把硬件差异纳入观测逻辑，而不是把所有主机都当成完全一样的“通用机器”。

## 为什么需要硬件感知

对 NVMe 来说正常的 queue depth，在 HDD 上可能已经是风险信号。混合核心或 NUMA 很重的 CPU 上，CPU 饱和阈值也不应该照搬小型 SMP 主机。GPU 可观测性更是高度依赖厂商运行时。

因此 collector 会维护一个缓存的硬件 profile，并用它来调整：

- 子采集器的采样间隔
- 阈值解释
- 异常评分
- 上报给 controller 的标签和 capability metrics

如果没有这一层，agent 要么会过拟合某一类硬件，要么只能使用过于宽泛的阈值，最终在生产环境里失去解释力。

## 相关实现在哪里

| 路径 | 作用 |
| --- | --- |
| [`backend/internal/collector/hardware_profile.go`](../../backend/internal/collector/hardware_profile.go) | 硬件发现、缓存 profile、采样 profile、阈值 profile、硬件指标输出 |
| [`backend/internal/collector/protection.go`](../../backend/internal/collector/protection.go) | 使用硬件感知阈值做异常评分和主机保护决策 |
| [`configs/collector.yaml`](../../configs/collector.yaml) | `hardware.refresh_interval`、`probe_core.*interval_samples`、protection 限额 |
| [`cpp/probe_core/gpu_nvml.cpp`](../../cpp/probe_core/gpu_nvml.cpp) | NVIDIA NVML-first 的 GPU 采集路径 |
| [`docs/reference/metrics.md`](../reference/metrics.md) | 硬件相关指标族的说明 |

## 硬件发现模型

collector 会缓存硬件发现结果，而不是高频反复扫描 `/sys` 和 `/proc`。

根据 [`hardware_profile.go`](../../backend/internal/collector/hardware_profile.go) 的当前实现：

- collector 启动时创建 hardware cache
- 硬件发现会读取 `/proc` 和 `/sys`
- 只有在 `hardware.refresh_interval` 到期后才会刷新
- 只有当硬件 profile 变化时，才会重新推导 sampling profile 和 threshold profile

[`configs/collector.yaml`](../../configs/collector.yaml) 中的默认值：

- `hardware.enabled: true`
- `hardware.refresh_interval: "6h"`

它解决的是一个很现实的开销问题：拓扑变化远慢于遥测节奏，频繁全量扫描硬件目录本身就是浪费。

## Collector 当前会识别什么

| 领域 | 代码里实际发现的字段 | 为什么重要 |
| --- | --- | --- |
| CPU | architecture、vendor、model、sockets、cores、threads、NUMA nodes、hybrid-core 信号 | 影响 process/fallback 采样节奏，也影响 CPU/内存阈值 |
| Storage | device count、NVMe/SSD/HDD 数量、dominant class、max queue depth | 决定磁盘 latency 和 queue-depth 的解释方式 |
| Network | interface count、high-speed count、max speed、dominant type、dominant driver、RDMA capability | 决定 retransmit/drop 期望和 netlink 节奏 |
| GPU | device count、vendor、driver、runtime | 决定 GPU 采样节奏和 GPU 阈值默认值 |

当前没有单独建模成硬件 inventory 对象的内容：

- 内存条/DIMM 拓扑或厂商信息

不过内存行为仍然会通过 pressure、utilization 和 NUMA 相关信号被解释，只是不是一个独立的 memory hardware profile 结构体。

## 硬件如何改变采样节奏

collector 会推导硬件感知的 sampling profile，并把它应用到 probe-core 子采集器上。

来自 `deriveHardwareSamplingProfile` 的当前例子：

| 硬件条件 | 当前调整 |
| --- | --- |
| CPU 线程很多、NUMA 节点多或 hybrid CPU | 放慢 per-process 和 `/proc` fallback 刷新 |
| 非常大的 CPU 数量 | 进一步放慢 process 和 pressure 采样 |
| 普通网络、没有 RDMA 或高带宽 NIC | 放慢 netlink 刷新 |
| 没有 GPU | 大幅放慢 GPU 刷新 |
| GPU 数量很多 | 适度放慢 GPU 刷新，控制单周期成本 |

这里做的不是重构 collector 架构，而是在现有 probe-core/collector 模型内调整节奏。

## 更具体的采样节奏计算

最容易理解当前行为的方法，是把下面几部分放在一起看：

- [`configs/collector.yaml`](../../configs/collector.yaml)
  - `collection_interval: "5s"`
  - `probe_core.interval: "1s"`
- [`hardware_profile.go`](../../backend/internal/collector/hardware_profile.go)
  - `deriveHardwareSamplingProfile`
- [`aux_sampling.go`](../../backend/internal/collector/aux_sampling.go)
  - 兼容进程扫描、日志、external metrics 的 helper cadence

### 例子 A：小型 VM、无 GPU、普通网卡

下面这些值是符合当前代码逻辑的示意 profile：

- `collector_hardware_cpu_threads = 16`
- `collector_hardware_cpu_numa_nodes = 1`
- `collector_hardware_gpu_devices_total = 0`
- `collector_hardware_network_high_speed_interfaces_total = 0`
- `collector_hardware_network_rdma_capable = 0`

更可能出现的派生 cadence 指标：

| 指标 | 可能值 | 原因 |
| --- | --- | --- |
| `collector_hardware_capability_process_interval_samples` | `2` | 主机规模还不需要放慢 process refresh |
| `collector_hardware_capability_host_proc_interval_samples` | `10` | 默认兼容 fallback 间隔 |
| `collector_hardware_capability_pressure_interval_samples` | `3` | 默认 PSI 节奏 |
| `collector_hardware_capability_netlink_interval_samples` | `3` | 普通网络会放慢 netlink 节奏 |
| `collector_hardware_capability_gpu_interval_samples` | `8` | 没有 GPU，GPU 刷新会明显放慢 |

它在运行时意味着：

- probe-core 的 process sampling 仍然每 `2s` 一次
- 兼容 `/proc` 进程 fallback 每 `max(5s, 1s * 10) = 10s` 一次
- GPU 子采集每 `8s` 一次，即使 collector batch 主循环还是每 `5s`
- log tail 维持在 `max(15s, 3 * 5s) = 15s`
- external metrics 维持在 `max(30s, 6 * 5s) = 30s`

### 例子 B：128 线程、NUMA、4 张 NVIDIA GPU 的主机

下面这些值是符合当前代码逻辑的示意 profile：

- `collector_hardware_cpu_threads = 128`
- `collector_hardware_cpu_numa_nodes = 2`
- `collector_hardware_gpu_devices_total = 4`
- `collector_hardware_network_high_speed_interfaces_total = 1`
- `collector_hardware_network_rdma_capable = 1`

更可能出现的派生 cadence 指标：

| 指标 | 可能值 | 原因 |
| --- | --- | --- |
| `collector_hardware_capability_process_interval_samples` | `4` | 大 CPU 数量会放慢 per-process refresh |
| `collector_hardware_capability_host_proc_interval_samples` | `16` | 大主机上的兼容 fallback 更贵 |
| `collector_hardware_capability_pressure_interval_samples` | `4` | 大 CPU profile 会放松 PSI 节奏 |
| `collector_hardware_capability_netlink_interval_samples` | `2` | 高速或 RDMA 网络保持更紧的 netlink 节奏 |
| `collector_hardware_capability_gpu_interval_samples` | `2` | GPU 多时会适度放慢 GPU refresh，但不会像无 GPU 那样大幅放慢 |

运行时效果：

- probe-core process sampling 每 `4s` 一次
- 兼容 `/proc` process fallback 每 `max(5s, 1s * 16) = 16s` 一次
- compatibility thermal / NIC sysfs / IRQ / RDMA 扫描会继续留在独立的慢硬件层，而不是跟运行时 extended tier 同频
- GPU 子采集每 `2s` 一次
- 如果 collector 进入 `pressure` 或 `critical`，[`effectiveAuxiliaryInterval`](../../backend/internal/collector/aux_sampling.go) 还会进一步优先使用更慢的 collector cadence

## 慢速硬件层到底发送什么

[`probe/cadence.go`](../../backend/internal/collector/probe/cadence.go) 里的慢速 compatibility 硬件层，现在也带上了单独的 payload suppression 语义。

一次真实硬件刷新时，fallback batch 可能包含：

```json
{
  "metrics": [
    {"name":"node_thermal_zone_temp_celsius","value":87.5},
    {"name":"node_network_interface_speed_mbps","value":25000,"labels":{"device":"eth0"}},
    {"name":"collector_compat_payload_refreshed","value":1,"labels":{"component":"hardware"}}
  ]
}
```

而下一次只是 cache hit、且 `suppress_cached_compat_hardware_metrics: true` 时，collector 可能只发送：

```json
{
  "metrics": [
    {"name":"collector_compat_collection_cache_hit","value":1,"labels":{"component":"hardware"}},
    {"name":"collector_compat_payload_suppressed","value":1,"labels":{"component":"hardware"}}
  ]
}
```

[`StoreMetrics`](../../backend/internal/controller/ingest/store.go) 会在这种情况下续接上一轮硬件 fallback 视图。这样做的权衡是：

- 更低的 steady-state batch 体积，以及更少重复 sysfs 风格值
- controller 侧的硬件上下文仍然保留
- 单个 batch 的冗余更少，但下一次真实硬件刷新或主路径 source 更新仍然能显式替换或清空这份视图

## 不增加新重探针的广义硬件诊断

当前实现增加了一层 broad hardware warning，但没有引入新的常驻高权限采集器：

- `collector_hardware_warning_total`
- `collector_hardware_warning{domain="cpu|memory|disk|network|gpu",reason=...,signal=...}`

这些 warning 来自 collector 已经掌握的信号和缓存硬件阈值，因此：

- CPU 提示来自 throttling、iowait、contention 这类已有信号
- 内存提示来自 pressure 和 NUMA miss / imbalance 风格信号
- 磁盘提示来自 latency、queue depth 和 IO pressure
- 网络提示来自 retransmit、softnet drop、errors 和 RDMA congestion
- GPU 提示来自 throttle、memory pressure 和活跃进程但低利用率的组合

这层设计故意保持“广而便宜”，而不是宣称做了完整厂商级硬件诊断。目标是在不增加新探针的前提下，给 operator 和下游筛选增加一层更容易消费的硬件导向摘要。

在 controller 侧，这些 broad hardware warning 现在也只会在它们真的构成症状上下文时才参与 retrieval。泛化问题不会因为“系统支持硬件诊断”就强行触发 RAG；但一旦磁盘延迟、NIC retransmit、GPU thermal pressure、CPU throttling 这类硬件信号真正进入 findings 或 anomaly hints，它们仍然可以流入 retrieval query 和 prompt。

## 硬件如何改变阈值

`deriveHardwareThresholdProfile` 会根据硬件类别修改阈值。

当前代码里的具体例子：

| 硬件条件 | 阈值影响 |
| --- | --- |
| dominant storage class 为 `nvme` | 更低的 latency 阈值、更高的预期 queue depth |
| dominant storage class 为 `hdd` | 更高可接受 latency、更低 queue depth、更高 IO pressure 容忍度 |
| hybrid CPU | 更低的 CPU busy/critical 阈值 |
| 多 NUMA CPU | 更低的 memory-pressure 阈值 |
| RDMA 或 high-speed NIC | 更严格的 retransmit 期望，并单独允许一定 softnet drop |
| NVIDIA GPU | 使用比通用默认值更低的 GPU memory pressure 阈值 |
| AMD 或 Intel GPU | 使用不同的 low-utilization 阈值 |

这层逻辑的目标不是声称“对每个型号都绝对精确”，而是避免明显错误的“一套阈值适配所有硬件”。

## 更具体的解释例子

阈值 profile 的意义在于：同一个原始指标，在不同硬件上可能代表完全不同的风险。

### 例子 1：同样的磁盘延迟，NVMe 和 HDD 的含义不同

示意性的实时指标：

```text
node_disk_request_latency_p99_seconds = 0.020
node_disk_queue_depth_total = 18
```

当前代码里会拿它和这些阈值比较：

- NVMe profile:
  - `collector_hardware_threshold_disk_latency_seconds = 0.015`
  - `collector_hardware_threshold_disk_queue_depth = 24`
- HDD profile:
  - `collector_hardware_threshold_disk_latency_seconds = 0.080`
  - `collector_hardware_threshold_disk_queue_depth = 2`

解释结果：

- 在 NVMe 上，latency 已经高于预期，但 queue depth 还没超过预期
- 在 HDD 上，latency 仍可接受，但 queue depth 已经远高于预期

这也是为什么 [`protection.go`](../../backend/internal/collector/protection.go) 里磁盘异常分数会取 latency 和 queue-depth 两个 over-threshold 检查的最大值，而不是用一条固定规则硬套所有存储。

### 例子 2：高速 NIC 上的 retransmit

示意性的实时指标：

```text
node_tcp_retransmit_ratio = 0.012
node_tcp_retransmits_per_second = 0.8
node_softnet_dropped_per_second = 2
```

当前阈值行为：

- 普通 NIC profile 会保持 `collector_hardware_threshold_network_retransmit_ratio = 0.02`
- RDMA 或高速 NIC profile 会把它降到 `0.01`

解释结果：

- 在普通 NIC 主机上，这还算偏高但没超过更严格阈值
- 在高速 NIC 或 RDMA 主机上，同样的 retransmit ratio 已经高于预期

这也是为什么 [`protection.go`](../../backend/internal/collector/protection.go) 里的网络异常评分会同时考虑 retransmit ratio 和 softnet drops。

### 例子 3：有活跃 GPU 进程但 SM 利用率偏低

示意性的实时指标：

```text
node_gpu_utilization_sm_avg_percent = 20
node_gpu_process_total = 3
node_gpu_memory_used_percent = 89
node_gpu_throttle_power_any = 0
```

当前偏 NVIDIA 的阈值：

- `collector_hardware_threshold_gpu_low_util_percent = 35`
- 代码里派生出的 memory pressure threshold: `85`

解释结果：

- 有活跃 GPU 进程但 SM 利用率偏低，可能意味着 feeder starvation 或 stall
- 即使 thermal / power throttle 还是 0，较高的 GPU memory pressure 也会抬高异常分数

这说明当前 GPU 评分并不是单靠 utilization 一个指标。

## 一些更具体的硬件画像例子

两台机器即使暴露同一个原始指标名，也可能需要完全不同的解释。

| 示例主机 | 当前代码里更可能出现的画像效果 |
| --- | --- |
| 小型 VM、无 GPU、普通网卡 | GPU 刷新会更慢，网络预期更简单，存储使用通用默认值 |
| 大型 NUMA 训练主机、多线程 CPU、多块 GPU | 进程和 fallback 采样更慢，内存压力解释更严格，GPU 特定阈值会启用 |
| NVMe 为主的机器 | 磁盘延迟阈值更低，但预期 queue depth 更高 |
| HDD 为主的机器 | 可接受延迟更高，但预期 queue depth 更低 |

这也是为什么 collector 不只导出原始信号，还会导出 `collector_hardware_capability_*` 和 `collector_hardware_threshold_*` 这类指标。

## Collector 会向 Controller 暴露哪些硬件指标

collector 会输出硬件元数据和派生策略指标，例如：

- `collector_hardware_cpu_sockets`
- `collector_hardware_cpu_cores`
- `collector_hardware_cpu_threads`
- `collector_hardware_cpu_numa_nodes`
- `collector_hardware_cpu_hybrid`
- `collector_hardware_storage_devices_total`
- `collector_hardware_network_interfaces_total`
- `collector_hardware_network_high_speed_interfaces_total`
- `collector_hardware_network_max_speed_mbps`
- `collector_hardware_network_rdma_capable`
- `collector_hardware_gpu_devices_total`
- `collector_hardware_capability_*`
- `collector_hardware_threshold_*`
- `collector_hardware_*_anomaly_score`
- `collector_hardware_warning_total`
- `collector_hardware_warning{domain=...,reason=...,signal=...}`

这些指标存在的意义，是让 controller 和运维能直接看到 collector 实际选择了什么 profile，而不是靠猜。

## 在真实主机上应该看什么

当你在验证一种新硬件类型时，不要只盯 workload 症状，建议把下面这些指标一起看：

| 问题 | 应该看哪些指标 | 为什么 |
| --- | --- | --- |
| 硬件发现真的成功了吗？ | `collector_hardware_refresh_age_seconds`、`collector_hardware_storage_profile`、`collector_hardware_network_profile`、`collector_hardware_gpu_profile` | 证明 collector 真正构建了 profile，而不是退回通用默认值 |
| collector 有没有对这类硬件放慢昂贵路径？ | `collector_hardware_capability_process_interval_samples`、`collector_hardware_capability_host_proc_interval_samples`、`collector_hardware_capability_gpu_interval_samples`、`collector_compat_collection_interval_seconds{component="hardware"}` | 直接看到大主机或无 GPU 主机上的 backoff 是否生效，以及 fallback 硬件扫描是否真的比运行时路径更慢 |
| 阈值是硬件特化的还是通用的？ | `collector_hardware_threshold_disk_latency_seconds`、`collector_hardware_threshold_disk_queue_depth`、`collector_hardware_threshold_network_retransmit_ratio` | 直接暴露阈值 profile |
| 当前到底是哪个硬件域在推动保护模式？ | `collector_hardware_cpu_anomaly_score`、`collector_hardware_memory_anomaly_score`、`collector_hardware_disk_anomaly_score`、`collector_hardware_gpu_anomaly_score`、`collector_hardware_network_anomaly_score`、`collector_hardware_warning_total` | 帮你定位当前 protection pressure 的硬件来源，并快速确认 broad hardware warning 是否已经被拉高 |

如果这些指标和真实主机类型对不上，通常问题在 discovery visibility 或容器部署位置，而不在 reasoning 层。

## GPU 这部分的现实边界

GPU 是当前仓库里最依赖运行时条件的一部分硬件观测能力。

当前仓库能做的事：

- 从 `/sys/class/drm/card*` 检测 GPU inventory
- 从 `/proc/driver/nvidia/version` 推断 runtime
- probe-core 中优先使用 NVML 采集 GPU（包括细粒度的降频原因、NVLink 拓扑状态和实时能耗）
- 当 NVML 不可用时，有限度地 fallback 到 `nvidia-smi`

实际含义是：

- NVIDIA 是当前最强的 GPU 遥测路径
- 非 NVIDIA GPU 的 inventory 可能仍可检测，但丰富的设备/进程级遥测会更有限
- 运行时库或设备访问缺失时，GPU 可观测性会下降，但不至于把整个 collector 一起拖死

## 部署上的注意事项

硬件感知逻辑只有在 collector 能正确看到主机时才有意义。

当前仓库的前提包括：

- `host-observer` 风格部署能提供最完整的 host namespace 和 kernel 可见性
- `/sys` 和 `/proc` 至少要有足够的可读性
- GPU 运行时可见性取决于 driver/runtime 访问
- eBPF 行为取决于内核支持和权限

如果这些前提不满足：

- collector 通常仍然能启动
- source marker 和 telemetry-quality 信号预期会显示 degraded 或 compatibility 模式
- 阈值和异常评分会变得不那么有代表性

## 如果硬件发现失败会怎样

collector 会退回一个保守默认 profile：

- architecture 使用 `runtime.GOARCH`
- CPU 数量使用 `runtime.NumCPU()`
- storage/network class 记为 unknown
- GPU 默认视为 none

这样做的效果是：collector 仍然能运行，但会失去大部分硬件特化能力。系统会变得更“通用”，而不是完全失明。

## 一个具体失败场景

如果容器里对 `/sys` 的访问受限：

- collector 仍然能启动
- CPU 数量还能退回到 `runtime.NumCPU()`
- 存储和 NIC 类型可能会变成 `unknown`
- 如果 runtime 库或设备访问也不可见，GPU 专用化能力也会下降

这时系统仍然可运行，只是阈值会更保守、更不依赖具体硬件画像。

## 限制与取舍

当前硬件模型的设计是偏务实的：

- 通过缓存来压低开销
- 区分主要硬件类别，而不是每个厂商 SKU 的细节
- 提升阈值和节奏的合理性，但不假装自己是完整 CMDB
- 在 `/sys` 和 `/proc` 能提供足够拓扑信息的 Linux 主机上效果最好

## 参见

- [指标与信号](13-metrics-and-signals.md)
- [代码库地图](09-codebase-map.md)
- [核心文件](10-core-files.md)
- [数据流](05-data-flow.md)
- [配置说明](../operations/configuration.md)
- [指标参考](../reference/metrics.md)
