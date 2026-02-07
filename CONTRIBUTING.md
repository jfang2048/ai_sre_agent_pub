# Contributing to AI SRE Agent

This project is a push-first Linux observability system (`sre-collector` + `sre-controller`).

## Repository structure

| Path | Purpose |
|---|---|
| `backend/cmd/collector` | Collector entrypoint (`sre-collector`) |
| `backend/cmd/controller` | Controller entrypoint (`sre-controller`) |
| `backend/internal/collector` | Push-first collector runtime (spool, transport, config) |
| `backend/internal/probe` | Kernel/proc/gpu/eBPF collection primitives |
| `backend/internal/controller` | Ingest store, APIs, ranking, analysis, GPU aggregation |
| `frontend/` | Optional React dashboard source |
| `web/` | Built/static web assets served by controller |
| `configs/` | Default YAML configuration |
| `deploy/` | Docker and Kubernetes manifests |
| `docs/` | Architecture, operations, and reference docs |

## Prerequisites

- Go `1.25+`
- Linux recommended for full collector behavior
- Node.js only if you are changing frontend assets

## Local development

```bash
make build
make ci
```

Optional checks:

```bash
make test-all
make test-race
```

## Frontend (optional)

```bash
npm -C frontend install
npm -C frontend run dev
```

## Engineering expectations

- Keep changes small, testable, and production-oriented.
- Preserve push-first semantics unless explicitly changing architecture.
- For behavior changes, update matching docs under `docs/` in the same PR.
- Prefer deterministic analysis paths first; gate expensive/optional features.

## Pull request checklist

1. Code compiles (`make build`).
2. Baseline checks pass (`make ci`).
3. Relevant docs are updated and accurate.
4. For observability/ranking/API changes, run through `docs/checklist.md`.

## Versioning note

Current release line is `v0.1`.

## License

By contributing, you agree your work is licensed under `GPL-3.0`.
