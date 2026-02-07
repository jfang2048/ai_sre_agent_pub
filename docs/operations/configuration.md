# Configuration

This guide documents the current runtime configuration behavior in code.

## Version baseline

Current release line: `v0.1`.

## Configuration precedence

### Controller

1. Built-in defaults (`DefaultConfig`).
2. YAML file (`--config`, or `SRE_CONTROLLER_CONFIG`, or `./configs/controller.yaml`, then `/etc/sre-controller/config.yaml`).
3. CLI flag overrides (`--listen`, `--grpc-listen`, `--nodes`, `--web-path`).
4. Env overrides (`SRE_CONTROLLER_HTTP_LISTEN`, `SRE_CONTROLLER_GRPC_LISTEN`, `SRE_CONTROLLER_WEB_PATH`, selected `SRE_AGENT_*`).

### Collector

1. Built-in defaults (`DefaultConfig`).
2. YAML file (`--config`, or `SRE_COLLECTOR_CONFIG`, or `./configs/collector.yaml`, then `/etc/sre-collector/config.yaml`).
3. Env overrides (`SRE_COLLECTOR_*`).

## Controller (`sre-controller`)

### CLI flags

| Flag | Default | Description |
|---|---|---|
| `--config`, `-c` | empty | Config file path |
| `--listen`, `-l` | `:8080` | HTTP listen address |
| `--grpc-listen` | `:9090` | gRPC ingest listen address |
| `--port-file` | empty | Write resolved listen addresses to JSON file |
| `--nodes`, `-n` | empty | Comma-separated pull-mode node addresses |
| `--log-level` | `info` | Log level |
| `--log-format` | `json` | `json` or `text` |
| `--web-path` | `./web` | Static web assets path |
| `--version`, `-v` | `false` | Print version and exit |

### Main YAML sections (`configs/controller.yaml`)

- `listen`, `grpc_listen`, `log_level`
- `scrape.interval`, `scrape.timeout`
- `nodes`
- `web.path`
- `analysis.*`
- `agent.*`
- `incidents.*`
- `gpu.*`
- `checks.*`
- `auth.*` (optional)

### Important controller env vars

| Env var | Purpose |
|---|---|
| `SRE_CONTROLLER_CONFIG` | Controller config file path |
| `SRE_CONTROLLER_HTTP_LISTEN` | Override HTTP listen |
| `SRE_CONTROLLER_GRPC_LISTEN` | Override gRPC listen |
| `SRE_CONTROLLER_WEB_PATH` | Override web assets path |
| `SRE_AGENT_CONTROLLER_API_KEY` | API key value when auth is enabled |
| `SRE_AGENT_RAG_ENABLED` | Enable/disable RAG in agent config |
| `SRE_AGENT_RAG_PATHS` | Comma-separated RAG document paths |
| `SRE_AGENT_RAG_MAX_CHARS` | RAG snippet size cap |
| `SRE_AGENT_LLM_ENABLED` | Toggle LLM enrichment for analysis/agent |
| `SRE_AGENT_LLM_PROVIDER` | LLM provider (`openai`, `anthropic`, `google`, `gemini`, `local`, `ollama`) |
| `SRE_AGENT_LLM_MODEL` | LLM model name |
| `SRE_AGENT_LLM_API_KEY` | LLM API key |
| `SRE_AGENT_LLM_BASE_URL` | Optional custom/provider base URL |

## Collector (`sre-collector`)

### CLI flags

| Flag | Default | Description |
|---|---|---|
| `--config` | empty | Config file path |
| `--log-level` | `info` | Log level |
| `--log-format` | `json` | `json` or `text` |
| `--version` | `false` | Print version and exit |

Note: collector has no `--level` CLI flag. Use YAML `level` or `SRE_COLLECTOR_LEVEL`.

### Main YAML keys (`configs/collector.yaml`)

| Key | Description |
|---|---|
| `collector_id`, `hostname`, `version`, `labels` | Collector identity metadata |
| `controller_endpoints` | gRPC target list |
| `collection_interval` | Collection loop interval |
| `topk` | Top process sample count |
| `log_paths` | Log files for fingerprinting |
| `spool_dir`, `spool_max_bytes` | Local spool/wal behavior |
| `grpc_compress`, `mirror_send` | Transport behavior |
| `level` | Collection depth (1..5) |
| `shm_enabled`, `shm_name` | Shared-memory ingestion |
| `external_metrics_cmd`, `external_metrics_timeout` | External command metrics bridge |
| `ebpf.enabled`, `ebpf.socket_path`, `ebpf.categories`, `ebpf.max_msg_bytes` | eBPF socket reader |

### Collector env overrides

| Env var | Description |
|---|---|
| `SRE_COLLECTOR_CONFIG` | Collector config file path |
| `SRE_COLLECTOR_ID` | Override collector ID |
| `SRE_COLLECTOR_HOSTNAME` | Override hostname |
| `SRE_COLLECTOR_VERSION` | Override collector version string |
| `SRE_COLLECTOR_COLLECTION_INTERVAL` | Override collection interval |
| `SRE_COLLECTOR_CONTROLLER_ENDPOINTS` | Comma-separated controller endpoints |
| `SRE_COLLECTOR_MIRROR_SEND` | Send to all endpoints instead of failover |
| `SRE_COLLECTOR_SPOOL_DIR` | Spool directory |
| `SRE_COLLECTOR_SPOOL_MAX_BYTES` | Spool max bytes |
| `SRE_COLLECTOR_TOPK` | Top process sample count |
| `SRE_COLLECTOR_LOG_PATHS` | Comma-separated log file paths |
| `SRE_COLLECTOR_SHM_ENABLED` | Enable shared-memory bridge |
| `SRE_COLLECTOR_SHM_NAME` | Shared-memory name |
| `SRE_COLLECTOR_GRPC_COMPRESS` | Enable gzip compression |
| `SRE_COLLECTOR_LEVEL` | Collection depth (recommended `5` for full RCA rankings) |
| `SRE_COLLECTOR_EXT_METRICS_CMD` | External metrics command |
| `SRE_COLLECTOR_EXT_METRICS_TIMEOUT` | External command timeout |
| `SRE_COLLECTOR_EBPF_ENABLED` | Enable eBPF socket reader |
| `SRE_COLLECTOR_EBPF_SOCKET_PATH` | eBPF socket path |
| `SRE_COLLECTOR_EBPF_CATEGORIES` | Comma-separated eBPF categories |
| `SRE_COLLECTOR_EBPF_MAX_MSG_BYTES` | eBPF max message size |

## Practical presets

### Full per-process ranking quality

```bash
SRE_COLLECTOR_LEVEL=5
SRE_COLLECTOR_LOG_PATHS=/var/log/syslog,/var/log/messages
```

### Minimal collector overhead

```bash
SRE_COLLECTOR_LEVEL=2
SRE_COLLECTOR_LOG_PATHS=
SRE_COLLECTOR_EBPF_ENABLED=0
```

## Related files

- Defaults: `configs/controller.yaml`, `configs/collector.yaml`
- Optional env examples: `.env.example`
- Usage walkthrough: `docs/operations/usage.md`
