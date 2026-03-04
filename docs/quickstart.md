# Quickstart (v0.5)

## Prerequisites

- Go toolchain (for backend build).
- Node.js (for frontend build/tests).
- Linux is recommended for full kernel-first collector coverage.

## Happy path

```bash
make build
./scripts/run-local.sh
```

Check:

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/api/v1/status
curl -sS http://127.0.0.1:8080/api/v1/fleet
```

## Advanced path

Kernel-first probe-core:

```bash
make build-probe-core
# then enable probe_core.enabled=true in configs/collector.yaml
```

Multi-collector local topology:

```bash
./scripts/run-local-multinode.sh --collectors 3
```

Docker:

```bash
./scripts/docker-run-stack.sh
./scripts/docker-stop-stack.sh
```

## Optional Python AI service

The Python runtime exposes a standalone AI service via JSON-RPC over HTTP:

```bash
cd python
python3 -m sre_agent.cli --version
python3 -m sre_agent.cli serve-ai --port 50052 --log-level INFO
```
