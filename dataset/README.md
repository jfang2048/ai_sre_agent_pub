# Dataset Layout

`dataset/` is the repository-local knowledge source used by the RAG demo and local verification workflows.

This directory intentionally keeps bundled source data separate from generated runtime state:

- `raw/structured/`: structured source files checked into the repository (`json`, `jsonl`, `csv`)
- `raw/archives/`: large upstream archives kept as source inputs, not runtime cache
- `tools/`: helper scripts for converting or inspecting custom formats

Current contents:

- `raw/structured/aiops2024-challenge-dataset.json`
- `raw/structured/question.jsonl`
- `raw/structured/helpdesk_dataset.csv`
- `raw/archives/data.zip`
- `raw/archives/ZTE_eReader_V4.11_20230525_lite.zip`
- `tools/zedx2txt.py`

Design rationale:

- The repository keeps source datasets under version control because local demo mode and RAG verification depend on them.
- Runtime extraction, chunking, indexing, and quarantine outputs do not belong here; they are written under `data/agent/rag/`.
- Archives remain in `raw/archives/` so it is obvious they are inputs, not generated cache directories.

License note:

- The repository source code is released under `GPL-3.0`.
- Bundled dataset files are third-party inputs and may follow their own upstream license or usage terms.
- Keep upstream notices with the dataset source material when redistributing or replacing these assets.

Upstream format notes retained from the original dataset packaging:

- `.zedx` documents can often be treated as zipped packages containing HTML pages plus `nodetree.xml`.
- `nodetree.xml` maps document titles to page paths.
- `documents/log.html` commonly contains acronym or glossary expansions.

Example local use:

```bash
make rag-rebuild
make rag-query QUERY="timeout deployment runbook"
```
