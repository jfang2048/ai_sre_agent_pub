# Runtime Data Layout

`data/` is reserved for controller- and agent-side runtime state generated after startup.

Expected subdirectories:

- `controller/ingest/`: bounded ingest snapshot persistence fallback
- `controller/tsdb/`: controller-side TSDB helper state and local metadata when enabled
- `agent/rag/`: RAG index, extraction cache, and quarantine manifests
- `gpu/`: GPU observability persistence

Design rationale:

- Runtime data is separated from source code and bundled datasets so the repository stays clean for public use.
- The collector remains lightweight and does not require a local database; controller-side durability is where storage complexity belongs.
- This directory is ignored by git except for this note.
