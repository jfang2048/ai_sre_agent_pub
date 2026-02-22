# GPU Observability Design / GPU 可观测性设计

This document describes the controller-side GPU aggregation module used in the current codebase.

本文档描述当前代码库中 controller 端的 GPU 聚合模块。

## Scope / 功能范围

### Provided capabilities / 提供的能力

GPU observability in this repository provides / 本仓库的 GPU 可观测性提供：

- Fleet-wide GPU inventory/snapshot APIs / 集群级 GPU 清单/快照 API
- Per-node GPU history persistence / 每节点 GPU 历史持久化
- K8s-friendly snapshot shaping / K8s 友好的快照格式
- Limited Prometheus re-export for stable cardinality / 有限的 Prometheus 重导出（稳定基数）

### Not provided / 不提供的功能

- Does NOT replace NVIDIA device plugin/GPU Operator / 不替换 NVIDIA 设备插件/GPU Operator
- Does NOT provide GPU capacity planning recommendations / 不提供 GPU 容量规划建议
- Does NOT directly support AMD/Intel GPUs (extensible) / 不直接支持 AMD/Intel GPU（可通过扩展实现）

## Data source / 数据来源

### Collector side / Collector 侧

Collector emits `node_gpu_*` metrics when `nvidia-smi` is available and returns data / 当 `nvidia-smi` 可用且返回数据时，Collector 发送 `node_gpu_*` 指标。

### Controller ingest pipeline / Controller 接入流程

```text
gRPC ingest → extract GPU metrics → update in-memory snapshots → persist history
gRPC 接收 → 提取 GPU 指标 → 更新内存快照 → 持久化历史
```

1. gRPC ingest receives telemetry batches / gRPC 接入接收遥测批次
2. `gpuobs.Store` extracts GPU metrics / `gpuobs.Store` 提取 GPU 指标
3. Store updates in-memory snapshots and persists periodic history / Store 更新内存快照并持久化定期历史

## In-memory model / 内存模型

### Primary keys / 主键

| Level | Primary key | Description / 说明 |
|---|---|---|
| Node | `collector_id` | Unique identifier in cluster / 集群中的唯一标识 |
| Device | `gpu_id` | GPU index within node / 节点内的 GPU 索引 |
| Process | `pid` | Process ID on device / 设备上的进程 ID |

### Capacity control / 容量控制

The store keeps bounded per-process details (`max_processes_per_gpu`) to avoid unbounded growth / Store 保持有界的每进程详情，避免无限增长。

## Persistence model / 持久化模型

### Storage paths / 存储路径

Default path: `./data/gpu` / 默认路径：`./data/gpu`

| Type | Path | Description / 说明 |
|---|---|---|
| Latest snapshots | `./data/gpu/snapshots/<collector_id>.json` | Latest state per collector / 每个 collector 的最新状态 |
| Daily history | `./data/gpu/history/<collector_id>-YYYY-MM-DD.jsonl` | Daily split history records / 按日期分割的历史记录 |

### Retention / 保留策略

Time-based retention (`gpu.retention` in controller config) / 基于时间的保留（controller 配置中的 `gpu.retention`）。

## API outputs / API 输出

### Base prefix / 基础前缀

`/api/v1`

### Endpoint list / 端点列表

| Endpoint | Description / 说明 |
|---|---|
| `GET /gpu/nodes` | Fleet GPU inventory and latest per-device data / 集群 GPU 清单和最新设备数据 |
| `GET /gpu/nodes/{collector_id}` | GPU snapshot for one collector / 单个 collector 的 GPU 快照 |
| `GET /k8s/gpu/nodes` | K8s-friendly compact GPU snapshot list / K8s 友好的紧凑快照列表 |

### K8s API design / K8s API 设计

`/k8s/gpu/nodes` is intentionally compact for scheduler/controller consumption / `/k8s/gpu/nodes` 故意设计为紧凑格式，方便 scheduler/controller 消费：

```json
{
  "nodes": [
    {
      "name": "node-1",
      "gpu_count": 4,
      "memory_total_mib": 163840,
      "memory_used_mib": 81920
    }
  ]
}
```

## Prometheus re-export / Prometheus 重导出

Controller `/metrics` re-exports a constrained subset / Controller 的 `/metrics` 端点重导出受限子集：

| Metric | Description / 说明 |
|---|---|
| `node_gpu_utilization_sm_percent` | GPU SM utilization / GPU SM 利用率 |
| `node_gpu_memory_used_mib` | GPU memory usage / GPU 内存使用量 |
| `node_gpu_memory_total_mib` | GPU memory total / GPU 内存总量 |

### Labels / 标签

Include node and GPU identity when available / 包含节点和 GPU 身份（当可用时）：

```prometheus
node_gpu_utilization_sm_percent{collector_id="node-1",gpu_id="0"} 85.5
```

## Performance design / 性能设计

### Optimization strategies / 优化策略

Current implementation minimizes overhead by / 当前实现通过以下方式最小化开销：

- Avoiding per-metric map allocations in hot parsing paths / 热路径解析中避免每指标 map 分配
- Updating per-process structures incrementally and sorting only on output paths / 增量更新每进程结构，仅在输出路径排序
- Persisting snapshots/history in buffered, batched patterns / 缓冲、批处理模式持久化快照/历史
- Exporting only required fields for Prometheus scrape paths / 仅导出 Prometheus 抓取路径所需的字段

### Capacity planning / 容量规划

| Component | Typical memory usage / 典型内存占用 |
|---|---|
| Per-device snapshot | ~1 KB |
| Per-device daily history | ~500 KB |
| 100-node cluster (8 GPUs each) with 7-day retention | ~1-2 GB / 含 7 天历史 |

## Integration notes / 集成说明

### Ecosystem integration / 与生态集成

| Integration point | Description / 说明 |
|---|---|
| NVIDIA device plugin | This module complements (does not replace) the device plugin / 本模块补充（不替换）设备插件 |
| GPU Operator | Compatible with GPU Operator deployments / 兼容 GPU Operator 部署的环境 |
| Prometheus | Scrape via `/metrics` / 通过 `/metrics` 抓取 |
| K8s Scheduler | Custom scheduling via `/k8s/gpu/nodes` / 通过 `/k8s/gpu/nodes` 自定义调度 |

### Typical K8s pipelines / 典型 K8s 管线

1. **Prometheus pipeline**: Scrape `/metrics` into Prometheus/Adapter / 抓取 `/metrics` 到 Prometheus/Adapter
2. **Custom controller**: Poll `/api/v1/k8s/gpu/nodes` / 轮询 `/api/v1/k8s/gpu/nodes`
3. **Alert integration**: Alert based on Prometheus rules / 基于 Prometheus 规则告警

### AGENT integration / AGENT 集成

`/api/v1/agent/query` reads GPU snapshots from `gpuobs.Store` and injects GPU context into LLM prompts / `/api/v1/agent/query` 读取 GPU 快照并注入 GPU 上下文到 LLM prompt：

- `util_sm_percent` - SM utilization / SM 利用率
- `memory_used_mib` - Memory usage / 内存使用量
- Per-device process pressure / 每设备进程压力

### Action proposal validation / 动作建议验证

AGENT action proposals should be validated against / AGENT 动作提案在执行前应验证：

- `gpu_count` - GPU quantity / GPU 数量
- Per-device utilization / 每设备利用率
- Playbook safety policy / Playbook 安全策略

## Troubleshooting / 故障排查

### Common issues / 常见问题

| Symptom | Possible cause | Solution / 解决方案 |
|---|---|---|
| GPU data missing | `nvidia-smi` unavailable / `nvidia-smi` 不可用 | Check driver and container permissions / 检查驱动和容器权限 |
| History not persisting | Disk permissions or path issue / 磁盘权限或路径问题 | Check `./data/gpu` write permissions / 检查 `./data/gpu` 写权限 |
| No GPU metrics in Prometheus | Controller GPU config not enabled / controller 配置未启用 GPU | Set `gpu.enabled=true` |

### Verification commands / 验证命令

```bash
# Check GPU API / 检查 GPU API
curl -s http://controller:8080/api/v1/gpu/nodes | jq .

# Check K8s API / 检查 K8s API
curl -s http://controller:8080/api/v1/k8s/gpu/nodes | jq .

# Check Prometheus metrics / 检查 Prometheus 指标
curl -s http://controller:8080/metrics | grep node_gpu
```
