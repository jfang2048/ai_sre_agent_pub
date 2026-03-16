# Business Use Cases (v0.7)

This document explains where the project fits in real AI/GPU operations, why a push-first design matters, and how to evaluate business value without pretending the platform is magic.

## 中文导读

- 这份文档回答的是“为什么企业会认真看这个项目”，不是“命令怎么敲”。
- 核心角度不是功能罗列，而是从事故成本、GPU 利用率、值班负担、合规边界这些业务问题出发，再反推为什么架构要这样设计。
- 读这份文档时，建议把它和根目录 `README.md` 一起看。README 解释系统怎么工作，这里解释系统为什么在商业上值得做。

## The market gap

AI/GPU infra has three problems that general observability stacks often expose but do not fully solve:

| Problem | Why teams still struggle |
| --- | --- |
| Transient failures | Pull-based polling often misses the first seconds of feeder starvation, PCIe stalls, retransmit bursts, or process-local OOM buildup. |
| Cross-layer RCA | Metrics, logs, process attribution, GPU state, and runtime security signals usually live in different systems and different timelines. |
| Safe automation | Even when the likely fix is obvious, production teams still need approvals, rollback paths, cost checks, and audit records. |

## Where this project fits

| Existing tool | Good at | Typical gap for AI/GPU infra | This project's angle |
| --- | --- | --- | --- |
| Prometheus | standard metrics ecosystem, pull scraping, alert rules | transient onset loss, weak process/GPU causality, little action orchestration | push-first evidence preservation plus RCA/action workflow |
| Datadog | managed UX, fast rollout, broad integrations | cost pressure at high-cardinality AI scale | controller-side storage choice and lightweight collectors |
| Sysdig / runtime security tools | syscall/runtime visibility and posture | weaker AI-specific RCA and remediation workflow | combines low-level runtime evidence with RCA and guarded actions |

## Enterprise use cases

### 1. Training instability and GPU underutilization

What operators actually need:

- know whether low SM utilization is a GPU issue or a feeder/storage/network issue
- detect GPU memory pressure before it becomes an OOM outage
- preserve enough per-process evidence to explain why a multi-million parameter job slowed down

Why the platform helps:

- probe-core prioritizes SM utilization, GPU memory pressure, and per-process attribution
- controller correlates GPU evidence with CPU, IO, network, and process signals
- joint-risk and RCA workflows keep the reasoning path inspectable instead of hiding it in a prompt

### 2. Faster root cause during bursty incidents

What enterprises care about:

- fewer 30-60 minute incident bridges for issues that actually started as a 10 second transition
- less manual “tab hopping” between metrics, logs, CMDB, and runbooks
- lower mean time to restore for expensive GPU-backed services

Why the platform helps:

- collectors push evidence instead of waiting for central scrapes
- short outages can replay from local spool
- repeated RCA refreshes are now idempotent for a short dedupe window, so API retries do not multiply incidents

### 3. Security-sensitive GPU farms

Why this matters:

- a suspicious process, lateral network behavior, or odd file activity can be both a security issue and a performance issue
- security and performance teams often look at the same node through different tools and arrive late at the same conclusion

Why the platform helps:

- eBPF runtime events, process lineage, and resource pressure can live in the same investigation path
- the same control plane can show whether a node is both noisy and suspicious

### 4. Enterprise-safe remediation

The hard problem is not generating a remediation idea. The hard problem is proving it is safe enough to run.

Current enterprise-oriented controls in `v0.7`:

- dry-run and approval-first defaults
- production actions require `change_id` or `compliance_ticket`
- large scale-up actions require explicit `cost_approved=true`
- rollback hints and audit records stay attached to workflow output
- Slack, PagerDuty, and generic webhook sinks are available for tiered routing

## Business impact framing

These are not guaranteed outcomes. They are the operational and financial levers the platform is designed to improve:

| Lever | Why it matters |
| --- | --- |
| MTTR reduction | training and inference incidents burn expensive GPU minutes quickly |
| GPU efficiency | underfed GPUs are an infrastructure waste problem, not just a dashboard problem |
| SRE toil reduction | deterministic evidence and pre-structured RCA reduce repetitive incident archaeology |
| Safer automation | approval and compliance guardrails make automation usable in real enterprises |

## How to benchmark value

Use the platform against your current stack on a few recurring scenarios:

1. Controller outage and replay.
2. Short GPU burst or feeder-starvation event.
3. Repeated RCA refresh traffic during an active incident.
4. A production remediation request that should fail closed without cost/compliance metadata.

What to measure:

- time to first believable hypothesis
- time to restore
- fraction of incidents with preserved onset evidence
- fraction of risky remediations blocked before execution

## Near-term roadmap

- publish repeatable large-fleet benchmarks for 1,000+ node controller sizing
- expand cost-aware scaling and quota-aware Kubernetes remediation
- add deeper change-management and enterprise identity integrations
- expose more business-facing SLO and MTTR proxy reporting in the UI and APIs
