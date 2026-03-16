# 概览

English version: [docs/en/01-overview.md](../en/01-overview.md)

AI SRE Agent 是一个面向 AI/GPU 基础设施的 push-first 可观测性与受控自动化项目。

理解这个项目，最重要的一句话是：

> collector 是主机侧的低影响观察者，controller 是更重的控制面，负责存储、分析、检索上下文并服务人类使用者。

在当前控制面路径里，controller 已经不再把原始 metrics 直接塞进 prompt。它会先生成结构化中间证据：

- `TrendAssessment[]`：描述单指标漂移、持续越阈和短期 forecast hint
- `InvestigationEvent[]`：描述多变量弱信号融合后的调查事件
- `RetrievalDecision[]`：显式记录 retrieval 为什么执行、跳过或被抑制

如果你想先读一篇“从第一条采样数据一路讲到最终响应”的完整真实说明，请直接看：

- [Pipeline 深度解析](02-pipeline-deep-dive.md)

如果你在那之后还想看更聚焦的子系统说明，请继续读：

- [采集队列与压缩](06-collector-queue-and-compaction.md)
- [控制平面分析](07-control-plane-analysis.md)

仓库围绕两个维护中的运行时角色组织：

- `collector`
  - 运行在被观测主机上或其附近
  - 采集主机、进程、内核、GPU 和日志信号
  - 在 controller 短时不可用时通过本地 spool 做缓冲和重放
- `controller`
  - 通过 gRPC 接收遥测
  - 提供 HTTP API 和 Web UI
  - 保存当前态和可选历史
  - 运行 RAG 和 agent 工作流

同样的 split 现在也通过显式部署模式暴露出来：

- `local-dev`：仓库相对路径和本地调试假设
- `standalone`：一套 controller 服务加若干外部 collector
- `cluster-lite`：单 controller `Deployment` 加 collector `DaemonSet`
- `distributed`：多副本 controller 加共享 HA/存储后端

这些模式的实际实现位置：

- [`../../backend/internal/collector/deployment.go`](../../backend/internal/collector/deployment.go)
- [`../../backend/internal/controller/deployment.go`](../../backend/internal/controller/deployment.go)
- [`../../configs/controller.yaml`](../../configs/controller.yaml)
- [`../../configs/collector.yaml`](../../configs/collector.yaml)

## 新读者应该预期什么

这个仓库已经具备：

- collector/controller 分离运行时
- 真实主机观测路径
- controller 侧 RAG 与 prompt 组装
- Web UI 和 HTTP API
- 本地、Docker、Kubernetes、Helm、systemd 的部署资产

这个仓库不会自动替你提供：

- 托管控制面
- 完整的发布服务
- 适配你环境的精炼生产 runbook 语料
- 无需操作员设置就能在所有主机上拿到完整 eBPF / probe-core 可见性

还有一个很重要但容易被忽略的设计选择：

- collector 的 steady state 优先追求低占用，所以不变的 low-churn 状态、helper 缓存命中的 payload、compatibility 硬件层 cache-hit payload 都会被抑制，而不是每个周期都重发
- controller 的推理路径优先追求选择性，所以当 telemetry stale、为空、最近成功分析可安全复用、或者症状上下文太弱时，会主动跳过 RAG 或 LLM

## 仓库里有什么

- `backend/`
  - collector、controller、ingest、RAG、API、workflow 的 Go 实现
- `cpp/`
  - collector 使用的原生 probe-core 运行时
- `frontend/`
  - Web UI
- `configs/`
  - 源码模式和容器模式配置
- `dataset/`
  - controller 侧 RAG 的种子知识库
- `deploy/`
  - Docker、Kubernetes、Helm、systemd 部署资产

## 主要数据流

```text
probe-core + eBPF -> collector -> local spool -> gRPC ingest -> controller state/history/RAG -> API/UI/agent workflows
```

collector 是主机侧观察者，controller 是控制面。

## 三个更具体的场景

### 场景 1：GPU 节点在 rollout 之后变慢

系统预期应该做的是：

1. 在主机上采到 CPU、内存、磁盘、网络、进程和 GPU 证据
2. 在不依赖 controller 实时可达的前提下把数据送过去
3. 判断这些 telemetry 是否足够新鲜和值得信任
4. 如有需要，检索相似案例或 runbook 片段
5. 给出一个有边界的、面向运维的解释

下一步建议读：

- [数据流](05-data-flow.md)
- [指标与信号](13-metrics-and-signals.md)
- [Prompt 与定制](12-prompts-and-customization.md)

### 场景 2：你想把内置 dataset 换成自己的 runbook

系统预期应该做的是：

1. 从 `dataset_path` 发现文件
2. 规范化并分类
3. 切成可检索 chunk
4. rebuild 或 update 本地索引

下一步建议读：

- [数据集与 RAG](11-dataset-and-rag.md)
- [核心文件](10-core-files.md)

### 场景 3：你想知道为什么 agent 跳过了 LLM

系统预期应该做的是：

1. 检查 telemetry 的 freshness 和 coverage
2. 当证据 stale 或缺失时跳过昂贵推理
3. 返回 deterministic fallback，而不是假装有高置信度

下一步建议读：

- [数据流](05-data-flow.md)
- [部署](15-deployment.md)
- [常见问题](16-faq.md)

## 一个具体例子

如果某个 GPU 训练节点在一次 rollout 之后变慢，项目预期中的路径是：

1. [`../../cpp/probe_core/main.cpp`](../../cpp/probe_core/main.cpp) 和 collector 先抓到主机、进程、GPU 压力信号。
2. [`../../backend/internal/controller/ingest/server.go`](../../backend/internal/controller/ingest/server.go) 接收 batch 并写入 controller store。
3. [`../../backend/internal/controller/telemetry_quality.go`](../../backend/internal/controller/telemetry_quality.go) 判断这些证据是否足够新鲜、完整和值得信任。
4. [`../../backend/internal/controller/agentcore/workflow_eventization.go`](../../backend/internal/controller/agentcore/workflow_eventization.go) 会先把这些状态转成 trend assessment、investigation event 和 retrieval decision。
5. [`../../backend/internal/controller/rag/service.go`](../../backend/internal/controller/rag/service.go) 可以从 [`../../dataset/`](../../dataset/) 中检索相似案例或 runbook 片段。
6. [`../../backend/internal/controller/agentcore/prompts.go`](../../backend/internal/controller/agentcore/prompts.go) 和 [`../../backend/internal/controller/agent/engine.go`](../../backend/internal/controller/agent/engine.go) 把这些证据变成面向运维的回答或报告。

如果操作员在同一份压缩证据没有变化的情况下反复提问，[`../../backend/internal/controller/agentcore/agent.go`](../../backend/internal/controller/agentcore/agent.go) 现在还可以复用最近一次成功分析，直接跳过重复的 retrieval 和模型调用。

这也是仓库为什么要拆成 collector、controller、dataset/RAG 和 UI，而不是一个大二进制。

## 推荐阅读顺序

1. [快速开始](03-getting-started.md)
2. [架构](04-architecture.md)
3. [Pipeline 深度解析](02-pipeline-deep-dive.md)
4. [数据流](05-data-flow.md)
5. [采集队列与压缩](06-collector-queue-and-compaction.md)
6. [控制平面分析](07-control-plane-analysis.md)
7. [UI 指南](08-ui-guide.md)
8. [代码库地图](09-codebase-map.md)
9. [核心文件](10-core-files.md)
10. [数据集与 RAG](11-dataset-and-rag.md)
11. [Prompt 与定制](12-prompts-and-customization.md)
12. [指标与信号](13-metrics-and-signals.md)
13. [硬件注意事项](14-hardware-considerations.md)
14. [部署](15-deployment.md)
15. [常见问题](16-faq.md)

## 如果你只有五分钟

建议按这个顺序看：

1. [快速开始](03-getting-started.md)，先知道仓库怎么真正跑起来
2. [架构](04-architecture.md)，理解为什么要拆成两个运行时
3. [Pipeline 深度解析](02-pipeline-deep-dive.md)，先把“从第一条采样到最终答案”的真实链路看完整
4. [数据流](05-data-flow.md)，再看更具体的 metric-to-answer walkthrough
5. [UI 指南](08-ui-guide.md)，看这些中间证据最后如何呈现在调查控制台里

## 深入参考

需要更细的操作、配置或 API 级说明时，请继续看：

- [运行指南](../operations/usage.md)
- [配置说明](../operations/configuration.md)
- [架构说明](../design/architecture.md)
- [API 参考](../reference/api.md)
- [指标参考](../reference/metrics.md)
- [RAG 参考](../reference/rag_knowledge_engine_zh.md)
- [LLM Schema 参考](../reference/llm_schema.md)
