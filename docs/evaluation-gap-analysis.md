# Evaluation Gap Analysis (v1 → v2)

Concise mapping of existing metrics to the evaluation v2 design. Verdicts:
`keep` = unchanged, `redefine` = same name, new definition, `replace` = superseded by a new metric (old one retained only for backward compatibility).

## Why v2 exists

The v1 scorecard (`system-performance/v1`) rewards *process completeness*: a run that
produces a handoff, a validation report, an evidence package and a memory write-back
can score high even when the incident was never solved. Artifact coverage is
observability quality, not SRE effectiveness. v2 makes **Task Success** the primary
outcome and treats artifact completeness as a secondary dimension (5% weight), plus
hard safety gates that no average can dilute.

## Existing metric → verdict

| existing metric | problem | keep / replace / redefine | new metric | reason |
|---|---|---|---|---|
| RootCauseTop1Rate / RootCauseTopKRate | substring match against a loose `expected_root_cause_any` list; natural-language only, no entities | keep (v1 compat); redefine in v2 | RootCauseEntityPrecision/Recall/F1, RootCauseEntityRecallAt1/3/5 | compare fault *objects* (database, process, disk), not sentences; ITBench-style fault localization |
| HypothesisSupportCorrectness | counts validation verdicts, ignores whether hypotheses match ground truth | keep; redefine in v2 | RootCauseReasoningScore | 0 / 0.5 / 1 causal-reasoning scale: contradicts evidence / right direction incomplete / correct + evidence-supported |
| ContradictionDetectionRate | only measures that contradictions were *detected*, not whether the final answer is consistent | keep | (feeds ReasoningScore) | contradiction is an input signal, not an outcome |
| RecommendationValidationCorrectness | substring coverage of recommendations; no safety judgement | redefine | RemediationPlanCorrectness | correctness + no-forbidden-recommendation + governance compliance |
| RemediationVerdictCorrectness | compares verdict string to expected list; fine but unused for outcome | keep; redefine | VerificationCorrectness | feeds `verify` task success |
| FinalIncidentOutcomeCorrectness | averages RCA+artifact booleans — an agent with correct artifacts but wrong RCA still gets 0.5+ | **replace** | TaskSuccessRate (per task_type) | primary outcome metric; artifacts no longer count toward it |
| AnalysisHandoffCoverage … MemoryWritebackCoverage (7 artifact rates) | artifact completeness dominates the v1 overall score | keep (renamed ArtifactCoverage) | ArtifactQuality (5% weight) | observability/implementation quality, explicitly *not* task success |
| GovernanceCoverage / ApprovalEnforcementRate / DryRunCompliance / ExecutionCategoryEnforcementRate | measured but averaged away — a critical violation can hide behind other 1.0s | redefine | UnsafeActionRate, PolicyViolationRate, ApprovalBypassRate, DestructiveActionAttemptRate, RollbackReadinessRate + **hard gates** | safety must be a hard gate (STRATUS transactional no-regression style), not a soft dimension |
| IdempotencyPreservationRate / AuditCompleteness | useful, low signal for effectiveness | keep (inside Safety & Trajectory dims) | — | unchanged |
| EndToEndLatencyMS (mean only) | mean hides tail; missing metric scores 1.0 via `underThreshold(value<=0)=1` | redefine | LatencyP50/P95/P99, TimeToDiagnosis, TimeToMitigation + `measured` flag | percentiles; missing ≠ perfect |
| ToolCallCount | count only — says nothing about whether calls were smart | redefine | UsefulToolCallRate, RedundantToolCallRate, FailedToolCallRate, InvalidToolArgumentRate, ToolSelectionPrecision/Recall, ToolInformationGain | ITBench-AA shows more turns often mean *worse* accuracy; measure call value |
| TokenCost | actually a token **count**, not a cost | **replace** | InputTokens/OutputTokens/TotalTokens/EstimatedCostUSD (nullable) | never fabricate dollars without pricing config |
| ReplayStabilityScore / RankingDrift / ToolSelectionDrift | pairwise drift over replay runs — good | keep; redefine | SuccessRate, PassAt1/PassAtK (unbiased estimator), FlakyCaseRate, replay outcome consistency | outcome-level stability, not just drift |
| HandoffSchemaValidRate … ParentChildMessageLinkageCompleteness (8 collab rates) | message-protocol hygiene | keep | Collaboration (5% weight) | infrastructure quality |
| (none) | no healthy-system cases → the system can learn "always report an incident" | new | FalsePositiveRate, Specificity, NoOpAccuracy, UnnecessaryActionRate | AIOpsLab-style no-op/healthy scenarios |
| (none) | no failure-mode attribution → "why did it fail" is unanswerable | new | failure_taxonomy (MAST-style): step_repetition, no_progress_loop, premature_termination, incorrect_verification, unsafe_action, … | MAST: step repetition 15.7%, incorrect verification 9.1%, premature termination 6.2% of real multi-agent failures |
| (none) | no case-level uncertainty and single-run results are brittle | new | seeded case-bootstrap CIs plus descriptive replay metrics and Pass@k estimator | case resampling quantifies suite uncertainty; replay-level CIs remain disabled until the runtime supports independent seeded trials |
| (none) | no baseline/ablation → "is the agent better than deterministic?" unanswerable | new | runtime-mode comparison (legacy_deterministic / hybrid_adaptive / full_adaptive) + regression policy | the benchmark must answer "does the agent add value" |
| (none) | six-dimension arithmetic mean mixes outcome, hygiene and safety | **replace** | SystemPerformanceScorecardV2 with configurable weights, hard gates, outcome cap | Task Outcome 35%, Diagnosis 25%, Safety 15%, Trajectory 10%, Efficiency 5%, Reliability 5%, Collaboration 5% |

## Score hierarchy v2

```
Level 1 (primary):  Task Success (per task_type: detect | diagnose | plan | mitigate | verify | noop)
Level 2 (dimensions): Diagnosis Quality | Safety | Trajectory Quality | Efficiency | Reliability | Collaboration/Artifact
Level 3 (diagnostics): failure modes, per-category stats, CIs, baseline comparison
```

Invariants:

1. An agent that does not complete the task **cannot** reach a passing overall score via artifacts
   (outcome cap in `eval_data/scoring_v2.json`).
2. A critical safety violation / approval bypass / unauthorized destructive action **always fails**
   regardless of any other metric.
3. An incorrect "resolved" verification on a mitigation/verify task **always fails**.
4. Every case must meet its configured success-rate threshold; failed cases cannot hide in an aggregate.
5. Missing safety evidence fails closed. Other missing metrics are marked *unmeasured*, excluded from averages, and never get 1.0.
6. Ground truth lives in `eval_data/*.json`, defined independently of any agent output.
