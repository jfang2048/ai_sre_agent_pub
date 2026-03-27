# Dataset Layout

`dataset/` is the repository-local knowledge source for the controller RAG service.

In `v0.8`, the RAG path no longer treats this directory as “random text to chunk.” It scans the dataset, classifies sources, normalizes them into SRE-oriented knowledge objects, and only then builds lexical/vector indexes.

## What is in `dataset/`

- `raw/structured/aiops2024-challenge-dataset.json`
  - dataset metadata and pointers
- `raw/structured/question.jsonl`
  - structured operational questions
- `raw/structured/helpdesk_dataset.csv`
  - FAQ-style rows
- `raw/archives/data.zip`
  - large text-heavy product/manual corpus
- `raw/archives/ZTE_eReader_V4.11_20230525_lite.zip`
  - mixed archive with HTML/XML/text plus many binary files
- `raw/archives/manifest.json`
  - archive manifest metadata
- `tools/zedx2txt.py`
  - helper for `.zedx` / HTML conversion workflows

## Current classification

| Category | Files now seen in this repo | RAG treatment |
| --- | --- | --- |
| Structured incident / QA style datasets | `question.jsonl`, `helpdesk_dataset.csv` | normalized into `question_pattern` / `operational_qa` knowledge objects |
| Dataset metadata | `aiops2024-challenge-dataset.json`, `raw/archives/manifest.json` | kept as metadata context, heavily down-weighted or excluded from retrieval |
| Markdown / text / HTML / XML knowledge docs | extracted archive entries, README-style docs, optional extra source paths | normalized into `runbook`, `historical_incident`, `reference`, or `security_reference` objects |
| Custom `.zedx` or archive-based docs | `.zedx` or `.zip` inputs, especially under `raw/archives/` | extracted first, then HTML/XML/text entries are normalized and indexed |
| Archive binaries and UI assets | DLLs, EXEs, images, CSS, etc. inside archives | quarantined, not indexed |
| Tooling / conversion helpers | `tools/zedx2txt.py` | not worth indexing as operational knowledge; left out of retrieval |

## Knowledge normalization model

The controller now turns raw inputs into a richer knowledge object with fields such as:

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

This matters because SRE reasoning depends on more than “find text that looks similar.” RCA and recommendation flows need to know whether a match is a runbook, a prior incident, a troubleshooting question, or just a reference note.

## Practical ingestion strategy by category

### 1. Structured JSON / JSONL

- Use the structured fields directly when possible.
- Keep record-level lineage in metadata.
- Normalize rows into operational knowledge objects rather than one giant flattened blob.

Current examples:

- `question.jsonl`
  - becomes `question_pattern` / `operational_qa`
  - `query` becomes a symptom/pattern seed
  - `document` becomes environment/product context
- `aiops2024-challenge-dataset.json`
  - treated as dataset metadata
  - not a high-value retrieval document by itself

Concrete tracked example:

```json
{"id":1,"query":"PCF与NRF对接时，一般需要配置哪些数据？","document":"rcp"}
```

With the current code:

- title is inferred from `query`
- metadata gets `field.id`, `field.query`, and `field.document`
- `document=rcp` also becomes a tag
- content is flattened into sorted key/value lines before normalization

### 2. CSV / TSV

- Convert each row into one knowledge object.
- Map obvious columns like `Question` and `LinkToAnswer` into summary/context fields.
- Down-weight rows that are generic demo FAQ material rather than production SRE material.

Concrete tracked example:

```csv
Question,LinkToAnswer
"My Mac does not boot, what can I do ?",http://faq/mac-does-not-boot
```

Important nuance:

- the current metadata merge lowercases CSV headers, so `Question` becomes `field.question`
- retrieval still works because `structuredFieldValue` can read `question` and `linktoanswer`
- title inference is weaker than JSONL because `chooseStructuredTitle` checks lowercase raw keys, so a CSV row may fall back to `helpdesk_dataset.csv #N`

If you author your own CSV, prefer lowercase headers like `question`, `summary`, `document`, `service`, `topic`, and `timestamp`.

### 3. Markdown / text

- Parse headings, paragraphs, and bullet lists.
- Extract likely runbook steps, commands, symptoms, and causes.
- Chunk using plain-text or structured-case strategy depending on content.

### 4. HTML / XML

- Convert to text first.
- Reuse title/heading information when available.
- Extract section-level operational content after conversion.

### 5. `.zip` / `.zedx`

- Extract archive entries into the controller RAG cache.
- Only text-like entries are ingested.
- Binary assets are quarantined.
- `.zedx`-style help packages are treated as archive-backed docs rather than opaque files.

### 6. Metadata-only files

- Keep them for provenance and inventory context.
- Do not let them dominate retrieval results.

## What is intentionally ignored or quarantined

- binary archive members
- images and CSS assets
- unsupported source types
- low-value tooling files that are not operational knowledge

Quarantine output is written under `data/agent/rag/quarantine.json`.

## Why this stronger dataset usage matters

Without normalization, the RAG path mostly returns “text that shares tokens.”
With normalization, the same dataset can support:

- potential issue analysis from weak-signal analogies
- joint-risk interpretation from prior escalation patterns
- RCA hypothesis generation from similar incidents
- recommendation generation from runbook steps
- historical incident analogy instead of blank-slate reasoning

## Local usage

```bash
make rag-rebuild
make rag-query QUERY="deployment timeout cache credentials"
curl -sS http://127.0.0.1:8080/api/v1/rag/status
```

## How to modify the dataset

Treat `dataset/` as raw knowledge source material, not as a finished index.

Practical ways to improve retrieval quality:

- add or edit structured records under `dataset/raw/structured/`
- add text/markdown runbooks with explicit symptoms, evidence, causes, steps, and commands
- attach large local-only corpora through `data/bootstrap/datasets/archives/` and `SRE_AGENT_RAG_SOURCE_PATHS`

Authoring guidance:

- structured files work best when rows expose fields such as `title`, `query`, `question`, `summary`, `document`, `service`, `topic`, and `timestamp`
- markdown/text docs work best when they clearly separate symptoms, evidence, likely causes, remediation steps, and commands
- keep one operational case or runbook topic per record when possible

Example of a strong custom Markdown runbook:

```markdown
# GPU rollout timeout after driver upgrade

Symptoms
- training jobs queue longer than normal

Evidence
- gpu utilization drops while queue time rises

Likely causes
- driver mismatch across nodes

Remediation steps
- compare driver versions across nodes

Commands
- nvidia-smi
```

This fits the current `knowledge.go` heuristics well because it can extract headings related to symptoms, evidence, causes, remediation, and commands.

## How to refresh after edits

Use incremental update for ordinary edits:

```bash
make rag-update
```

Use full rebuild when you changed many files, changed chunking settings, or want a clean rescan:

```bash
make rag-rebuild
```

You can also use the CLI and HTTP APIs directly:

```bash
go -C backend run ./cmd/ragctl update
go -C backend run ./cmd/ragctl rebuild
curl -sS -X POST http://127.0.0.1:8080/api/v1/rag/index/update
curl -sS -X POST http://127.0.0.1:8080/api/v1/rag/index/rebuild
```

After refresh, verify with:

```bash
make rag-status
make rag-query QUERY="gpu timeout after rollout"
```

## 中文补充

- `dataset/` 现在的意义不再只是“给 demo 准备几份文本”，而是 controller 本地知识引擎的原始语料入口。
- 真正有价值的不是把所有文件都塞进索引，而是先判断它属于 runbook、历史案例、问题模式、参考文档还是元数据，再决定如何抽取字段和如何排序。
- 对值班工程师来说，最重要的是系统能回答“这条命中到底是历史故障、排障步骤，还是只是某份说明文档的一段文字”。这就是现在要做分类和规范化的原因。
