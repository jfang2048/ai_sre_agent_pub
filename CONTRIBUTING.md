# Contributing to AI SRE Agent (v0.6)

## Workflow

1. Create a branch (`feat/*`, `fix/*`, `docs/*`, `chore/*`).
2. Implement the change.
3. Update tests and docs in the same PR.
4. Run required checks.
5. Open a PR with results.

## Required checks

```bash
make fmt-check
make vet
make test
```

Run broader checks when relevant:

```bash
make test-stability
make test-ui
make security-audit
```

For controller, ingest, collector transport, or agent workflow changes, also run the narrowest package tests you touched directly before opening the PR.

## Pull request expectations

- Keep PRs scoped to one reliability or product concern.
- Include the failure mode being addressed and the verification steps you ran.
- For reloadable config, degraded-mode, or HA changes, describe restart-required vs hot-reloadable behavior explicitly.
- For UI changes, include at least one updated test or explain why the existing UI coverage is sufficient.

## Review expectations

- Collector privilege changes need a capability/namespace impact note.
- Ingest/storage changes need replay, fallback, or durability reasoning.
- Agent/tool/action changes need guardrail and approval-path coverage.
- HA or deployment changes need upgrade/failover behavior called out.

## Code and architecture rules

- Keep module boundaries explicit.
- Keep collector and controller responsibilities separate.
- Preserve ingest hot-path independence from optional modules.
- Use wrapped errors (`fmt.Errorf("...: %w", err)`).
- Add focused tests for behavior changes.

## Documentation sync rules

- API changes: update `docs/reference/api.md`.
- Metric changes: update `docs/reference/metrics.md`.
- Security behavior changes: update `SECURITY.md` and `docs/security/threat-model.md`.
- Deployment behavior changes: update `README.md` and `deploy/k8s/push-first/README.md` when manifests or chart values change.

## License

By contributing, you agree contributions are licensed under `GPL-3.0`.
