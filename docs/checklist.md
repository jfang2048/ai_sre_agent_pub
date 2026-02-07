# Readiness Checklist (Current Codebase)

Use this before shipping or deploying.

## 1. Core runtime

- [ ] Controller starts and listens on configured HTTP/gRPC addresses.
- [ ] Collector pushes telemetry to controller (`controller_endpoints` valid).
- [ ] `/api/v1/fleet` returns at least one collector snapshot.

## 2. Data coverage

- [ ] Host metrics from `/proc` are present (CPU, memory, disk, network).
- [ ] Top process samples are present (`processes` in fleet payload).
- [ ] Log fingerprints are present when `log_paths` is configured.
- [ ] GPU metrics appear on NVIDIA hosts with `nvidia-smi` available.

## 3. Process ranking and UI resource pages

- [ ] `/api/v1/top/programs` returns `resource_pages` for categories (`cpu`, `gpu`, `memory`, `network`, `disk`, `disk_io`, `logs`).
- [ ] Per-process ranking entries include current values, totals, and frequency fields.
- [ ] Ranking order is descending by overall usage and then frequency.
- [ ] `Disk` and `Disk I/O` semantics are validated in UI/docs:
  - `Disk`: cumulative footprint/activity totals.
  - `Disk I/O`: live throughput/syscall pressure.

## 4. Collector depth settings

- [ ] Collector level is set appropriately:
  - `level >= 4` for kernel log-related metrics.
  - `level = 5` recommended for full RCA-style per-process attribution.
- [ ] Log sources configured (`SRE_COLLECTOR_LOG_PATHS` or `log_paths`).

## 5. Optional eBPF path

- [ ] eBPF reader enabled only when sidecar/socket is available.
- [ ] Socket path is correct and readable (default `/var/run/sre_collector_ebpf.sock`).
- [ ] Collector still runs cleanly if eBPF source is absent.

## 6. Optional analysis/agent/incidents

- [ ] Analysis endpoints work when `analysis.enabled=true`.
- [ ] Agent endpoints work when `agent.enabled=true`.
- [ ] Incident alert ingestion (`POST /api/v1/incidents/alerts`) works when incidents are enabled.

## 7. Persistence and storage

- [ ] Collector spool path is writable.
- [ ] GPU persistence paths are writable when GPU store enabled.
- [ ] Agent persistence path is writable when agent enabled.

## 8. Operational endpoints

- [ ] Health probes return 200 (`/health`, `/healthz`).
- [ ] Prometheus scrape works (`/metrics`).
- [ ] API auth behavior verified when auth is enabled.

## 9. Documentation accuracy

- [ ] `README.md` quick start works as written.
- [ ] `docs/operations/configuration.md` matches actual flags/envs.
- [ ] `docs/operations/usage.md` matches scripts and runtime flow.
- [ ] `docs/reference/api.md` matches currently registered handlers.
