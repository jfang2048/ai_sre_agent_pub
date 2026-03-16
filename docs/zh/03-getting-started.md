# 快速开始

English version: [docs/en/03-getting-started.md](../en/03-getting-started.md)

这份文档只保留最短可运行路径。

## 前置条件

- Docker 和 `docker compose`
- GNU `make`
- `curl`

如果你想直接从源码构建而不是走容器路径，还需要 [`Makefile`](../../Makefile) 对应的 Go 和 C++ 构建环境。

## 推荐的本地启动方式

使用容器优先的 host-observer 路径：

```bash
cp .env.example .env
make container-build
make container-up-host-observer
```

验证 controller：

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/readyz
curl -sS http://127.0.0.1:8080/api/v1/status
curl -sS http://127.0.0.1:8080/api/v1/rag/status
curl -sS http://127.0.0.1:8080/api/v1/fleet
```

打开 UI：

```text
http://127.0.0.1:8080/
```

停止：

```bash
make container-down-host-observer
```

为什么推荐这条路径：

- 它是仓库维护中的容器主路径
- 它更接近 collector 真实的 host-observer 假设
- 它能同时验证 controller、collector、UI 和 gRPC ingest 链路

## 一次成功启动通常会长什么样

一个比较真实的首轮成功案例通常是：

- `healthz` 能立刻返回
- `readyz` 会在 controller 完成当前模式所需的启动检查后返回
- `/api/v1/status` 能返回 controller 的运行元数据
- `/api/v1/status.deployment.mode` 能告诉你当前启用的是哪种部署形态
- host-observer 栈稳定后，`/api/v1/fleet` 至少能看到一个 collector
- UI 能加载出 dashboard shell

可以直接这样看：

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/readyz
curl -sS http://127.0.0.1:8080/api/v1/status | jq .
curl -sS http://127.0.0.1:8080/api/v1/status | jq '.deployment'
curl -sS http://127.0.0.1:8080/api/v1/fleet | jq '.count, .nodes[0].collector_id'
```

这些结果可以这样理解：

- 如果 `healthz` 失败，先看 controller 启动问题
- 如果 `healthz` 正常但 `/api/v1/fleet` 为空，优先排查 collector 启动或 ingest 连通性
- 如果 UI 能开但 RAG 是 disabled，这仍然是一个有效的 telemetry/API/UI 验证结果
- 如果 UI 能开、RAG 也 enabled，但后续某些查询仍然没有 retrieval 上下文，这可能是刻意行为：泛化、弱症状问题现在会增加 `agent_rag_skipped_context_total`，而不是强行执行低价值检索

## 一个更具体的验证清单

启动后，第一轮健康检查建议直接看：

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/readyz
curl -sS http://127.0.0.1:8080/api/v1/status
curl -sS http://127.0.0.1:8080/api/v1/rag/status
```

这些结果分别说明：

- `healthz` 表示 controller 的 HTTP 进程活着
- `readyz` 表示 controller 完成了当前模式所需的启动检查
- `/api/v1/status` 表示 controller 自己认为核心子系统是否正常
- `/api/v1/rag/status` 表示本地知识索引是否已启用并加载

如果 UI 能打开，但 `/api/v1/rag/status` 显示 disabled，系统依然能用，只是暂时没有检索增强上下文。

## 一组很小但有代表性的 API 巡检

栈起来之后，下面这些接口最适合先感受系统是否真的连通：

```bash
curl -sS http://127.0.0.1:8080/api/v1/status
curl -sS http://127.0.0.1:8080/api/v1/fleet
curl -sS "http://127.0.0.1:8080/api/v1/fleet/timeseries?window=30m&limit=20"
curl -sS http://127.0.0.1:8080/api/v1/rag/status
```

它们分别回答：

- `/api/v1/status`：controller 进程和主要子系统是不是活着
- `/api/v1/fleet`：当前热状态里是否真的有 collector
- `/api/v1/fleet/timeseries`：指标历史是否已经在积累并暴露
- `/api/v1/rag/status`：检索是否启用、是否 ready、索引路径是什么

## 一条很短的 Cluster-Lite 路径

如果你的环境已经有 `kubectl`，当前仓库维护的最短集群路径就是 [`../../deploy/k8s/push-first/`](../../deploy/k8s/push-first/) 这套原始 manifest：

```bash
kubectl apply -k deploy/k8s/push-first
kubectl -n sre-agent port-forward deploy/sre-controller 8080:8080
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:8080/api/v1/status | jq '.deployment'
```

这条路径默认假设：

- 一个中心化 controller `Deployment`
- 每节点一个 collector pod 的 `DaemonSet`
- collector 身份通过 Kubernetes `spec.nodeName` 注入到 `SRE_COLLECTOR_ID` 和 `SRE_COLLECTOR_HOSTNAME`
- 首轮 rollout 可以接受本地文件型 RAG

如果你需要 HA、ingress 或外部向量后端，就继续看 [deployment.md](15-deployment.md)。

## 源码模式替代方案

如果你想直接从本地工作区运行：

```bash
make build
./scripts/run-local.sh --enable-agent
```

如果你只想启动带种子数据的 controller demo：

```bash
./scripts/run-local.sh --enable-agent --demo --llm=stub
```

这种模式适合的场景是：

- 你想先看 controller、UI 和 agent 界面，而不依赖 live collector
- 你想用确定性的 stub LLM，而不接真实 provider
- 你想先验证 prompt 和 workflow 的联动路径

## UI 验证与截图刷新

如果你改了调查控制台 UI，或者想刷新 README / UI 指南截图，仓库现在有一条明确的源码模式路径：

1. 先启动带种子数据和 stub LLM 的本地 demo controller：

```bash
./scripts/run-local.sh --enable-agent --demo --llm=stub
```

2. 再启动带 DevTools 的 headless Chrome：

```bash
google-chrome-stable --headless=new --disable-gpu --remote-debugging-port=9224 --no-sandbox about:blank
```

3. 最后用显式 warmup 和稳定等待执行截图脚本：

```bash
CAPTURE_WARMUP_MS=15000 CAPTURE_LIVE_WAIT_MS=30000 CAPTURE_STABILIZE_MS=12000 UI_URL=http://127.0.0.1:8080 node scripts/capture_readme_screenshots.mjs
```

这些等待存在的原因是：

- controller demo 需要时间把种子调查数据暴露出来
- UI 需要时间完成前端 fetch 和图表渲染
- 脚本现在会在页面 ready 之后继续等待，确保文档截图里看到的是稳定证据，而不是刚加载出的空壳

要核对每张截图应该对应哪个页面，可以继续读 [ui-guide.md](08-ui-guide.md)。

## 一组很快的低占用验证

启动后，建议顺手确认系统真的在按“低影响 steady state”运行，而不是把所有辅助路径都拉满：

```bash
curl -sS http://127.0.0.1:8080/metrics | grep -E 'collector_metrics_suppressed_count|collector_aux_payload_suppressed|collector_compat_payload_suppressed|agent_rag_skipped_context_total|agent_llm_bypassed'
curl -sS http://127.0.0.1:8080/api/v1/agent/status | jq '.query_service.analysis_reuse_enabled, .query_service.metrics.analysis_reused_total'
curl -sS http://127.0.0.1:8080/api/v1/agent/status | jq '.control_plane.triggered_trends, .control_plane.investigation_events, .control_plane.retrieval_skipped'
curl -sS http://127.0.0.1:8080/api/v1/agent/status | jq '.report_engine.report_suppressed_total, .report_engine.predictive_log_suppressed_total'
```

你希望看到的是：

- `collector_metrics_suppressed_count` 在平稳期经常大于 `0`
- `collector_aux_payload_suppressed` 会出现在 cache-hit 的日志 / 进程 helper 周期
- 如果 fallback 硬件扫描活跃且状态没变，`collector_compat_payload_suppressed{component="hardware"}` 会出现
- 当你问 `"what is happening here"` 这类泛化问题时，`agent_rag_skipped_context_total` 可以上升
- `analysis_reused_total` 只会在同一份压缩证据真的重复出现时增长
- 当 workflow engine 已经生成事件化证据后，`control_plane.triggered_trends` 和 `control_plane.investigation_events` 会变成非零
- `report_engine.report_suppressed_total` 在稳定 demo / canary 节点上应该逐步增长，这表示 legacy report 引擎正在原地刷新最新报告，而不是不断追加几乎相同的副本

## 最常见的首轮问题

| 现象 | 可能含义 | 下一步看哪里 |
| --- | --- | --- |
| `healthz` 失败 | controller 没有正常启动 | [部署](15-deployment.md)、[`configs/controller.yaml`](../../configs/controller.yaml) |
| controller 正常，但 `/api/v1/fleet` 为空 | collector 没有接入 ingest，或者还没有产生可用数据 | [`configs/container/collector.yaml`](../../configs/container/collector.yaml)、[数据流](05-data-flow.md) |
| RAG status 是 disabled | 如果你没有显式打开 RAG，这是正常的 | [数据集与 RAG](11-dataset-and-rag.md) |
| UI 能开，但回答只有 deterministic fallback | 如果 `llm_enabled: false` 或 telemetry stale，这是正常行为 | [Prompt 与定制](12-prompts-and-customization.md)、[常见问题](16-faq.md) |

## 关键文件

- [`configs/controller.yaml`](../../configs/controller.yaml)
- [`configs/collector.yaml`](../../configs/collector.yaml)
- [`configs/container/controller.yaml`](../../configs/container/controller.yaml)
- [`configs/container/collector.yaml`](../../configs/container/collector.yaml)
- [`deploy/k8s/push-first/`](../../deploy/k8s/push-first/)
- [`deploy/charts/sre-agent/`](../../deploy/charts/sre-agent/)
- [`.env.example`](../../.env.example)

## 下一步

- 如果你想看不同部署边界，继续看 [部署](15-deployment.md)
- 如果你想理解 RAG 数据路径，继续看 [数据集与 RAG](11-dataset-and-rag.md)
- 如果你想修改 prompt 行为，继续看 [Prompt 与定制](12-prompts-and-customization.md)
- 如果你想理解或刷新调查控制台截图，继续看 [UI 指南](08-ui-guide.md)
- 更完整的运维手册见 [operations/usage.md](../operations/usage.md)
