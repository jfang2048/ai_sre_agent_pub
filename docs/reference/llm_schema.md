# LLM Input Schema (Schema `v1`) / LLM 输入模式

This document describes the JSON schema sent to the LLM path in the current controller implementation.

本文档描述当前 controller 实现中发送到 LLM 路径的 JSON 模式。

## Version status / 版本说明

| Item | Version | Description / 说明 |
|---|---|---|
| Schema version | `v1` | JSON structure version identifier / JSON 结构版本标识 |
| Product version | `v0.2` | AI SRE Agent release version / AI SRE Agent 发布版本 |

They are independent / 两者相互独立。

## Top-level structure / 顶层结构

```json
{
  "schema_version": "v1",
  "generated_at": "2026-02-06T12:00:00Z",
  "node_name": "node-01",
  "metrics": {
    "node_cpu_usage_percent": 82.5,
    "node_memory_Used_bytes": 12300000000
  },
  "trends": {
    "node_cpu_usage_percent": "rising",
    "node_memory_Used_bytes": "stable"
  },
  "alerts": ["alert-node-01-cpu-high"],
  "anomalies": ["CPU usage anomaly detected"],
  "context": "RCA request from controller analysis engine",
  "evidence": {
    "schema_version": "v1",
    "node_name": "node-01",
    "summary": {
      "node_cpu_usage_percent": 82.5
    },
    "top_metrics": [
      { "name": "node_cpu_usage_percent", "value": 82.5 }
    ],
    "gpu": {},
    "network": {},
    "disk": {},
    "memory": {},
    "processes": [
      {
        "pid": 1234,
        "name": "python",
        "cpu_percent": 92,
        "rss_bytes": 123456789,
        "io_read_bps": 0,
        "io_write_bps": 0
      }
    ],
    "logs": [
      { "fingerprint": "abc123", "count": 12, "example": "OOM killed process 1234" }
    ],
    "alerts": ["alert-node-01-cpu-high"],
    "anomalies": ["CPU usage anomaly detected"],
    "context": "RCA request from controller analysis engine"
  }
}
```

## Field reference / 字段参考

| Field | Type | Description / 说明 |
|---|---|---|
| `schema_version` | string | Fixed schema identifier (`v1`) / 固定模式标识符 |
| `generated_at` | string | RFC3339 timestamp / RFC3339 时间戳 |
| `node_name` | string | Node/collector identity / 节点/collector 身份 |
| `metrics` | object | Numeric metric snapshot / 数值指标快照 |
| `trends` | object or null | Trend labels (`rising`, `falling`, `stable`) / 趋势标签 |
| `alerts` | array[string] | Alert IDs or alert-like findings / 告警 ID 或类似告警发现 |
| `anomalies` | array[string] | Anomaly summaries/forecasts / 异常摘要/预测 |
| `context` | string | Prompt context text / Prompt 上下文文本 |
| `evidence` | object | Compact evidence pack / 紧凑证据包 |

### Evidence pack fields / 证据包字段

| Field | Type | Description / 说明 |
|---|---|---|
| `schema_version` | string | Evidence schema version (`v1`) / 证据模式版本 |
| `node_name` | string | Node/collector identity / 节点/collector 身份 |
| `summary` | object | Selected high-signal metrics / 选定的高信号指标 |
| `top_metrics` | array | Top metrics by value / 按值排名的 Top 指标 |
| `gpu`, `network`, `disk`, `memory` | object | Domain-specific metric subsets / 领域特定指标子集 |
| `processes` | array | Top process summaries / Top 进程摘要 |
| `logs` | array | Log fingerprint summaries / 日志指纹摘要 |
| `alerts` | array[string] | Related alert IDs / 相关告警 ID |
| `anomalies` | array[string] | Related anomaly text / 相关异常文本 |
| `context` | string | Evidence context text / 证据上下文文本 |

## Trend labels / 趋势标签

| Label | Meaning | Usage / 使用场景 |
|---|---|---|
| `rising` | Rising / 上升 | Metric consistently increasing / 指标持续增长 |
| `falling` | Falling / 下降 | Metric consistently decreasing / 指标持续减少 |
| `stable` | Stable / 稳定 | Metric fluctuates normally / 指标波动正常 |
| `unknown` | Unknown / 未知 | Insufficient data to determine / 数据不足无法判断 |

## Chinese context examples / 中文上下文示例

### CPU high load analysis / CPU 高负载分析

```json
{
  "context": "分析节点 CPU 使用率异常高的原因 / Analyze the root cause of abnormally high CPU usage on the node",
  "metrics": {
    "node_cpu_usage_percent": 95.2,
    "node_load1": 48.5
  },
  "trends": {
    "node_cpu_usage_percent": "rising"
  },
  "evidence": {
    "processes": [
      {
        "pid": 1234,
        "name": "python",
        "cpu_percent": 85.0,
        "cmdline": "python train_model.py"
      }
    ]
  }
}
```

### GPU OOM analysis / GPU 内存不足分析

```json
{
  "context": "GPU 内存接近 OOM 的根因分析 / Root cause analysis for GPU memory near OOM",
  "metrics": {
    "node_gpu_memory_used_percent": 98.5
  },
  "evidence": {
    "gpu": {
      "device_0": {
        "memory_used_mib": 79800,
        "memory_total_mib": 81000,
        "processes": [
          {
            "pid": 5678,
            "name": "python",
            "gpu_memory_mib": 40000
          }
        ]
      }
    }
  }
}
```

## Source locations / 代码位置

| Component | Path |
|---|---|
| Schema structs/builders / Schema 结构/构建器 | `backend/internal/controller/analysis/schema.go` |
| AGENT query prompt/schema builder / AGENT query prompt/schema 构建器 | `backend/internal/agent/prompts.go` |
| Evidence pack / 证据包 | `backend/internal/controller/analysis/evidence.go` |
| LLM client env + provider handling / LLM 客户端 env + provider 处理 | `backend/internal/controller/analysis/llm_client.go` |

## Best practices / 最佳实践

### Prompt context design / Prompt 上下文设计

1. **Clarify problem type**: Distinguish performance, availability, and resource issues / **明确问题类型**：区分性能问题、可用性问题、资源问题
2. **Provide time range**: Indicate whether it's a spike or sustained issue / **提供时间范围**：说明是突发还是持续问题
3. **Include key metrics**: At minimum CPU, memory, network, disk / **包含关键指标**：至少包含 CPU、内存、网络、磁盘
4. **Process information**: Top process PID, name, command line / **进程信息**：Top 进程的 PID、名称、命令行
5. **Log clues**: Related error log fingerprints and examples / **日志线索**：相关错误日志的指纹和示例

### Response handling / 响应处理

```python
# Parse LLM response example / 处理 LLM 响应示例
def parse_llm_response(response: dict) -> dict:
    return {
        "issue_detected": response.get("issue_detected", False),
        "issue_type": response.get("issue_type", ""),
        "severity": response.get("severity", "info"),
        "confidence": response.get("confidence", 0.0),
        "root_cause": response.get("root_cause", ""),
        "remediation": response.get("remediation", ""),
        "notes": response.get("notes", "")
    }
```
