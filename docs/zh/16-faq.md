# 常见问题

English version: [docs/en/16-faq.md](../en/16-faq.md)

## 这是单个二进制项目吗？

不是。当前维护中的运行时模型是 `collector` + `controller` 分离。

## 如果我对代码库不熟，应该先读什么？

建议顺序：

1. [概览](01-overview.md)
2. [快速开始](03-getting-started.md)
3. [架构](04-architecture.md)
4. [数据流](05-data-flow.md)
5. [核心文件](10-core-files.md)

## 推荐的本地启动方式是什么？

使用容器优先的 host-observer 路径：

```bash
cp .env.example .env
make container-build
make container-up-host-observer
```

## 一定要外部向量数据库吗？

不需要。默认 RAG 是 local-first，外部向量同步只是可选能力。

## 一定要配置 LLM 吗？

不需要。仓库默认配置里 `llm_enabled: false`。

## 为什么 RAG 或 LLM 没开，UI 仍然能工作？

因为 UI 首先依赖的是 controller ingest 和 API 热状态，而不是 retrieval 或模型调用。

一个完全有效的首轮启动可以是：

- controller 健康
- fleet 里已经有节点
- RAG 关闭
- LLM 关闭

这依然是一个正确的可观测性部署。retrieval 和模型推理只是叠加在它之上的可选层。

## RAG 数据从哪里来？

来自仓库跟踪的 [`dataset/`](../../dataset/) 目录，以及 `rag_source_paths` / `SRE_AGENT_RAG_SOURCE_PATHS` 指向的额外目录。

## Prompt 放在哪里？

主要在 Go 代码里，不是独立 prompt 文件。起点通常是：

- [`backend/internal/controller/agentcore/prompts.go`](../../backend/internal/controller/agentcore/prompts.go)
- [`backend/internal/controller/agentcore/llm_analysis.go`](../../backend/internal/controller/agentcore/llm_analysis.go)
- [`backend/internal/controller/analysis/llm_client.go`](../../backend/internal/controller/analysis/llm_client.go)

## 运行时数据落到哪里？

源码模式默认值下：

- collector 数据在 `./data/collector/`
- controller ingest 数据在 `./data/controller/`
- agent 和 RAG 数据在 `./data/agent/`

容器模式下，这些路径会按容器配置迁移到 `/var/lib/ai-sre-agent/...`。

## 改完 dataset 后怎么刷新 RAG 索引？

使用：

```bash
make rag-update
```

如果你改了较多 ingestion 设置，建议改用 `make rag-rebuild`。

## 如果本地 RAG 索引坏了，会发生什么？

controller 现在不会再盲目信任它。

- 无效索引会在启动时被 quarantine
- 运行状态里会保留告警信息
- 后续行为取决于 `rag_rebuild_policy`

建议继续看：

- [数据集与 RAG](11-dataset-and-rag.md)
- [部署](15-deployment.md)

## 为什么 agent 可能会同时跳过 RAG 和 LLM？

当 telemetry stale 或缺失时，query-service 可以直接绕过昂贵路径。

这由下面这些配置控制：

- `skip_llm_on_stale_telemetry`
- `skip_llm_on_no_telemetry`

在这种情况下，系统会返回 deterministic fallback，而不是装作有高置信度。这是有意的 host-first 行为，不是静默故障。

## 下一步建议看什么？

- [快速开始](03-getting-started.md)
- [架构](04-architecture.md)
- [数据集与 RAG](11-dataset-and-rag.md)
- [Prompt 与定制](12-prompts-and-customization.md)

## 一个成功的端到端检查应该长什么样？

一个实用的检查方式是：

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/api/v1/status
curl -sS http://127.0.0.1:8080/api/v1/rag/status
```

然后：

- 打开 `http://127.0.0.1:8080/`
- 确认 UI 能正常加载
- 确认 RAG 是启用状态，或者是有意关闭
- 先确认 controller 响应正常，再去排查 collector 或 prompt 问题
