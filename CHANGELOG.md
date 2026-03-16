# Changelog

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

### Documentation

- Updated README opening section for `v0.7`.
- Added predictive architecture and runbook content.
- Added Python runtime reference documentation.
