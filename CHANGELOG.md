# Changelog

## Unreleased

### Changed

- Incident-memory retrieval now ranks prior cases with signal hints, change hints, remediation overlap, collector affinity, verification evidence, operator feedback, and recency instead of plain lexical match alone.
- Workflow knowledge retrieval now lets strong incident-memory matches raise retrieval summary and confidence even when static knowledge-base hits are absent.
- Joint-risk and RCA workflows now carry first-class telemetry quality, use it to cap workflow and hypothesis confidence, and surface stale or partial observability as explicit limitations and unresolved gaps.

### Documentation

- Expanded the English and Chinese incident-runtime and RAG docs to explain why incident memory is a separately ranked retrieval source, what trust signals affect reuse, and what tradeoffs still remain.
- Expanded the incident-runtime docs to explain why workflow confidence now depends on telemetry quality, how the deterministic confidence ceiling works, and why the repo avoids a second probabilistic uncertainty layer here.

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
- Clarified the product narrative: the project is aimed at reducing operator uncertainty, preserving onset evidence, and making guarded automation usable in AI/GPU operations.
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

- Predictive findings are designed to stay deterministic and audit-friendly; the hot path does not rely on LLM calls.
- Workflow/CI scaffolding now includes explicit SBOM and security-scan hooks for supply-chain verification.
