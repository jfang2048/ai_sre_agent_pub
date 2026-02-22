# Configuration / 配置指南

This guide documents the current runtime configuration behavior in code.

本文档记录当前代码中的运行时配置行为。

## Version baseline / 版本基线

Current release line: `v0.2` / 当前发布版本：`v0.2`

## Chinese Quick Reference (For Production Duty) / 中文速查（面向生产值班）

This section helps Chinese SREs quickly identify "which parameter to change and why" / 本节用于中文 SRE 快速定位"该改哪个参数、为什么要改"。

### Controller Key Configurations / Controller 关键配置

| Goal / 目标 | Recommended Config / 推荐配置 | Description / 说明 |
|---|---|---|
| API/UI external access / API/UI 对外访问 | `listen: 0.0.0.0:8080` | Provides HTTP API, UI, health checks / 提供 HTTP API、UI、健康检查 |
| Telemetry ingest / 采集接入 | `grpc_listen: 0.0.0.0:9090` | Collector push data entry point / collector push 数据入口 |
| Static assets / 静态资源 | `web.path: ./web` (or absolute path) / （或绝对路径） | Check this path first if UI is blank / UI 空白时优先检查该路径 |
| Log level / 日志级别 | `log_level: info` | Use `info` in production, temporarily switch to `debug` for troubleshooting / 生产建议 `info`，问题排查短时切 `debug` |
| Authentication / 认证 | `auth.enabled: true` + `SRE_AGENT_CONTROLLER_API_KEY` | Recommended when exposed externally / 对外暴露时建议开启 |

### Collector Key Configurations / Collector 关键配置

| Goal / 目标 | Recommended Config / 推荐配置 | Description / 说明 |
|---|---|---|
| Full RCA attribution / 完整 RCA 归因 | `level: 5` / `SRE_COLLECTOR_LEVEL=5` | More complete process-level CPU/memory/network/IO attribution / 进程级 CPU/内存/网络/IO 归因更完整 |
| Log ranking available / 日志排名可用 | `log_paths` or / 或 `SRE_COLLECTOR_LOG_PATHS` | Without this, logs ranking will be empty / 未配置会导致 logs 排名为空 |
| Native low-overhead path / 原生低开销路径 | `probe_core.enabled: true` + `SRE_COLLECTOR_PROBE_CORE_BINARY_PATH` | Use C++ probe-core for critical-path kernel/device telemetry / 使用 C++ probe-core 作为关键路径采集 |
| Reduce overhead / 降低开销 | `level: 2` and disable eBPF / 且关闭 eBPF | Suitable for resource-constrained environments / 适合资源紧张环境 |
| Ensure transport stability / 保障传输稳定 | Configure `spool_dir` + reasonable `spool_max_bytes` / 配置 `spool_dir` + 合理 `spool_max_bytes` | Avoid data loss during network fluctuations / 网络抖动下避免数据丢失 |
| Multi-controller strategy / 多 controller 策略 | `controller_endpoints` + `mirror_send` | Parallel send when `mirror_send=true` / `mirror_send=true` 时并行发送 |

### Chinese Recommended Presets (Copy-Ready) / 中文推荐预设（可直接复制）

**Production high-quality attribution (recommended) / 生产高质量归因（推荐）：**

```bash
SRE_COLLECTOR_LEVEL=5
SRE_COLLECTOR_LOG_PATHS=/var/log/syslog,/var/log/messages
SRE_COLLECTOR_EBPF_ENABLED=1
SRE_COLLECTOR_EBPF_SOCKET_PATH=/var/run/sre_collector_ebpf.sock
```

**Low-overhead collection (cost-priority) / 低开销采集（成本优先）：**

```bash
SRE_COLLECTOR_LEVEL=2
SRE_COLLECTOR_LOG_PATHS=
SRE_COLLECTOR_EBPF_ENABLED=0
```

### Chinese Common Misconfigurations and Symptoms / 中文常见误配与症状

| Symptom / 症状 | Likely Cause / 高概率原因 | Fix / 修复建议 |
|---|---|---|
| `/api/v1/fleet` is empty / `/api/v1/fleet` 为空 | Collector not connected to gRPC / collector 未连上 gRPC | Check `controller_endpoints`, port 9090 connectivity / 检查 `controller_endpoints`、9090 连通性 |
| Curves show anomalies but process ranking empty / 曲线有异常但进程排名空 | `SRE_COLLECTOR_LEVEL` too low / `SRE_COLLECTOR_LEVEL` 太低 | Increase to `5` / 提升到 `5` |
| Logs category has no data / logs 分类无数据 | `log_paths` not configured / 未配置 `log_paths` | Set `SRE_COLLECTOR_LOG_PATHS` and verify file readability / 设置 `SRE_COLLECTOR_LOG_PATHS` 并确认文件可读 |
| UI opens blank / UI 打开空白 | `web.path` incorrect or not built / `web.path` 错误或未构建 | Rebuild frontend and verify path / 重新构建 frontend 并校验路径 |
| GPU page has no data / GPU 页面无数据 | `nvidia-smi` unavailable / `nvidia-smi` 不可用 | Check driver, container permissions, device mount / 检查驱动、容器权限、设备挂载 |

---

## Configuration precedence / 配置优先级

### Controller / 控制器

1. Built-in defaults (`DefaultConfig`) / 内置默认值
2. YAML file (`--config`, or `SRE_CONTROLLER_CONFIG`, or `./configs/controller.yaml`, then `/etc/sre-controller/config.yaml`) / YAML 文件
3. CLI flag overrides (`--listen`, `--grpc-listen`, `--nodes`, `--web-path`) / CLI 标志覆盖
4. Env overrides (`SRE_CONTROLLER_HTTP_LISTEN`, `SRE_CONTROLLER_GRPC_LISTEN`, `SRE_CONTROLLER_WEB_PATH`, selected `SRE_AGENT_*`) / 环境变量覆盖

### Collector / 采集器

1. Built-in defaults (`DefaultConfig`) / 内置默认值
2. YAML file (`--config`, or `SRE_COLLECTOR_CONFIG`, or `./configs/collector.yaml`, then `/etc/sre-collector/config.yaml`) / YAML 文件
3. Env overrides (`SRE_COLLECTOR_*`) / 环境变量覆盖

## Controller (`sre-controller`)

### CLI flags / CLI 标志

| Flag | Default | Description / 说明 |
|---|---|---|
| `--config`, `-c` | empty / 空 | Config file path / 配置文件路径 |
| `--listen`, `-l` | `:8080` | HTTP listen address / HTTP 监听地址 |
| `--grpc-listen` | `:9090` | gRPC ingest listen address / gRPC 采集监听地址 |
| `--port-file` | empty / 空 | Write resolved listen addresses to JSON file / 将解析的监听地址写入 JSON 文件 |
| `--nodes`, `-n` | empty / 空 | Comma-separated pull-mode node addresses / 逗号分隔的拉取模式节点地址 |
| `--log-level` | `info` | Log level / 日志级别 |
| `--log-format` | `json` | `json` or `text` |
| `--web-path` | `./web` | Static web assets path / 静态 Web 资源路径 |
| `--version`, `-v` | `false` | Print version and exit / 打印版本并退出 |

### Main YAML sections (`configs/controller.yaml`) / 主要 YAML 章节

- `listen`, `grpc_listen`, `log_level`
- `scrape.interval`, `scrape.timeout`
- `nodes`
- `web.path`
- `analysis.*`
- `orchestration.*`
- `inventory.*`
- `kubernetes.*`
- `agent.*`
- `incidents.*`
- `gpu.*`
- `checks.*`
- `auth.*` (optional / 可选)

### Kubernetes integration YAML (`kubernetes.*`) / Kubernetes 集成 YAML

| Key | Description / 说明 |
|---|---|
| `enabled` | Enable read-only multi-cluster discovery APIs / 启用只读多集群发现 API |
| `refresh_interval` | Background snapshot interval / 后台快照间隔 |
| `request_timeout` | Timeout per Kubernetes API call / 每次 Kubernetes API 调用超时 |
| `max_pods_per_cluster` | Safety cap for pod list size / Pod 列表大小安全上限 |
| `clusters[].name` | Logical cluster name / 逻辑集群名称 |
| `clusters[].in_cluster` | Use in-cluster service account auth / 使用集群内服务账号认证 |
| `clusters[].kubeconfig` | Kubeconfig path for out-of-cluster access / 集群外访问 kubeconfig 路径 |
| `clusters[].context` | Kubeconfig context / kubeconfig context |
| `clusters[].namespace` | Namespace scope (`*` for all) / 命名空间范围（`*` 表示全部） |
| `clusters[].label_selector` | Pod label selector filter / Pod 标签选择器过滤 |

### Inventory YAML (`inventory.*`) / Inventory YAML

| Key | Description / 说明 |
|---|---|
| `enabled` | Enable merged probe inventory APIs / 启用合并探针清单 API |
| `heartbeat_ttl` | Probe freshness window / 探针新鲜度窗口 |

### Orchestration YAML (`orchestration.*`) / Orchestration YAML

| Key | Description / 说明 |
|---|---|
| `slo_breach_ratio` | Breach threshold multiplier over `latency_slo_ms` / `latency_slo_ms` 的 breach 阈值倍率 |
| `slo_breach_consecutive` | Consecutive reconcile breaches before remediation / 触发修复前连续 breach 次数 |
| `auto_remediation_enabled` | Enable automatic reversible remediation hooks / 启用自动可回滚修复钩子 |
| `remediation_cooldown` | Minimum interval between auto-remediations per workload / 单工作负载自动修复最小间隔 |
| `max_remediations_per_reconcile` | Safety cap for auto-actions per cycle / 每次循环自动动作上限 |
| `max_remediations_per_workload` | Lifetime safety cap per workload / 单工作负载生命周期自动动作上限 |
| `remediation_min_improvement` | Required estimated latency improvement ratio / 触发修复需要的预估延迟改进比例 |

### Important controller env vars / 重要 controller 环境变量

| Env var | Purpose / 用途 |
|---|---|
| `SRE_CONTROLLER_CONFIG` | Controller config file path / Controller 配置文件路径 |
| `SRE_CONTROLLER_HTTP_LISTEN` | Override HTTP listen / 覆盖 HTTP 监听地址 |
| `SRE_CONTROLLER_GRPC_LISTEN` | Override gRPC listen / 覆盖 gRPC 监听地址 |
| `SRE_CONTROLLER_WEB_PATH` | Override web assets path / 覆盖 Web 资源路径 |
| `SRE_AGENT_CONTROLLER_API_KEY` | API key value when auth is enabled / 启用认证时的 API Key 值 |
| `SRE_AGENT_RAG_ENABLED` | Legacy compatibility switch for context retrieval / 旧版上下文检索兼容开关 |
| `SRE_AGENT_RAG_PATHS` | Legacy compatibility context document paths / 旧版上下文文档路径兼容配置 |
| `SRE_AGENT_RAG_MAX_CHARS` | Legacy compatibility snippet size cap / 旧版摘录长度兼容配置 |
| `SRE_AGENT_LLM_ENABLED` | Toggle LLM enrichment for analysis/agent / 切换分析/agent 的 LLM 增强 |
| `SRE_AGENT_LLM_PROVIDER` | LLM provider (`openai`, `anthropic`, `google`, `gemini`, `local`, `ollama`) / LLM 提供商 |
| `SRE_AGENT_LLM_MODEL` | LLM model name / LLM 模型名称 |
| `SRE_ORCHESTRATION_ENABLED` | Enable unified resource orchestration module / 启用统一资源编排模块 |
| `SRE_ORCHESTRATION_RECONCILE_INTERVAL` | Reconcile interval for scheduling/self-healing loop / 调度与自愈循环的重平衡间隔 |
| `SRE_ORCHESTRATION_STALE_AFTER` | Telemetry stale threshold used for placement health / 用于放置健康判断的遥测 stale 阈值 |
| `SRE_ORCHESTRATION_PEAK_PRESSURE` | Peak pressure threshold for batch deferral / 批处理延迟触发的峰值压力阈值 |
| `SRE_ORCHESTRATION_QUEUE_MAX` | Max queued/deferred workloads / 队列/延迟工作负载上限 |
| `SRE_ORCHESTRATION_SLO_BREACH_RATIO` | SLO breach ratio over `latency_slo_ms` / 相对 `latency_slo_ms` 的 breach 倍率 |
| `SRE_ORCHESTRATION_SLO_BREACH_CONSECUTIVE` | Consecutive breach count before remediation / 触发修复前连续 breach 次数 |
| `SRE_ORCHESTRATION_AUTO_REMEDIATION_ENABLED` | Toggle auto-remediation hooks / 开关自动修复钩子 |
| `SRE_ORCHESTRATION_REMEDIATION_COOLDOWN` | Cooldown between auto-remediations / 自动修复冷却时间 |
| `SRE_ORCHESTRATION_REMEDIATIONS_PER_RECONCILE` | Max remediation actions per reconcile / 每次重平衡最多修复动作数 |
| `SRE_ORCHESTRATION_REMEDIATIONS_PER_WORKLOAD` | Max remediation actions per workload / 单工作负载最多修复动作数 |
| `SRE_ORCHESTRATION_REMEDIATION_MIN_IMPROVEMENT` | Required latency improvement ratio to remediate / 触发修复所需延迟改进比例 |
| `SRE_INVENTORY_ENABLED` | Enable merged probe inventory APIs / 启用合并探针清单 API |
| `SRE_INVENTORY_HEARTBEAT_TTL` | Probe liveness TTL for telemetry/heartbeat freshness / 遥测与心跳新鲜度的探针存活 TTL |
| `SRE_K8S_ENABLED` | Enable read-only Kubernetes integration / 启用只读 Kubernetes 集成 |
| `SRE_K8S_REFRESH_INTERVAL` | Kubernetes snapshot refresh interval / Kubernetes 快照刷新间隔 |
| `SRE_K8S_REQUEST_TIMEOUT` | Per-cluster Kubernetes API request timeout / 单集群 Kubernetes API 请求超时 |
| `SRE_AGENT_LLM_API_KEY` | LLM API key / LLM API 密钥 |
| `SRE_AGENT_LLM_BASE_URL` | Optional custom/provider base URL / 可选的自定义/提供商基础 URL |
| `SRE_AGENT_REASONING_ENABLED` | Enable explicit runtime orchestrator / 启用显式运行时编排器 |
| `SRE_AGENT_RUNTIME_BACKEND` | Runtime backend (`haystack` or `native`) / 运行时后端（`haystack` 或 `native`） |
| `SRE_AGENT_RUNTIME_FAIL_OPEN` | Fallback to native when Haystack unavailable / Haystack 不可用时是否回退 native |
| `SRE_AGENT_CONTEXT_ENABLED` | Enable local runbook/document context retrieval (Haystack BM25) / 启用本地 runbook/文档上下文检索（Haystack BM25） |
| `SRE_AGENT_CONTEXT_PATHS` | Comma-separated context paths / 逗号分隔上下文路径 |
| `SRE_AGENT_CONTEXT_TOP_K` | Max retrieved snippets per request / 每次请求最多检索摘录数 |
| `SRE_AGENT_CONTEXT_MAX_CHARS` | Context chunk size cap / 上下文分块大小上限 |
| `SRE_AGENT_TOOL_TIMEOUT_SECONDS` | Per-tool timeout in orchestration runtime / 编排运行时的单工具超时 |
| `SRE_AGENT_TOOL_RETRIES` | Per-tool retry count / 单工具重试次数 |
| `SRE_AGENT_MEMORY_MAX_EVENTS` | Bounded memory store size / 有界内存存储容量 |
| `SRE_AGENT_MEMORY_FILE` | Optional JSONL memory persistence path / 可选 JSONL 内存持久化路径 |
| `SRE_AGENT_LLM_TIMEOUT_SECONDS` | LLM timeout for runtime reasoner / 运行时推理器的 LLM 超时 |
| `SRE_AGENT_DRY_RUN` | Force AGENT action execution into dry-run mode (`true`/`false`) / 强制 AGENT 动作执行进入 dry-run 模式 |
| `SRE_AGENT_REQUIRE_APPROVAL_TOKEN` | Require approval token for non-dry-run action execution / 非 dry-run 动作执行时要求审批令牌 |
| `SRE_AGENT_ACTION_APPROVAL_TTL` | Pending action TTL (example: `30m`) / 待执行动作过期时间（例如 `30m`） |
| `SRE_AGENT_MAX_PENDING_ACTIONS` | Maximum pending action cache size / 待执行动作缓存最大容量 |
| `SRE_AGENT_MAX_ACTIONS_PER_QUERY` | Max actions returned and cached per query / 每次查询返回并缓存的动作上限 |
| `SRE_AGENT_MAX_CONCURRENT_QUERIES` | Max in-flight AGENT query requests before busy rejection / AGENT 查询在触发 busy 拒绝前的最大并发在途请求数 |
| `SRE_AGENT_MAX_TELEMETRY_AGE` | Max allowed telemetry age before query is marked stale (example: `2m`) / 查询被标记为 stale 前允许的最大遥测年龄（例如 `2m`） |
| `SRE_AGENT_ALLOW_ACTIONS_ON_STALE_DATA` | Allow action proposal/execution flow even when telemetry is stale / 在遥测 stale 时仍允许动作建议/执行流 |
| `SRE_AGENT_SKIP_LLM_ON_STALE_TELEMETRY` | Skip LLM call and force deterministic fallback when telemetry is stale / 遥测 stale 时跳过 LLM 并强制确定性回退 |
| `SRE_AGENT_SKIP_LLM_ON_NO_TELEMETRY` | Skip LLM call when telemetry slice is insufficient / 遥测切片不足时跳过 LLM 调用 |
| `SRE_AGENT_ACTION_TIMEOUT` | Timeout per action execution (example: `15s`) / 单个动作执行超时（例如 `15s`） |
| `SRE_AGENT_MAX_PARALLEL_ACTION_EXEC` | Maximum parallel action executions / 动作执行并行度上限 |
| `SRE_AGENT_EXPLAINABILITY_EVIDENCE_MAX` | Max explainability evidence/limitation items in response / 响应中 explainability 证据/限制条目上限 |
| `SRE_AGENT_EVENT_WEBHOOK_URL` | Optional webhook endpoint for async AGENT query/execute events / 可选 AGENT 查询/执行异步事件 webhook 地址 |
| `SRE_AGENT_EVENT_WEBHOOK_TOKEN` | Optional bearer token for webhook authentication / webhook 认证可选 Bearer Token |
| `SRE_AGENT_EVENT_WEBHOOK_TIMEOUT` | Webhook publish timeout (example: `2s`) / webhook 发布超时（例如 `2s`） |
| `SRE_AGENT_EVENT_PUBLISH_RETRIES` | Event publish retry count per sink after initial attempt / 每个 sink 初次尝试后的重试次数 |
| `SRE_AGENT_EVENT_RETRY_BACKOFF` | Base backoff between event publish retries (example: `200ms`) / 事件发布重试间隔基线（例如 `200ms`） |
| `SRE_AGENT_EVENT_SLACK_WEBHOOK_URL` | Optional Slack incoming webhook for native AGENT event delivery / 可选 Slack incoming webhook 用于原生 AGENT 事件推送 |
| `SRE_AGENT_EVENT_PAGERDUTY_ROUTING_KEY` | Optional PagerDuty Events API routing key for AGENT event delivery / 可选 PagerDuty Events API routing key 用于 AGENT 事件推送 |
| `SRE_AGENT_EVENT_PAGERDUTY_EVENTS_URL` | Optional PagerDuty Events API endpoint override / 可选 PagerDuty Events API 地址覆盖 |

## Collector (`sre-collector`)

### CLI flags / CLI 标志

| Flag | Default | Description / 说明 |
|---|---|---|
| `--config` | empty / 空 | Config file path / 配置文件路径 |
| `--log-level` | `info` | Log level / 日志级别 |
| `--log-format` | `json` | `json` or `text` |
| `--level` | `0` | Override collection depth (1..5) / 覆盖采集深度 |
| `--interval` | `0` | Override collection interval / 覆盖采集间隔 |
| `--endpoint` | empty / 空 | Override controller endpoints (repeatable) / 覆盖 controller 端点（可重复） |
| `--metrics-listen` | empty / 空 | Enable collector `/metrics` endpoint / 启用 collector `/metrics` 端点 |
| `--version` | `false` | Print version and exit / 打印版本并退出 |

### Main YAML keys (`configs/collector.yaml`) / 主要 YAML 键

| Key | Description / 说明 |
|---|---|
| `collector_id`, `hostname`, `version`, `labels` | Collector identity metadata / Collector 身份元数据 |
| `controller_endpoints` | gRPC target list / gRPC 目标列表 |
| `collection_interval` | Collection loop interval / 采集循环间隔 |
| `adaptive_polling`, `min_collection_interval`, `max_collection_interval` | Adaptive polling controls / 自适应轮询控制 |
| `topk` | Top process sample count / Top 进程采样数量 |
| `log_paths` | Log files for fingerprinting / 用于指纹识别的日志文件 |
| `spool_dir`, `spool_max_bytes` | Local spool/wal behavior / 本地队列/WAL 行为 |
| `grpc_compress`, `mirror_send` | Transport behavior / 传输行为 |
| `metrics_listen_address` | Collector Prometheus listen address / Collector Prometheus 监听地址 |
| `tracing_jaeger_endpoint` | Jaeger collector endpoint for trace export / 用于追踪导出的 Jaeger 收集器端点 |
| `level` | Collection depth (1..5) / 采集深度 |
| `shm_enabled`, `shm_name` | Shared-memory ingestion / 共享内存接入 |
| `external_metrics_cmd`, `external_metrics_timeout` | External command metrics bridge / 外部命令指标桥接 |
| `transport.dial_timeout`, `transport.rpc_timeout` | gRPC timeout knobs / gRPC 超时参数 |
| `transport.tls.*` | TLS/mTLS settings and cert reload interval / TLS/mTLS 设置和证书重载间隔 |
| `ebpf.enabled`, `ebpf.socket_path`, `ebpf.categories`, `ebpf.max_msg_bytes` | eBPF socket reader / eBPF socket 读取器 |
| `probe_core.enabled`, `probe_core.binary_path`, `probe_core.collectors`, `probe_core.*` | Optional C++ probe-core IPC source, module selection, and fallback policy / 可选 C++ probe-core IPC 来源、模块选择与回退策略 |

### Collector env overrides / Collector 环境变量覆盖

| Env var | Description / 说明 |
|---|---|
| `SRE_COLLECTOR_CONFIG` | Collector config file path / Collector 配置文件路径 |
| `SRE_COLLECTOR_ID` | Override collector ID / 覆盖 collector ID |
| `SRE_COLLECTOR_HOSTNAME` | Override hostname / 覆盖主机名 |
| `SRE_COLLECTOR_VERSION` | Override collector version string / 覆盖 collector 版本字符串 |
| `SRE_COLLECTOR_COLLECTION_INTERVAL` | Override collection interval / 覆盖采集间隔 |
| `SRE_COLLECTOR_CONTROLLER_ENDPOINTS` | Comma-separated controller endpoints / 逗号分隔的 controller 端点 |
| `SRE_COLLECTOR_MIRROR_SEND` | Send to all endpoints instead of failover / 发送到所有端点而非故障转移 |
| `SRE_COLLECTOR_SPOOL_DIR` | Spool directory / 队列目录 |
| `SRE_COLLECTOR_SPOOL_MAX_BYTES` | Spool max bytes / 队列最大字节数 |
| `SRE_COLLECTOR_TOPK` | Top process sample count / Top 进程采样数量 |
| `SRE_COLLECTOR_LOG_PATHS` | Comma-separated log file paths / 逗号分隔的日志文件路径 |
| `SRE_COLLECTOR_SHM_ENABLED` | Enable shared-memory bridge / 启用共享内存桥接 |
| `SRE_COLLECTOR_SHM_NAME` | Shared-memory name / 共享内存名称 |
| `SRE_COLLECTOR_GRPC_COMPRESS` | Enable gzip compression / 启用 gzip 压缩 |
| `SRE_COLLECTOR_LEVEL` | Collection depth (recommended `5` for full RCA rankings) / 采集深度（完整 RCA 排名推荐 `5`） |
| `SRE_COLLECTOR_EXT_METRICS_CMD` | External metrics command / 外部指标命令 |
| `SRE_COLLECTOR_EXT_METRICS_TIMEOUT` | External command timeout / 外部命令超时 |
| `SRE_COLLECTOR_ADAPTIVE_POLLING` | Enable adaptive polling / 启用自适应轮询 |
| `SRE_COLLECTOR_MIN_COLLECTION_INTERVAL` | Min adaptive poll interval / 最小自适应轮询间隔 |
| `SRE_COLLECTOR_MAX_COLLECTION_INTERVAL` | Max adaptive poll interval / 最大自适应轮询间隔 |
| `SRE_COLLECTOR_METRICS_ADDR` | Collector metrics listen address / Collector 指标监听地址 |
| `SRE_COLLECTOR_JAEGER_ENDPOINT` | Jaeger endpoint for tracing / 用于追踪的 Jaeger 端点 |
| `SRE_COLLECTOR_GRPC_DIAL_TIMEOUT` | gRPC dial timeout / gRPC 拨号超时 |
| `SRE_COLLECTOR_GRPC_RPC_TIMEOUT` | gRPC call timeout / gRPC 调用超时 |
| `SRE_COLLECTOR_TLS_ENABLED` | Enable TLS for ingest client / 为接入客户端启用 TLS |
| `SRE_COLLECTOR_TLS_CA_FILE` | TLS CA file / TLS CA 文件 |
| `SRE_COLLECTOR_TLS_CERT_FILE` | TLS client cert file / TLS 客户端证书文件 |
| `SRE_COLLECTOR_TLS_KEY_FILE` | TLS client key file / TLS 客户端密钥文件 |
| `SRE_COLLECTOR_TLS_SERVER_NAME` | TLS server name override / TLS 服务器名称覆盖 |
| `SRE_COLLECTOR_TLS_INSECURE_SKIP_VERIFY` | Skip TLS hostname/cert verification / 跳过 TLS 主机名/证书验证 |
| `SRE_COLLECTOR_TLS_RELOAD_INTERVAL` | TLS cert reload interval / TLS 证书重载间隔 |
| `SRE_COLLECTOR_EBPF_ENABLED` | Enable eBPF socket reader / 启用 eBPF socket 读取器 |
| `SRE_COLLECTOR_EBPF_SOCKET_PATH` | eBPF socket path / eBPF socket 路径 |
| `SRE_COLLECTOR_EBPF_CATEGORIES` | Comma-separated eBPF categories / 逗号分隔的 eBPF 类别 |
| `SRE_COLLECTOR_EBPF_MAX_MSG_BYTES` | eBPF max message size / eBPF 最大消息大小 |
| `SRE_COLLECTOR_PROBE_CORE_ENABLED` | Enable C++ probe-core path / 启用 C++ probe-core 路径 |
| `SRE_COLLECTOR_PROBE_CORE_BINARY_PATH` | Probe-core binary path / probe-core 二进制路径 |
| `SRE_COLLECTOR_PROBE_CORE_COLLECTORS` | Comma-separated probe-core modules (`host,disk,network,rdma,netlink,ethtool,perf,ebpf,gpu,process`) or `all` / 逗号分隔 probe-core 模块或 `all` |
| `SRE_COLLECTOR_PROBE_CORE_ARGS` | Extra comma-separated probe-core args (do not mix with `SRE_COLLECTOR_PROBE_CORE_COLLECTORS` for `--collectors`) / 额外 probe-core 参数（不要与 `SRE_COLLECTOR_PROBE_CORE_COLLECTORS` 混用 `--collectors`） |
| `SRE_COLLECTOR_PROBE_CORE_COMPRESSION` | IPC payload compression (`none`/`gzip`) / IPC 负载压缩 |
| `SRE_COLLECTOR_PROBE_CORE_QUEUE_DEPTH` | Probe-core frame queue depth / probe-core 帧队列深度 |
| `SRE_COLLECTOR_PROBE_CORE_WINDOW_SAMPLES` | Sliding window sample metadata size / 滑动窗口样本元数据大小 |
| `SRE_COLLECTOR_PROBE_CORE_GPU_INTERVAL_SAMPLES` | GPU polling cadence in samples / GPU 采集采样步长 |
| `SRE_COLLECTOR_PROBE_CORE_STARTUP_TIMEOUT` | Probe-core startup timeout / probe-core 启动超时 |
| `SRE_COLLECTOR_PROBE_CORE_STALE_AFTER` | Max age for fresh probe-core frame / probe-core 帧新鲜度阈值 |
| `SRE_COLLECTOR_PROBE_CORE_FRAME_MAX_BYTES` | Max frame size guardrail / IPC 最大帧大小保护 |
| `SRE_COLLECTOR_PROBE_CORE_FALLBACK_TO_GO` | Allow fallback to Go probe collector / 允许回退到 Go probe 采集器 |

## Practical presets / 实用预设

### Full per-process ranking quality / 完整进程排名质量

```bash
SRE_COLLECTOR_LEVEL=5
SRE_COLLECTOR_LOG_PATHS=/var/log/syslog,/var/log/messages
```

### C++ probe-core primary path / C++ probe-core 主路径

```bash
SRE_COLLECTOR_PROBE_CORE_ENABLED=1
SRE_COLLECTOR_PROBE_CORE_BINARY_PATH=./build/sre-probe-core
SRE_COLLECTOR_PROBE_CORE_COMPRESSION=none
SRE_COLLECTOR_PROBE_CORE_FALLBACK_TO_GO=1
```

### C++ probe-core Unix-style module mode / C++ probe-core Unix 风格模块模式

Run only required collectors to reduce overhead and keep each run focused:

按需启用采集模块，降低开销并保持单次运行目标明确：

```bash
SRE_COLLECTOR_PROBE_CORE_ENABLED=1
SRE_COLLECTOR_PROBE_CORE_COLLECTORS=host,network,rdma,process
```

Run all modules:

启用全部模块：

```bash
SRE_COLLECTOR_PROBE_CORE_COLLECTORS=all
```

Available probe-core modules:

可用 probe-core 模块：

- `host`
- `disk`
- `network`
- `rdma`
- `netlink`
- `ethtool`
- `perf`
- `ebpf`
- `gpu`
- `process`

List modules from binary (script-friendly):

从二进制直接列出模块（便于脚本化）：

```bash
./build/sre-probe-core --list-collectors
```

### Minimal collector overhead / 最小 Collector 开销

```bash
SRE_COLLECTOR_LEVEL=2
SRE_COLLECTOR_LOG_PATHS=
SRE_COLLECTOR_EBPF_ENABLED=0
```

## Related files / 相关文件

- Defaults: `configs/controller.yaml`, `configs/collector.yaml`
- Optional env examples: `.env.example`
- Usage walkthrough: `docs/operations/usage.md`
- Runtime architecture: `docs/design/agent_runtime.md`
