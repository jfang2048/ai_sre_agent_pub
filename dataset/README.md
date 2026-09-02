# Dataset Boundary

`dataset/` contains public metadata and ingestion tools, not private corpora or generated indexes.

## Tracked content

- `metadata/web_sources.txt`: one public source URL per line
- `scripts/fetch_web_sources.sh`: fetches approved public sources into an ignored local directory
- `tools/zedx2txt.py`: optional conversion helper for locally supplied documentation packages

Downloaded pages, archives, extracted text, indexes, and quarantined files are intentionally untracked. Keep them under `dataset/sources/`, `dataset/raw/`, `dataset/processed/`, or `data/`; those paths are excluded from public publishing.

## Input contract

The RAG loader accepts explicitly configured source paths. Prefer small, reviewable UTF-8 JSON, JSONL, CSV, Markdown, text, HTML, or XML inputs. Keep one operational case or runbook topic per record and preserve source provenance outside free-form content.

Useful structured fields include:

- `title`, `query`, `question`, or `summary`
- `service`, `topic`, `environment`, and `timestamp`
- `symptoms`, `evidence`, `likely_causes`, `remediation_steps`, and `commands`

Binary members of local archives are not knowledge documents and should remain outside the repository.

## Local workflow

Fetch the allowlisted public pages:

```bash
dataset/scripts/fetch_web_sources.sh
```

Attach any additional local-only corpus explicitly and rebuild the index:

```bash
export SRE_AGENT_RAG_SOURCE_PATHS=/absolute/path/to/reviewed/local/corpus
make rag-rebuild
make rag-status
```

Before publishing, verify that no local corpus or workstation identity entered Git history:

```bash
make public-repo-audit
```

The audit is intentionally independent of the RAG loader: ingestion policy and public-release policy have different failure modes and remain separate Unix-style tools.
