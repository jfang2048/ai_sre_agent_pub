# FAQ

中文版本：[docs/zh/16-faq.md](../zh/16-faq.md)

## Is this a single binary?

No. The maintained runtime model is split between `collector` and `controller`.

## What should I read first if I am not familiar with the codebase?

Use this order:

1. [Overview](01-overview.md)
2. [Getting started](03-getting-started.md)
3. [Architecture](04-architecture.md)
4. [Data flow](05-data-flow.md)
5. [Core files](10-core-files.md)

## What is the recommended local startup path?

Use the container-first host-observer path:

```bash
cp .env.example .env
make container-build
make container-up-host-observer
```

## Does the project require an external vector database?

No. The default RAG path is local-first. External vector sync is optional.

## Does the project require an LLM provider?

No. LLM features are optional. The repo defaults keep `llm_enabled: false`.

## Why does the UI work even when RAG or LLM is disabled?

Because the UI depends first on controller ingest and API state, not on retrieval or model calls.

A valid first boot can look like:

- controller health is good
- fleet state is present
- RAG is disabled
- LLM is disabled

That is still a correct observability deployment. Retrieval and model reasoning are optional layers on top.

## Where does the RAG data come from?

From the tracked [`dataset/`](../../dataset/) directory plus any extra paths configured through `rag_source_paths` or `SRE_AGENT_RAG_SOURCE_PATHS`.

## Where are prompts stored?

Mostly in Go code, not in standalone prompt files. Start with:

- [`backend/internal/controller/agentcore/prompts.go`](../../backend/internal/controller/agentcore/prompts.go)
- [`backend/internal/controller/agentcore/llm_analysis.go`](../../backend/internal/controller/agentcore/llm_analysis.go)
- [`backend/internal/controller/analysis/llm_client.go`](../../backend/internal/controller/analysis/llm_client.go)

## Where does runtime data go?

In source-mode defaults:

- collector data under `./data/collector/`
- controller ingest data under `./data/controller/`
- agent and RAG data under `./data/agent/`

Container-mode paths move under `/var/lib/ai-sre-agent/...` according to the container configs.

## How do I update the RAG index after changing the dataset?

Use:

```bash
make rag-update
```

Use `make rag-rebuild` after larger ingestion-setting changes.

## What happens if the local RAG index is broken?

The controller does not blindly trust it anymore.

- invalid local indexes are quarantined on startup
- the service records the warning in runtime status
- behavior after that depends on `rag_rebuild_policy`

Read:

- [Dataset and RAG](11-dataset-and-rag.md)
- [Deployment](15-deployment.md)

## Why might the agent skip both RAG and the LLM?

The query-service can bypass expensive work when telemetry is stale or missing.

That behavior is controlled by:

- `skip_llm_on_stale_telemetry`
- `skip_llm_on_no_telemetry`

In that case the system returns deterministic fallback rather than pretending confidence. This is intentional host-first behavior, not a silent failure.

## Which docs should I read next?

- [Getting started](03-getting-started.md)
- [Architecture](04-architecture.md)
- [Dataset and RAG](11-dataset-and-rag.md)
- [Prompts and customization](12-prompts-and-customization.md)

## What does a successful end-to-end check look like?

One practical example is:

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/api/v1/status
curl -sS http://127.0.0.1:8080/api/v1/rag/status
```

Then:

- open `http://127.0.0.1:8080/`
- verify the UI loads
- verify RAG is either enabled or intentionally disabled
- verify the controller is responding before debugging collector or prompt issues
