# Contributing to AI SRE Agent (v0.5)

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

## License

By contributing, you agree contributions are licensed under `GPL-3.0`.
