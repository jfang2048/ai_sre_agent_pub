# Contributing to AI SRE Agent

## Working rules

- Keep changes narrow.
- Update tests and docs in the same PR.
- Prefer deletion over new abstraction.
- Do not add dependencies unless there is no cheaper fix.
- Keep the collector/controller split explicit.

The main code entrypoints reviewers usually need are:

- `backend/cmd/collector/main.go`
- `backend/cmd/controller/main.go`
- `backend/internal/collector/collector.go`
- `backend/internal/controller/controller.go`
- `backend/internal/controller/agentcore/`
- `cpp/probe_core/`

## Branches and review

Use a short-lived branch name such as `feat/*`, `fix/*`, `docs/*`, or `chore/*`.

A good PR explains three things:

1. the failure mode or gap being addressed
2. the smallest code path that changed
3. the checks that prove the change

## Required checks

Run the narrowest useful checks first, then the broader ones that match the change.

```bash
make fmt-check
make vet
make test
make verify-version
```

When relevant, also run:

```bash
make predictive-test
make helm-smoke
make sbom
make test-stability
make test-ui
make security-audit
make build-probe-core
npm --prefix frontend run build
```

Package-level changes should be checked directly before the full suite. UI work should include the targeted Playwright or Vitest coverage that actually touches the route or component.

## What to call out in review

- **Collector changes**: privilege footprint, replay behavior, spool growth, host resource cost.
- **Ingest and storage changes**: ordering, replay, idempotency, durability, failure recovery.
- **Workflow changes**: handoff schema, validation path, approval path, rollback path.
- **Deployment changes**: restart behavior, shared storage, TLS, failover, upgrade path.
- **Performance changes**: CPU, memory, queue pressure, payload size, and hot-path cost.

## Documentation sync

Keep docs simple. Update `README.md` and, when the user-facing text changes, `README.zh-CN.md`. Keep detailed API, workflow, and security truth in code, config, and tests instead of adding new Markdown pages.

For documentation-only changes, run:

```bash
npx --yes markdownlint-cli2@0.20.0 "**/*.md"
make verify-readme-screenshots
```

Do not auto-format `eval_data/knowledge/cases/`. Those Markdown files are
retrieval fixtures, so whitespace changes can alter chunking and evaluation
results.

## License

Contributions are accepted under GPL-3.0.
