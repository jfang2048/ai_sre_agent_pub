# Documentation

English guides: [Overview](en/01-overview.md) | [Pipeline Deep Dive](en/02-pipeline-deep-dive.md) | [Getting Started](en/03-getting-started.md) | [Architecture](en/04-architecture.md) | [Data Flow](en/05-data-flow.md) | [Collector Queue and Compaction](en/06-collector-queue-and-compaction.md) | [Control-Plane Analysis](en/07-control-plane-analysis.md) | [UI Guide](en/08-ui-guide.md) | [Codebase Map](en/09-codebase-map.md) | [Core Files](en/10-core-files.md) | [Dataset and RAG](en/11-dataset-and-rag.md)

中文文档: [概览](zh/01-overview.md) | [Pipeline 深度解析](zh/02-pipeline-deep-dive.md) | [快速开始](zh/03-getting-started.md) | [架构](zh/04-architecture.md) | [数据流](zh/05-data-flow.md) | [采集队列与压缩](zh/06-collector-queue-and-compaction.md) | [控制平面分析](zh/07-control-plane-analysis.md) | [UI 指南](zh/08-ui-guide.md) | [代码库地图](zh/09-codebase-map.md) | [核心文件](zh/10-core-files.md) | [数据集与 RAG](zh/11-dataset-and-rag.md)

This directory has two layers:

- concise bilingual guides under [`en/`](en/) and [`zh/`](zh/)
- deeper operations, design, reference, and security material under the existing `operations/`, `reference/`, `design/`, and `security/` trees

## Start Here By Goal

| If you want to... | Read this first | Then read |
| --- | --- | --- |
| understand the project quickly without reading code first | [en/01-overview.md](en/01-overview.md) / [zh/01-overview.md](zh/01-overview.md) | [en/02-pipeline-deep-dive.md](en/02-pipeline-deep-dive.md) / [zh/02-pipeline-deep-dive.md](zh/02-pipeline-deep-dive.md) |
| get a local stack running | [en/03-getting-started.md](en/03-getting-started.md) / [zh/03-getting-started.md](zh/03-getting-started.md) | [en/15-deployment.md](en/15-deployment.md) / [zh/15-deployment.md](zh/15-deployment.md) |
| deploy in one cluster or plan a distributed rollout | [en/15-deployment.md](en/15-deployment.md) / [zh/15-deployment.md](zh/15-deployment.md) | [operations/configuration.md](operations/configuration.md), [reference/api.md](reference/api.md) |
| understand the runtime split | [en/04-architecture.md](en/04-architecture.md) / [zh/04-architecture.md](zh/04-architecture.md) | [en/02-pipeline-deep-dive.md](en/02-pipeline-deep-dive.md) / [zh/02-pipeline-deep-dive.md](zh/02-pipeline-deep-dive.md) |
| understand queueing, suppression, and send-path cost | [en/06-collector-queue-and-compaction.md](en/06-collector-queue-and-compaction.md) / [zh/06-collector-queue-and-compaction.md](zh/06-collector-queue-and-compaction.md) | [en/13-metrics-and-signals.md](en/13-metrics-and-signals.md) / [zh/13-metrics-and-signals.md](zh/13-metrics-and-signals.md) |
| understand trend analysis, weak-signal fusion, and recommendation output | [en/07-control-plane-analysis.md](en/07-control-plane-analysis.md) / [zh/07-control-plane-analysis.md](zh/07-control-plane-analysis.md) | [en/11-dataset-and-rag.md](en/11-dataset-and-rag.md), [en/12-prompts-and-customization.md](en/12-prompts-and-customization.md) / [zh/11-dataset-and-rag.md](zh/11-dataset-and-rag.md), [zh/12-prompts-and-customization.md](zh/12-prompts-and-customization.md) |
| trace one incident end to end | [en/02-pipeline-deep-dive.md](en/02-pipeline-deep-dive.md) / [zh/02-pipeline-deep-dive.md](zh/02-pipeline-deep-dive.md) | [en/10-core-files.md](en/10-core-files.md) / [zh/10-core-files.md](zh/10-core-files.md) |
| modify dataset, retrieval, or prompts | [en/11-dataset-and-rag.md](en/11-dataset-and-rag.md), [en/12-prompts-and-customization.md](en/12-prompts-and-customization.md) / [zh/11-dataset-and-rag.md](zh/11-dataset-and-rag.md), [zh/12-prompts-and-customization.md](zh/12-prompts-and-customization.md) | [en/10-core-files.md](en/10-core-files.md) / [zh/10-core-files.md](zh/10-core-files.md) |

## Guide Set

| Topic | English | 中文 |
| --- | --- | --- |
| Overview | [en/01-overview.md](en/01-overview.md) | [zh/01-overview.md](zh/01-overview.md) |
| Pipeline deep dive | [en/02-pipeline-deep-dive.md](en/02-pipeline-deep-dive.md) | [zh/02-pipeline-deep-dive.md](zh/02-pipeline-deep-dive.md) |
| Getting started | [en/03-getting-started.md](en/03-getting-started.md) | [zh/03-getting-started.md](zh/03-getting-started.md) |
| Architecture | [en/04-architecture.md](en/04-architecture.md) | [zh/04-architecture.md](zh/04-architecture.md) |
| Data flow | [en/05-data-flow.md](en/05-data-flow.md) | [zh/05-data-flow.md](zh/05-data-flow.md) |
| Collector queue and compaction | [en/06-collector-queue-and-compaction.md](en/06-collector-queue-and-compaction.md) | [zh/06-collector-queue-and-compaction.md](zh/06-collector-queue-and-compaction.md) |
| Control-plane analysis | [en/07-control-plane-analysis.md](en/07-control-plane-analysis.md) | [zh/07-control-plane-analysis.md](zh/07-control-plane-analysis.md) |
| UI guide | [en/08-ui-guide.md](en/08-ui-guide.md) | [zh/08-ui-guide.md](zh/08-ui-guide.md) |
| Codebase map | [en/09-codebase-map.md](en/09-codebase-map.md) | [zh/09-codebase-map.md](zh/09-codebase-map.md) |
| Core files | [en/10-core-files.md](en/10-core-files.md) | [zh/10-core-files.md](zh/10-core-files.md) |
| Dataset and RAG | [en/11-dataset-and-rag.md](en/11-dataset-and-rag.md) | [zh/11-dataset-and-rag.md](zh/11-dataset-and-rag.md) |
| Prompts and customization | [en/12-prompts-and-customization.md](en/12-prompts-and-customization.md) | [zh/12-prompts-and-customization.md](zh/12-prompts-and-customization.md) |
| Metrics and signals | [en/13-metrics-and-signals.md](en/13-metrics-and-signals.md) | [zh/13-metrics-and-signals.md](zh/13-metrics-and-signals.md) |
| Hardware considerations | [en/14-hardware-considerations.md](en/14-hardware-considerations.md) | [zh/14-hardware-considerations.md](zh/14-hardware-considerations.md) |
| Deployment | [en/15-deployment.md](en/15-deployment.md) | [zh/15-deployment.md](zh/15-deployment.md) |
| FAQ | [en/16-faq.md](en/16-faq.md) | [zh/16-faq.md](zh/16-faq.md) |

## Detailed Reference

- [Operations usage](operations/usage.md)
- [Configuration reference](operations/configuration.md)
- [Testing guide](operations/testing.md)
- [Architecture notes](design/architecture.md)
- [Collector overhead audit](design/collector_overhead_audit.md)
- [API reference](reference/api.md)
- [Metrics reference](reference/metrics.md)
- [Predictive signals](reference/predictive_signals.md)
- [RAG reference](reference/rag_knowledge_engine.md)
- [RAG reference (Chinese)](reference/rag_knowledge_engine_zh.md)
- [LLM schema reference](reference/llm_schema.md)
- [Threat model](security/threat-model.md)
