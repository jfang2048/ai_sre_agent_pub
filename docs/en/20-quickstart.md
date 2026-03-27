# Quickstart (v0.8)

This page is intentionally short. Its purpose is to prove that the stack can boot and that the main control-plane surfaces are reachable.

If you need the full operational guidance, use [21-usage.md](21-usage.md).

## Shortest Local Bring-Up

```bash
cp .env.example .env
make container-build
make container-up
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
```

Open `http://127.0.0.1:8080/`.

## Recommended Production-Like Local Path

The minimal `container-up` path is useful for quick UI/API verification. It is not the best path for validating the primary host-observer model.

For a closer approximation of the intended runtime:

```bash
cp .env.example .env
make container-build
make container-up-host-observer
curl -fsS http://127.0.0.1:8080/api/v1/status | jq '.deployment'
curl -fsS http://127.0.0.1:8080/api/v1/agent/workflow/runs | jq '.count'
```

Why this matters:

- the collector can see host namespaces, kernel interfaces, and eBPF surfaces more faithfully
- the result is closer to the real collector/controller split used in production-like deployments
- the workflow APIs can be checked immediately instead of only the dashboard shell

## What A Good First Boot Looks Like

- `/healthz` succeeds
- `/readyz` succeeds after startup checks complete
- `/api/v1/status` returns controller runtime status
- `/api/v1/rag/status` returns even if RAG is disabled or empty
- `/api/v1/agent/joint-risk` and `/api/v1/agent/rca` return valid JSON once telemetry exists
- `/api/v1/agent/workflow/runs` returns a list surface, even when there are no runs yet

## Stop

```bash
make container-down
make container-down-host-observer
```

## Next Reads

- [21-usage.md](21-usage.md)
- [03-getting-started.md](03-getting-started.md)
- [../zh/03-getting-started.md](../zh/03-getting-started.md)
