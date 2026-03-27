# Testing Guide (v0.8)

This guide is organized by validation layer rather than by language or directory because the main question is usually: what kind of risk did my change introduce?

## Command Matrix

```bash
# Fast local correctness
make fmt-check
make vet
make test

# Focused incident-agent workflow coverage
make test-agent-workflow
make test-agent-replay

# Golden evaluation
make eval-fast

# Broader backend package coverage
make test-all

# Frontend unit tests
cd frontend && npm test -- --watch=false

# Frontend production build
cd frontend && npm run build

# Manifest validation
make validate-manifests

# Container runtime smoke
make container-smoke
```

## What Each Layer Proves

| Layer | What it proves |
| --- | --- |
| `make test` | general code correctness and package regression |
| `make test-agent-workflow` | the incident workflow path, APIs, and controller integration still agree |
| `make test-agent-replay` | replay stability layer still passes |
| `make eval-fast` | golden retrieval and workflow behavior still match expectations |
| `make container-smoke` | supported container bring-up path still works |

## Runtime Sanity Checks

```bash
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:8080/api/v1/status
curl -fsS http://127.0.0.1:8080/api/v1/rag/status
curl -fsS "http://127.0.0.1:8080/api/v1/agent/joint-risk?limit=3"
curl -fsS "http://127.0.0.1:8080/api/v1/agent/rca?limit=3"
curl -fsS "http://127.0.0.1:8080/api/v1/agent/workflow/runs?limit=5"
curl -fsS "http://127.0.0.1:8080/api/v1/agent/workflow/audit?limit=20"
```

## Why v0.8 Needs More Than Health Checks

The repository now contains:

- durable workflow runs
- workflow evidence packages
- change intelligence
- causal graph ranking
- incident-memory write-back
- replay evaluation

That means testing only HTTP `200 OK` is not enough. The validation layer also has to check behavior and stability.
