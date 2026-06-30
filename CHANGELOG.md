# Changelog

## Unreleased

### Changed

- The controller runtime is now documented and tested as skills-first: operational capabilities are governed through registry contracts, deterministic scoring, policy checks, normalized results, and replay-aware artifacts.
- RAG is a read-only knowledge skill. `rag_query` and the other knowledge retrieval surfaces remain compatibility evidence skills, but retrieval text no longer owns control-plane causality.
- Tool contracts now expose API-friendly skill fields for description, capability family, schemas, approval requirement, autonomy eligibility, query hints, follow-up families, idempotency, and replay semantics.
- Adaptive scoring now includes policy posture, prior yield, budget state, and repeated low-yield suppression before selecting the next skill.
- Workflow metrics and audit records now include skills-first fields such as selected skill, policy verdict, approval state, evidence IDs, skill invocation counts, low-yield counts, policy blocks, approval-required counts, score/duration samples, adaptive stop reasons, and RAG skill calls.
- Incident-memory retrieval now ranks prior cases with signal hints, change hints, remediation overlap, collector affinity, verification evidence, operator feedback, and recency instead of plain lexical match alone.
- Workflow knowledge retrieval now lets strong incident-memory matches raise retrieval summary and confidence even when static knowledge-base hits are absent.
- Joint-risk and RCA workflows now carry first-class telemetry quality, use it to cap workflow and hypothesis confidence, and surface stale or partial observability as explicit limitations and unresolved gaps.

### Documentation

- Added migration documentation for the skills-first taxonomy, runtime invariants, artifact schema compatibility, baseline validation, and golden incident replay checks.
- Updated the incident runtime, adaptive runtime, tool catalog, observability, pipeline, and upgrade-practice docs to describe skills-first control, RAG demotion, registry fields, rollback/replay rules, and runtime metrics.
- Expanded the English and Chinese incident-runtime and RAG docs to explain why incident memory is a separately ranked retrieval source, what trust signals affect reuse, and what tradeoffs still remain.
- Expanded the incident-runtime docs to explain why workflow confidence now depends on telemetry quality, how the deterministic confidence ceiling works, and why the repo avoids a second probabilistic uncertainty layer here.
- Kept the release notes tied to concrete implementation paths such as `backend/internal/controller/agentcore/workflow_engine.go`, `backend/internal/controller/incidentmemory/store.go`, and `backend/internal/controller/agentcore/agent.go`.

## v0.9 - 2026-03-27

### Release narrative

`v0.9` is a documentation-first release. The goal of this pass is to reduce code-to-doc drift around the current collector, controller, and two-agent incident runtime rather than to announce a new architecture.

The repository already contained the main mechanisms covered here: push-first collection, bounded spool replay, controller-owned hot state, bounded history, change intelligence, causal graph reasoning, incident memory, the `AnalysisAgent` -> `AnalysisHandoff` -> `ValidationActionAgent` RCA split, governed tool execution, post-action validation, and replay/evaluation coverage. `v0.9` updates the documentation so those mechanisms are described more directly, with stronger numbering, clearer boundaries, and better English/Chinese alignment.

### Added

- More detailed English and Chinese explanations of the two-agent RCA runtime, including the `AnalysisHandoff` contract, bounded validation loop, execution-category gating, and before/after post-action verification.
- Clearer boundary documentation for host-local evidence, controller hot state, retrieval, workflow governance, and operator approval.
- Evaluation documentation for workflow-level quality metrics such as analysis handoff coverage, validation report coverage, validation loop coverage, evidence package coverage, and memory write-back coverage.
- ADR coverage for validation-time write gating and before/after verification.

### Changed

- Updated visible project-owned documentation references from `v0.8` to `v0.9`.
- Added explicit heading numbering across the main English and Chinese document paths.
- Reworked top-level README navigation and release context so the reading path, runtime path, and current production boundaries are easier to scan.
- Tightened English/Chinese structure and terminology around hot state, bounded history, durable run, evidence package, incident memory, `AnalysisAgent`, `ValidationActionAgent`, `AnalysisHandoff`, `validation_action_react_loop`, and `post_action_validation`.

### Documentation

- Expanded pipeline, architecture, incident-runtime, ADR, testing/evaluation, deployment, usage, API, and RAG pages to reflect the current code more precisely.
- Removed several remaining vague or future-sounding phrases that overclaimed beyond the implemented runtime.

## v0.8 - 2026-03-20

### Release narrative

`v0.8` aligns the public documentation and release metadata with the current repository state.

The codebase is no longer only a push-first observability stack with deterministic RCA and local RAG. The controller now also contains a durable incident-agent runtime with governed tool execution, change intelligence, causal graph reasoning, incident-memory write-back, replay evaluation, and evidence packaging. The documentation now explains that architecture directly, using the actual files and runtime APIs rather than generic AIOps language.

### Added

- English and Chinese incident-agent runtime guides covering durable runs, tool governance, change intelligence, causal graph reasoning, verification, compensation, and incident memory.
- A stronger documentation index organized by audience and by goal.
- A code-grounded v0.8 release narrative across the top-level README, architecture docs, API reference, evaluation guide, and business-use-case material.

### Changed

- Updated project-owned documentation and release metadata from `v0.7` to `v0.8`.
- Reworked the top-level README and documentation entry points around the actual collector -> ingest -> analysis -> retrieval -> workflow -> audit path.
- Expanded architecture and control-plane docs to explain why each subsystem exists, what tradeoff it manages, and how it is implemented in the current code.
- Updated the API and evaluation docs to reflect durable workflow runs, workflow evidence packages, replay evaluation, and the new incident-agent surfaces.

### Documentation

- Clarified the engineering narrative: this repository is an evidence pipeline and governed incident agent, not a chatbot over metrics.
- Clarified the use-case framing: the project is aimed at reducing operator uncertainty, preserving onset evidence, and making guarded automation usable in AI/GPU operations.
- Clarified the research narrative: the docs now describe weak-signal analysis, retrieval gating, deterministic fallbacks, and workflow governance as implementation choices with explicit tradeoffs.

## v0.7 - 2026-03-12

### Added

- Predictive early-warning analysis for GPU thermal, GPU power, PCIe pressure, network jitter, IO pressure, CPU saturation, and memory exhaustion precursors.
- Structured predictive records with audit-ready fields such as `prediction_id`, `asset_id`, `hazard_class`, `control_reference`, `algorithm_version`, and evidence window bounds.
- Root `VERSION` file so release metadata can be checked from one source of truth.
- New docs for predictive signals, predictive operations, and the Python runtime boundary.
- CI workflow skeletons for security scanning, multi-arch builds, and predictive validation.

### Changed

- Project-owned release references were aligned to `v0.7` across docs, manifests, build metadata, and package metadata.
- Agent reports now combine deterministic threshold findings with bounded predictive warnings derived from retained metric history.
- Trend retention now preserves GPU thermal, GPU power, PCIe, memory-percent, and pressure metrics needed for low-cost forecasting.
- README and architecture docs now describe predictive early warning as part of the core control-plane design instead of an implied future capability.
- Makefile includes release-verification, predictive validation, Helm packaging, SBOM, and low-overhead benchmark targets.

### Fixed

- Accidental release/version drift across project-owned Markdown, YAML, comments, build scripts, and packaging metadata.
- A stale third-party dependency bump caused by broad text replacement during the version sweep.
- Missing documentation for the Python `sre_agent` runtime package and its production support boundary.
- Gaps between the documented low-overhead predictive design and the actual controller report path.

### Security

- Predictive findings stay deterministic and audit-friendly; the hot path does not rely on LLM calls.
- Workflow/CI scaffolding now includes explicit SBOM and security-scan hooks for supply-chain verification.
