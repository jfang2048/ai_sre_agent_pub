# Predictive Runbook (v0.8)

This runbook explains how to validate and triage predictive early warnings.

## Quick validation

```bash
make predictive-test
curl -sS http://127.0.0.1:8080/api/v1/agent/reports/latest | jq '.reports[0].predictions'
```

If you want a single collector:

```bash
curl -sS http://127.0.0.1:8080/api/v1/agent/reports/<collector-id>/latest | jq '.report.predictions'
```

## What a healthy predictive record looks like

Each record should include:

- `prediction_id`
- `asset_id`
- `predictive_slo`
- `hazard_class`
- `algorithm_version`
- `evidence_window_start`
- `evidence_window_end`
- `audit_hash`

If these fields are missing, the warning is not audit-ready even if the UI shows a risk card.

## Triage order

1. Confirm the collector and controller are healthy.
2. Confirm the metric is trend-retained and has enough recent samples.
3. Confirm the predictive record is structurally complete.
4. Confirm the current value, baseline, z-score, and forecast are internally consistent.
5. Route by hazard class.

## Hazard-class guidance

| Hazard class | First operator action |
| --- | --- |
| `thermal_runaway` | inspect GPU cooling, fans, placement, and recent power changes |
| `power_anomaly` | review power cap, PSU events, and recent workload phase change |
| `pcie_path_pressure` | inspect feeder path, PCIe topology, and host-side transport bottlenecks |
| `oom_precursor` | identify top RSS growth, reclaim pressure, and retry amplification |
| `network_jitter` | inspect retransmits, queueing, packet loss, and upstream congestion |
| `io_degradation` | inspect queue depth, latency, writeback, and storage backend health |
| `compute_contention` | inspect runnable queue, cgroup placement, and noisy-neighbor activity |

## Business reason

Predictive alerts are valuable only if they create a safe intervention window.

中文解释:

- 预警的商业价值不在于“更早看到红灯”，而在于让值班工程师还有时间做低风险动作。
- 对 AI 训练集群来说，GPU 热失控、功耗异常、PCIe 压力、内存逼近这些问题一旦越过 onset window，就会从“可控退化”变成“昂贵停机”。
- 所以 runbook 的目标不是证明算法很聪明，而是保证预警能被一致地验证、解释和处理。
