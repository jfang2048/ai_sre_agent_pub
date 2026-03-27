# Usage Guide (v0.8)

This document is operational. It explains how to run the current system and how to validate the main controller surfaces after startup.

For architectural rationale, start from [../../README.md](../../README.md).

## Recommended Single-Node Path

```bash
cp .env.example .env
make container-build
make container-up-host-observer
```

Why this is the recommended path:

- it is closer to the intended host-observer model
- the collector sees more realistic namespace and kernel surfaces
- workflow APIs can be exercised against a more faithful evidence path

## Minimal Convenience Path

```bash
cp .env.example .env
make container-build
make container-up
```

Use this when you need the shortest demo/UI loop rather than the most faithful collector path.

## First Validation Checklist

```bash
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:8080/api/v1/status | jq '.deployment'
curl -fsS http://127.0.0.1:8080/api/v1/rag/status
curl -fsS "http://127.0.0.1:8080/api/v1/agent/joint-risk?limit=3"
curl -fsS "http://127.0.0.1:8080/api/v1/agent/rca?limit=3"
curl -fsS "http://127.0.0.1:8080/api/v1/agent/workflow/runs?limit=5"
```

## Workflow Runtime Validation

The incident runtime is now a first-class operator surface. After startup, validate:

- the workflow list API responds
- audit records can be queried
- RCA and joint-risk endpoints return structured JSON

Useful paths:

- `/api/v1/agent/workflow/runs`
- `/api/v1/agent/workflow/audit`
- `/api/v1/agent/rca`
- `/api/v1/agent/joint-risk`

## Stop

```bash
make container-down
make container-down-host-observer
```

## Deeper Reads

- [17-incident-agent-runtime.md](17-incident-agent-runtime.md)
- [24-api-reference.md](24-api-reference.md)
- [23-testing.md](23-testing.md)
