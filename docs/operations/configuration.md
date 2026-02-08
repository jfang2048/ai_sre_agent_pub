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
- `agent.*`
- `incidents.*`
- `gpu.*`
- `checks.*`
- `auth.*` (optional / 可选)

### Important controller env vars / 重要 controller 环境变量

| Env var | Purpose / 用途 |
|---|---|
| `SRE_CONTROLLER_CONFIG` | Controller config file path / Controller 配置文件路径 |
| `SRE_CONTROLLER_HTTP_LISTEN` | Override HTTP listen / 覆盖 HTTP 监听地址 |
| `SRE_CONTROLLER_GRPC_LISTEN` | Override gRPC listen / 覆盖 gRPC 监听地址 |
| `SRE_CONTROLLER_WEB_PATH` | Override web assets path / 覆盖 Web 资源路径 |
| `SRE_AGENT_CONTROLLER_API_KEY` | API key value when auth is enabled / 启用认证时的 API Key 值 |
| `SRE_AGENT_RAG_ENABLED` | Enable/disable RAG in agent config / 启用/禁用 agent 配置中的 RAG |
| `SRE_AGENT_RAG_PATHS` | Comma-separated RAG document paths / 逗号分隔的 RAG 文档路径 |
| `SRE_AGENT_RAG_MAX_CHARS` | RAG snippet size cap / RAG 摘录大小上限 |
| `SRE_AGENT_LLM_ENABLED` | Toggle LLM enrichment for analysis/agent / 切换分析/agent 的 LLM 增强 |
| `SRE_AGENT_LLM_PROVIDER` | LLM provider (`openai`, `anthropic`, `google`, `gemini`, `local`, `ollama`) / LLM 提供商 |
| `SRE_AGENT_LLM_MODEL` | LLM model name / LLM 模型名称 |
| `SRE_AGENT_LLM_API_KEY` | LLM API key / LLM API 密钥 |
| `SRE_AGENT_LLM_BASE_URL` | Optional custom/provider base URL / 可选的自定义/提供商基础 URL |
| `SRE_AGENT_DRY_RUN` | Force AGENT action execution into dry-run mode (`true`/`false`) / 强制 AGENT 动作执行进入 dry-run 模式 |

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

## Practical presets / 实用预设

### Full per-process ranking quality / 完整进程排名质量

```bash
SRE_COLLECTOR_LEVEL=5
SRE_COLLECTOR_LOG_PATHS=/var/log/syslog,/var/log/messages
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
