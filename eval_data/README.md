# Evaluation Data

This directory contains the golden evaluation inputs for the repo's RAG and AIOps workflows.

- `retrieval_cases.json`: retrieval-focused cases with expected target documents, expected knowledge types, and noisy-query variants.
- `incident_cases.json`: end-to-end incident cases with synthetic telemetry scenarios, expected RCA outcomes, expected recommendation content, and RAG expectations.
- `knowledge/`: a small deterministic knowledge corpus used by the evaluation runner so retrieval quality can be measured against stable targets.

The evaluation corpus is intentionally small and deterministic. It is designed for regression testing, not for claiming broad benchmark coverage.
