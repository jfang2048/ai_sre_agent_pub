# Benchmark And Performance Guide (v0.7)

This document defines the benchmark scenarios that matter for a push-based AI/GPU observability platform.
It is not a marketing sheet. The goal is to make performance claims reviewable, repeatable, and tied to operational/business outcomes.

## 中文说明

- 这个文档的重点不是追求“跑分好看”，而是回答系统在真实 AI/GPU 集群里最怕什么退化。
- 对这类系统来说，单纯的 QPS 或单次 API 延迟不够，必须同时看采集开销、突发信号保真、回放稳定性、分析去重效果、以及 UI/控制面在高负载下还能否保持可用。
- 这样设计 benchmark 的原因是: 企业用户真正关心的是 GPU 训练是否少停机、值班团队是否更快定位根因、控制面是否会在事故中反过来放大故障。

## Why These Benchmarks Exist

The platform sits in the middle of several competing forces:

- collectors must stay lightweight enough to run on busy GPU nodes
- probe-core must capture sub-minute and sub-second regressions that coarse pull loops miss
- controller ingestion must absorb bursts without duplicating incidents or dropping replayable data
- RCA and joint-risk workflows must stay deterministic and idempotent during operator refresh storms
- UI and APIs must remain responsive enough to support active incident work

If any of those fail, the product loses business value even if individual components still "work".

## Benchmark Matrix

| Scenario | Primary concern | Engineering question | Business reason |
| --- | --- | --- | --- |
| Collector steady-state overhead | observer cost | does normal collection stay inside acceptable CPU and memory budgets on busy nodes | observability must not consume meaningful training capacity |
| GPU burst capture | fidelity | does 1s probe-core sampling preserve short SM or memory-utilization spikes | missing onset signals prolongs RCA and increases GPU idle time |
| Controller replay after outage | durability and recovery | can backlog drain without silent loss or runaway duplicate processing | short controller outages should not become permanent blind spots |
| Joint-risk/RCA refresh storm | idempotency | do repeated identical requests reuse recent results instead of multiplying incidents | oncall engineers often click refresh during incidents; the system must not amplify noise |
| Top-k process attribution | algorithmic efficiency | does top-k selection scale sublinearly relative to full-sort behavior | large hosts can expose thousands of processes; full sorting wastes CPU on irrelevant tails |
| UI chart construction | frontend responsiveness | does chart assembly avoid repeated linear scans and remain smooth on larger series sets | slow UI during an incident directly increases operator time-to-answer |

## Recent Efficiency Changes Covered By This Guide

These repository changes are now part of the expected performance posture:

- probe-core top-k process selection uses `std::nth_element` plus a bounded final sort instead of fully sorting every row
- RCA timeline assembly merges already-sorted stage and plan streams with a two-pointer merge instead of sorting a concatenated slice every time
- risk/RCA chart series selection uses `Map` and `Set` based helpers instead of repeated `Array.find` scans in each page
- workflow refresh paths reuse recent identical results during a short dedupe window to limit duplicate analysis and incident churn

## Baseline Success Criteria

These are initial engineering guardrails, not hard SLAs:

| Area | Expected direction |
| --- | --- |
| Collector transport cadence | default `5s`, adaptive down to `1s` only when pressure rises and node headroom allows |
| Probe-core sampling | default `1s` internal loop, GPU interval samples default `1` for short-spike preservation |
| Replay correctness | no silent ack corruption; mismatched or empty ack batch IDs must fail closed |
| Workflow refresh behavior | repeated identical refreshes during the dedupe TTL should reuse prior results |
| Remediation safety | unsafe production actions must fail closed without change/compliance metadata or explicit cost approval |
| Frontend regression bar | chart helper tests and production build must pass after visualization-path changes |

## How To Run The Core Checks

From the repository root:

```bash
go -C backend test ./...
npm --prefix frontend test -- --run src/components/Insights/__tests__/riskChart.test.ts
npm --prefix frontend run build
make build-probe-core
```

For broader regression sweeps:

```bash
make test-stability
make test-ui
make bench
```

## Scenario Notes

### 1. Collector Steady-State Overhead

Measure collector CPU, RSS, and spool growth while the node is idle, moderately loaded, and under synthetic pressure.

Why:

- a collector that consumes too much CPU on a training host is not production-safe
- a collector that grows backlog under healthy controller conditions is already too close to failure

建议重点观察:

- collector CPU 是否在高压节点上持续抬升而不回落
- adaptive cadence 是否会在压力恢复后回到基线，而不是长期卡在高频采样
- spool backlog 是否只在控制面异常时上升，而不是平时也慢慢累积

### 2. GPU Burst Capture

Inject short GPU utilization or memory pressure bursts and confirm they appear in probe-core output and downstream charts.

Why:

- the platform is explicitly positioned against coarse pull-only systems
- if 1s probe-core sampling still misses brief spikes, the product loses one of its main market differentiators

### 3. Controller Replay And Recovery

Stop or isolate the controller briefly, let collectors continue spooling, then restore connectivity and confirm backlog drains correctly.

Why:

- push-first systems earn trust by surviving short control-plane outages without silent data loss
- replay correctness is more important than perfect freshness during the outage window

### 4. Workflow Refresh Storm

Issue repeated identical joint-risk and RCA refresh requests against the same collector/window pair.

Why:

- incidents create pathological user behavior: repeated refreshes, parallel viewers, automated polling
- the controller must remain idempotent enough that human debugging activity does not create synthetic incidents

### 5. Top-k Process Attribution

Exercise hosts with large process tables and compare runtime between top-k selection and full-sort approaches.

Why:

- most operators only need the hottest processes, not a globally sorted tail
- partial-selection algorithms reduce wasted work and preserve more CPU budget for actual collection

从一阶原理看，这里的目标不是“所有进程都排序得很精确”，而是“以更低成本找出最重要的前 K 个对象”。
这正符合 incident triage 的真实需求。

### 6. UI Chart Construction

Render risk and RCA views with wider time-series sets and confirm build/test success plus acceptable client responsiveness.

Why:

- a slow incident UI increases MTTR even if backend data is correct
- repeated `find` scans scale poorly and create avoidable client CPU work

## Interpreting Results

Do not treat one fast local run as proof of production readiness.
Use benchmark outputs to answer these practical questions:

- does the collector remain cheaper than the signal value it produces
- does the controller recover predictably after disruption
- does the workflow layer stay bounded under repeated requests
- does the UI remain usable when operators actually need it

## Release And PR Expectations

For performance-sensitive changes, PRs should include:

- the hot path being changed
- the previous complexity or bottleneck
- the new algorithm or concurrency model
- the validation commands run
- any tradeoff in memory, ordering guarantees, or failure semantics

中文要求:

- 如果改动了采集热路径、分析循环、排序/合并逻辑、缓存策略或并发模型，PR 里应该写明为什么这个优化对真实业务场景有价值。
- 只写“更快了”不够，需要说明它减少的是哪类事故成本，例如降低 GPU 空转、减少重复告警、缩短 RCA 时间、或降低 controller 高峰期 CPU。
