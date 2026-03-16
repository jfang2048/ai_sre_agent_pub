# 本项目中的 RAG 详解

这份文档不是在讲通用意义上的“什么是 RAG”，而是在解释 **AI SRE Agent 这个项目里，RAG 到底是什么、为什么要有、具体怎么工作、在系统里起什么作用**。

如果你只想先抓住一句话，可以这样理解：

> 本项目的 RAG 不是一个“把文档喂给 LLM”的附件，而是 controller 内部的一个本地优先知识引擎，用来把 runbook、历史故障、运维 FAQ、静态架构资料，转换成可以被 RCA / joint-risk / recommendation 工作流直接消费的结构化证据。

## 1. 是什么

在这个项目里，RAG 主要由 controller 侧的 `backend/internal/controller/rag/` 负责。

它做的事情可以拆成四层：

1. 发现知识源
2. 规范化知识
3. 建立可检索索引
4. 在工作流里按意图检索并返回证据

所以它不是单纯的全文搜索，也不是只靠向量相似度的“语义检索”。
它更像一个面向 SRE 场景的小型知识处理流水线。

## 2. 为什么这个项目需要 RAG

实时遥测只能回答一部分问题：

- 现在发生了什么
- 哪些指标异常了
- 哪个进程、GPU、磁盘、网卡最可疑

但值班和 RCA 还需要回答另外一些问题：

- 这种模式以前出现过吗
- 以前的根因通常是什么
- 哪份 runbook 最相关
- 哪些处理步骤是安全的，哪些是高风险的
- 某些症状是“正常噪声”还是“已知故障前兆”

这就是本项目引入 RAG 的原因。

本项目的 agent 并不把 LLM 当作第一层证据来源。顺序是：

1. 先拿 metrics / logs / security / topology 这些确定性证据
2. 再用 RAG 补充静态知识和历史案例
3. 最后才让 LLM 做综合解释和建议

这样做有两个直接好处：

- 避免 LLM 在缺少历史和 runbook 上下文时给出过于泛化的结论
- 避免把“检索到一段文本”误当成工程上的证明

## 3. 它回答什么，不回答什么

RAG 在这个项目里适合回答：

- 哪些 runbook 与当前症状最相关
- 有没有相似的历史 incident / postmortem
- 某类故障通常对应哪些信号组合
- 运维 FAQ、操作手册、参考资料里有哪些已知处理模式

RAG 不负责替代：

- 实时指标采集
- controller 的当前态和趋势态存储
- eBPF / probe-core 的低层观测
- 最终的根因判定
- 自动化动作的安全审计

换句话说，RAG 是“补证据”和“补记忆”，不是“替代事实”。

## 4. 本项目里的 RAG 架构

核心代码主要在这些位置：

- `backend/internal/controller/rag/service.go`
- `backend/internal/controller/rag/ingest.go`
- `backend/internal/controller/rag/knowledge.go`
- `backend/internal/controller/rag/index.go`
- `backend/internal/controller/rag/retriever.go`
- `backend/internal/controller/rag/vector_backend.go`
- `backend/internal/controller/rag/milvus.go`
- `backend/internal/controller/rag/embed.go`
- `backend/internal/controller/rag/util.go`
- `backend/internal/controller/rag/chunk.go`

对外接口主要在：

- `backend/internal/controller/rag_integration.go`
- `backend/cmd/ragctl/main.go`
- `frontend/src/api/rag.ts`

整体路径可以概括成：

```text
dataset / source paths
  -> source discovery
  -> parse / normalize
  -> chunk
  -> lexical index + vector index
  -> query planning
  -> retrieval / rerank
  -> evidence packaging
  -> agent workflow / UI / API
```

## 5. 为什么说它是“本地优先”

这个项目把 RAG 设计成 local-first，而不是“外部向量数据库优先”。

原因很务实：

- 运维场景不能把知识检索完全绑死在外部依赖上
- 控制面在受限网络、离线、演练环境里也要能工作
- 很多查询是路径名、错误码、命令、配置项、设备名，这类内容 lexical 检索很有效
- 即使外部 embedding 或 vector backend 出问题，也不应该把整个 agent 工作流一起拖死

因此当前实现采用：

- 默认本地索引
- 默认本地 embedding fallback
- 默认 `hybrid` 检索模式
- 可选外部 vector backend

如果外部向量后端不可用，系统会回退到本地检索，而不是直接让工作流失败。

## 6. 知识从哪里来

RAG 的输入不只是一堆 Markdown。

当前实现支持的知识来源大致包括：

- `json`
- `jsonl`
- `csv`
- `tsv`
- `md`
- `txt`
- 抽取后的 `html`
- `.zip` / `.zedx` 里的文本类内容

这些来源通常对应几类运维知识：

- runbook / playbook / manual / guide
- 历史 incident / postmortem / RCA
- 运维问答或 helpdesk 类型数据
- 安全参考资料
- 普通参考文档
- dataset 元信息

不适合直接参与 RAG 的内容会被 quarantine，而不是硬塞进索引里。
这很重要，因为很多二进制、图片、样式文件、无意义元文件只会污染检索结果。

## 6.1 这个仓库当前自带什么 dataset

当前仓库里已经跟踪的种子知识主要在：

- `dataset/raw/structured/question.jsonl`
- `dataset/raw/structured/helpdesk_dataset.csv`
- `dataset/raw/structured/aiops2024-challenge-dataset.json`
- `dataset/raw/archives/manifest.json`
- `dataset/raw/archives/README.md`

此外还有一类“本地可选大语料”，不放进 Git 跟踪树，而是放在：

- `data/bootstrap/datasets/archives/`

导入方式是：

```bash
scripts/bootstrap/manage_optional_datasets.sh import --from /path/to/archive-dir
```

然后通过 `SRE_AGENT_RAG_SOURCE_PATHS` 让 controller 在运行时把这些本地知识也纳入 ingestion。

## 7. ingestion 过程中到底做了什么

### 7.1 source discovery

`ingest.go` 会从 `dataset_path` 和额外的 `source_paths` 递归发现文件。

它会做几件事：

- 扫描目录或额外 source path
- 识别文件类型
- 对 zip / zedx 做安全抽取
- 为每个 source 生成签名和元信息
- 把无法处理的 source 记入 quarantine

### 7.2 normalization

解析出来的文档不会直接拿去切 chunk。
系统会先变成一个统一的 `SourceDocument` 结构。

这一步会尽量抽出运维相关字段，例如：

- `knowledge_type`
- `case_type`
- `summary`
- `symptoms`
- `evidence`
- `likely_causes`
- `remediation_steps`
- `commands`
- `environment`
- `signals`
- `retrieval_text`
- `embedding_text`
- `retrieval_weight`

这也是本项目 RAG 和“普通全文检索”最不一样的地方：

- 它不只是保留原文
- 它会尽量把文档里的“症状、证据、原因、处理步骤”提炼出来
- 检索命中的结果因此更适合直接喂给 RCA 和 recommendation 流程

### 7.3 classification

`knowledge.go` 会根据路径、标题、内容、结构字段等信息，把文档归类为：

- `runbook`
- `historical_incident`
- `question_pattern`
- `security_reference`
- `reference`
- `dataset_meta`

同时还会给出 `case_type`，例如：

- `runbook`
- `historical_incident`
- `operational_qa`
- `security_event`
- `reference`

这一步的意义是让后面的检索和 rerank 更“懂上下文”，而不是所有文档一视同仁。

### 7.4 chunking

当前 chunk 策略不只一种：

- `paragraph`
- `markdown`
- `line`
- `record`
- `case`
- `auto`

其中 `auto` 很关键。
如果一个文档已经长得像 runbook、故障案例或运维问答，系统会优先选择 `case` 风格切分，而不是简单按字数切块。

这样做的收益是：

- 检索结果更容易命中完整的“原因 + 步骤 + 证据”片段
- 不容易把一个 incident 的上下文切碎
- 更适合 UI 展示和 agent 证据拼装

## 7.5 如果我想修改 dataset，应该怎么做

这套 RAG 不靠“写一个 prompt 去训练知识库”，而是**直接吃文件**。

所以最正确的修改方式通常是：

- 直接编辑 `dataset/` 里的结构化文件
- 新增 Markdown / TXT runbook
- 通过额外 source path 接入你自己的私有文档目录

如果你希望抽取效果更好，建议内容写法贴近当前解析器：

- 结构化数据优先使用 `title`、`query`、`question`、`summary`、`document`、`service`、`topic`、`timestamp` 这类字段
- Markdown / 文本 runbook 最好明确写出 Symptoms、Evidence、Likely Causes、Remediation Steps、Commands 之类的段落
- 一条 JSONL / CSV 记录尽量只表达一个问题模式、一个 runbook 条目或一个历史案例
- 多写具体错误字符串、命令、组件名、路径名，而不是只写泛化描述

简单说：

- 想让 lexical 更准，就多给精确术语
- 想让 structured `case` chunking 更准，就把症状/证据/步骤写清楚

## 7.6 改完之后怎么更新

最常用的是这几个命令：

```bash
make rag-status
make rag-query QUERY="gpu timeout after rollout"
make rag-update
make rag-rebuild
```

区别是：

- `make rag-update`
  - 增量更新
  - 会按 source signature 复用没变的 source、doc、chunk
  - 适合日常改几个文件后刷新
- `make rag-rebuild`
  - 全量重建
  - 适合大规模改动、chunking 参数变动、怀疑缓存/抽取状态不一致时

对应的 CLI / API 也有：

```bash
go -C backend run ./cmd/ragctl update
go -C backend run ./cmd/ragctl rebuild
curl -sS -X POST http://127.0.0.1:8080/api/v1/rag/index/update
curl -sS -X POST http://127.0.0.1:8080/api/v1/rag/index/rebuild
```

## 8. 索引是怎么建的

当前实现不是只建一个向量索引。
而是两套能力并行：

- lexical index
- vector index

### 8.1 lexical index

`index.go` 会把 chunk 的标题、摘要、检索文本做 token 化，然后构建倒排索引。

这一层特别适合：

- 错误码
- 路径名
- 配置键
- 命令
- 设备名
- 精确术语

在运维场景里，这些精确字符串非常常见，所以 lexical 不只是“fallback”，而是核心路径之一。

### 8.2 vector index

向量检索主要解决“表达不同但语义相近”的情况。

例如：

- 两份 runbook 用词不同，但描述的是同一类 GPU feeder starvation
- 两个历史 incident 的日志关键字不一致，但症状组合很相似

当前实现支持：

- 本地 embedding / 本地向量逻辑
- 可选外部 vector backend
- Milvus 风格后端抽象

### 8.3 hybrid 模式

默认检索模式是 `hybrid`。

这是因为：

- lexical 擅长精确匹配
- vector 擅长语义相近
- 混合模式对真实 SRE 查询更稳

很多值班查询同时包含“明确术语 + 模糊意图”，单一路径通常不够好。

## 9. 查询时到底发生了什么

当 controller 收到一次 RAG 查询时，大致流程是：

1. 构建 query plan
2. 标准化 query
3. 推断 intent
4. 做 token 化和 query expansion
5. 跑 lexical / vector / hybrid 检索
6. 根据 knowledge type / case type / intent 做 rerank
7. 生成 evidence package
8. 返回给 API、UI 或 agent workflow

### 9.1 intent 为什么重要

当前实现支持的 intent 包括：

- `general`
- `runbook`
- `historical_incident`
- `rca`
- `joint_risk`
- `recommendation`
- `security`

intent 的作用不是“魔法提示词”，而是让检索更有边界感，例如：

- 更偏向 runbook
- 更偏向历史案例
- 更偏向安全知识
- 对某些类型做过滤或加权

这能减少“检索到一堆看起来相关但工作流其实用不上”的噪声。

### 9.2 查询结果为什么是结构化的

本项目返回的不是一段裸文本，而是更完整的证据对象。

每个 hit 可以包含：

- `doc_id`
- `chunk_id`
- `score`
- `source_path`
- `source_type`
- `knowledge_type`
- `case_type`
- `summary`
- `snippet`
- `likely_causes`
- `remediation_steps`
- `commands`
- `signals`

这么设计的原因是：

- agent 工作流需要“可消费字段”，不是大段原文
- UI 需要向值班工程师解释“为什么推荐这条证据”
- recommendation / RCA 需要区分“症状”、“原因”、“处理步骤”

## 9.3 如果我想手动 query，应该写什么 prompt

如果你是直接调用 `POST /api/v1/rag/query`，其实不需要复杂 prompt。
你只需要提供一个像值班工程师真实会问的问题，再加上合适的 `intent`。

例子：

- `gpu timeout after rollout`
  - `intent=rca`
- `checkpoint burst stalls training workers`
  - `intent=runbook`
- `nvme queue saturation high iowait`
  - `intent=historical_incident`
- `certificate rotation broke collector handshake`
  - `intent=recommendation`

也就是说，这里更像“带路由信息的检索请求”，不是长篇 prompt engineering。

如果你要缩小范围，还可以加：

- `knowledge_types`
- `case_types`
- `source_types`

## 10. RAG 如何接入 agent 工作流

本项目不是先问 LLM 再决定是否检索，而是把 RAG 显式放进工作流工具链中。

典型工具包括：

- `rag_query`
- `historical_incident_retrieval`
- `runbook_retrieval`
- `similar_case_retrieval`

这些结果会进入：

- potential-risk
- joint-risk
- RCA
- recommendation
- context bundle

工作流里会记录类似这些状态：

- `retrieved_docs`
- `retrieved_cases`
- `retrieved_runbooks`
- `similar_incident_patterns`
- `retrieval_summary`
- `retrieval_confidence`
- `retrieval_evidence_ids`

所以从系统设计上讲，RAG 在这里不是“附加查询框”，而是 agent reasoning pipeline 的一部分。

## 10.1 那真正喂给 LLM 的 prompt 里有什么

当前主要有两条 prompt 路径：

### 1. `/api/v1/agent/query`

`backend/internal/controller/agentcore/prompts.go` 会构造：

- system prompt
  - 要求模型只能使用给定 telemetry facts
  - 不允许编造 metrics 或命令输出
  - 必须返回固定 JSON 字段
- user prompt
  - 包含 telemetry quality
  - 包含 metrics / trends / process summaries / log fingerprints
  - 包含 RAG snippets
  - 包含 retrieval summary
  - 包含 retrieval intent / retrieval mode
  - 最多选几条 retrieved docs，把其中的 summary、causes、steps 这些结构化字段拼进去

所以这里不是把整篇 runbook 原文硬塞给模型。
而是先做 retrieval，再把提炼后的知识证据放进 prompt。

### 2. workflow LLM analysis

`backend/internal/controller/agentcore/llm_analysis.go` 里还有工作流分析 prompt。

这条路径会：

- 给模型一个严格 JSON schema
- 把 context bundle 作为证据包传进去
- 明确声明 logs、retrieved documents、free-form snippets 都是 untrusted data

这一点很关键，因为它降低了 prompt injection 和“文档里写了错误操作建议就被盲信”的风险。

## 11. 为什么它对 UI 也重要

RAG 的价值不只是给 agent 用。

前端也需要把这些知识以可审计方式展示出来。
当前前端 API 对应在 `frontend/src/api/rag.ts`，核心接口包括：

- `GET /api/v1/rag/status`
- `POST /api/v1/rag/query`
- `POST /api/v1/rag/index/rebuild`
- `POST /api/v1/rag/index/update`
- `GET /api/v1/rag/doc/{id}`

这些接口让操作员可以看到：

- RAG 是否启用
- 索引是否 ready
- 数据集路径和索引路径
- 文档、chunk、source、quarantine 数量
- 当前 retrieval mode / embedding provider
- 命中的证据详情

这符合本项目一贯的设计原则：

- 知识来源必须可见
- 检索结果必须可解释
- 降级状态必须可观察

## 12. 配置项怎么理解

RAG 相关配置既可以在配置文件里写，也可以通过环境变量覆盖。

常用项包括：

- `enabled`
- `dataset_path`
- `source_paths`
- `index_path`
- `top_k`
- `max_snippet_chars`
- `chunk_size`
- `chunk_overlap`
- `chunk_strategy`
- `retrieval_mode`
- `embedding_provider`
- `embedding_model`
- `vector_backend`
- `vector_endpoint`
- `vector_collection`
- `rebuild_policy`

常见运行思路：

- 开发/演示环境：本地 embedding + 本地索引 + `manual` rebuild
- 小规模生产：`hybrid` 检索，索引持久化到 controller 本地磁盘
- 更大规模知识库：保留 controller 侧规范化和 query plan，把向量层接到外部 backend

## 13. 它怎么保证“可用”而不是“看起来高级”

本项目对 RAG 的要求不是学术最强，而是运维上可靠。

所以它有几条明确策略：

- RAG disabled 时，controller 和 agent 仍能继续走 deterministic 路径
- 外部 vector backend 挂了，回退到本地检索
- 索引不存在时，可以按策略 rebuild
- 结果为空时，不把整个 RCA 流程直接判死
- 命中的知识只算支持证据，不算最终证明

这跟很多“把向量库接上就算完成”的项目很不一样。

## 14. 它和前面 telemetry / RCA 架构的关系

可以把整个系统理解成三层：

1. **观测层**：collector、probe-core、eBPF、ingest
2. **状态层**：hot state、history、logs、inventory、security
3. **知识层**：RAG

然后 agent 在这三层之上做：

- 证据收集
- 风险判断
- RCA 解释
- 受控建议

这里最重要的一点是：

**RAG 不替代观测层，而是给观测层增加“历史和经验”维度。**

如果没有观测层，RAG 会变成空泛知识问答。
如果没有 RAG，系统又容易在 runbook 和记忆层面失忆。

## 15. 现在的限制是什么

当前 RAG 路径有明确边界：

- 文档规范化带有启发式，不是严格领域本体
- 本地 embedding 是轻量确定性实现，不追求最强语义表达
- 数据质量仍高度依赖输入文档本身
- 某些 FAQ/案例数据可能问题写得很好，但答案很弱
- 检索命中不等于根因成立

## 15.1 常见问题

### 我应该先改 prompt，还是先改 dataset？

先改 dataset。
对这个项目来说，RAG 质量首先取决于知识内容和结构，而不是 prompt 花样。

### 我怎么确认新文档真的进索引了？

看这些地方：

- `make rag-status`
- `make rag-query QUERY="..."`
- `GET /api/v1/rag/status`
- `GET /api/v1/rag/doc/{id}`
- `data/agent/rag/quarantine.json`

### 什么时候该用 extra source paths？

当知识是：

- 私有环境特定文档
- 很大，不适合提交到仓库
- 发布时不希望放进公开 tree

这时就用 `SRE_AGENT_RAG_SOURCE_PATHS`。

### 一定要外部向量库吗？

不一定。
这个项目的设计就是 local-first，外部 vector backend 是扩展项，不是前提条件。

这些限制不是缺陷隐藏，而是为了让系统行为更可预期。

## 16. 在这个项目里，应该怎样正确理解 RAG

最合适的理解方式是：

- 它不是聊天机器人记忆
- 它不是替代 TSDB / logs / topology 的数据面
- 它不是把文档原文拼进 prompt 的简单插件
- 它是一个把“静态知识 + 历史案例”转成结构化运行证据的 controller 子系统

如果要从产品角度总结，它的职责是：

**让 AI SRE Agent 不只知道“机器现在怎么了”，还知道“这种情况以前通常意味着什么，以及下一步通常该怎么查”。**

## 17. 推荐阅读顺序

如果你想继续沿着代码往下看，建议顺序是：

1. `docs/reference/rag_knowledge_engine.md`
2. `backend/internal/controller/rag/service.go`
3. `backend/internal/controller/rag/ingest.go`
4. `backend/internal/controller/rag/knowledge.go`
5. `backend/internal/controller/rag/index.go`
6. `backend/internal/controller/rag/retriever.go`
7. `backend/internal/controller/rag_integration.go`
8. `frontend/src/api/rag.ts`

如果你更关心“它怎么和 agent 结合”，再继续看：

- `backend/internal/controller/agent/`
- `backend/internal/controller/agentcore/`

这样会更容易把“检索服务本身”和“上层工作流消费方式”区分开。
