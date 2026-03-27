# Predictive Signals (v0.8)

This document describes the low-overhead predictive path present in `v0.8`.

## Design intent

The predictive path is deliberately narrow:

- simple algorithms only: `EWMA`, `Z-score`, `adaptive threshold`, and short-horizon smoothing
- deterministic output only on the hot path
- bounded history only from trend-retained metrics
- structured results that can be audited and routed without reinterpreting free-form text

中文解释:

- 这条路径追求的不是“预测越复杂越好”，而是“用最低计算成本尽早发现持续漂移”。
- 在 GPU 节点上，真正重要的是把热失控、功耗异常、内存逼近、网络抖动这类问题提前几十秒到几十分钟暴露出来，而不是在每台机器上跑重模型。
- 因此这里故意选了最朴素、最可解释、最好控资源的算法组合。

## Signals covered first

| Metric | Why it matters | Current heuristic |
| --- | --- | --- |
| `node_gpu_temperature_celsius` | thermal runaway / throttling precursor | EWMA crossover + adaptive threshold against recent baseline |
| `node_gpu_power_draw_watts` with `node_gpu_power_limit_watts` | power-cap pressure / unstable power envelope | draw-to-limit ratio forecast |
| `node_gpu_pcie_link_utilization_percent` | feeder path pressure / PCIe saturation risk | high-z-score link pressure trend |
| `node_memory_used_percent` or derived used/total ratio | OOM precursor | rising memory headroom exhaustion forecast |
| `node_tcp_retransmit_ratio` | network jitter / packet loss symptom | anomaly against recent baseline and projected threshold crossing |
| `node_pressure_io_some_avg10` | storage or writeback degradation | sustained pressure trend detection |
| `node_cpu_usage_percent` | compute contention / queueing risk | sustained saturation drift |

## Predictive record fields

Every predictive finding includes:

- `prediction_id`
- `asset_id`
- `metric`
- `predictive_slo`
- `hazard_class`
- `control_reference`
- `algorithm`
- `algorithm_version`
- `evidence_window_start`
- `evidence_window_end`
- `audit_hash`

These fields exist so an operator can answer three questions after the fact:

1. Which asset and control objective did this warning belong to?
2. Which exact evidence window and algorithm version produced it?
3. Can the emitted record be traced and compared across runs?

## Operational thresholds

Default thresholds are intentionally conservative:

- GPU thermal risk: `82C`
- GPU power draw ratio risk: `92%` of power cap
- PCIe link pressure risk: `80%`
- memory exhaustion risk: `85%`
- TCP retransmit ratio risk: `0.01`
- IO pressure risk: `20`
- CPU saturation risk: `85%`

These are not permanent “truth values”. They are starting points that combine with adaptive thresholds and recent variance so the system does not overreact to every burst.

## Why not use an LLM here

- hot-path prediction must remain explainable and bounded
- early warning needs repeatability under incident pressure
- controller CPU budget should go to aggregation and correlation, not probabilistic inference on every sample window

LLMs remain useful for later RCA and recommendation synthesis, but not for deciding whether the predictive threshold was crossed.
