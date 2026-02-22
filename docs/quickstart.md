# Quickstart

## SMART START (first 5 minutes)

Use this order to get from zero to actionable diagnostics quickly:

1. `S` Start stack.
2. `M` Measure health + ingest.
3. `A` Analyze timeseries shape.
4. `R` Rank top offenders.
5. `T` Trace topology and cross-layer diagnostics.

Command scaffold:

```bash
./scripts/run-local.sh
curl -s http://127.0.0.1:8080/healthz
curl -s http://127.0.0.1:8080/api/v1/status
curl -s http://127.0.0.1:8080/api/v1/ingest/status
curl -s "http://127.0.0.1:8080/api/v1/fleet/timeseries?window=30m&limit=180"
curl -s "http://127.0.0.1:8080/api/v1/top/programs?limit=30"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/data-path"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/kernel-path"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/root-cause"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/workload-path"
```

For the full incident loop (`SMART START`), see `docs/operations/usage.md`.

## 1) Single-node local stack

```bash
./scripts/run-local.sh
```

Validate:

```bash
curl -s http://127.0.0.1:8080/healthz
curl -s http://127.0.0.1:8080/api/v1/status
curl -s http://127.0.0.1:8080/api/v1/fleet
```

## 2) Multi-node local stack (controller + N collectors)

```bash
./scripts/run-local-multinode.sh --collectors 3
```

Validate probe inventory and per-node visibility:

```bash
curl -s http://127.0.0.1:8080/api/v1/inventory/probes
curl -s "http://127.0.0.1:8080/api/v1/top/programs?limit=30"
```

## 3) Optional Kubernetes read-only integration

Enable in `configs/controller.yaml`:

```yaml
kubernetes:
  enabled: true
  refresh_interval: "20s"
  request_timeout: "6s"
  clusters:
    - name: "prod-a"
      kubeconfig: "/path/to/kubeconfig"
      context: "prod-a"
      namespace: "*"
```

Check APIs:

```bash
curl -s http://127.0.0.1:8080/api/v1/k8s/status
curl -s http://127.0.0.1:8080/api/v1/k8s/clusters
curl -s "http://127.0.0.1:8080/api/v1/k8s/workloads/top?metric=pressure&limit=20"
```

## 4) CI-style validation

```bash
./scripts/ci.sh
```

Quick screenshot tooling sanity checks:

```bash
make capture-keys
make test-screenshot-tools
CAPTURE_ONLY=dashboard_live,trends_nic CAPTURE_BREAKDOWN_VARIANTS=nic node scripts/capture_ui_screenshots.mjs --print-plan
CAPTURE_STRICT=1 CAPTURE_ONLY=dashboard_live,trends_live node scripts/capture_ui_screenshots.mjs --print-plan
```

`CAPTURE_STRICT=1` returns exit code `2` for invalid capture inputs.

## 5) Optional C++ probe-core path

```bash
make build-probe-core
SRE_COLLECTOR_PROBE_CORE_ENABLED=1 ./scripts/run-local.sh
```

Unix-style minimal module mode:

```bash
SRE_COLLECTOR_PROBE_CORE_ENABLED=1 \
SRE_COLLECTOR_PROBE_CORE_COLLECTORS=host,network,rdma,process \
./scripts/run-local.sh
```

Use all modules:

```bash
SRE_COLLECTOR_PROBE_CORE_COLLECTORS=all
```

List available probe-core modules:

```bash
./build/sre-probe-core --list-collectors
```

Validate probe-core ingestion health:

```bash
curl -s http://127.0.0.1:8080/metrics | grep -E "collector_probe_source|collector_probe_core_(active|client_available|fresh|last_frame_age_seconds)" | head -n 20
curl -s http://127.0.0.1:8080/metrics | grep -E "collector_probe_core_collector_module_(requested|active)" | head -n 30
curl -s http://127.0.0.1:8080/metrics | grep -E "probe_core_collector_module_enabled|node_rdma_" | head -n 20
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/data-path"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/kernel-path"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/root-cause"
curl -s "http://127.0.0.1:8080/api/v1/diagnostics/workload-path"
```

`collector_probe_core_collector_selection_valid=0` indicates malformed or unsupported `--collectors` selection.
