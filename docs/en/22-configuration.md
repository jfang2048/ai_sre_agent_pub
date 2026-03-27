# Configuration Guide (v0.8)

## Precedence

```mermaid
flowchart LR
    A["image or code defaults"] --> B["config file"]
    B --> C["environment overrides"]
    C --> D["CLI flags"]
```

## 中文说明

- 这份配置文档最关键的问题不是“有哪些字段”，而是“最后到底哪份值会生效”。很多现场问题不是参数不存在，而是改了 YAML 以后又被 env 或 CLI 覆盖。
- 把 image/code defaults、config file、environment、CLI 明确排成一条优先级链，是为了让排障路径可预测。你可以沿着同一顺序往回推，而不是在多个来源之间猜。
- source-mode 和 container-mode 配置分开，也不是为了增加复杂度，而是为了把开发态与容器态的路径、监听地址和数据目录硬性分离，避免一份配置悄悄同时服务两种边界。

## Primary files

- Source-mode controller defaults: `configs/controller.yaml`
- Controller-side target inventory: `configs/controller_targets.yaml`
- Source-mode collector defaults: `configs/collector.yaml`
- Container image defaults: `configs/container/controller.yaml`, `configs/container/controller_targets.yaml`, `configs/container/collector.yaml`
- Container stack env template: `.env.example`

## Deployment-aware config

Both runtimes now understand a small deployment block:

```yaml
deployment:
  mode: "cluster-lite"      # local-dev | standalone | cluster-lite | distributed
  cluster_name: "prod-eu1"
  data_root: "/var/lib/ai-sre-agent"
  external_url: "https://ai-sre-agent.example.com"   # controller only
```

Relevant env overrides:

- controller:
  - `SRE_CONTROLLER_DEPLOYMENT_MODE`
  - `SRE_CONTROLLER_CLUSTER_NAME`
  - `SRE_CONTROLLER_DATA_ROOT`
  - `SRE_CONTROLLER_EXTERNAL_URL`
- collector:
  - `SRE_COLLECTOR_DEPLOYMENT_MODE`
  - `SRE_COLLECTOR_CLUSTER_NAME`
  - `SRE_COLLECTOR_DATA_ROOT`
- shared shorthand:
  - `SRE_DEPLOYMENT_MODE`
  - `SRE_CLUSTER_NAME`
- distributed retrieval auth:
  - `SRE_AGENT_RAG_VECTOR_TOKEN`

LLM-backed anomaly explanation judging for `evalctl` uses the same provider env as the main analysis path:

- `SRE_AGENT_LLM_API_KEY`
- `SRE_AGENT_LLM_PROVIDER`
- `SRE_AGENT_LLM_MODEL`
- `SRE_AGENT_LLM_BASE_URL`

The CLI flags are separate from config on purpose:

- `-judge-llm`
- `-judge-limit`
- `-judge-batch-size`

That keeps the normal evaluation path deterministic by default. You opt into live provider grading only when you explicitly want it.

What those knobs do:

- `mode` changes only built-in default-like paths; it does not override explicit custom paths
- `cluster_name` is propagated into collector labels as `cluster`
- `data_root` is the base used for non-local default path rewrite
- `external_url` is controller-only metadata for deployment docs and status output
- `SRE_AGENT_RAG_VECTOR_TOKEN` is intentionally env-first so Kubernetes and Helm can inject it from a Secret instead of a ConfigMap

## Why there are separate source and container configs

The source-mode configs keep local development predictable: loopback listeners, repo-relative paths, and direct execution assumptions.
The container configs change only what the runtime boundary changes:

- bind addresses move from loopback to `0.0.0.0`
- data paths move from repo-relative directories to mounted paths under `/var/lib/ai-sre-agent/...`
- the controller web path points at the web assets baked into the image
- the controller RAG dataset path points at the dataset copied into the controller image
- the controller target inventory path points at the collector inventory file shipped with or mounted into the image

This split avoids a common operational failure mode where one YAML file tries to serve both host-side development and container deployment, then quietly carries the wrong bind/path defaults into one of them.

## Collector primary path

The collector now has two primary low-level signal paths:

- `probe_core`: primary host/process/runtime telemetry
- `ebpf`: primary syscall/network/file/security/runtime event telemetry

`/proc` and sysfs remain compatibility fallbacks for the host telemetry path only.

中文原因补充:

- 这里强调 `primary`，是为了防止把“系统支持 fallback”误读成“主路径和兼容路径地位相同”。实际上 v0.8 明确偏向 probe-core 和 eBPF。
- `probe_core` 和 `ebpf` 被并列写出，是因为它们负责的是不同信号面: 前者偏 host/process/GPU 采样，后者偏 syscall/network/file/security 事件。
- `/proc` 和 sysfs 被保留，是出于现实环境兼容性考虑，不是因为它们与主路径等价。文档需要把这种退化语义讲清楚，否则数据解释会出问题。

Representative collector config:

```yaml
probe_core:
  enabled: true
  binary_path: "./build/sre-probe-core"   # container image uses /usr/local/bin/sre-probe-core
  args: ["--host-mode", "auto"]
  fallback_to_go: true

ebpf:
  enabled: true
  socket_path: "./data/collector/run/sre_collector_ebpf.sock"
  categories: ["syscall", "process", "network", "file", "security", "resource"]

security:
  enabled: true
  audit_interval: "5m"
  recent_event_limit: 128
  baseline_warmup_samples: 3
  max_walk_entries: 6000
  large_file_threshold_bytes: 104857600
  rapid_growth_threshold_bytes: 16777216
```

Design notes:

- `probe_core.enabled` should remain `true` in normal deployments.
- `probe_core.args=["--host-mode","auto"]` means “prefer kernel-oriented collectors first, then degrade to `/proc` only when necessary.”
- `probe_core.fallback_to_go` enables the compatibility host collector when probe-core cannot start or its IPC stream goes stale. It does not redefine the steady-state architecture.
- `ebpf` is started independently of the compatibility host collector so kernel-event telemetry remains available even when the Go fallback path never activates.
- `ebpf.socket_path` should point at a writable runtime directory. The shipped source config uses `./data/collector/run/...`; the container config uses `/var/lib/ai-sre-agent/collector/data/run/...`.
- If the eBPF runtime cannot start because the environment blocks the Unix socket or required capabilities, the collector now stays up in degraded mode instead of exiting. The degraded reason is emitted in collector runtime metrics.
- `security` is a collector-side audit module. It uses eBPF runtime events as the primary source for runtime/process/network/file security signals, uses probe-core process snapshots for resource correlation, and uses `/proc` plus bounded filesystem walks only for posture checks and compatibility gaps.
- Containerized host-observer deployments usually need additional capabilities/mounts to get the highest-fidelity kernel visibility.

### Collector cadence and adaptive polling

`collection_interval` is the baseline cadence for the main host telemetry push loop. In `v0.8` the default is now `5s`, bounded by:

- `min_collection_interval`: `1s`
- `max_collection_interval`: `20s`

`probe_core.interval` is now a separate knob. Its default is `1s`, which means probe-core can keep higher-resolution host/GPU samples even while the collector only pushes a summarized batch every `5s` in steady state.

The collector no longer speeds up just because the host is idle. Instead:

- it backs off when collector CPU or spool backlog is high, to avoid turning the observer into part of the incident
- it temporarily tightens the interval when multiple pressure signals emerge and the collector still has headroom
- it returns to the configured baseline when the node is calm

That makes the cadence operational rather than cosmetic: more detail when an incident is starting, less self-inflicted pressure when the collector or transport path is stressed.

中文原因补充:

- 把 `collection_interval` 和 `probe_core.interval` 分开，是这次生产化调整里最关键的改动之一。
- 如果两者绑死在一起，要么控制面写入太密、成本和压力过高，要么 GPU/进程级短时尖峰会被稀释掉。
- 现在的默认策略更接近真实企业诉求: steady state 成本可控，incident onset 仍然保留足够分辨率。

The collector now also has an explicit self-protection layer:

- `protection.nice` lowers collector scheduling priority so business workloads win CPU first.
- `protection.max_cpu_percent` and `protection.max_cpu_time_per_interval` bound collector self-cost per cycle.
- `protection.max_drain_records_per_cycle` and `protection.max_drain_duration` prevent backlog replay from consuming the whole sampling window.
- `protection.disable_{logs,security,external}_under_pressure` sheds optional work first.
- `spool_sync_interval` and `spool_offset_sync_interval` trade tiny replay duplication risk for much lower steady-state fsync pressure.
- `suppress_cached_aux_payloads` keeps cadence-cached fallback process lists and log fingerprints out of steady-state batches until the helper really refreshes again.

Hardware discovery is now cached as a long-lived profile instead of being re-scanned in hot loops:

- `hardware.refresh_interval` controls how rarely `/sys` and `/proc` topology discovery is refreshed.
- the cached profile adjusts probe-core sub-collector cadences for large NUMA/hybrid hosts and hosts without GPUs
- the same profile also tunes disk/network/GPU anomaly thresholds so NVMe, HDD, high-speed NIC, and heterogeneous CPU hosts are interpreted differently
- when the legacy Go compatibility path is active, thermal, NIC sysfs, IRQ, and RDMA scans now sit on a separate slow hardware tier instead of sharing the PSI/TCP cadence

### Collector transport security

Collector -> controller gRPC transport supports TLS/mTLS through:

- `transport.tls.enabled`
- `transport.tls.ca_file`
- `transport.tls.cert_file`
- `transport.tls.key_file`
- `transport.tls.server_name`
- `transport.tls.reload_interval`

Production recommendation:

- use mTLS for split deployments by default
- avoid `insecure_skip_verify` except in temporary lab environments
- rotate certs without changing replay semantics; transport security should not change the spool/ack model

### Collector low-level source map

| Signal family | Primary source | Compatibility path | Why the split exists |
|---|---|---|---|
| host CPU/memory/process/runtime | probe-core (`perf`/netlink/sysinfo/PSI/cgroup) | Go host collector via `/proc` and sysfs | snapshots and counters are different from kernel event streams; keep the fast host path in probe-core |
| syscall/network/file/runtime events | dedicated eBPF runtime | synthetic `/proc/net/tcp` assist for a few degraded counters | event streams need a socket/ring-buffer style runtime, not a host metrics poller |
| GPU inventory/utilization/process attribution | probe-core dynamic NVML load | bounded `nvidia-smi` queries inside probe-core | NVML is closer to the driver/runtime boundary and exposes richer PCIe/BAR1/ECC/process state |
| security posture and drift | collector security audit fed by eBPF + probe-core | bounded `/proc` and filesystem posture scans | state drift and runtime behavior are different classes of evidence |

### GPU collection path

- probe-core refreshes GPU metrics on its own cadence (`gpu_interval_samples` inside probe-core).
- The collector does not treat the old Go GPU collector as the steady-state source anymore; the primary path is probe-core.
- Probe-core now attempts dynamic NVML loading first. If the NVIDIA management library is unavailable, it falls back to bounded `nvidia-smi` queries instead of blocking the sampling loop.
- Exported GPU metrics now include device inventory, SM/memory utilization, PCIe link state and throughput, BAR1 memory, ECC counters, and per-process GPU memory/context attribution.
- GPU process names are resolved opportunistically from `/proc/<pid>/comm` only for already-identified GPU-active PIDs. That is a small metadata read, not the main telemetry path.

### Tracing and event correlation path

- The eBPF runtime keeps recent event envelopes, but it also now exports bounded correlation aggregates:
  - category totals/rates/bytes/latency
  - remote endpoint scope totals/rates
  - sensitive-path class totals/rates
  - top per-process category totals/rates
- This is intentional. Raw event replay belongs in event buffers or external pipelines; controller and agent reasoning need bounded summaries that survive ingestion, indexing, and UI rendering without label explosion.

## Controller sections

| Key | Purpose |
|---|---|
| `listen`, `grpc_listen` | HTTP and gRPC bind addresses |
| `ingest` | retention bounds + embedded persistence |
| `tsdb` | optional controller-side InfluxDB durable trend store |
| `analysis` | threshold/stat/ML/correlation/LLM analysis knobs |
| `agent` | query and incident workflow behavior |
| `ha` | active/standby mode |
| `inventory` | collector inventory + heartbeat APIs |
| `kubernetes` | optional K8s inventory/topology |
| `orchestration` | scheduling/policy/status APIs |
| `gpu` | GPU aggregation/timeline/event store |
| `checks` | runtime checks endpoints |

### Controller RAG backend fields

The controller-side `agent` section now supports both local-file and external-vector retrieval config:

```yaml
agent:
  rag_enabled: true
  rag_dataset_path: "/var/lib/ai-sre-agent/controller/dataset"
  rag_index_path: "/var/lib/ai-sre-agent/controller/data/agent/rag/index.json"
  rag_vector_backend: "local"        # local | milvus
  rag_vector_endpoint: ""
  rag_vector_collection: "ai_sre_agent_knowledge"
  rag_vector_database: ""
  rag_vector_token: ""
  rag_vector_timeout: "5s"
```

Matching env overrides:

- `SRE_AGENT_RAG_VECTOR_BACKEND`
- `SRE_AGENT_RAG_VECTOR_ENDPOINT`
- `SRE_AGENT_RAG_VECTOR_COLLECTION`
- `SRE_AGENT_RAG_VECTOR_DATABASE`
- `SRE_AGENT_RAG_VECTOR_TOKEN`
- `SRE_AGENT_RAG_VECTOR_TIMEOUT`

This keeps cluster deployment more realistic:

- `local` works for one controller instance or `cluster-lite`
- `milvus` is the current externalized retrieval backend path for distributed deployments

Recommended cluster split:

- keep `rag_vector_backend`, `rag_vector_endpoint`, `rag_vector_collection`, `rag_vector_database`, and `rag_vector_timeout` in config
- inject `SRE_AGENT_RAG_VECTOR_TOKEN` from a Secret

Reference assets:

- [`../../deploy/charts/sre-agent/templates/controller-rag-secret.yaml`](../../deploy/charts/sre-agent/templates/controller-rag-secret.yaml)
- [`../../deploy/charts/sre-agent/examples/distributed-values.yaml`](../../deploy/charts/sre-agent/examples/distributed-values.yaml)

## Ingest retention and persistence

```yaml
ingest:
  node_retention: "24h"
  history_samples_per_node: 1440
  max_nodes: 5000
  persistence:
    enabled: false
    path: "./data/controller/ingest/store.db"
    sync_interval: "5s"
    compaction_interval: "30m"
    max_db_size_bytes: 536870912
    compact_tx_max_size: 8388608
```

Containerized deployments normally keep the same logic but mount the path inside the controller data volume instead of the repo worktree.

## Controller-side TSDB

```yaml
tsdb:
  enabled: false
  provider: "influxdb"
  url: "http://127.0.0.1:8086"
  org: "ai-sre-agent"
  bucket: "controller_metrics"
  measurement: "telemetry_metric"
  retention: "168h"
  write_batch_size: 512
  write_queue_size: 256
  flush_interval: "2s"
  query_timeout: "5s"
  fallback_to_memory: true
  manage_bucket: false
```

Design notes:

- The collector intentionally has no DB dependency; durable time-series storage lives on the controller side only.
- `ingest` remains the hot cache and latest-state API source.
- `tsdb` is used for longer-window history reads by trend APIs and Agent workflows, with automatic memory fallback when unavailable.
- `SRE_TSDB_TOKEN` stays env-driven; do not commit it to YAML.

## Controller target inventory file

`configs/controller_targets.yaml` is a separate controller-side inventory of known collectors.

It stores:

- collector IDs
- hostnames and addresses
- ports
- labels/tags
- enabled/disabled state
- descriptive auth metadata

In the current push-first design, this file does not replace collector -> controller gRPC delivery. It complements it by giving the controller a stable list of expected collectors even before telemetry arrives.

Important keys:

| Key | Purpose |
|---|---|
| `id` | stable collector identifier used in inventory and policy scope |
| `hostname` / `address` / `port` | operator-maintained endpoint metadata |
| `enabled` | whether the collector should be considered part of the active estate |
| `labels` / `tags` | grouping, filtering, and future policy scoping |
| `auth.*` | descriptive transport/auth expectations for future reverse-dial or management flows |

## Container-first env model

The canonical container stack is env-driven. Copy `.env.example` to `.env` when using `docker compose` locally.

Important variables:

| Env var | Effect |
|---|---|
| `SRE_BIND_HOST` | host interface for published controller ports |
| `SRE_CONTROLLER_HTTP_PORT` | published controller UI/API port |
| `SRE_CONTROLLER_GRPC_PORT` | published controller ingest port |
| `SRE_DOCKER_NETWORK_MODE` | plain-docker fallback mode: `auto`, `bridge`, `host` |
| `REPO_URL` | optional alternate Git source for Docker image builds |
| `REPO_REF` | optional branch/tag/commit fetched from `REPO_URL` |
| `SRE_COLLECTOR_CONTROLLER_ENDPOINTS` | remote controller gRPC target for collector containers |
| `SRE_AGENT_RAG_ENABLED` | enable local-first RAG inside the controller image |
| `SRE_AGENT_RAG_REBUILD_POLICY` | RAG bootstrap behavior on startup |
| `SRE_AGENT_LLM_ENABLED` | enable external LLM calls |
| `SRE_AGENT_DRY_RUN` | keep action generation read-only by default |
| `SRE_CONTROLLER_CONFIG_FILE` | host-side mount path for `docker-run-controller.sh` |
| `SRE_CONTROLLER_TARGETS_MOUNT` | host-side mount path for controller target inventory |
| `SRE_COLLECTOR_CONFIG_FILE` | host-side mount path for `docker-run-collector.sh` |

## Useful environment overrides

| Env var | Effect |
|---|---|
| `SRE_COLLECTOR_COLLECTION_INTERVAL` | baseline collector push cadence (default `5s`) |
| `SRE_COLLECTOR_MIN_COLLECTION_INTERVAL` | adaptive lower bound (default `1s`) |
| `SRE_COLLECTOR_MAX_COLLECTION_INTERVAL` | adaptive upper bound (default `20s`) |
| `SRE_COLLECTOR_SPOOL_SYNC_INTERVAL` | minimum interval between spool file fsync calls |
| `SRE_COLLECTOR_SPOOL_OFFSET_SYNC_INTERVAL` | minimum interval between persisted offset updates |
| `SRE_COLLECTOR_PROBE_CORE_ENABLED` | enable/disable the primary probe-core host telemetry path |
| `SRE_COLLECTOR_PROBE_CORE_BINARY_PATH` | probe-core binary location |
| `SRE_COLLECTOR_PROBE_CORE_COLLECTORS` | explicit probe-core module selection |
| `SRE_COLLECTOR_PROBE_CORE_ARGS` | raw extra probe-core arguments (for example `--host-mode,auto`) |
| `SRE_COLLECTOR_PROBE_CORE_INTERVAL` | internal probe-core sample cadence (default `1s`) |
| `SRE_COLLECTOR_PROBE_CORE_PROCESS_INTERVAL_SAMPLES` | process snapshot cadence multiplier inside probe-core |
| `SRE_COLLECTOR_PROBE_CORE_HOST_PROC_FALLBACK_INTERVAL_SAMPLES` | cadence for slow `/proc` supplement refreshes inside probe-core |
| `SRE_COLLECTOR_PROBE_CORE_PRESSURE_INTERVAL_SAMPLES` | PSI and cgroup refresh cadence multiplier inside probe-core |
| `SRE_COLLECTOR_PROBE_CORE_NETLINK_INTERVAL_SAMPLES` | netlink refresh cadence multiplier inside probe-core |
| `SRE_COLLECTOR_PROBE_CORE_GPU_INTERVAL_SAMPLES` | GPU sample frequency multiplier inside probe-core (default `1`) |
| `SRE_COLLECTOR_PROBE_CORE_NICE` | child probe-core process nice value |
| `SRE_COLLECTOR_PROBE_CORE_FALLBACK_TO_GO` | enable the compatibility Go host collector when probe-core is unavailable |
| `SRE_COLLECTOR_PROTECTION_ENABLED` | enable collector self-protection and load shedding |
| `SRE_COLLECTOR_PROTECTION_NICE` | lower the collector process scheduling priority |
| `SRE_COLLECTOR_PROTECTION_MAX_CPU_PERCENT` | soft CPU budget for collector self-cost |
| `SRE_COLLECTOR_PROTECTION_MAX_CPU_TIME_PER_INTERVAL` | per-cycle CPU time budget for telemetry work |
| `SRE_COLLECTOR_PROTECTION_MEMORY_SOFT_LIMIT_BYTES` | soft RSS limit before the collector starts shedding optional work |
| `SRE_COLLECTOR_PROTECTION_SPOOL_HIGH_WATERMARK_RATIO` | backlog ratio that triggers pressure mode |
| `SRE_COLLECTOR_PROTECTION_SPOOL_CRITICAL_WATERMARK_RATIO` | backlog ratio that triggers critical mode |
| `SRE_COLLECTOR_PROTECTION_MAX_DRAIN_RECORDS_PER_CYCLE` | max replayed batches per collector cycle |
| `SRE_COLLECTOR_PROTECTION_MAX_DRAIN_DURATION` | max time spent draining backlog in one collector cycle |
| `SRE_COLLECTOR_PROTECTION_DISABLE_LOGS_UNDER_PRESSURE` | shed log tailing before core telemetry |
| `SRE_COLLECTOR_PROTECTION_DISABLE_SECURITY_UNDER_PRESSURE` | shed security audit work before core telemetry |
| `SRE_COLLECTOR_PROTECTION_DISABLE_EXTERNAL_UNDER_PRESSURE` | shed external metrics commands before core telemetry |
| `SRE_COLLECTOR_HARDWARE_ENABLED` | enable cached hardware discovery and hardware-aware thresholds |
| `SRE_COLLECTOR_HARDWARE_REFRESH_INTERVAL` | refresh interval for cached topology/device metadata |
| `SRE_COLLECTOR_TLS_ENABLED` | enable TLS/mTLS for collector -> controller gRPC |
| `SRE_COLLECTOR_TLS_CA_FILE` | CA bundle used to verify the controller |
| `SRE_COLLECTOR_TLS_CERT_FILE` | collector client certificate for mTLS |
| `SRE_COLLECTOR_TLS_KEY_FILE` | collector client private key for mTLS |
| `SRE_COLLECTOR_TLS_SERVER_NAME` | server name override for TLS verification |
| `SRE_COLLECTOR_EBPF_SOCKET_PATH` | Unix socket used by the eBPF runtime |
| `SRE_COLLECTOR_EBPF_CATEGORIES` | event categories enabled in the eBPF runtime |
| `SRE_COLLECTOR_SECURITY_ENABLED` | enable collector-side structured security auditing |
| `SRE_COLLECTOR_SECURITY_AUDIT_INTERVAL` | cadence for security posture/drift scans |
| `SRE_COLLECTOR_SECURITY_RECENT_EVENT_LIMIT` | number of recent eBPF security/runtime events folded into findings |
| `SRE_COLLECTOR_SECURITY_BASELINE_WARMUP_SAMPLES` | minimum local observations before drift scoring becomes active |
| `SRE_COLLECTOR_SECURITY_MAX_WALK_ENTRIES` | bound on filesystem posture scans |
| `SRE_COLLECTOR_SECURITY_LARGE_FILE_THRESHOLD_BYTES` | size threshold for large-file findings |
| `SRE_COLLECTOR_SECURITY_RAPID_GROWTH_THRESHOLD_BYTES` | per-audit growth threshold for rapid file growth findings |
| `SRE_INGEST_NODE_RETENTION` | override `ingest.node_retention` |
| `SRE_INGEST_HISTORY_SAMPLES_PER_NODE` | override `ingest.history_samples_per_node` |
| `SRE_INGEST_MAX_NODES` | override `ingest.max_nodes` |
| `SRE_INGEST_PERSIST_ENABLED` | enable embedded persistence |
| `SRE_INGEST_PERSIST_PATH` | persistence file path |
| `SRE_INGEST_PERSIST_SYNC_INTERVAL` | persistence sync interval |
| `SRE_INGEST_PERSIST_COMPACTION_INTERVAL` | persistence compaction interval |
| `SRE_INGEST_PERSIST_MAX_DB_BYTES` | persistence size cap |
| `SRE_TSDB_ENABLED` | enable controller-side TSDB |
| `SRE_TSDB_PROVIDER` | TSDB backend selector (`influxdb`) |
| `SRE_TSDB_URL` | InfluxDB base URL |
| `SRE_TSDB_ORG` | InfluxDB org |
| `SRE_TSDB_BUCKET` | InfluxDB bucket |
| `SRE_TSDB_TOKEN` | InfluxDB token |
| `SRE_TSDB_MEASUREMENT` | measurement name used for durable metric writes |
| `SRE_TSDB_RETENTION` | desired retention window |
| `SRE_TSDB_WRITE_BATCH_SIZE` | async write batch size |
| `SRE_TSDB_WRITE_QUEUE_SIZE` | async in-memory queue size |
| `SRE_TSDB_FLUSH_INTERVAL` | write flush cadence |
| `SRE_TSDB_QUERY_TIMEOUT` | TSDB read/write timeout |
| `SRE_TSDB_FALLBACK_TO_MEMORY` | keep APIs/Agent readable when TSDB is unhealthy |
| `SRE_TSDB_MANAGE_BUCKET` | let controller create bucket when credentials allow |
| `SRE_ANALYSIS_ML_ANOMALY_ENABLED` | ML anomaly path on/off |
| `SRE_ANALYSIS_ML_METHOD` | ML method selector |
| `SRE_ANALYSIS_ML_SEASONAL_PERIOD` | seasonal period |
| `SRE_ANALYSIS_ML_SCORE_THRESHOLD` | anomaly score threshold |
| `SRE_ANALYSIS_CROSS_NODE_CORRELATION` | cross-node correlations on/off |
| `SRE_CONTROLLER_HA_ENABLED` | HA mode enabled |
| `SRE_CONTROLLER_HA_MODE` | `active` / `standby` |
| `SRE_AGENT_K8S_REMEDIATION_ENABLED` | allow controlled pod restart action |

## Deterministic workflow engine overrides

Joint-risk/RCA workflows use runtime env overrides (controller startup + workflow engine init):

| Env var | Effect |
|---|---|
| `SRE_AGENT_WORKFLOW_ENABLED` | enable/disable deterministic workflow engine |
| `SRE_AGENT_WORKFLOW_WINDOW` | default evaluation window (`45m`, `1h`, ...) |
| `SRE_AGENT_WORKFLOW_MAX_WINDOW` | max request window clamp |
| `SRE_AGENT_WORKFLOW_MAX_SAMPLES` | max metric samples consumed per run |
| `SRE_AGENT_WORKFLOW_MAX_SIGNALS` | cap ranked signal count in joint-risk output |
| `SRE_AGENT_WORKFLOW_MAX_HYPOTHESES` | cap ranked hypotheses in RCA output |
| `SRE_AGENT_WORKFLOW_AUDIT_RETENTION` | in-memory workflow audit record retention |
| `SRE_AGENT_WORKFLOW_DRY_RUN` | default dry-run mode for generated actions |
| `SRE_AGENT_WORKFLOW_REQUIRE_APPROVAL` | force approval gate on non-safe execution |
| `SRE_AGENT_WORKFLOW_ALLOW_PROFILING_EXEC` | allow profiling tool to run command |
| `SRE_AGENT_WORKFLOW_PROFILING_COMMAND` | profiling command template |
| `SRE_AGENT_WORKFLOW_INSIGHTS_ENABLED` | enable optional insights interface metadata |
| `SRE_AGENT_WORKFLOW_INSIGHTS_PROVIDER` | insights provider label |
| `SRE_AGENT_WORKFLOW_INSIGHTS_MODEL` | insights model label |
| `SRE_AGENT_WORKFLOW_INSIGHTS_API_KEY_ENV` | env var name to read insights API key from |
| `SRE_AGENT_WORKFLOW_HIGH_RISK_THRESHOLD` | `high` risk threshold |
| `SRE_AGENT_WORKFLOW_MEDIUM_RISK_THRESHOLD` | `medium` risk threshold |
| `SRE_AGENT_WORKFLOW_REQUEST_DEDUPE_TTL` | how long identical joint-risk/RCA refresh requests reuse a recent result |
| `SRE_AGENT_WORKFLOW_REQUEST_DEDUPE_ENTRIES` | bound on in-memory dedupe entries |
| `SRE_AGENT_WORKFLOW_BEHAVIOR_MEMORY_ENABLED` | enable/disable workload behavioral memory |
| `SRE_AGENT_WORKFLOW_BEHAVIOR_MEMORY_LONG_WINDOW` | long-window history span used when comparing the current burst with past behavior |
| `SRE_AGENT_WORKFLOW_BEHAVIOR_MEMORY_MIN_SAMPLES` | minimum learned samples before suppression is allowed |
| `SRE_AGENT_WORKFLOW_BEHAVIOR_MEMORY_MIN_RECURRING_BURSTS` | minimum repeated bursts before a workload is treated as recurring |
| `SRE_AGENT_WORKFLOW_BEHAVIOR_MEMORY_CACHE_ENTRIES` | bound on the short-lived in-memory history cache |
| `SRE_AGENT_WORKFLOW_BEHAVIOR_MEMORY_CACHE_TTL` | max age of cached history results before the workflow asks the metric-history provider again |

Why these knobs exist:

- incident dashboards and API clients often retry or refresh aggressively during live troubleshooting
- without a short dedupe window, identical RCA refreshes can create duplicate reports, incidents, and recommendations
- this is intentionally a bounded cache, not a durable exactly-once ledger
- recurring-burst suppression should be tunable independently of collector cadence because it changes controller-side incident decisions, not host-side evidence capture

Behavioral-memory tuning guidance:

- raise `MIN_SAMPLES` or `MIN_RECURRING_BURSTS` if suppression seems too eager
- lower them only when service labeling is stable and the environment has enough recurring workloads to justify faster learning
- keep `LONG_WINDOW` wide enough to catch the workload cadence you care about; daily jobs usually need several days, not one hour
- keep the cache small; it is only there to avoid repeated reads during one workflow burst, not to become a second history store

## Scheduled agent report controls

The legacy scheduled report engine under [`backend/internal/controller/agent/engine.go`](../../backend/internal/controller/agent/engine.go) now has a small semantic dedupe stage in [`backend/internal/controller/agent/report_dedupe.go`](../../backend/internal/controller/agent/report_dedupe.go). The main YAML knobs are:

```yaml
agent:
  suppress_unchanged_reports: true
  report_refresh_interval: "3m"
  predictive_log_cooldown: "5m"
```

What they do:

- `suppress_unchanged_reports`: keep this enabled for production-like steady state so unchanged scheduled reports refresh the latest in-memory record instead of appending another near-identical copy
- `report_refresh_interval`: upper bound for how long one unchanged report can keep being refreshed in place before the engine must append a fresh record again
- `predictive_log_cooldown`: reduces repeated log spam when the same predictive warning stays true across several scheduled runs

How to observe the result:

- `/api/v1/agent/status` exposes `report_engine.report_suppressed_total`, `report_engine.report_refreshed_total`, and `report_engine.predictive_log_suppressed_total`
- on a stable demo or canary node, rising suppression counters are expected and usually mean the scheduled engine is behaving efficiently rather than going idle

## LLM provider and optional RAG overrides

| Env var | Effect |
|---|---|
| `SRE_AGENT_LLM_PROVIDER` | provider selector (`mock`/`stub`/`local_stub`/`openai`/`anthropic`/`google`/`gemini`/`local`) |
| `SRE_AGENT_LLM_MODEL` | model name |
| `SRE_AGENT_LLM_BASE_URL` | provider base URL override |
| `SRE_AGENT_LLM_API_KEY` | primary API key env |
| `SRE_AGENT_JIMMYNIGHT_API_KEY` | JimmyNight-compatible API key env fallback |
| `SRE_AGENT_EVENT_WEBHOOK_URL` | generic incident/event sink |
| `SRE_AGENT_EVENT_SLACK_WEBHOOK_URL` | Slack sink for workflow/incident events |
| `SRE_AGENT_EVENT_PAGERDUTY_ROUTING_KEY` | PagerDuty routing key for workflow/incident events |
| `SRE_AGENT_LLM_TIMEOUT` | request timeout |
| `SRE_AGENT_LLM_MAX_QUERY_CHARS` | query truncation limit |
| `SRE_AGENT_LLM_MAX_TOKENS` | output token cap |
| `SRE_AGENT_LLM_MAX_RETRIES` | retry count |
| `SRE_AGENT_LLM_RETRY_BASE` | retry base backoff |
| `SRE_AGENT_LLM_RETRY_MAX` | retry max backoff |
| `SRE_AGENT_LLM_RATE_LIMIT_RPS` | request-per-second limiter |
| `SRE_AGENT_LLM_RATE_BURST` | limiter burst |
| `SRE_AGENT_RAG_ENABLED` | enable the local-first dataset-backed RAG service |
| `SRE_AGENT_RAG_DATASET_PATH` | recursive dataset root (default `./dataset`) |
| `SRE_AGENT_RAG_SOURCE_PATHS` | optional comma-separated extra files/directories to ingest |
| `SRE_AGENT_RAG_INDEX_PATH` | persistent index path (default `./data/agent/rag/index.json`) |
| `SRE_AGENT_RAG_TOP_K` | retrieved hit count |
| `SRE_AGENT_RAG_MAX_SNIPPET_CHARS` | snippet truncation length |
| `SRE_AGENT_RAG_MAX_QUERY_CHARS` | cap for the retrieval query constructed from operator text plus filtered findings and anomaly hints |
| `SRE_AGENT_RAG_MAX_FINDINGS` | max combined symptom lines forwarded into one retrieval query after dedupe and boilerplate filtering |
| `SRE_AGENT_RAG_MIN_CONFIDENCE` | suppress prompt injection of retrieved snippets below this confidence |
| `SRE_AGENT_ANALYSIS_REUSE_ENABLED` | enable bounded reuse of one recent successful query-service analysis when compact evidence is unchanged |
| `SRE_AGENT_ANALYSIS_REUSE_WINDOW` | how long the query-service may reuse a recent successful analysis before forcing fresh retrieval/LLM work |
| `SRE_AGENT_ANALYSIS_REUSE_MAX_KEYS` | bound on in-memory recent-analysis fingerprints |
| `SRE_AGENT_RAG_CHUNK_SIZE` | chunk size before indexing |
| `SRE_AGENT_RAG_CHUNK_OVERLAP` | overlap preserved between adjacent chunks |
| `SRE_AGENT_RAG_CHUNK_STRATEGY` | `auto`, `case`, `paragraph`, `markdown`, `line`, or `record` |
| `SRE_AGENT_RAG_RETRIEVAL_MODE` | `hybrid`, `lexical`, or `vector` |
| `SRE_AGENT_RAG_EMBEDDING_PROVIDER` | `local` by default, or an external provider label |

RAG-specific notes:

- `SRE_AGENT_RAG_CHUNK_STRATEGY=auto` now prefers structured `case` chunking when the ingested source already looks like an incident, runbook, or operational Q&A item
- the controller normalizes dataset material into knowledge objects such as incidents, symptoms, likely causes, remediation steps, commands, and signals before indexing
- retrieval requests can also carry intent hints such as `runbook`, `historical_incident`, `joint_risk`, `rca`, or `recommendation`; those are API-level knobs rather than process-level env vars
- the query-service can now skip retrieval entirely when the operator query does not contain meaningful operational keywords and the current deterministic findings/anomaly hints are too weak to justify retrieval cost

## Collector payload and low-overhead overrides

These knobs change how aggressively the collector emits repeated state, especially in steady state.

| Env var | Effect |
|---|---|
| `SRE_COLLECTOR_SUPPRESS_UNCHANGED_LOW_CHURN_METRICS` | enable/disable suppression of unchanged low-churn collector/runtime inventory |
| `SRE_COLLECTOR_LOW_CHURN_METRICS_REFRESH_INTERVAL` | force a periodic full refresh of suppressed collector/runtime inventory |
| `SRE_COLLECTOR_SUPPRESS_CACHED_AUX_PAYLOADS` | omit cache-hit fallback process/log payloads and carry the previous controller view forward instead |
| `SRE_COLLECTOR_SUPPRESS_UNCHANGED_PROCESS_PAYLOADS` | omit near-identical process payloads from steady-state batches |
| `SRE_COLLECTOR_PROCESS_PAYLOAD_REFRESH_INTERVAL` | force a periodic full resend of process payloads even when the coarse process fingerprint is unchanged |
| `SRE_COLLECTOR_SUPPRESS_CACHED_COMPAT_HARDWARE_METRICS` | omit cache-hit slow compatibility hardware payloads and carry the previous controller hardware view forward instead |
| `SRE_COLLECTOR_PROBE_CORE_EMIT_RAW_ALIASED_METRICS` | re-emit raw `probe_core_*` host/resource metrics even when equivalent `node_*` / `rca_*` aliases already exist |

Why these knobs exist:

- without low-churn suppression, the collector keeps resending the same probe-source, runtime-mode, module-selection, and hardware-profile state every cycle
- without process-payload suppression, the same hot-process attribution list is serialized again even when only tiny CPU/RSS/IO drift changed
- without raw-alias suppression, the same host state is often serialized twice under both `probe_core_*` and `node_*`
- these controls reduce spool growth, network cost, and protobuf size without changing the overall collector/controller architecture
