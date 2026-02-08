# Readiness Checklist (Current Codebase) / 上线检查清单

Use this before shipping or deploying / 上线或部署前使用此清单。

## Chinese Production/Duty Checklist (Recommended) / 中文上线/值班检查清单（推荐）

Execute in sequence before going live / 上线前建议按顺序执行：

### 1. Runtime Status / 运行状态

- [ ] `sre-controller` is listening on `8080/9090` / `sre-controller` 已监听 `8080/9090`
- [ ] `sre-collector` successfully pushes to controller / `sre-collector` 已成功推送到 controller
- [ ] `GET /api/v1/status` returns `v0.2` / `GET /api/v1/status` 返回 `v0.2`

### 2. Data Coverage / 数据覆盖

- [ ] `GET /api/v1/fleet` has at least one collector / `GET /api/v1/fleet` 至少有一个 collector
- [ ] `GET /api/v1/fleet/timeseries` shows curve data points / `GET /api/v1/fleet/timeseries` 可看到曲线点位
- [ ] `GET /api/v1/top/programs` has `resource_pages` / `GET /api/v1/top/programs` 有 `resource_pages`
- [ ] `GET /api/v1/topology` returns `nodes` and `links` / `GET /api/v1/topology` 返回 `nodes` 与 `links`

### 3. RCA Availability / RCA 可用性

- [ ] `SRE_COLLECTOR_LEVEL=5` (recommended for production) / `SRE_COLLECTOR_LEVEL=5`（生产建议）
- [ ] `SRE_COLLECTOR_LOG_PATHS` configured (when log attribution needed) / `SRE_COLLECTOR_LOG_PATHS` 已配置（需要日志归因时）
- [ ] Can drill down from curves to process rankings during anomalies / 异常场景下可从曲线 Drill down 到进程排名

### 4. Operability / 可运维性

- [ ] `/health`, `/healthz` return 200 / `/health`、`/healthz` 返回 200
- [ ] `/metrics` can be scraped by Prometheus / `/metrics` 可被 Prometheus 抓取
- [ ] API Key enabled for external exposure (if applicable) / 对外环境已开启 API Key（如有外部访问）

### 5. Handover Information / 值班交接信息

- [ ] Key collector to hostname mapping recorded / 已记录关键 collector 与 hostname 映射
- [ ] Common troubleshooting commands prepared (status / timeseries / top/programs / topology) / 已准备常用排障命令
- [ ] Upgrade/rollback strategy defined (config rollback, collector level rollback) / 已明确升级回退策略

---

## English Checklist

### 1. Core runtime

- [ ] Controller starts and listens on configured HTTP/gRPC addresses.
- [ ] Collector pushes telemetry to controller (`controller_endpoints` valid).
- [ ] `/api/v1/fleet` returns at least one collector snapshot.

### 2. Data coverage

- [ ] Host metrics from `/proc` are present (CPU, memory, disk, network).
- [ ] Top process samples are present (`processes` in fleet payload).
- [ ] Log fingerprints are present when `log_paths` is configured.
- [ ] GPU metrics appear on NVIDIA hosts with `nvidia-smi` available.

### 3. Process ranking and UI resource pages

- [ ] `/api/v1/top/programs` returns `resource_pages` for categories (`cpu`, `gpu`, `memory`, `network`, `disk`, `disk_io`, `logs`).
- [ ] Per-process ranking entries include current values, totals, and frequency fields.
- [ ] Ranking order is descending by overall usage and then frequency.
- [ ] `Disk` and `Disk I/O` semantics are validated in UI/docs:
  - `Disk`: cumulative footprint/activity totals.
  - `Disk I/O`: live throughput/syscall pressure.

### 4. Collector depth settings

- [ ] Collector level is set appropriately:
  - `level >= 4` for kernel log-related metrics.
  - `level = 5` recommended for full RCA-style per-process attribution.
- [ ] Log sources configured (`SRE_COLLECTOR_LOG_PATHS` or `log_paths`).

### 5. Optional eBPF path

- [ ] eBPF reader enabled only when sidecar/socket is available.
- [ ] Socket path is correct and readable (default `/var/run/sre_collector_ebpf.sock`).
- [ ] Collector still runs cleanly if eBPF source is absent.

### 6. Optional analysis/agent/incidents

- [ ] Analysis endpoints work when `analysis.enabled=true`.
- [ ] Agent endpoints work when `agent.enabled=true`.
- [ ] Incident alert ingestion (`POST /api/v1/incidents/alerts`) works when incidents are enabled.

### 7. Persistence and storage

- [ ] Collector spool path is writable.
- [ ] GPU persistence paths are writable when GPU store enabled.
- [ ] Agent persistence path is writable when agent enabled.

### 8. Operational endpoints

- [ ] Health probes return 200 (`/health`, `/healthz`).
- [ ] Prometheus scrape works (`/metrics`).
- [ ] API auth behavior verified when auth is enabled.

### 9. Documentation accuracy

- [ ] `README.md` quick start works as written.
- [ ] `docs/operations/configuration.md` matches actual flags/envs.
- [ ] `docs/operations/usage.md` matches scripts and runtime flow.
- [ ] `docs/reference/api.md` matches currently registered handlers.

---

## Quick Verification Commands / 快速验证命令

```bash
# Health checks / 健康检查
curl -s http://controller:8080/healthz
curl -s http://controller:8080/health

# Status check / 状态检查
curl -s http://controller:8080/api/v1/status

# Fleet data / 集群数据
curl -s http://controller:8080/api/v1/fleet | jq .

# Process ranking / 进程排名
curl -s http://controller:8080/api/v1/top/programs | jq .

# Timeseries / 时序数据
curl -s "http://controller:8080/api/v1/fleet/timeseries?window=1h" | jq .

# Topology / 拓扑
curl -s http://controller:8080/api/v1/topology | jq .
```
