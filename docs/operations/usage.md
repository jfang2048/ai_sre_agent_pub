# Usage Guide (v0.7)

This document is operational. It explains how to run the system in two modes:

- **Single-node convenience mode:** controller and collector on one machine
- **Separated deployment mode:** controller on one machine, one or many collectors on other machines

The architecture rationale stays in [README.md](../../README.md). This file focuses on commands, files, and expected behavior.

## 中文导读

- 这份文档回答的是“系统怎么跑”，不是“系统为什么这样设计”。因此它更强调启动命令、配置入口、健康检查和常见故障点。
- 这里把运行方式分成 single-node 和 separated deployment，不是因为它们是两套产品，而是因为它们共享同一套控制面语义，只是物理拓扑和权限边界不同。
- 实际阅读顺序可以很简单: 先用 single-node 建立闭环，再用 separated deployment 验证真实边界。这样既能快启动，也不会误把 demo 路径当成生产路径。
- 文档里反复强调 `host-observer`，原因是 v0.7 的主路径依赖 probe-core、eBPF、host namespace 和内核挂载点；如果这些前提不成立，你看到的就是兼容退化信号，而不是完整观测面。

## Section A: Single-node usage

### When this mode is useful

- local development
- UI/API demos
- validating RAG, RCA, and agent workflows on one host
- quick smoke testing without provisioning remote collector hosts

### Recommended path: container-first single-node stack

```bash
cp .env.example .env
make container-build
make container-up-host-observer
```

Health checks:

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/api/v1/status
curl -sS http://127.0.0.1:8080/api/v1/inventory/probes
curl -sS http://127.0.0.1:8080/api/v1/rag/status
```

Stop:

```bash
make container-down-host-observer
```

Why `host-observer` is the recommended single-node path:

- it exposes the primary collector model from `v0.7` instead of a reduced container sandbox
- probe-core stays the primary host telemetry source
- the eBPF runtime gets the namespaces/capabilities it needs for high-fidelity kernel events

中文理解:

- `host-observer` 被推荐，不是因为它更“高级”，而是因为它更接近系统真实设计假设。
- 如果只跑普通容器沙箱，collector 可能也能启动，但 PID、网络、BPF 和内核状态视角会不完整；这时页面能打开，不代表你已经验证了高保真采集链路。
- 排障时先分清“系统没起来”和“系统起来了但在退化模式里”非常关键，这会直接影响后面所有数据解释。

`make container-up` still exists, but it is better treated as a controller/UI/demo convenience stack than as the full observability path.

### Production-like local overlays

Add controller-side InfluxDB:

```bash
make container-up-tsdb
make container-down-tsdb
```

Add host-observer collector privileges/mounts:

```bash
make container-up-host-observer
make container-down-host-observer
```

Full local stack:

```bash
make container-up-full
make container-down-full
```

### Expected behavior in single-node mode

- The collector pushes gRPC telemetry to the controller on `:9090`
- The controller serves the UI/API on `:8080`
- The collector uses probe-core as the primary host/process telemetry path
- The collector uses the eBPF runtime as the primary kernel-event path
- `configs/controller_targets.yaml` is still loaded by the controller and appears in inventory APIs
- RAG, RCA, joint-risk, and recommendation flows work exactly as in split deployment; only the physical topology changes

### Source-mode fallback

Use this when you want direct process control without rebuilding images:

```bash
make build
./scripts/run-local.sh --enable-agent
```

Controller-only demo mode with seeded telemetry:

```bash
./scripts/run-local.sh --enable-agent --demo --llm=stub
```

In `--demo`, the launcher now defaults to controller-only startup because synthetic
telemetry is already seeded into the controller. This removes the live collector
and gRPC ingest path from the critical startup path for UI/RAG/agent demos.

If you want the demo stack to also start a real collector, opt in explicitly:

```bash
SRE_DEMO_START_COLLECTOR=1 ./scripts/run-local.sh --enable-agent --demo --llm=stub
```

Local multi-collector source simulation:

```bash
./scripts/run-local-multinode.sh --collectors 3
```

## Section B: Separated multi-machine usage

### Topology

- **Controller host:** runs `sre-controller`, UI/API, ingest, RAG, agent workflows, optional TSDB
- **Collector hosts:** run `sre-collector`, collect host telemetry, spool locally, push to controller

Current transport direction is still push-first:

- collector -> controller over gRPC (`:9090`)
- browser/UI -> controller over HTTP (`:8080`)

The controller target inventory file is still useful even though the primary data path is push-oriented. It acts as:

- a hand-maintained list of known collectors
- inventory metadata for UI grouping and policy scoping
- a future-safe place for endpoint/auth metadata
- a reference map for actions, lookups, and audit attribution

中文原因补充:

- push-first 架构并不等于 inventory 没用了。collector 负责把实时 telemetry 推上来，inventory 负责告诉控制面“这个节点是谁、属于哪里、应该怎么分组和归属”。
- 这能避免系统把“最近谁连进来了”直接当成资产真相。运行时事实和资产元数据应该分层，否则 UI、策略和审计都容易失真。

### 1. Prepare the controller host

Edit the controller config if needed:

- `configs/controller.yaml`
- `configs/controller_targets.yaml`

Example target inventory entry:

```yaml
---
collectors:
  - id: "collector-edge-a"
    hostname: "edge-a.example.net"
    address: "10.20.0.11"
    port: 9464
    enabled: true
    labels:
      site: "dc-a"
      env: "prod"
    tags: ["gpu", "edge"]
    auth:
      mode: "mtls"
      server_name: "controller.example.net"
      token_env: "SRE_COLLECTOR_TOKEN"
```

Build the controller image from the local checkout:

```bash
./scripts/docker-build-controller.sh
```

Build the controller image from a fork or mirror instead:

```bash
REPO_URL=https://github.com/your-org/your-fork.git \
REPO_REF=main \
./scripts/docker-build-controller.sh
```

Run the controller:

```bash
./scripts/docker-run-controller.sh \
  --config-file ./configs/container/controller.yaml \
  --targets-file ./configs/controller_targets.yaml
```

Equivalent compose-only controller host:

```bash
docker compose -f deploy/docker/docker-compose.controller.yml up -d
```

### 2. Prepare each collector host

Edit the collector config or override the controller endpoint through env:

- `configs/collector.yaml`
- `SRE_COLLECTOR_CONTROLLER_ENDPOINTS=controller.example.net:9090`

Build from the local checkout:

```bash
./scripts/docker-build-collector.sh
```

Build from another repository/ref:

```bash
REPO_URL=https://github.com/your-org/your-fork.git \
REPO_REF=main \
./scripts/docker-build-collector.sh
```

Run the collector:

```bash
SRE_COLLECTOR_CONTROLLER_ENDPOINTS=controller.example.net:9090 \
SRE_COLLECTOR_ID=collector-edge-a \
SRE_COLLECTOR_HOSTNAME=edge-a.example.net \
./scripts/docker-run-collector.sh --config-file ./configs/container/collector.yaml
```

If the collector must observe host kernel/runtime state more directly:

```bash
SRE_COLLECTOR_CONTROLLER_ENDPOINTS=controller.example.net:9090 \
SRE_COLLECTOR_ID=collector-edge-a \
SRE_COLLECTOR_HOSTNAME=edge-a.example.net \
./scripts/docker-run-collector.sh --config-file ./configs/container/collector.yaml --host-observer
```

`--host-observer` is the recommended production-like container mode because probe-core/perf/eBPF visibility depends on host namespaces, mounted kernel interfaces, and container capabilities. Without it, the collector can still start, but the runtime may degrade to compatibility behavior and reduced kernel visibility.

Equivalent compose-only collector host:

```bash
SRE_COLLECTOR_CONTROLLER_ENDPOINTS=controller.example.net:9090 \
docker compose -f deploy/docker/docker-compose.collector.yml up -d
```

### Production deployment note: transport security and escalation sinks

For production split deployments, treat the following as the normal baseline rather than optional polish:

- enable TLS/mTLS on collector -> controller gRPC
- configure at least one incident sink (`Slack`, `PagerDuty`, or generic webhook)
- keep workflow actions in dry-run/approval mode until change-control and rollback paths are validated

Typical env additions on the controller side:

```bash
SRE_COLLECTOR_TLS_ENABLED=true
SRE_COLLECTOR_TLS_CA_FILE=/etc/ai-sre-agent/pki/ca.pem
SRE_COLLECTOR_TLS_CERT_FILE=/etc/ai-sre-agent/pki/collector.pem
SRE_COLLECTOR_TLS_KEY_FILE=/etc/ai-sre-agent/pki/collector-key.pem
SRE_COLLECTOR_TLS_SERVER_NAME=controller.example.net

SRE_AGENT_EVENT_SLACK_WEBHOOK_URL=https://hooks.slack.com/services/...
SRE_AGENT_EVENT_PAGERDUTY_ROUTING_KEY=...
SRE_AGENT_EVENT_WEBHOOK_URL=https://incident-gateway.example.net/events
```

中文原因补充:

- `host-observer` 保证的是观测面真实性，mTLS 保证的是遥测传输面真实性；两者缺一不可。
- Slack / PagerDuty / webhook 这些 sink 不是附属提醒，而是把分析结果真正接进企业值班链路的必要部分。
- 如果没有这些出口，RCA 可能在页面里是“完成”的，但在组织流程里仍然是断开的。

### 3. Add or remove collectors

To add a collector:

1. Add an entry to `configs/controller_targets.yaml`
2. Start a collector container on the remote host with a unique `SRE_COLLECTOR_ID`
3. Verify it appears in:
   - `GET /api/v1/inventory/probes`
   - `GET /api/v1/fleet`

To remove a collector:

1. Stop the collector container on the remote host
2. Remove or disable the entry from `configs/controller_targets.yaml`
3. Restart or redeploy the controller if the mounted file changed outside the container image

### 4. How the controller target config is used today

Today the file is not a reverse-dial transport list for the controller's ingest plane. The collector still initiates the main telemetry connection.

The file is used for:

- known collector registration
- inventory listing even before telemetry arrives
- grouping by labels/tags
- separating enabled vs disabled collectors
- storing auth/endpoint metadata without hardcoding it in the binary

### 5. Troubleshooting basics

Controller host:

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/api/v1/inventory/status
curl -sS http://127.0.0.1:8080/api/v1/inventory/probes
curl -sS http://127.0.0.1:8080/api/v1/fleet
docker logs ai-sre-agent-controller --tail 100
```

Collector host:

```bash
curl -sS http://127.0.0.1:9464/healthz
curl -sS http://127.0.0.1:9464/metrics | head
docker logs ai-sre-agent-collector --tail 100
```

Common failure points:

- wrong `SRE_COLLECTOR_CONTROLLER_ENDPOINTS`
- controller `:9090` not reachable through host firewall or security group
- host-observer collector started without required kernel capabilities/mounts
- controller target inventory file mounted to the wrong path
- controller and collector IDs not matching the intended inventory entry

排障建议顺序:

1. 先看 `healthz` 和 `status`，确认 controller 自己活着。
2. 再看 `inventory/probes` 和 `fleet`，确认“节点已知”和“遥测已到达”是否同时成立。
3. 最后再解释单个指标、RCA 或 UI 页面内容。否则很容易在控制面未就绪时提前推断业务根因。

### 6. Validate predictive early warning

`v0.7` adds predictive findings to agent reports. The lowest-friction validation path is:

```bash
make predictive-test
curl -sS http://127.0.0.1:8080/api/v1/agent/reports/latest | jq '.reports[0].predictions'
```

For a single node:

```bash
curl -sS http://127.0.0.1:8080/api/v1/agent/reports/<collector-id>/latest | jq '.report.predictions'
```

What to expect:

- `predictions[]` should contain structured fields such as `prediction_id`, `predictive_slo`, `hazard_class`, `algorithm_version`, and `audit_hash`
- predictive findings should appear only for trend-retained metrics with enough recent history
- the hot path remains deterministic; the presence or absence of an LLM does not change whether a predictive finding is generated

Business reason:

- this is the mechanism that turns “node is already broken” into “node is drifting toward an outage and still has time for safe intervention”
- in GPU fleets, that distinction matters because thermal, power, PCIe, and memory faults often become expensive only after the onset window is missed

中文原因补充:

- predictive 验证不能只看页面有没有红色提示，更重要的是确认预警对象、证据窗口、算法版本、审计字段是否齐全。
- 如果这些字段缺失，系统也许能“看起来像在预测”，但在生产事故复盘、合规审计和自动化审批里仍然不可用。
- 推荐把 `predictions[]` 当成新的控制面契约来验证，而不是把它理解成 UI 附属信息。

Further operator guidance lives in [`predictive_runbook.md`](predictive_runbook.md).

### 7. Validate RAG and operational knowledge retrieval

`v0.7` treats RAG as a first-class controller capability rather than a background helper. The quickest validation path is:

```bash
curl -sS http://127.0.0.1:8080/api/v1/rag/status | jq .
curl -sS -X POST http://127.0.0.1:8080/api/v1/rag/query \
  -H 'Content-Type: application/json' \
  -d '{
        "query":"gpu timeout after rollout",
        "intent":"rca",
        "top_k":5,
        "knowledge_types":["historical_incident","runbook"]
      }' | jq .
curl -sS -X POST http://127.0.0.1:8080/api/v1/rag/reindex | jq .
```

What to expect:

- `rag/status` should show `doc_count`, `chunk_count`, `source_types`, `knowledge_types`, and `case_types`
- `rag/query` should return normalized evidence rather than raw text only: `knowledge_type`, `case_type`, `summary`, `likely_causes`, `remediation_steps`, `signals`, and `evidence_id`
- `rag/reindex` should rebuild from `dataset/` and any configured extra source paths without requiring an external vector service

Why this matters operationally:

- potential-risk analysis needs similar weak-signal patterns, not only current telemetry deltas
- joint-risk analysis needs prior escalation patterns so low-severity signals can be interpreted together
- RCA needs historical analogies and runbooks to turn “what failed” into “what to verify next”
- recommendations become materially better when they are grounded in retrieved runbook steps and prior case outcomes
- the Knowledge page now exposes `intent`, `knowledge type`, `case type`, and `source type` filters so operators can validate what kind of operational memory the controller is actually using

中文补充:

- RAG 验证不能只看“有没有命中”。更重要的是命中的知识类型对不对，返回的是 runbook、历史案例还是问题模式。
- 如果 query 结果只有 snippet，没有 summary、likely causes、remediation steps 这些结构化字段，那说明知识还没有真正进入运维闭环。
- `reindex` 是控制面知识刷新动作，不应该影响 collector 采集路径；失败时也应该是知识面退化，而不是把整套系统拖垮。

### 7.1 Refresh RAG knowledge after dataset changes

If you edited `dataset/` or changed extra knowledge paths, the normal operator loop is:

```bash
make rag-update
curl -sS http://127.0.0.1:8080/api/v1/rag/status | jq .
curl -sS -X POST http://127.0.0.1:8080/api/v1/rag/query \
  -H 'Content-Type: application/json' \
  -d '{"query":"gpu timeout after rollout","intent":"rca","top_k":5}' | jq .
```

Use a full rebuild instead of incremental update when:

- many files changed at once
- chunking settings changed
- optional archive corpora were imported or replaced
- you want to discard any stale extraction/index state

```bash
make rag-rebuild
curl -sS -X POST http://127.0.0.1:8080/api/v1/rag/index/rebuild | jq .
```

For local-only archive corpora:

```bash
scripts/bootstrap/manage_optional_datasets.sh import --from /path/to/archive-dir
export SRE_AGENT_RAG_SOURCE_PATHS=/absolute/path/to/extra/docs,$(pwd)/data/bootstrap/datasets/archives
make rag-rebuild
```

Operational rule:

- changing knowledge should refresh the controller knowledge plane only
- it should not require collector restarts
- retrieval degradation should stay isolated from telemetry ingestion

### 8. Verify RAG influence on workflows

The value of the stronger RAG path is visible in the workflow APIs:

```bash
curl -sS "http://127.0.0.1:8080/api/v1/agent/joint-risk?limit=1" | jq '.reports[0] | {retrieved_cases,retrieved_runbooks,similar_incident_patterns,retrieval_summary}'
curl -sS "http://127.0.0.1:8080/api/v1/agent/rca?limit=1" | jq '.reports[0] | {retrieved_docs,retrieved_cases,retrieved_runbooks,recommendations}'
```

Look for:

- `retrieved_cases[]` when the workflow found analogous incidents
- `retrieved_runbooks[]` when the workflow found concrete checks or remediation steps
- `similar_incident_patterns[]` when joint-risk correlated current weak signals with prior escalation patterns
- recommendation text that cites retrieved evidence instead of generic advice

Graceful degradation is still required:

- if the RAG backend is disabled or rebuilding, workflows continue running
- missing knowledge evidence lowers retrieval confidence and reduces enrichment quality, but it does not crash RCA or recommendation generation
