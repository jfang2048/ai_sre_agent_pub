# Getting Started

中文版本：[docs/zh/03-getting-started.md](../zh/03-getting-started.md)

This page is the shortest practical path to a working local stack.

## Prerequisites

- Docker with `docker compose`
- GNU `make`
- `curl`

If you want to build from source instead of containers, you will also need the Go and C++ toolchains described by the build targets in the [`Makefile`](../../Makefile).

## Recommended Local Path

Use the container-first host-observer path:

```bash
cp .env.example .env
make container-build
make container-up-host-observer
```

Verify the controller:

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/readyz
curl -sS http://127.0.0.1:8080/api/v1/status
curl -sS http://127.0.0.1:8080/api/v1/rag/status
curl -sS http://127.0.0.1:8080/api/v1/fleet
```

Open the UI:

```text
http://127.0.0.1:8080/
```

Stop the stack:

```bash
make container-down-host-observer
```

Why this path is recommended:

- it matches the maintained container workflow
- it is closer to the collector's real host-observer assumptions
- it exercises the controller, collector, UI, and gRPC ingest path together

## What Success Looks Like

One realistic first-pass success case is:

- `healthz` responds immediately
- `readyz` responds once controller startup checks are satisfied
- `/api/v1/status` returns controller runtime metadata
- `/api/v1/status.deployment.mode` tells you which deployment shape is active
- `/api/v1/fleet` returns at least one collector after the host-observer stack has settled
- the UI loads and shows a dashboard shell

Illustrative checks:

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/readyz
curl -sS http://127.0.0.1:8080/api/v1/status | jq .
curl -sS http://127.0.0.1:8080/api/v1/status | jq '.deployment'
curl -sS http://127.0.0.1:8080/api/v1/fleet | jq '.count, .nodes[0].collector_id'
```

Example interpretation:

- if `healthz` fails, debug controller startup first
- if `healthz` works but `/api/v1/fleet` is empty, debug collector startup or ingest connectivity
- if the UI loads but RAG is disabled, the stack is still valid for telemetry and API verification
- if the UI loads and RAG is enabled, but later operator questions still show no retrieval context, that may be intentional: generic low-signal questions now increment `agent_rag_skipped_context_total` instead of forcing low-value retrieval

## Example Verification Checklist

After startup, a healthy first-pass check looks like this:

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/readyz
curl -sS http://127.0.0.1:8080/api/v1/status
curl -sS http://127.0.0.1:8080/api/v1/rag/status
```

How to read the result:

- `healthz` proves the controller HTTP process is alive
- `readyz` proves the controller completed startup checks for the current mode
- `/api/v1/status` tells you whether the controller believes its core subsystems are up
- `/api/v1/rag/status` tells you whether the local knowledge index is enabled and loaded

If the UI loads but `/api/v1/rag/status` reports disabled, the stack is still usable; you just will not get retrieval-backed context until RAG is configured or rebuilt.

## A Small First API Tour

After the stack is up, these endpoints give a good first feel for the system:

```bash
curl -sS http://127.0.0.1:8080/api/v1/status
curl -sS http://127.0.0.1:8080/api/v1/fleet
curl -sS "http://127.0.0.1:8080/api/v1/fleet/timeseries?window=30m&limit=20"
curl -sS http://127.0.0.1:8080/api/v1/rag/status
```

What each one tells you:

- `/api/v1/status`: controller process and major subsystem state
- `/api/v1/fleet`: which collectors are currently represented in hot state
- `/api/v1/fleet/timeseries`: whether metric history is being recorded and exposed
- `/api/v1/rag/status`: whether retrieval is enabled, ready, and what index path it is using

## Quick Cluster-Lite Path

If your environment already has `kubectl`, the shortest maintained cluster path is the raw manifest set in [`../../deploy/k8s/push-first/`](../../deploy/k8s/push-first/):

```bash
kubectl apply -k deploy/k8s/push-first
kubectl -n sre-agent port-forward deploy/sre-controller 8080:8080
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:8080/api/v1/status | jq '.deployment'
```

What this path assumes:

- one central controller `Deployment`
- one collector pod per node via `DaemonSet`
- collector identity comes from Kubernetes `spec.nodeName` through `SRE_COLLECTOR_ID` and `SRE_COLLECTOR_HOSTNAME`
- local-file RAG is acceptable for the first rollout

If you need HA, ingress, or an external vector backend, continue with [deployment.md](15-deployment.md).

## Source-Mode Alternative

If you want to run from the local checkout without Docker:

```bash
make build
./scripts/run-local.sh --enable-agent
```

For a controller-only demo with seeded data:

```bash
./scripts/run-local.sh --enable-agent --demo --llm=stub
```

Example use case for this mode:

- you want to inspect the controller, UI, and agent surfaces without relying on a live collector
- you want deterministic stub LLM behavior instead of a real provider
- you want to validate prompt and workflow integration paths locally

## UI Validation And Screenshot Refresh

If you changed the investigation UI or want to refresh the README and UI-guide screenshots, the repo now has one documented source-mode path:

1. Start the local demo controller with seeded data and stub LLM output:

```bash
./scripts/run-local.sh --enable-agent --demo --llm=stub
```

2. Start a headless Chrome instance with DevTools enabled:

```bash
google-chrome-stable --headless=new --disable-gpu --remote-debugging-port=9224 --no-sandbox about:blank
```

3. Run the screenshot capture script with explicit warmup and stabilization waits:

```bash
CAPTURE_WARMUP_MS=15000 CAPTURE_LIVE_WAIT_MS=30000 CAPTURE_STABILIZE_MS=12000 UI_URL=http://127.0.0.1:8080 node scripts/capture_readme_screenshots.mjs
```

Why these waits exist:

- the controller demo needs time to expose seeded investigation data
- the UI needs time to finish client-side fetches and render charts
- the script now waits after the page is already ready so screenshots show stable evidence, not shell placeholders

Use [ui-guide.md](08-ui-guide.md) to check which screenshots are expected and which pages they represent.

## Quick Low-Overhead Validation

After the stack is up, check that the collector is behaving like a bounded telemetry agent rather than a noisy demo collector:

```bash
curl -sS http://127.0.0.1:8080/metrics | grep -E 'collector_metrics_suppressed_count|collector_aux_payload_suppressed|collector_compat_payload_suppressed|agent_rag_skipped_context_total|agent_llm_bypassed'
curl -sS http://127.0.0.1:8080/api/v1/agent/status | jq '.query_service.analysis_reuse_enabled, .query_service.metrics.analysis_reused_total'
curl -sS http://127.0.0.1:8080/api/v1/agent/status | jq '.control_plane.triggered_trends, .control_plane.investigation_events, .control_plane.retrieval_skipped'
curl -sS http://127.0.0.1:8080/api/v1/agent/status | jq '.report_engine.report_suppressed_total, .report_engine.predictive_log_suppressed_total'
```

What you want to see:

- `collector_metrics_suppressed_count` is often above `0` during calm periods
- `collector_aux_payload_suppressed` appears on cache-hit log/process helper cycles
- `collector_compat_payload_suppressed{component="hardware"}` appears if fallback hardware scans are active and unchanged
- `agent_rag_skipped_context_total` can rise when you ask generic questions like `"what is happening here"` without clear operational symptoms
- `analysis_reused_total` rises only when the same compact evidence really repeats
- `control_plane.triggered_trends` and `control_plane.investigation_events` become non-zero once the workflow engine has already built eventized evidence
- `report_engine.report_suppressed_total` rises on stable demo or canary nodes when the legacy report engine is refreshing the latest report in place instead of appending another near-identical copy

## Common First Problems

| Symptom | Likely meaning | Where to look next |
| --- | --- | --- |
| `healthz` fails | controller did not start cleanly | [Deployment](15-deployment.md), [`configs/controller.yaml`](../../configs/controller.yaml) |
| controller is up but `/api/v1/fleet` is empty | collector did not reach ingest or has not produced usable data yet | [`configs/container/collector.yaml`](../../configs/container/collector.yaml), [Data flow](05-data-flow.md) |
| RAG status is disabled | normal unless you explicitly enabled RAG | [Dataset and RAG](11-dataset-and-rag.md) |
| UI loads but answers are deterministic only | normal when `llm_enabled: false` or telemetry is stale | [Prompts and customization](12-prompts-and-customization.md), [FAQ](16-faq.md) |

## Key Files

- [`configs/controller.yaml`](../../configs/controller.yaml)
- [`configs/collector.yaml`](../../configs/collector.yaml)
- [`configs/container/controller.yaml`](../../configs/container/controller.yaml)
- [`configs/container/collector.yaml`](../../configs/container/collector.yaml)
- [`deploy/k8s/push-first/`](../../deploy/k8s/push-first/)
- [`deploy/charts/sre-agent/`](../../deploy/charts/sre-agent/)
- [`.env.example`](../../.env.example)

## Next Steps

- If you want the deployment variants, read [Deployment](15-deployment.md).
- If you want to understand the RAG data path, read [Dataset and RAG](11-dataset-and-rag.md).
- If you want to change prompt behavior, read [Prompts and customization](12-prompts-and-customization.md).
- If you want to understand or refresh the investigation console screenshots, read [UI guide](08-ui-guide.md).
- For a fuller operator runbook, use [operations/usage.md](../operations/usage.md).
