# 采集队列与压缩

English version: [docs/en/06-collector-queue-and-compaction.md](../en/06-collector-queue-and-compaction.md)

本页解释最直接影响宿主机开销控制的实现部分：

- 发送前的去重、抑制、压缩
- 有界的本地 spool 队列
- 发送与重放路径
- 控制器或网络变慢时会发生什么

内容基于当前 `v0.8` 实现，而不是通用的监控理论。

## 为什么需要这一层

如果没有压缩和队列，collector 只能在两种坏结果里选一种：

1. 每个周期都直接发送，一旦控制器或网络变慢就阻塞采集
2. 一直把待发送数据留在内存里，最终让 collector 自己占用过多内存

当前实现用三件事解决这个问题：

| 问题 | 实际实现 | 价值 |
| --- | --- | --- |
| 未变化数据每个周期都重复发 | [`../../backend/internal/collector/metric_suppression.go`](../../backend/internal/collector/metric_suppression.go), [`../../backend/internal/collector/process_payload_suppression.go`](../../backend/internal/collector/process_payload_suppression.go), [`../../backend/internal/collector/aux_sampling.go`](../../backend/internal/collector/aux_sampling.go) | 平稳期 payload 更小 |
| 控制器慢不能卡住采集循环 | [`../../backend/internal/collector/spool/spool.go`](../../backend/internal/collector/spool/spool.go) | 采集与发送解耦 |
| 重放不能抢占过多 CPU 时间 | [`../../backend/internal/collector/transport/client.go`](../../backend/internal/collector/transport/client.go), [`../../backend/internal/collector/collector.go`](../../backend/internal/collector/collector.go) | backlog 回放是有界的 |

对业务或产品读者来说，这一层的意义是：降低“监控系统反过来干扰生产负载”的风险。

## 端到端发送工作流

collector 侧的投递路径本身就是一条有清晰失败边界的工作流。

| 步骤 | 实际发生什么 | 主要文件 | 为什么这一步存在 | 如果没有会怎样 |
| --- | --- | --- | --- | --- |
| 1. 构造一个 batch | collector 把当前采样周期收敛成一个 `TelemetryBatch` | [`../../backend/internal/collector/collector.go`](../../backend/internal/collector/collector.go) | 发送必须从一个有边界的 payload 出发，而不是零散内存状态 | retry 和 queue 就得对半成品状态工作 |
| 2. 抑制重复数据 | low-churn 指标、cache-hit helper、近似不变的进程 payload 会被 marker 化后省略 | [`../../backend/internal/collector/metric_suppression.go`](../../backend/internal/collector/metric_suppression.go), [`../../backend/internal/collector/aux_sampling.go`](../../backend/internal/collector/aux_sampling.go), [`../../backend/internal/collector/process_payload_suppression.go`](../../backend/internal/collector/process_payload_suppression.go) | collector 不应该为未变化证据支付完整发送成本 | 平静节点会在 CPU、spool、网络上浪费太多字节 |
| 3. 序列化并入队 | protobuf payload 在真正发送前先写入 `spool.log` | [`../../backend/internal/collector/spool/spool.go`](../../backend/internal/collector/spool/spool.go) | 采样和投递必须解耦 | controller 或网络抖动会直接卡住采样热路径 |
| 4. 有界 drain | transport 通过 `Next()` 读取未确认数据，并遵守 `MaxRecords` / `MaxDuration` | [`../../backend/internal/collector/transport/client.go`](../../backend/internal/collector/transport/client.go) | backlog 回放不能长期霸占 collector 时间 | 一次恢复期可能比正常采样更贵 |
| 5. 发往 controller | client 走 failover 或 mirror，支持可选 gzip、timeout、retry | [`../../backend/internal/collector/transport/client.go`](../../backend/internal/collector/transport/client.go) | 投递需要有界重试和多端点策略 | 一个坏 endpoint 或一条慢 RPC 就可能拖慢整体进度 |
| 6. ACK 后提交 | 只有 controller ACK 的 batch 和发送内容一致时，offset 才推进 | [`../../backend/internal/collector/spool/spool.go`](../../backend/internal/collector/spool/spool.go), [`../../backend/internal/collector/transport/client.go`](../../backend/internal/collector/transport/client.go) | collector 需要“最近证据至少可重放一次”的语义 | batch 可能被静默丢失，或者在真正被接收前就被提交 |

这也是为什么 queue、send、commit 在代码里是分开的，而不是一个笼统的 `send()`。

## 关键文件

| 文件 | 职责 | 如果缺失或被误解 |
| --- | --- | --- |
| [`../../backend/internal/collector/probe_core_convert.go`](../../backend/internal/collector/probe_core_convert.go) | 默认丢弃大部分重复的原始 `probe_core_*` host/resource 别名，只保留控制面真正使用的 `node_*` / `rca_*` | batch 再次膨胀，指标契约变得混乱 |
| [`../../backend/internal/collector/metric_suppression.go`](../../backend/internal/collector/metric_suppression.go) | 在 `low_churn_metrics_refresh_interval` 之前抑制未变化的 collector/runtime/hardware 低频状态 | 平稳期重复发送相同运行时状态 |
| [`../../backend/internal/collector/aux_sampling.go`](../../backend/internal/collector/aux_sampling.go) | 缓存慢速 helper 输出，并在 cache hit 时抑制重复的日志/兼容进程 payload | 日志和兼容 `/proc` 路径更贵、更吵 |
| [`../../backend/internal/collector/process_payload_suppression.go`](../../backend/internal/collector/process_payload_suppression.go) | 对近似不变的热点进程列表做抑制 | `TelemetryBatch.Processes` 在平稳期占太多字节 |
| [`../../backend/internal/collector/spool/spool.go`](../../backend/internal/collector/spool/spool.go) | 持久化、有界的发送前队列 | 不是阻塞采集，就是内存失控 |
| [`../../backend/internal/collector/transport/client.go`](../../backend/internal/collector/transport/client.go) | failover 发送与受限 drain | backlog 重放可能长期霸占 collector |
| [`../../backend/internal/controller/ingest/server.go`](../../backend/internal/controller/ingest/server.go) | 只有在“真正刷新且结果为空”时才清空 helper 状态 | 被抑制 payload 会被误判为“数据丢了” |
| [`../../backend/internal/controller/ingest/store.go`](../../backend/internal/controller/ingest/store.go) | 对被抑制的低频状态和慢速硬件状态做 carry-forward | 小 payload 会看起来像不完整数据 |

## 发送前有哪些压缩阶段

```mermaid
flowchart LR
    A["原始 probe/core + helper"] --> B["别名转换"]
    B --> C["低频指标抑制"]
    C --> D["helper payload 抑制"]
    D --> E["进程 payload 抑制"]
    E --> F["TelemetryBatch"]
    F --> G["spool.log + spool.offset"]
    G --> H["有界 drain + gRPC 发送"]
```

这些阶段各自解决不同问题，不能简单互相替代。

## 哪些内容会被抑制

### 1. 原始别名重复

之前的问题：

- 一个宿主机事实可能被发两次
- 例如 `probe_core_memory_used_bytes` 和 `node_memory_Used_bytes`

当前行为：

- [`../../backend/internal/collector/probe_core_convert.go`](../../backend/internal/collector/probe_core_convert.go) 默认保留控制面真正使用的别名
- 只有显式设置 `probe_core.emit_raw_aliased_metrics: true` 才恢复原始重复项

保留了什么：

- 控制器看到的仍然是它实际分析用的指标族

代价：

- 如果外部工具直接依赖原始 `probe_core_*` 指标，需要显式重新开启

### 2. collector/runtime 低频状态抑制

之前的问题：

- probe source、runtime mode、probe-core 模块状态、硬件 profile 等低变化信息每个周期都重发

当前行为：

- [`../../backend/internal/collector/metric_suppression.go`](../../backend/internal/collector/metric_suppression.go) 会在 `low_churn_metrics_refresh_interval` 之前抑制未变化项
- batch 里会打上：
  - `collector_metrics_partial_update = 1`
  - `collector_metrics_suppressed_count = N`

保留了什么：

- 控制面沿用上一次值
- [`../../backend/internal/controller/ingest/store.go`](../../backend/internal/controller/ingest/store.go) 会重建被省略的状态

引入的风险：

- 如果不了解 `collector_metrics_partial_update`，很容易误以为 collector 没有采到这些状态

### 3. cache-hit helper payload 抑制

之前的问题：

- 即使日志或兼容进程扫描本轮只是命中缓存，payload 仍然会被重复发送

当前行为：

- [`../../backend/internal/collector/aux_sampling.go`](../../backend/internal/collector/aux_sampling.go) 在缓存命中且视图未刷新时不再发送 payload
- 同时输出：
  - `collector_aux_payload_refreshed`
  - `collector_aux_payload_suppressed`

保留了什么：

- 控制器继续使用上一次有效的日志/进程视图

如果没有这些 marker 会怎样：

- “本轮没发 payload” 会和 “本轮刷新后确实为空” 混在一起

### 4. active-source 进程 payload 抑制

之前的问题：

- 即使 helper 已经压缩，热点进程列表仍可能每个周期都发一份几乎相同的内容

当前行为：

- [`../../backend/internal/collector/process_payload_suppression.go`](../../backend/internal/collector/process_payload_suppression.go) 会基于 PID、归一化进程名、CPU 桶、RSS 桶、IO 桶生成粗粒度指纹
- 指纹没有明显变化时，进程 payload 会被省略，直到：
  - 形态真正变化
  - 或达到 `process_payload_refresh_interval`

同时输出：

- `collector_process_payload_refreshed`
- `collector_process_payload_suppressed`

代价：

- 强归因的进程细节在两次强制刷新之间会更粗一些
- 但节点级压力指标仍然每个周期都在

这是有意的取舍：进程归因是贵的上下文，不是平稳期最先要保留的信号。

## 发送前的队列到底是什么

这里的“队列”不是抽象概念，而是 [`../../backend/internal/collector/spool/spool.go`](../../backend/internal/collector/spool/spool.go) 里的持久化 spool。

### 实际结构

| 文件/结构 | 作用 |
| --- | --- |
| `spool.log` | 追加写的记录文件 |
| `spool.offset` | 最近一次成功提交的读取偏移 |
| 4 字节头 | 每条记录的长度 |

### 实际 API

| 方法 | 作用 |
| --- | --- |
| `Enqueue(payload)` | 追加一条 batch |
| `Next()` | 读取下一条未确认 payload，但不推进 offset |
| `Commit(nextOffset)` | 只有发送成功后才推进 offset |
| `Snapshot()` | 暴露 backlog、文件大小、驱逐次数、损坏恢复次数 |

### 为什么要先入队再发

如果每个采集周期都同步直发：

- gRPC 卡顿会直接进入采集热路径
- 网络重试会和采集抢时间片
- 接收端慢会反向影响采样稳定性

spool 的价值是把三件事拆开：

- 采集节奏
- 本地落盘缓冲
- 之后的重放节奏

## 为什么 direct send 会更差

如果 collector 不经过 spool，而是在每个周期都同步直发：

- 主机侧采样循环会直接被 controller 延迟拖住
- retry 逻辑会和采集 CPU 预算正面竞争
- 接收端短时变慢更容易变成 missed sample
- 只靠内存缓冲要么膨胀太大，要么只能突然丢数据

现在这套 queue + drain 设计是一种折中：

- 对宿主机更安全
- 平稳期更便宜
- 但在极长故障期间不会追求“完美回放”，而是优先保留最近数据

## 接收端慢的时候会发生什么

### 正常低开销路径

与默认配置一致的示例：

```yaml
collection_interval: "5s"
spool_max_bytes: 134217728
spool_sync_interval: "1s"
spool_offset_sync_interval: "1s"
protection:
  max_drain_records_per_cycle: 8
  max_drain_duration: "750ms"
```

平稳期被压缩后的 batch 可以像这样：

```json
{
  "collector_id": "node-a",
  "metrics": {
    "node_cpu_usage_percent": 27.4,
    "node_memory_Used_bytes": 9544371776,
    "node_memory_MemTotal_bytes": 17179869184,
    "collector_self_cpu_percent": 1.2,
    "collector_spool_backlog_bytes": 0
  },
  "processes": [],
  "logs": []
}
```

这里 `processes` 和 `logs` 为空，不代表“没有视图”，而可能代表“本轮刻意不重复发”。

### 慢接收端示例

符合当前代码逻辑的序列：

1. collector 仍按 `5s` 节奏采样
2. gRPC 发送连续 `30s` 失败
3. 每个 batch 写入 `spool.log`
4. `collector_spool_backlog_bytes` 持续上升
5. 网络恢复后，[`DrainWithOptions`](../../backend/internal/collector/transport/client.go) 每个周期最多只回放：
   - `MaxRecords`
   - `MaxDuration`
6. collector 在清 backlog 的同时继续正常采集新数据

为什么 drain 要限流：

- 如果不限制，重放 backlog 可能长时间霸占 collector CPU
- 这会让“恢复期”反而比“故障期”更容易干扰被观测业务

### 如果 spool 满了

[`compactLocked`](../../backend/internal/collector/spool/spool.go) 的实际行为是：

- 尽量保留最新的未发送数据
- 为了满足上限，会驱逐更老的未发送记录
- `collector_spool_evicted_records_total` 增加

这是一种明确的取舍：

- 优先保留新的、仍有诊断价值的数据
- 代价是长时间故障期间会丢失部分旧历史

### 如果未读尾部损坏

`Next()` 和 `recoverCorruptionLocked(...)` 的实际行为：

- 发现截断或非法记录时丢弃损坏未读尾部
- `collector_spool_corruption_recoveries_total` 增加
- `collector_spool_last_recovery_reason{reason=...}` 记录原因

这样可以避免一个坏记录把整个 collector 卡死。

## 入队之后，真正的发送路径还做了什么

真正的发送路径实现位于 [`../../backend/internal/collector/transport/client.go`](../../backend/internal/collector/transport/client.go)。

| 行为 | 当前实现 | 为什么需要 |
| --- | --- | --- |
| failover send | `sendWithFailover(...)` 会按顺序尝试配置的 endpoint，直到收到一个成功 ACK | 一个坏 controller endpoint 不应该让整条投递路径失效 |
| mirror send | `sendMirror(...)` 可以同时发往所有 endpoint | 适合操作员明确需要“一份 telemetry 同时发给多个 controller”的场景 |
| 可选 gzip | `Compress` 打开 gRPC gzip | 当 payload 较大时，降低网络成本 |
| timeout 受限 RPC | `DialTimeout` 和 `RPCTimeout` 限制连接和发送时间 | 防止一条慢网络路径无限拖住 collector |
| ACK 校验 | client 会检查 ACK 里的 batch ID 是否为空或不匹配 | 防止 spool 错误提交错误 payload |
| retry 可观测 | client stats 会记录 retry、最后 endpoint、compression mode、error kind | 让发送路径行为变成可观测，而不是黑盒 |

取舍：

- failover 比单 endpoint 安全，但如果 controller 已接收成功而 ACK 丢失，仍可能重放同一 payload
- mirror mode 天生更贵，应该是有意开启的策略，而不是平时默认打开

## 一个具体例子：原始样本如何变成更小的 batch

与当前实现一致的示例值：

```text
memory_used_mb = 14320
memory_total_mb = 16384
memory_usage_pct = 87.4
cpu_iowait_pct = 28.4
disk_await_ms = 41.7
nic_rx_drops = 134
log_burst = 12
```

### 抑制前

```json
{
  "metrics": {
    "node_memory_Used_bytes": 15015608320,
    "node_memory_MemTotal_bytes": 17179869184,
    "node_cpu_iowait_percent": 28.4,
    "node_disk_request_latency_p99_seconds": 0.0417,
    "node_network_total_drop_per_second": 2.1,
    "collector_probe_source": 1,
    "collector_runtime_mode": 1,
    "collector_hardware_cpu_anomaly_score": 0.63
  },
  "processes": [
    {"pid": "2100", "name": "checkout-api", "cpu_percent": 71.2, "rss_bytes": 8589934592}
  ],
  "logs": [
    {"fingerprint": "dial tcp timeout", "count": 42}
  ]
}
```

### 平稳 cache-hit 周期的抑制后

```json
{
  "metrics": {
    "node_memory_Used_bytes": 15015608320,
    "node_memory_MemTotal_bytes": 17179869184,
    "node_cpu_iowait_percent": 28.4,
    "node_disk_request_latency_p99_seconds": 0.0417,
    "node_network_total_drop_per_second": 2.1,
    "collector_metrics_partial_update": 1,
    "collector_metrics_suppressed_count": 9,
    "collector_process_payload_suppressed": 1,
    "collector_aux_payload_suppressed{component=\"logs\"}": 1
  },
  "processes": [],
  "logs": []
}
```

被省掉了什么：

- 重复的 runtime/hardware 低频状态
- 相同的热点进程列表
- 相同的日志 fingerprint 集

保留了什么：

- 用于控制面分析的节点压力指标
- 足够的 marker，保证控制器安全 carry-forward 上一轮视图

## 控制平面如何重建这些状态

[`../../backend/internal/controller/ingest/store.go`](../../backend/internal/controller/ingest/store.go) 和 [`../../backend/internal/controller/ingest/server.go`](../../backend/internal/controller/ingest/server.go) 会解释这些抑制标记：

- 低频 metric 的 partial update 不会清空 runtime mode 或硬件 profile
- cache-hit 的进程/日志抑制不会把旧视图清空
- cache-hit 的兼容硬件 tier 抑制不会擦掉之前的温度、NIC、RDMA 视图

如果没有这一层：

- 压缩看起来就像随机丢数据
- 控制面会在“有状态”和“空状态”之间抖动

## 如何在线验证

建议一起观察这些指标：

| 指标 | 说明 |
| --- | --- |
| `collector_metrics_suppressed_count` | 低频状态被有意省略了 |
| `collector_aux_payload_suppressed{component="logs"}` | 日志视图 cache hit，本轮未重发 |
| `collector_process_payload_suppressed` | 热点进程列表近似未变，本轮未重发 |
| `collector_spool_backlog_bytes` | 仍有待重放 backlog |
| `collector_spool_evicted_records_total` | backlog 超过容量上限后驱逐了旧记录 |
| `collector_spool_corruption_recoveries_total` | 遇到损坏未读尾部并恢复 |
| `collector_transport_errors_total` | 发送路径失败 |
| `collector_transport_retries_total` | retry/failover 正在发生 |

## 调优开关

| 配置项 | 文件 | 调整原因 |
| --- | --- | --- |
| `spool_max_bytes` | [`../../configs/collector.yaml`](../../configs/collector.yaml) | 允许更大或更小的本地 backlog |
| `spool_sync_interval` | [`../../configs/collector.yaml`](../../configs/collector.yaml) | 落盘可靠性与磁盘写放大之间的取舍 |
| `spool_offset_sync_interval` | [`../../configs/collector.yaml`](../../configs/collector.yaml) | commit 持久化与磁盘开销取舍 |
| `low_churn_metrics_refresh_interval` | [`../../configs/collector.yaml`](../../configs/collector.yaml) | 多久强制重发一次完整低频状态 |
| `suppress_cached_aux_payloads` | [`../../configs/collector.yaml`](../../configs/collector.yaml) | 是否抑制 cache-hit 的日志/兼容进程 payload |
| `suppress_unchanged_process_payloads` | [`../../configs/collector.yaml`](../../configs/collector.yaml) | 是否抑制近似不变的 active-source 进程列表 |
| `process_payload_refresh_interval` | [`../../configs/collector.yaml`](../../configs/collector.yaml) | 最长允许沿用多久旧进程视图 |
| `protection.max_drain_records_per_cycle` | [`../../configs/collector.yaml`](../../configs/collector.yaml) | backlog 恢复速度与公平性取舍 |
| `protection.max_drain_duration` | [`../../configs/collector.yaml`](../../configs/collector.yaml) | 每个 collector 周期允许多少 drain CPU 时间 |

## 这一层没有解决什么

- 它还不是对所有指标的完整 delta 编码
- 它不能让控制器无限耐久
- 因为 spool 是有界的，长时间故障下仍可能丢失部分旧数据
- 它也不能替代 operator 对 `collector_spool_backlog_bytes`、retry、eviction 的监控

另见：

- [数据流](05-data-flow.md)
- [指标与信号](13-metrics-and-signals.md)
- [控制平面分析](07-control-plane-analysis.md)
- [部署](15-deployment.md)
