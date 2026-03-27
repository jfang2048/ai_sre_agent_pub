# Incident Agent Runtime

中文版本：[docs/zh/17-incident-agent-runtime.md](../zh/17-incident-agent-runtime.md)

This page explains the controller-side incident agent as it exists in `v0.8`.

It is not a roadmap. It is a mechanism-level description grounded in the current runtime:

- [`../../backend/internal/controller/agentcore/workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go)
- [`../../backend/internal/controller/agentcore/workflow_orchestrator.go`](../../backend/internal/controller/agentcore/workflow_orchestrator.go)
- [`../../backend/internal/controller/agentcore/workflow_tools.go`](../../backend/internal/controller/agentcore/workflow_tools.go)
- [`../../backend/internal/controller/agentcore/workflow_memory.go`](../../backend/internal/controller/agentcore/workflow_memory.go)
- [`../../backend/internal/controller/agentcore/workflow_evidence.go`](../../backend/internal/controller/agentcore/workflow_evidence.go)
- [`../../backend/internal/controller/changeintel/`](../../backend/internal/controller/changeintel/)
- [`../../backend/internal/controller/causalgraph/`](../../backend/internal/controller/causalgraph/)
- [`../../backend/internal/controller/incidentmemory/`](../../backend/internal/controller/incidentmemory/)
- [`../../backend/internal/controller/evaluation/`](../../backend/internal/controller/evaluation/)

Read [Pipeline Deep Dive](02-pipeline-deep-dive.md) first if you want the collector-to-controller evidence path. Read [Architecture](04-architecture.md) first if you want the trust and persistence boundaries around this runtime.

## In This Page

- [Why This Runtime Exists](#why-this-runtime-exists)
- [What Makes It A Governed Control Loop](#what-makes-it-a-governed-control-loop)
- [Why RCA Is Now A Two-Agent Runtime](#why-rca-is-now-a-two-agent-runtime)
- [Run Lifecycle](#run-lifecycle)
- [Durable Run State Model](#durable-run-state-model)
- [Event And Step Persistence Model](#event-and-step-persistence-model)
- [Tool Gateway Behavior](#tool-gateway-behavior)
- [Idempotency And Request Dedupe](#idempotency-and-request-dedupe)
- [Approval And Policy Checks](#approval-and-policy-checks)
- [Verification Loop](#verification-loop)
- [Compensation And Rollback Intent](#compensation-and-rollback-intent)
- [Change Intelligence, Causal Graph, And Incident Memory](#change-intelligence-causal-graph-and-incident-memory)
- [Behavioral Memory In The Decision Loop](#behavioral-memory-in-the-decision-loop)
- [Evidence Packages And World Model Snapshots](#evidence-packages-and-world-model-snapshots)
- [Evaluation And Replay Hooks](#evaluation-and-replay-hooks)
- [Operator Surfaces](#operator-surfaces)
- [Practical Limits](#practical-limits)
- [Read Next](#read-next)

## Why This Runtime Exists

Before the durable workflow runtime, the controller could still analyze incidents, but it was weaker as an operational agent:

- run state was harder to reconstruct after restart
- change evidence was mostly indirect
- prior incidents could be retrieved as text, but not as structured operational memory with action outcomes
- policy, approval, verification, and compensation lived as scattered concerns instead of one explicit state machine

The current runtime solves a narrower problem:

> turn evidence-grounded incident analysis into a resumable, inspectable, policy-governed control loop without pretending that unconstrained autonomy is safe.

The default posture in [`workflow_types.go`](../../backend/internal/controller/agentcore/workflow_types.go) reflects that:

- `DryRun = true`
- `RequireApproval = true`
- `AllowProfilingExec = false`
- `AllowRemediationExec = false`
- `VerificationWindow = 2m`
- `DegradedModePolicy = deterministic_only`

## What Makes It A Governed Control Loop

This runtime is not just “LLM output plus tools.”

It is governed because the current code gives first-class state to:

- the workflow request contract
- durable run status
- explicit plan revisions and step records
- policy verdicts per tool call
- approval state per guarded step
- idempotency keys and cached action reuse
- post-step verification results
- compensation and rollback outcomes
- evidence packages and incident-memory write-back

Without those pieces, the system would be a chatbot wrapper around controller APIs. With them, it becomes a bounded control loop that can be inspected after the fact.

## Why RCA Is Now A Two-Agent Runtime

The RCA path is no longer one mixed loop that both analyzes and tries to act.

It now has two explicit controller-side roles:

- `AnalysisAgent`: incident synthesis, hypothesis generation, evidence ranking, recommendation drafting, and `AnalysisHandoff` generation
- `ValidationActionAgent`: tool-driven validation, contradiction search, recommendation checks, guarded action planning, and post-action validation

The split is practical, not ornamental.

- analysis needs broad correlation and ranking across telemetry, security, change, and incident memory
- validation and action need sharper tool selection, stricter budgets, stronger policy boundaries, and explicit before/after verification

That is why the handoff is persisted as a structured object instead of free-form text. The validation side can start from ranked causes, supporting evidence IDs, weak evidence IDs, recommendations, unresolved gaps, telemetry quality, and suggested validation targets without re-deriving the whole incident.

## Run Lifecycle

The main RCA and joint-risk entrypoints are `EvaluateJointRisk` and `BuildRCAWorkflow` in [`workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go).

The durable flow is:

```text
StartRunWithRequest
  -> analysis_handoff_finalize
     -> AttachAnalysisHandoff
        -> validation_action_react_loop
           -> RecordValidationLoop / AttachValidationReport
              -> [AttachApproval + SuspendRun] or [RecordVerification]
                 -> [RecordCompensation if needed]
                    -> CompleteRun or FailRun
                       -> AttachEvidencePackage
                       -> AppendMemoryRecord
```

The corresponding durable statuses are:

- `running`
- `suspended`
- `completed`
- `failed`

The main RCA control-loop step is now `validation_action_react_loop`. The older `plan_act_verify_loop` remains as the disabled-path compatibility fallback when the validation agent is turned off.

## Durable Run State Model

The durable state model lives in [`workflow_orchestrator.go`](../../backend/internal/controller/agentcore/workflow_orchestrator.go).

### DurableRun

`DurableRun` is the top-level persistent record.

| Field group | Important fields | Why they exist |
| --- | --- | --- |
| identity and request | `RunID`, `WorkflowType`, `CollectorID`, `Request` | bind every derived decision back to the exact workflow request |
| progress | `Status`, `CurrentStep`, `CurrentStage`, `LastResumeAt` | tell operators where the run stopped or resumed |
| audit timeline | `Events` | every important state transition also becomes a durable `WorkflowEvent` |
| governed execution | `ToolCalls`, `PlanRevisions`, `Steps`, `AnalysisHandoff`, `Validation`, `ValidationLoops` | preserve what analysis concluded first, what the validation/action side actually checked, and what was verified or blocked |
| durable artifacts | `EvidencePackage`, `WorldModel`, `MemoryRecords` | preserve supporting artifacts outside the main row |
| replay and terminal state | `ReplayCount`, `Result`, `Error` | support replay accounting and post-run inspection |
| scratch context | `Context` | stores runtime-specific metadata such as suspend reason |

### Durable step-level records

Each step is persisted as `DurableStepRecord`.

Important fields:

- identity: `StepID`, `Stage`, `Order`, `Iteration`
- intended action: `Title`, `Tool`, `Query`, `Required`, `OriginalAction`
- outcome: `Status`, `ResultSummary`, `ErrorMessage`
- evidence: `EvidenceIDs`, `LastToolCallID`
- governance: `Policy`, `Approval`, `Verification`, `Compensation`
- timing: `StartedAt`, `CompletedAt`

This separation exists because a run can contain multiple plan revisions and multiple tool calls for the same stage. The runtime needs step-level state, not only a final report blob.

### Default storage

By default the current code uses:

- BoltDB for runs: `data/agent/workflow_runs.db`
- evidence packages: `data/agent/workflows/evidence/`
- incident memory: `data/agent/workflows/incident_memory/`
- change intelligence: `data/agent/workflows/changeintel/`

If BoltDB cannot be opened, the runtime falls back to an in-memory durable store. That keeps the engine runnable, but restart durability is reduced immediately.

## Event And Step Persistence Model

The persistence model is intentionally redundant in one useful way:

- structured state is stored directly on the `DurableRun`
- important changes are also appended as `WorkflowEvent`

That means the runtime keeps both:

- the latest state of each step, tool call, or evidence reference
- an event log of how the run moved there

### What gets persisted

| Mutation | Main method | What it changes |
| --- | --- | --- |
| run start | `StartRunWithRequest` | initializes `DurableRun` and logs `run_started` |
| stage transition | `RecordStepTransition` | updates `CurrentStep` and `CurrentStage` |
| plan revision | `RecordPlanRevision` | appends a full `AgentPlanRevision` snapshot and logs `plan_revision_recorded` |
| tool call | `RecordToolCall` | appends `WorkflowToolCall` and logs `tool_call_recorded` |
| step update | `RecordStepState` | upserts `DurableStepRecord` by `StepID` |
| analysis handoff | `AttachAnalysisHandoff` | persists the structured packet passed from `AnalysisAgent` to `ValidationActionAgent` |
| validation loop record | `RecordValidationLoop` | persists one bounded ReAct iteration record with tool choice, observation, and verdict change |
| validation report | `AttachValidationReport` | persists per-target verdicts, validated and rejected recommendations, contradiction summary, and post-action validation |
| policy attachment | `AttachPolicy` | stores the policy verdict on the step |
| approval attachment | `AttachApproval` | stores pending or resolved approval state |
| verification | `RecordVerification` | stores post-step verification outcome and merges evidence IDs |
| compensation | `RecordCompensation` | stores rollback or skipped-compensation state |
| evidence package | `AttachEvidencePackage` | records the generated artifact path |
| world model | `AttachWorldModel` | stores topology, scope, and recent-change context |
| memory write-back | `AppendMemoryRecord` | stores persisted incident-memory artifact paths |
| suspension | `SuspendRun` | marks the run suspended and stores the suspend reason |
| completion / failure | `CompleteRun`, `FailRun` | stores terminal status and terminal result or error |

### Why this model exists

The runtime could have stored only final reports. That was implicitly rejected because it would lose:

- which steps were planned but never executed
- whether a tool call was forced into dry-run
- whether execution stopped on policy or on approval
- what was verified versus merely suggested
- whether compensation was attempted

## Tool Gateway Behavior

All governed tool execution is centralized in `workflowToolManager.call` in [`workflow_tools.go`](../../backend/internal/controller/agentcore/workflow_tools.go).

That function is the actual execution boundary for workflow tools.

### Call sequence

For each tool call, the current runtime does this in order:

1. construct a `WorkflowToolCall` record with tool name, actor, stage, collector, dry-run flag, policy version, and risk tag
2. validate the request shape with `validateWorkflowToolRequest`
3. evaluate policy with `PolicyEngine.Evaluate`
4. reject blocked calls immediately
5. if approval is required and the request is not dry-run, stop with `ApprovalState=pending`
6. if policy requires dry-run, rewrite the request into dry-run mode
7. generate or use an idempotency key
8. check the in-memory idempotency cache
9. for non-read-only tools, search recent durable runs for a prior successful tool call with the same idempotency key
10. enforce a tool-specific timeout
11. retry only when the tool is read-only and the error is retryable
12. cache successful results by idempotency key
13. persist the governed `WorkflowToolCall` on the durable run

### Why centralization matters

Without a centralized gateway:

- each tool would decide its own timeout policy
- approval logic would drift
- idempotency behavior would be inconsistent
- audit coverage would depend on each tool implementation

The current design makes the tool boundary deliberately boring and uniform.

### What the validation/action side can use now

The second agent reuses the same governed gateway, but its practical tool surface is much broader than the old RCA loop.

Current categories:

- core observability: metrics, logs, eBPF/runtime, topology, GPU, service health
- change and config: change query, deployment history, config state, container revision
- deep diagnostics: memory pressure, storage health, connectivity, DNS
- workload and platform: Kubernetes resource identity, process lineage, network blast radius
- security and forensics: security findings, security graph, runtime/process graph
- knowledge and memory: runbook retrieval, similar-case retrieval, historical incident retrieval, prior action outcome retrieval
- guarded execution: profiling and remediation through the existing policy, approval, and dry-run path

## Idempotency And Request Dedupe

There are two different dedupe layers in the runtime.

### 1. Workflow-request dedupe

At workflow entry, `beginWorkflowRun` deduplicates equivalent workflow requests for a short TTL.

Defaults from [`workflow_types.go`](../../backend/internal/controller/agentcore/workflow_types.go):

- `RequestDedupeTTL = 30s`
- `RequestDedupeEntries = 256`

The dedupe key includes:

- workflow type
- collector
- window
- limit
- trigger
- dry-run flag

Why it exists:

- repeated API refreshes should not launch duplicate RCA or joint-risk runs immediately
- in-flight identical runs are serialized instead of racing

### 2. Tool-call idempotency

At tool execution time, `stableWorkflowToolKey` hashes:

- tool name
- workflow
- stage
- collector
- query payload
- dry-run mode

Why it exists:

- the same remediation or profiling request should not be executed repeatedly just because the workflow revisited the same step
- prior successful guarded actions can be reused from durable history instead of replayed blindly

Limitation:

- the idempotency key is only as stable as the request payload normalization
- semantically equivalent requests with different text still produce different keys

## Approval And Policy Checks

The workflow runtime separates read-only evidence collection from guarded execution.

### Tool classes in the current code

| Tool class | Examples | Safety behavior |
| --- | --- | --- |
| read-only | metrics, logs, topology, change query, security query, knowledge retrieval, eBPF query, GPU query, security graph, process lineage | can retry; supports dry-run trivially; no approval needed |
| guarded execution | profiling | policy-controlled; may require dry-run or approval depending on config |
| approval-gated | remediation | explicitly marked unsafe; approval gate is first-class |

### Policy outcomes

`ActionPolicyDecision` can effectively do four things:

- allow the call
- block the call
- require approval
- require dry-run

### Approval path in the current runtime

Inside `validation_action_react_loop`, when a tool call returns an approval-required policy result:

1. the step status becomes `approval_required`
2. `DurableApprovalRecord{State:"pending"}` is attached
3. the run is suspended with `SuspendRun`
4. the workflow stop reason becomes `awaiting approval`

This is what makes the run resumable instead of silently failing or quietly skipping execution.

### Why approval is separate from policy

Policy answers “is this category of action allowed under current rules?”

Approval answers “did a human authorize this specific action instance?”

The runtime stores both because they are different operational questions.

## Verification Loop

Verification is not an optional comment. It is a persisted stage in the runtime, and the validation side now owns that stage explicitly.

### Target validation before action

`ValidationActionAgent` works against explicit target types:

- hypothesis validation
- recommendation validation
- change-correlation validation
- remediation-outcome validation
- contradiction search

For each target it runs a bounded loop:

1. inspect the target and current verdict
2. choose the next tool from the target-aware catalog
3. call the governed tool gateway
4. record a concise observation and confidence delta
5. update verdict: `confirmed`, `contradicted`, `partially_supported`, or `insufficient_evidence`
6. stop on support, contradiction, budget, timeout, or tool-sequence exhaustion

The loop persists concise artifacts only. It does not store hidden chain-of-thought.

### Deterministic usefulness checks

The older deterministic checks still matter as a fallback and for specific tool payloads.

Examples:

- `ToolMetrics` requires a node plus at least three history samples
- `ToolLogs` requires actual matching log evidence when a log-burst hypothesis exists
- `ToolChangeQuery` requires at least one correlated change
- `ToolEBPFQuery` requires runtime events or syscall statistics

The runtime records a `DurableVerificationRecord` either way.

### Remediation and post-action validation

For remediation, the runtime is now stricter in two separate ways:

1. the validation side decides whether the recommendation is even supported strongly enough to keep
2. if a guarded remediation actually runs, `post_action_validation` records a separate `PostActionValidationSummary`
3. the deterministic fallback still calls `verifyRemediationEffect`
4. the final report keeps the validation verdict and the post-action verdict side by side

Current defaults:

- `VerificationWindow = 2m`
- `MediumRiskThreshold = 0.45`

What this buys:

- the runtime distinguishes “action executed” from “action helped”
- incident memory can prefer verified actions over merely attempted ones

Limitation:

- the verification function is intentionally simple today
- it does not yet compare a rich before/after world model, only the new joint-risk score and supporting notes

## Compensation And Rollback Intent

Compensation is handled in `stepCompensate`.

The runtime does not assume rollback is always possible. It records one of three states:

- `executed`
- `failed`
- `skipped`

### How compensation works today

If all of these are true:

- `AutoRollbackOnVerificationFailure` is enabled
- a `PlaybookRunner` is available
- the step has an `OriginalAction`
- `OriginalAction.RollbackCommand` is populated

then the runtime attempts `ExecuteRollback`.

Otherwise it records compensation as skipped with reason `no rollback command`.

### Why compensation is explicit

If rollback intent were only implied in logs, operators could not tell:

- whether rollback was attempted
- whether it failed
- whether there was simply nothing safe to run

The current runtime makes that difference durable.

## Change Intelligence, Causal Graph, And Incident Memory

These three subsystems turn the workflow into more than a trend-and-RAG wrapper.

### Change intelligence

`changeintel.Store` persists normalized `ChangeEvent` JSON artifacts and correlates them against:

- collector or node scope
- incident summary text
- incident window
- scope hints

Current scoring weights are simple and explicit:

- temporal adjacency: 55%
- scope overlap: 30%
- semantic overlap: 15%

Why this exists:

- many incidents are triggered by deployment, config, driver, or feature-flag changes
- the runtime needs a first-class place to represent that evidence instead of burying it in log snippets

### Causal graph

`causalgraph.Analyze` is not a learned graph model. It is a typed ranking pass.

It builds a graph from evidence nodes and edges, then:

- boosts likely upstream causes
- heavily boosts `change` nodes
- gives runtime and security nodes more causal weight than plain symptom nodes
- computes shortest-path cause and impact paths

Why this exists:

- operators need a plausible ordering of cause versus symptom, not only a flat finding list

### Incident memory

The workflow memory bridge writes `WorkflowMemoryRecord` into the incident-memory store.

Current write-back points:

- successful remediation via `recordSuccessfulRemediation`
- completed RCA via `persistRCAArtifacts`

Stored fields include:

- root cause
- verification summary
- causal path
- impact scope
- signals
- actions
- evidence IDs
- hypotheses
- action outcomes
- operator feedback when present

Why this exists:

- static runbooks answer “what is usually recommended”
- incident memory answers “what previously happened here and what actually worked”

## Behavioral Memory In The Decision Loop

The runtime now contains a separate but related discrimination layer for recurring workload behavior.

It is not the same as incident memory:

- incident memory stores prior incident outcomes and actions
- recurring-burst logic reads longer-window metric history to decide whether an active burst resembles known benign behavior

### Why this exists

Before this feature, the workflow could already explain:

- current value versus baseline
- trend direction
- weak-signal co-occurrence

But it still lacked one practical memory:

- has this service shown this same short-lived burst many times before without correlated damage?

That gap produced false positives for workloads such as build jobs, artifact uploads, and deployment helpers.

### Where it runs

The integration point is inside `workflowState.refreshDerivedEvidence()` in [`workflow_engine.go`](../../backend/internal/controller/agentcore/workflow_engine.go):

1. build `RiskSeries`
2. call `BehavioralMemoryStore.Evaluate(...)`
3. feed the result into `buildRiskSignals(...)`
4. feed the same result into `buildTrendAssessments(...)`
5. emit explicit evidence records through `buildBehavioralAssessmentEvidence(...)`

### What state exists

This layer does not persist a second copy of long-term workload history.

Instead it uses:

- the existing metric-history provider for long-window reads
- optional TSDB-backed history when that provider is configured to read from the durable timeseries path
- a bounded in-memory cache so repeated workflow evaluations do not ask the same history question over and over

That keeps one source of truth for long-window metrics and avoids a second behavior database.

### How decisions change

The workflow now classifies each active signal as:

- `expected_recurring_burst`
- `suspicious_deviation`
- `correlated_anomaly`
- `confirmed_anomaly`

Those classes are then used to change the runtime output:

- expected recurring bursts lose score and can be untriggered
- correlated anomalies stay visible and raise confidence without pretending every corroborated burst is a hard fault
- confirmed anomalies gain confidence
- service-latency regressions can corroborate a weak resource spike when optional `service_latency_p95_ms`/`p99_ms` metrics are present
- sparse history stays conservative and visible by staying in `suspicious_deviation`
- suppression reasons are preserved in evidence and operator-facing summaries

### Why this belongs in the runtime docs

This memory affects incident conclusions and audit trails directly:

- `JointRiskAssessment` now includes `behavioral_assessments`
- RCA context and final workflow reports also include `behavioral_assessments`
- evidence packages now contain `behavioral_memory_decision` records

That makes the suppression path inspectable instead of hidden.

## Evidence Packages And World Model Snapshots

The runtime writes a JSON evidence package through [`workflow_evidence.go`](../../backend/internal/controller/agentcore/workflow_evidence.go).

Each package contains:

- the persisted `DurableRun`
- the optional `AgentTrace`
- recent `WorkflowAuditRecord` entries
- the final report payload
- `GeneratedAt`

This package is referenced from `DurableRun.EvidencePackage` and exposed through:

- `GET /api/v1/agent/workflow/evidence/{run_id}`

### World model

`AttachWorldModel` stores a `DurableWorldModel` that currently includes:

- `Summary`
- `Scope`
- `DownstreamNodes`
- `RecentChanges`
- `Topology`

This is intentionally separate from the final RCA report because it captures the environment snapshot the run reasoned over, not only the report conclusion.

## Evaluation And Replay Hooks

The repo has explicit replay hooks because a governed workflow should be regression-testable.

### Runtime-level replay

`ReplayRun` increments `ReplayCount` and logs `run_replayed`.

This is lightweight bookkeeping, not a full deterministic re-execution engine by itself.

### Evaluation wrapper

`evaluation.RunReplay` runs the golden evaluation twice and compares drift in:

- root-cause accuracy
- recommendation safety
- governance coverage
- verification coverage
- durable run coverage
- evidence package coverage
- memory write-back coverage
- retrieval metrics

Why this matters:

- the runtime is not only evaluated on “did it return a result?”
- it is also evaluated on “did it persist runs, verification, evidence, and memory the way the control loop claims?”

## Operator Surfaces

Main read APIs:

- `GET /api/v1/agent/joint-risk`
- `GET /api/v1/agent/rca`
- `GET /api/v1/agent/workflow/audit`
- `GET /api/v1/agent/workflow/runs`
- `GET /api/v1/agent/workflow/runs/{run_id}`
- `GET /api/v1/agent/workflow/evidence/{run_id}`
- `GET /api/v1/agent/workflow/incidents`

Approval and action APIs exposed by the controller include:

- `POST /api/v1/agent/incidents/{incident_id}/actions/{action_id}/approve`
- `POST /api/v1/agent/incidents/{incident_id}/actions/{action_id}/execute`
- `POST /api/v1/agent/incidents/{incident_id}/actions/{action_id}/rollback`

These surfaces matter because they make the runtime inspectable as an API-backed control loop, not only as internal Go code.

## Practical Limits

The current implementation is stronger than a report-only pipeline, but it still has clear limits:

- durability is local-first, not a distributed workflow service
- verification is heuristic and currently reuses fresh joint-risk evaluation rather than a richer world-state comparator
- change intelligence is repository-local and heuristic, not a CMDB or enterprise release-management system
- incident memory quality depends on workflows actually writing back verified outcomes
- idempotency is payload-based and does not capture semantic equivalence
- rollback quality depends on having meaningful rollback commands and safe action descriptors
- approval is explicit, but it still depends on surrounding operator process and token handling

Those are real boundaries of the current code, not missing marketing polish.

## Read Next

- [Pipeline Deep Dive](02-pipeline-deep-dive.md): where the evidence entering this runtime comes from
- [Architecture](04-architecture.md): state ownership, trust boundaries, and hot-vs-durable design choices
- [Architecture Decisions](18-architecture-decisions.md): ADR-style rationale for the durable governed runtime and related control-plane choices
- [Testing and Evaluation](19-testing-and-evaluation.md): the golden and replay evaluation surfaces behind the runtime
