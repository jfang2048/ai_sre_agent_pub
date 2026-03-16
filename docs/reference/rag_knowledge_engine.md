# RAG Knowledge Engine (v0.7)

This document describes how the controller-side RAG service now works as a first-class operational knowledge engine rather than a weak text-search helper.

## Why RAG is essential here

Live telemetry explains what is happening now.
RAG explains what similar situations meant before, which runbook usually applies, and which remediation patterns are already known to be safe or unsafe.

For practical SRE work, those are different jobs:

- telemetry answers `what changed`
- historical knowledge answers `what this pattern usually means`
- runbooks answer `what to check or do next`

Without that accumulated operational memory, RCA and recommendations stay generic even when the telemetry is good.

## Ingestion pipeline

```text
dataset/source paths
  -> source discovery
  -> archive extraction / text conversion
  -> source classification
  -> knowledge normalization
  -> structured-case or plain-text chunking
  -> lexical index + local/vector embedding index
  -> retrieval + evidence packaging
```

## Source categories

| Category | Examples | Normalization outcome |
| --- | --- | --- |
| Structured QA / incident rows | `json`, `jsonl`, `csv`, `tsv` | `question_pattern`, `operational_qa`, `historical_incident` |
| Runbooks / manuals / guides | `md`, `txt`, extracted `html` | `runbook` or `reference` |
| Archive-backed docs | `.zip`, `.zedx` entries | converted text-like docs with source lineage preserved |
| Dataset metadata | manifest/pointer files | metadata only, low-weight retrieval or excluded |
| Unsupported / binary files | executables, images, CSS | quarantine |

## What dataset ships in this repo

The tracked seed dataset is small on purpose and lives under `dataset/`:

- `dataset/raw/structured/question.jsonl`
  - structured operational questions
- `dataset/raw/structured/helpdesk_dataset.csv`
  - FAQ-like Q/A rows
- `dataset/raw/structured/aiops2024-challenge-dataset.json`
  - dataset metadata and pointers
- `dataset/raw/archives/manifest.json`
  - metadata for optional archive corpora
- `dataset/raw/archives/README.md`
  - explains the publish-safe archive policy

Optional large corpora are expected to live outside the tracked tree in:

- `data/bootstrap/datasets/archives/`

Those can be imported with:

```bash
scripts/bootstrap/manage_optional_datasets.sh import --from /path/to/archive-dir
```

and then included at runtime with `SRE_AGENT_RAG_SOURCE_PATHS`.

## Normalized knowledge schema

Each normalized document and chunk can now carry:

- `doc_id`
- `source_path`
- `source_type`
- `knowledge_type`
- `case_type`
- `title`
- `summary`
- `symptoms[]`
- `evidence[]`
- `likely_causes[]`
- `remediation_steps[]`
- `commands[]`
- `environment[]`
- `signals[]`
- `tags[]`
- `retrieval_text`
- `embedding_text`
- `retrieval_weight`

This schema is intentionally operational. It is meant to feed RCA, joint-risk, recommendations, and UI evidence views.

## Chunking strategies

`v0.7` now supports two broad chunking paths:

- plain-text chunking
  - `paragraph`
  - `markdown`
  - `line`
  - `record`
- structured-case chunking
  - `case`
  - splits a normalized document into summary/evidence/remediation/body sections

`auto` chooses `case` when the document already contains structured knowledge fields.

## How dataset material is processed

The controller-side ingestion path is implemented in `backend/internal/controller/rag/ingest.go`, `knowledge.go`, and `chunk.go`.

Operationally, one source file goes through these steps:

1. Source discovery
   - recursively scan `dataset_path` and extra `source_paths`
   - detect file kind
   - extract archive entries when the source is `.zip` or `.zedx`
2. Parsing
   - text/markdown/html/xml become one or more text documents
   - `json`, `jsonl`, `csv`, and `tsv` are converted into structured per-record documents
3. Normalization
   - infer `title`, `summary`, `symptoms`, `evidence`, `likely_causes`, `remediation_steps`, `commands`, `environment`, and `signals`
   - classify each document into `runbook`, `historical_incident`, `question_pattern`, `security_reference`, `reference`, or `dataset_meta`
4. Chunking
   - `auto` uses `case` chunking when the document already looks operationally structured
   - otherwise it falls back to `paragraph`, `markdown`, `record`, `line`, or another configured strategy
5. Indexing
   - lexical search index over retrieval text
   - embedding generation and local/external vector search support
6. Retrieval packaging
   - results are returned as normalized evidence objects, not raw text fragments only

The incremental update path reuses existing documents and chunks when the source signature has not changed.

## How to modify the dataset cleanly

There is no special prompt needed to author the dataset. The knowledge base is file-driven.

The practical ways to improve it are:

- add or edit structured files under `dataset/raw/structured/`
- add or edit text runbooks under `dataset/` or an extra source path
- attach local-only archive corpora through `data/bootstrap/datasets/archives/` plus `SRE_AGENT_RAG_SOURCE_PATHS`

Authoring hints that match the current extractor:

- for JSON/JSONL/CSV/TSV, use fields or columns such as `title`, `query`, `question`, `summary`, `document`, `service`, `topic`, `timestamp`
- for text/markdown runbooks, use explicit headings and lists for symptoms, evidence, likely causes, remediation steps, and commands
- keep one incident/runbook/question per record when using structured formats
- prefer operational wording over generic prose; specific commands, error strings, and component names help lexical retrieval a lot

If you want better `case` chunking and richer retrieval fields, include material that clearly exposes:

- symptoms
- evidence
- likely causes
- remediation steps
- commands
- environment or service names

## Update vs rebuild

Use `update` when you made ordinary content edits and want the index to reuse unchanged sources:

```bash
make rag-update
go -C backend run ./cmd/ragctl update
curl -sS -X POST http://127.0.0.1:8080/api/v1/rag/index/update
```

Use `rebuild` when:

- you changed a lot of files at once
- you changed chunking or embedding settings
- you suspect extraction or cache drift
- you want a clean full rescan

```bash
make rag-rebuild
go -C backend run ./cmd/ragctl rebuild
curl -sS -X POST http://127.0.0.1:8080/api/v1/rag/index/rebuild
```

Useful inspection commands:

```bash
make rag-status
make rag-query QUERY="gpu timeout after rollout"
curl -sS http://127.0.0.1:8080/api/v1/rag/status | jq .
curl -sS -X POST http://127.0.0.1:8080/api/v1/rag/query \
  -H 'Content-Type: application/json' \
  -d '{"query":"gpu timeout after rollout","intent":"rca","top_k":5}'
```

## Retrieval modes

The controller remains local-first:

- `lexical`
  - best for exact names, error strings, file paths, commands, and product terms
- `vector`
  - useful for semantically similar cases when exact wording differs
- `hybrid`
  - default
  - combines lexical and vector scores

If an external embedding or vector backend is unavailable, the controller falls back to deterministic local hashing and local search instead of failing the workflow.

## Query intents

`POST /api/v1/rag/query` now supports intent hints such as:

- `general`
- `runbook`
- `historical_incident`
- `rca`
- `joint_risk`
- `recommendation`
- `security`

Intent changes the retrieval behavior in a bounded, deterministic way:

- query expansion
- type-aware reranking
- runbook/case preference
- filter application

## What query or prompt should operators send

For direct RAG retrieval, the caller prompt is simply the query payload to `POST /api/v1/rag/query`.

Good examples:

- `"gpu timeout after rollout"` with `intent=rca`
- `"high iowait nvme queue saturation"` with `intent=historical_incident`
- `"checkpoint burst stalls training workers"` with `intent=runbook`
- `"certificate rotation broke collector handshake"` with `intent=recommendation`

In practice, operators should think in terms of:

- what symptom they want to explain
- whether they want a runbook, a similar historical case, or general reference material
- whether retrieval should be narrowed with `knowledge_types` or `case_types`

There is no separate author prompt for indexing. Indexing is driven by dataset content plus configuration.

## Evidence packaging

Retrieval results now carry more than a snippet:

- `evidence_id`
- `doc_id`
- `chunk_id`
- `score`
- `source_path`
- `source_type`
- `knowledge_type`
- `case_type`
- `summary`
- `snippet`
- `likely_causes[]`
- `remediation_steps[]`
- `commands[]`
- `signals[]`

That makes the result usable directly inside workflows and UI evidence panels.

## Workflow integration

RAG is now attached to the deterministic workflow engine through explicit tools:

- `rag_query`
- `historical_incident_retrieval`
- `runbook_retrieval`
- `similar_case_retrieval`

These tools feed:

- potential-risk findings
- joint-risk assessment
- RCA context gathering
- recommendation generation
- context bundle assembly

The workflow state now keeps:

- `retrieved_docs[]`
- `retrieved_cases[]`
- `retrieved_runbooks[]`
- `similar_incident_patterns[]`
- `retrieval_summary`
- `retrieval_confidence`
- `retrieval_evidence_ids[]`

The older `/api/v1/agent/query` path now uses the same engine more deliberately as well:

- it infers likely intent from the operator query and live findings
- it narrows retrieval toward runbooks, historical incidents, operational QA, or security references as appropriate
- it packages retrieved knowledge into structured prompt snippets instead of dumping raw text only
- it returns `retrieval_intent` and `retrieval_mode` so operators can see how the controller routed the lookup

## How RAG enters model prompts

There are two main prompt paths:

### 1. `/api/v1/agent/query`

`backend/internal/controller/agentcore/prompts.go` builds:

- a system prompt that says the model must use only provided telemetry facts and return strict JSON
- a user prompt that includes:
  - telemetry quality
  - metrics, trends, process summaries, and log fingerprints
  - compact RAG snippets
  - retrieval summary
  - retrieval routing (`intent` and `mode`)
  - up to a few retrieved documents with structured fields such as summary, causes, and steps

Important behavior:

- retrieved docs are summarized, not pasted wholesale
- logs and retrieved docs are treated as untrusted context
- recommendations must be tied back to evidence or marked as limitations

### 2. Workflow LLM analysis

`backend/internal/controller/agentcore/llm_analysis.go` builds:

- a strict system prompt with a fixed JSON schema
- a user prompt containing the full context bundle
- explicit instruction that logs, retrieved documents, and free-form snippets are untrusted data

That means the project does not rely on “RAG text stuffed into a prompt and hope for the best.”
It uses a constrained evidence bundle plus structured retrieval fields.

## UI/API visibility

The knowledge page and workflow evidence panels now surface:

- knowledge type
- case type
- summary
- likely causes
- runbook steps
- commands
- signals
- source-linked snippets

This is deliberate: knowledge has to be inspectable by operators, not hidden behind one summary sentence.

## Limits

Current implementation limits are explicit:

- normalization is heuristic, not a domain-specific ontology
- local embeddings are deterministic and lightweight, not SOTA semantic embeddings
- some structured datasets contain strong questions but weak answers, so the archive/manual corpus still matters a lot
- retrieval is evidence support, not proof by itself

## Operator FAQ

### What should I edit if I want better answers?

Edit the dataset content, not the prompt first. The biggest gains usually come from:

- adding clearer runbooks
- adding better historical incident summaries and remediation steps
- adding structured question/case records with explicit symptoms and causes

### How do I know whether my new content was ingested?

Check:

- `GET /api/v1/rag/status`
- `GET /api/v1/rag/doc/{id}`
- `make rag-query QUERY="..."` against terms that should match the new content
- the quarantine manifest under `data/agent/rag/quarantine.json`

### When should I use extra source paths instead of editing `dataset/`?

Use extra source paths when the knowledge is:

- environment-specific
- large and local-only
- not appropriate to commit into the repository

### Do I need an external vector database?

No. The design is local-first. External vector backends are optional scale-out components, not a prerequisite for correct behavior.

## 中文补充

- 这个 RAG 设计的核心不是“加一个向量库”，而是把静态知识真正变成 controller 可以消费的运行记忆。
- 现在系统区分 runbook、历史案例、问题模式、参考材料，而不是只返回一组分数最高的文本片段。
- 这样做的意义是：RCA、joint risk、recommendation 都能明确知道自己拿到的是哪一种知识证据，值班工程师也能在 UI 上直接看出来。
