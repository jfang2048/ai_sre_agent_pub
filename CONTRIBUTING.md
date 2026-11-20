# Contributing to AI SRE Agent

## Working rules

- Keep changes narrow.
- Update tests and docs in the same PR.
- Prefer deletion over new abstraction.
- Do not add dependencies unless there is no cheaper fix.
- Keep the collector/controller split explicit.

## Repository layout

| Path | Responsibility |
| --- | --- |
| [backend/cmd/](backend/cmd/) | Collector and controller process entry points |
| [backend/internal/collector/](backend/internal/collector/) and [cpp/probe_core/](cpp/probe_core/) | Host sampling, buffering, and telemetry delivery |
| [backend/internal/controller/](backend/internal/controller/) | Ingest, HTTP APIs, policy, and investigation workflows |
| [frontend/](frontend/) | Operator console and frontend tests |
| [configs/](configs/) and [deploy/](deploy/) | Runtime configuration and deployments |
| [tests/](tests/README.md) and [eval_data/](eval_data/README.md) | Integration checks, evaluation cases, and fixtures |

## Build locally

Use the [README prerequisites](README.md#quick-start), then run the build from the repository root.

```bash
npm -C frontend ci
make build
npm -C frontend run build
```

The backend binaries go to `build/` and the web build goes to `web/`. Use `make run-both` to launch the local stack.

## Branches and review

Use a short-lived branch name such as `feat/*`, `fix/*`, `docs/*`, or `chore/*`.

A good PR explains three things:

1. the failure mode or gap being addressed
2. the smallest code path that changed
3. the checks that prove the change

## Checks for code changes

Run the narrowest useful checks first, then the broader ones that match the change.

```bash
make fmt-check
make vet
make test-all
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

Use `go -C backend test <package>` for a focused Go check and `npm -C frontend test` for frontend unit tests. The [test guide](tests/README.md) describes integration, browser, and stability checks. [CI workflow](.github/workflows/ci.yml)

## What to call out in review

- **Collector changes**: privilege footprint, replay behavior, spool growth, host resource cost.
- **Ingest and storage changes**: ordering, replay, idempotency, durability, failure recovery.
- **Workflow changes**: handoff schema, validation path, approval path, rollback path.
- **Deployment changes**: restart behavior, shared storage, TLS, failover, upgrade path.
- **Performance changes**: CPU, memory, queue pressure, payload size, and hot-path cost.

## Repository and release checks

Run each check independently on a workstation or in CI.

| Command | Checks |
| --- | --- |
| `make public-repo-audit` | Public content, commit identities and historical paths |
| `make test-publish-privacy` | Tracked-file selection and rejection of credential-shaped input |
| `make test-dataset-fetch` | Line-oriented HTTPS dataset acquisition using local test fixtures |

The publisher's default input is reviewed, tracked source. Generated runtime state and optional corpora live outside Git. Machine-readable results go to stdout and diagnostics go to stderr.

## Documentation sync

Keep the English and Chinese homepages aligned on product capabilities and navigation. Update the guide that owns the changed workflow or interface. Link to that guide from the homepage when readers need a new entry point. Use code, config and tests to verify behavior and examples.

For documentation changes, check the published entry points and handbook. Add other edited Markdown paths to the command as needed.

```bash
npx --yes markdownlint-cli2@0.20.0 --no-globs 'README*.md' CONTRIBUTING.md CHANGELOG.md 'docs/*.md'
make verify-readme-screenshots
```

Do not auto-format `eval_data/knowledge/cases/`. Those Markdown files are
retrieval fixtures, so whitespace changes can alter chunking and evaluation
results.

## License

Contributions are accepted under GPL-3.0.
