# Behavioral Baseline And Recurring-Burst Discrimination

Chinese version: [docs/zh/21-behavioral-baseline-design.md](../zh/21-behavioral-baseline-design.md)

This note describes the current controller-side design implemented in:

- [`../../backend/internal/controller/agentcore/behavioral_memory.go`](../../backend/internal/controller/agentcore/behavioral_memory.go)
- [`../../backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go)
- [`../../backend/internal/controller/agentcore/workflow_eventization.go`](../../backend/internal/controller/agentcore/workflow_eventization.go)
- [`../../backend/internal/controller/agentcore/evidence_contract.go`](../../backend/internal/controller/agentcore/evidence_contract.go)
- [`../../backend/internal/controller/agentcore/workflow_types.go`](../../backend/internal/controller/agentcore/workflow_types.go)

It exists to solve one specific false-positive pattern:

- a build worker burns CPU for ten minutes
- a deployment helper emits a temporary log burst
- a backup job spikes network or disk
- the workflow sees a threshold breach and treats it like a fresh incident again

The old design direction tried to solve that with a second persistence layer for behavior profiles. That was the wrong tradeoff here.

## What was wrong with the duplicate-store idea

The system already has two history paths for metrics:

- bounded hot history in ingest
- an optional longer-lived TSDB path behind the existing metric-history provider

Adding a separate behavior-profile database on top of that would have created three avoidable problems.

First, it would duplicate long-window facts. Operators would have to answer an annoying debugging question every time a suppression looked wrong: "which store is authoritative, the TSDB history or the behavior profile?"

Second, it would add operational surface for weak benefit. A second store means new retention rules, new corruption cases, new schema drift, new configuration, and a new class of restart bugs.

Third, it would tempt the code into storing derived summaries that quietly drift away from the real historical source. Once that happens, suppression becomes harder to explain.

This repo does not need that. The recurring-burst decision only needs longer-window comparisons, not a second general-purpose database.

## Final design

The controller now follows a simpler rule:

- long-window metric history comes from the existing `MetricHistoryProvider`
- if that provider is backed by the TSDB path, the TSDB is the durable source of truth
- the recurring-burst layer keeps only a small in-memory cache of recent reads

There is no dedicated behavior-profile store.

### Why this fits the codebase

The access pattern is narrow:

- identify the current workload
- ask for a longer history window for that collector
- compare the active burst against that history
- classify the signal

That is a read-mostly problem. It does not need its own persistence engine.

## Identity model

The classifier uses the most stable identity already available in the workflow:

1. `collector_id`
2. `service` or `job` labels when present
3. top-process job or process name as fallback
4. inferred workload role such as `build_compile`, `batch_io`, `deployment`, or `general_service`

The code builds that key in `deriveBehaviorIdentity(...)`.

Why this is enough:

- recurring bursts are usually workload-specific
- the workflow already has node labels and top-process context
- using the existing identity surface avoids inventing a parallel naming scheme

What can still go wrong:

- unstable service naming fragments history
- ephemeral workloads with poor labels stay conservative

## Decision flow

The controller does this after `buildRiskSeries(...)`:

1. Collect active signal observations from the current workflow window.
2. Ask the metric-history provider for a longer history window for the same collector.
3. For each active signal, compute:
   - current value
   - short baseline from the current `RiskSeries`
   - long-window mean and deviation
   - high-water mark from history
   - recurrence count for similar bursts
   - hour-of-day matches for the same signal
4. Gather corroboration from other signals:
   - error/log activity
   - service latency regression
   - eBPF runtime behavior
   - security findings
   - deployment/change context as a hint, not as a reason to suppress
5. Classify the signal as:
   - `expected_recurring_burst`
   - `suspicious_deviation`
   - `correlated_anomaly`
   - `confirmed_anomaly`
6. Feed that classification into:
   - `buildRiskSignals(...)`
   - `buildTrendAssessments(...)`
   - behavioral evidence output

The key point is that history changes the meaning of the burst. A high CPU point is not enough by itself. The workflow now asks whether the point is:

- large relative to the recent baseline
- unusual relative to the longer window
- familiar at the same hour of day
- corroborated by downstream harm

## Algorithm

This is deliberately lightweight.

### Baselines

The classifier uses two timescales:

- short-term baseline: the baseline already computed from the current `RiskSeries`
- long-term baseline: mean and spread from the longer history window

This distinction matters:

- short-term catches local change
- long-term catches habit

### Recurrence

The code counts prior burst runs above a history-derived threshold and also checks a simple hour-of-day bucket.

Why hour-of-day instead of a heavier seasonal model:

- daily scheduled jobs are common
- hour-of-day is cheap to compute
- it keeps the rule easy to explain

Why not day-of-week or a learned seasonal model here:

- the repo does not yet need that complexity
- many recurring bursts are daily enough that hour-of-day already helps

### Corroboration

A familiar resource spike should not be fully suppressed when other evidence says the system is actually degraded.

The classifier therefore escalates when the burst is accompanied by real corroboration such as:

- error-log burst
- service latency regression
- runtime anomaly evidence
- security findings

This is what separates "build job did its normal thing" from "build job spiked CPU and the service started failing."

### Cold start behavior

Sparse history stays conservative.

The code does not emit a special `insufficient_history` class anymore. It keeps the signal visible as `suspicious_deviation` and explains that the longer history is too thin to justify suppression.

That is a deliberate safety choice. The system should learn slowly, not silence new behavior too early.

## Local state

The only extra state introduced by this layer is a bounded in-memory cache:

- key: collector ID
- value: recently fetched history samples plus fetch time

Why it exists:

- one workflow burst can ask for history several times
- repeating the same long-window query in a tight loop is wasteful

Why it is safe:

- it is bounded by entry count and TTL
- it is not authoritative
- it can be dropped at any time without losing meaning

This is cache, not memory in the database sense.

## Storage tradeoffs

### Chosen path: existing metric-history provider plus tiny in-memory cache

Benefits:

- one source of truth for long-window metrics
- no schema migration for a second store
- no duplicate retention or cleanup rules
- cheap to explain during incident review

Costs:

- suppression quality depends on the quality of the existing history path
- if history is sparse, the workflow stays conservative

### SQLite

Why it was considered:

- embedded
- durable
- easy exact-key lookup

Why it was rejected:

- this problem does not need a second general-purpose store
- it would duplicate facts already present in metric history
- it would add failure modes without adding real query power for the current classifier

### Local structured files

Why they were considered:

- simpler than a database
- easy to inspect

Why they were rejected:

- they still create a duplicate history layer
- they still need retention, pruning, and schema discipline
- human readability is not enough reason to duplicate the source of truth

### Markdown summaries

Useful only for human-facing notes. Not acceptable as machine state.

Why not:

- weak structure
- poor update semantics
- wrong medium for active classification state

## Practical Cases And Regression Tests

The current regression suite in
[`../../backend/internal/controller/agentcore/behavioral_memory_test.go`](../../backend/internal/controller/agentcore/behavioral_memory_test.go)
now covers production-style cases instead of only synthetic threshold checks. The point is not to mimic every fleet, but to keep the classifier honest on the cases that actually create pager noise.

The suite is split on purpose:

- table-driven classifier cases exercise the classification logic directly
- workflow-level cases exercise `EvaluateJointRisk(...)` so suppression or escalation is also verified in the emitted `JointRiskAssessment`

The workflow-level cases are important because they catch policy drift in the real controller path, not just in the classifier helper. The newest additions explicitly check three cases that tend to regress in practice:

- a deployment log burst that is historically normal must stay downgraded in the final `JointRiskSignal`
- deployment or startup latency warmup must explain itself as deploy context, not generic sparse-history noise, and must only suppress fully once the same shape has repeated enough times
- an OOM-style memory incident must remain a `confirmed_anomaly` once kill evidence appears
- a moderate CPU deviation that arrives with rising latency and error evidence is allowed to cross the line into a full incident, even if the CPU delta alone would not justify that
- same-service peer comparison can keep one hot replica visible even when the same workload is usually bursty, while also recognizing when the whole peer group is under the same healthy load

### CPU cases

#### Build or compile worker with a known burst

What happens in practice:

- a build worker or compiler pool regularly pegs CPU for a short step
- the shape is bursty but healthy
- errors and latency usually stay flat

Expected output:

- `expected_recurring_burst`
- reason mentions recurring history, same-hour recurrence, or healthy prior bursts

Why that is correct:

- CPU alone is not enough evidence when the same workload has already done this repeatedly without downstream damage

#### Traffic surge with autoscaling lag

What happens in practice:

- request rate jumps
- CPU rises before extra capacity arrives
- latency stays within budget and errors remain low

Expected output:

- usually `expected_recurring_burst` or `suspicious_deviation`, depending on how much history exists
- not `confirmed_anomaly` when the burst is load-driven and healthy

Why that is correct:

- the system should surface the pressure, but a temporary saturation window without harm is not the same as a broken service

#### Runaway CPU or busy loop

What happens in practice:

- CPU climbs sharply
- there is no recurring pattern and no healthy deployment or batch context
- the process may stop making forward progress

Expected output:

- `suspicious_deviation` at minimum
- `confirmed_anomaly` if the value materially exceeds the historical high-water mark or if corroboration also appears

#### Deployment-related temporary CPU spike

What happens in practice:

- a rollout or restart causes short CPU churn
- the same service has shown similar warm restart behavior before

Expected output:

- downgrade toward `expected_recurring_burst` when the shape is familiar and downstream impact stays low

Why that is correct:

- restarts and rollout work often spend a short window recompiling caches, warming classes, or rebuilding in-memory state
- if that same shape has already happened repeatedly without harm, it should not keep re-opening incidents

### Memory cases

#### Startup warmup or cache fill

What happens in practice:

- memory rises after process start because caches, models, or classloaders fill
- the process then stabilizes

Expected output:

- `expected_recurring_burst` when history shows the same warmup shape and memory stops at the usual range

#### Gradual memory leak

What happens in practice:

- memory keeps climbing over a longer window
- the slope matters more than a one-minute spike

Expected output:

- `suspicious_deviation` or `confirmed_anomaly`
- never fully suppressed as a benign recurring burst

Why that is correct:

- a leak is not a legitimate burst pattern; adaptive baselines must not normalize it away

#### OOM-risk or OOMKilled pattern

What happens in practice:

- memory approaches limit
- pressure rises
- logs or runtime evidence show OOM kill, restart, or kill pressure

Expected output:

- `confirmed_anomaly`
- reason includes `oom` style evidence

Why that is correct:

- this is no longer a benign warmup or cache-fill shape once the workload is being killed
- the workflow-level regression for this case checks that the final `JointRiskSignal` for `memory_pressure` stays triggered, not only the internal classification helper

#### Node-level pressure or eviction

What happens in practice:

- the node itself is under memory pressure
- pods or workloads are evicted, throttled, or fail placement

Expected output:

- `confirmed_anomaly`
- do not suppress, even if a workload-level memory pattern looks familiar

### GPU cases

#### Expected training or inference burst

What happens in practice:

- GPU utilization and GPU memory rise sharply during a legitimate ML job
- the pattern is recurring and historically healthy

Expected output:

- `expected_recurring_burst`

#### Unexpected GPU memory saturation

What happens in practice:

- GPU memory spikes hard without a known training or inference pattern

Expected output:

- `suspicious_deviation` or stronger

#### GPU memory pinned while utilization stays low

What happens in practice:

- GPU memory remains allocated
- utilization drops or stays low
- the job looks stuck or resources were leaked

Expected output:

- `correlated_anomaly`
- reason mentions pinned GPU memory or stuck workload behavior

#### GPU hardware or driver fault

What happens in practice:

- XID-like errors, page retirement events, or job-health failures show up
- utilization by itself may not look catastrophic

Expected output:

- `confirmed_anomaly`
- single critical fault evidence is enough to escalate

#### GPU degradation with user-visible harm

What happens in practice:

- GPU is busy
- service latency regresses
- error evidence appears

Expected output:

- `correlated_anomaly` or `confirmed_anomaly`, depending on severity

### Correlated multi-signal cases

#### Bursty workload with no downstream harm

What happens in practice:

- CPU, network, or logs spike repeatedly
- no matching error burst
- no service-latency regression
- no runtime or security anomaly

Expected output:

- trend toward `expected_recurring_burst`

#### Same burst pattern, but now with harm

What happens in practice:

- the recurring burst shape is familiar
- this time error logs, latency, or runtime evidence also degrade

Expected output:

- `correlated_anomaly` when the resource pattern is familiar but corroborated
- `confirmed_anomaly` when the burst also exceeds prior bounds or carries critical fault evidence

#### Backup or artifact upload network burst

What happens in practice:

- throughput rises during a backup window or artifact transfer
- the network is busy but healthy

Expected output:

- `expected_recurring_burst`

#### Deployment-related log burst

What happens in practice:

- rollout and restart paths emit many logs for a short interval

Expected output:

- downgrade when that pattern is familiar and healthy
- escalate when the same log burst now carries error-heavy text or restart-loop evidence

Why that is correct:

- rollout logs are often noisy by design
- the workflow-level regression for this case uses indexed logs plus TSDB-backed history so the downgrade is verified through the real signal-collection path

#### Moderate metric deviation with strong corroboration

What happens in practice:

- the raw CPU or memory delta is not huge
- logs, latency, or runtime evidence all point the same way

Expected output:

- `correlated_anomaly` or `confirmed_anomaly`, depending on how visible the downstream harm already is

Why that is correct:

- once latency and error growth are already visible to users, the detector should be allowed to treat the resource signal as part of a real incident rather than background pressure
- the current workflow regression keeps this case at `confirmed_anomaly` for `cpu_pressure` and also leaves `service_latency` triggered as a separate signal

### Peer-comparison cases

#### One replica is hot, peers are normal

What happens in practice:

- the service usually has some bursty load history
- one replica shows the spike again
- other replicas in the same service stay near their normal range

Expected output:

- do not suppress the hot replica as `expected_recurring_burst`
- keep it at least `suspicious_deviation`
- reason should mention that the behavior is isolated relative to peers

Why that is correct:

- peer context is the cheapest way to distinguish "the service is busy" from "this one replica is unhealthy"

#### The whole peer group is busy in the same way

What happens in practice:

- several replicas of the same service all show the same short CPU burst
- latency and logs stay healthy
- the burst shape also matches prior history

Expected output:

- `expected_recurring_burst`
- explanation can mention that same-service peers are under the same burst

Why that is correct:

- a fleet-wide load burst is usually less suspicious than a single-replica outlier
- this uses the current fleet snapshot only; it does not introduce a second historical store

### Example result shape

```json
{
  "signal_id": "gpu_memory_pressure",
  "classification": "correlated_anomaly",
  "reason": "GPU memory remains pinned while utilization stays low and latency regressed",
  "cross_signal_support": [
    "gpu_memory_pinned",
    "service_latency_regression"
  ]
}
```

The exact wording varies by signal, but the shape is deliberate:

- the class says how hard the workflow should lean in
- the reason explains the decision in operator language
- `cross_signal_support` makes the escalation path reviewable

## Evaluation link

The concrete regression dataset for this design now lives in
[`../../eval_data/anomaly_cases.json`](../../eval_data/anomaly_cases.json),
and the evaluation method plus current measured results are documented in
[`19-testing-and-evaluation.md`](19-testing-and-evaluation.md).

That split is intentional:

- this note explains the design and its tradeoffs
- the evaluation note records what the current implementation actually gets right and wrong

## Limits

This design is intentionally narrow.

It does not yet solve:

- rich trace seasonality
- historical peer comparison across replicas over longer windows
- semantic log-template modeling
- cross-cluster memory sharing

It also depends on identity quality. If workloads churn names or labels constantly, recurrence becomes hard to prove and the workflow will correctly remain conservative.

## Why this is the maintainable choice

The Unix-style version of this change is:

- let the history system keep history
- let the workflow classify
- keep the extra state as a cache, not a database

That removes a layer, keeps the control flow readable, and makes suppression easier to debug.
