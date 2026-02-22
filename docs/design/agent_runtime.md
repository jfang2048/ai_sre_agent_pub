# Agent Runtime (v2) / Agent 编排运行时

This document describes the production Python reasoning runtime based on Haystack pipelines.

本文档描述基于 Haystack 管道的生产级 Python 推理运行时。

## Goals / 目标

- Use an industrial pipeline framework with explicit control flow / 使用工业级管道框架并保持显式控制流
- Keep deterministic behavior, strong typing, and debuggability / 保持确定性行为、强类型和可调试性
- Preserve controller/API integration contracts / 保持与 controller/API 的集成契约兼容

## Runtime Modules / 运行时模块

| Module | Responsibility | Code path |
|---|---|---|
| Planner | Build deterministic per-request plan | `python/sre_agent/runtime/planner.py` |
| Context store | Haystack BM25 retrieval (with deterministic lexical fallback) / Haystack BM25 检索（含确定性回退） | `python/sre_agent/runtime/context_store.py` |
| Tool components | Typed tool execution with timeout+retry in Haystack components | `python/sre_agent/runtime/haystack_runtime.py` |
| Plan state component | Build ordered traces and runtime state from tool outputs | `python/sre_agent/runtime/haystack_runtime.py` |
| Memory store | Bounded incident memory with optional JSONL persistence | `python/sre_agent/runtime/memory_store.py` |
| Reasoner | Deterministic synthesis + optional LLM refinement | `python/sre_agent/runtime/reasoner.py` |
| Orchestrator | Backend selection (`haystack`/`native`) and end-to-end lifecycle | `python/sre_agent/runtime/orchestrator.py` |

## Execution Flow / 执行流程

1. Build `TelemetryEnvelope` with request ID and bounded telemetry slice.
2. Planner emits deterministic steps.
3. Haystack pipeline executes tool steps and emits typed `StepTrace` for each planned tool.
4. State builder component assembles successful outputs and required-step failures.
5. Reasoning component synthesizes baseline decision and optionally refines with LLM strict JSON contract.
6. Decision and trace metadata are returned as `AnalysisResult`.
7. Detected incidents are persisted into bounded memory for node-local recall.

## Fault Tolerance / 容错设计

- Tool-level timeout and retries (`SRE_AGENT_TOOL_TIMEOUT_SECONDS`, `SRE_AGENT_TOOL_RETRIES`)
- Non-critical tool failures do not abort the full request
- LLM refinement is optional and always fails safe to deterministic output
- Runtime backend can fail open to native executor when Haystack is unavailable (`SRE_AGENT_RUNTIME_FAIL_OPEN=true`)
- Memory persistence failures are logged and do not block analysis

## Observability and Debugging / 可观测性与可调试性

- Structured JSON logs for orchestration start/end and each tool step
- `request_id` and `runtime_backend` in logs/metadata for correlation
- `execution_trace` returned in analysis metadata for post-incident debugging
- Explicit step status (`success`, `timeout`, `failed`) with duration and retry attempt

## Deployment and Multi-node Notes / 部署与多节点说明

- Runtime is stateless per request except bounded memory cache
- Node-specific memory keys prevent cross-node context contamination
- For multi-node controller deployments, keep runtime instances independent and aggregate logs centrally
- Optional JSONL memory file can be mounted per node/pod when local continuity is required

## Compatibility / 兼容性

- Legacy env vars `SRE_AGENT_ENABLE_RAG` and `SRE_AGENT_RAG_*` remain aliases for context retrieval settings
- Legacy class `ReActRAGAgent` remains as a shim over `ReasoningAgent`
- Runtime backend is configurable via `SRE_AGENT_RUNTIME_BACKEND` (`haystack` by default)
