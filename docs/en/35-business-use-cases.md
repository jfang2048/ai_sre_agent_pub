# Business Use Cases (v0.8)

This document explains the business and product relevance of the current codebase without pretending the system is magic.

It is written for technical product reviewers, infrastructure leaders, investors, and senior operators who need to understand:

- what operational pain this project addresses
- why the architecture is split the way it is
- where the defensibility comes from
- where the production boundaries still are

## The Practical Problem

AI and GPU infrastructure suffers from a mismatch between cost and observability quality.

The expensive part of the fleet is often the least forgiving:

- GPU minutes are costly, so slow RCA has direct financial impact
- failures are often transient and cross-layer, so pull-only dashboards miss the onset
- remediation is risky, so "the model suggested it" is not an operational control

In that environment, value comes from reducing uncertainty at the right points:

- preserving onset evidence
- connecting host, process, runtime, network, storage, and GPU context
- keeping operator output inspectable
- making automation governable rather than merely possible

## Where This Repository Fits

| Category | Typical strength | Typical gap | This repository's angle |
| --- | --- | --- | --- |
| pull-first observability stacks | mature metrics ecosystem, broad dashboarding | can miss short-lived local evidence and do not naturally include action governance | push-first host evidence plus controller-side incident workflows |
| managed monitoring SaaS | fast adoption and polished UX | high-cardinality AI/GPU fleets can become expensive and black-box RCA remains a problem | explicit runtime boundaries, local-first evidence, repository-owned logic |
| runtime security tooling | strong syscall/process visibility | often weak on AI-specific performance RCA and remediation workflow | unify runtime, performance, topology, and action control in one path |
| chatbot-style ops assistants | fast natural-language interface | weak auditability, weak grounding, weak action safety | deterministic evidence path, retrieval gating, durable workflow records |

## Why Push-First Matters Commercially

Push-first is not only a transport preference. It changes what evidence survives.

Before a push-first design:

- short queue buildups, retransmit bursts, or process-local pressure may be over before the next central scrape
- controller reachability problems can become telemetry loss
- the host has no local backlog to replay after a short outage

With the current design:

- the collector captures host-local evidence before it disappears
- the local spool absorbs short controller/network interruptions
- the controller reasons over richer incident windows instead of thinner snapshots

Business consequence:

- better first-hypothesis quality
- fewer "we know it was broken, but we missed the start" incidents
- more credible RCA for expensive GPU-backed services

## Real Use Cases In The Current Codebase

### 1. GPU training slowdown and feeder starvation

What teams need:

- distinguish GPU-side saturation from upstream storage, CPU, or network bottlenecks
- retain per-process and node-local evidence long enough to investigate
- avoid treating every GPU slowdown as a generic accelerator problem

What the codebase does today:

- collector-side GPU and host evidence gathering
- controller-side RCA that combines GPU, CPU, disk, network, process, and runtime context
- change-aware and memory-aware retrieval in the workflow engine

Why that matters commercially:

- GPU underutilization is not just an SRE issue; it is direct capital inefficiency

### 2. Faster RCA during bursty incidents

What teams need:

- reduce the time spent reconstructing the first minutes of an incident
- avoid manual hopping across metrics, logs, runbooks, and postmortems
- generate a short list of credible hypotheses rather than one vague summary

What the codebase does today:

- deterministic trend and weak-signal analysis
- change correlation and causal-path generation
- incident memory retrieval plus static runbook retrieval
- durable evidence packages and workflow audit trails

Why that matters commercially:

- shorter investigation time reduces MTTR and lowers senior-engineer interruption cost

### 3. Guarded automation in regulated or sensitive environments

What teams need:

- approval and dry-run defaults
- rollback or compensation thinking before execution
- audit records after execution
- evidence about whether an action actually improved the system

What the codebase does today:

- centralized policy and approval handling in the workflow tool gateway
- durable step records with verification and compensation state
- action outcome write-back into incident memory
- explicit workflow evidence and audit APIs

Why that matters commercially:

- automation without governance is unusable in serious production environments

### 4. Mixed performance and security investigation on the same node

What teams need:

- one control plane that can show both runtime suspicion and performance degradation
- less duplication between platform and security teams

What the codebase does today:

- eBPF/runtime evidence, process lineage, security findings, and performance signals can enter the same RCA path
- the causal graph can rank upstream causes separately from downstream symptoms

Why that matters commercially:

- the same incident can affect uptime, cost, and security posture at once

## Defensibility

The project's defensibility is not "we attached an LLM."

The stronger technical moat in the current implementation is:

- host-local evidence preservation
- bounded hot-state and trend-history design
- deterministic intermediate evidence objects before LLM use
- governed workflow execution with durable state
- change intelligence, causal graph reasoning, and incident-memory reuse inside the same controller

These are harder to copy well than a prompt template because they require coherent end-to-end control flow.

## How To Evaluate Business Value

Use the current repo against recurring operational scenarios and measure:

1. time to first believable hypothesis
2. time to restore
3. fraction of incidents with preserved onset evidence
4. fraction of risky actions blocked before execution
5. fraction of incidents where prior memory or runbooks actually improved the next step

Suggested repo-grounded scenarios:

1. controller outage followed by spool replay
2. short GPU or IO burst that would be easy to miss in pull-only systems
3. change-linked regression after rollout
4. remediation request that should remain dry-run or approval-gated

## What v0.8 Adds To The Product Narrative

Compared with the earlier documentation set, `v0.8` can now explain a more credible operational story because the code now includes:

- durable workflow runs instead of transient-only workflow state
- change intelligence as a first-class evidence source
- causal graph reasoning for probable-cause vs symptom separation
- incident memory write-back and reuse
- replay-oriented evaluation and stability checks

That shifts the project narrative from "analysis plus RAG" toward "evidence-grounded incident agent with guarded control."

## Boundaries

This repository still has important boundaries:

- it is not a hosted SaaS platform
- it does not yet ship with enterprise identity or CMDB integrations
- it does not default to autonomous remediation
- it does not claim perfect open-world RCA accuracy

Those boundaries are strengths if read correctly. They keep the architecture inspectable and credible.
