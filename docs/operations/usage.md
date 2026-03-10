# Usage Guide (v0.6)

This document is operational. It explains how to run the system in two modes:

- **Single-node convenience mode:** controller and collector on one machine
- **Separated deployment mode:** controller on one machine, one or many collectors on other machines

The architecture rationale stays in [README.md](../../README.md). This file focuses on commands, files, and expected behavior.

## Section A: Single-node usage

### When this mode is useful

- local development
- UI/API demos
- validating RAG, RCA, and agent workflows on one host
- quick smoke testing without provisioning remote collector hosts

### Recommended path: container-first single-node stack

```bash
cp .env.example .env
make container-build
make container-up-host-observer
```

Health checks:

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/api/v1/status
curl -sS http://127.0.0.1:8080/api/v1/inventory/probes
curl -sS http://127.0.0.1:8080/api/v1/rag/status
```

Stop:

```bash
make container-down-host-observer
```

Why `host-observer` is the recommended single-node path:

- it exposes the primary collector model from `v0.6` instead of a reduced container sandbox
- probe-core stays the primary host telemetry source
- the eBPF runtime gets the namespaces/capabilities it needs for high-fidelity kernel events

`make container-up` still exists, but it is better treated as a controller/UI/demo convenience stack than as the full observability path.

### Production-like local overlays

Add controller-side InfluxDB:

```bash
make container-up-tsdb
make container-down-tsdb
```

Add host-observer collector privileges/mounts:

```bash
make container-up-host-observer
make container-down-host-observer
```

Full local stack:

```bash
make container-up-full
make container-down-full
```

### Expected behavior in single-node mode

- The collector pushes gRPC telemetry to the controller on `:9090`
- The controller serves the UI/API on `:8080`
- The collector uses probe-core as the primary host/process telemetry path
- The collector uses the eBPF runtime as the primary kernel-event path
- `configs/controller_targets.yaml` is still loaded by the controller and appears in inventory APIs
- RAG, RCA, joint-risk, and recommendation flows work exactly as in split deployment; only the physical topology changes

### Source-mode fallback

Use this when you want direct process control without rebuilding images:

```bash
make build
./scripts/run-local.sh --enable-agent
```

Controller-only demo mode with seeded telemetry:

```bash
./scripts/run-local.sh --enable-agent --demo --llm=stub
```

In `--demo`, the launcher now defaults to controller-only startup because synthetic
telemetry is already seeded into the controller. This removes the live collector
and gRPC ingest path from the critical startup path for UI/RAG/agent demos.

If you want the demo stack to also start a real collector, opt in explicitly:

```bash
SRE_DEMO_START_COLLECTOR=1 ./scripts/run-local.sh --enable-agent --demo --llm=stub
```

Local multi-collector source simulation:

```bash
./scripts/run-local-multinode.sh --collectors 3
```

## Section B: Separated multi-machine usage

### Topology

- **Controller host:** runs `sre-controller`, UI/API, ingest, RAG, agent workflows, optional TSDB
- **Collector hosts:** run `sre-collector`, collect host telemetry, spool locally, push to controller

Current transport direction is still push-first:

- collector -> controller over gRPC (`:9090`)
- browser/UI -> controller over HTTP (`:8080`)

The controller target inventory file is still useful even though the primary data path is push-oriented. It acts as:

- a hand-maintained list of known collectors
- inventory metadata for UI grouping and policy scoping
- a future-safe place for endpoint/auth metadata
- a reference map for actions, lookups, and audit attribution

### 1. Prepare the controller host

Edit the controller config if needed:

- `configs/controller.yaml`
- `configs/controller_targets.yaml`

Example target inventory entry:

```yaml
---
collectors:
  - id: "collector-edge-a"
    hostname: "edge-a.example.net"
    address: "10.20.0.11"
    port: 9464
    enabled: true
    labels:
      site: "dc-a"
      env: "prod"
    tags: ["gpu", "edge"]
    auth:
      mode: "mtls"
      server_name: "controller.example.net"
      token_env: "SRE_COLLECTOR_TOKEN"
```

Build the controller image from the local checkout:

```bash
./scripts/docker-build-controller.sh
```

Build the controller image from a fork or mirror instead:

```bash
REPO_URL=https://github.com/your-org/your-fork.git \
REPO_REF=main \
./scripts/docker-build-controller.sh
```

Run the controller:

```bash
./scripts/docker-run-controller.sh \
  --config-file ./configs/container/controller.yaml \
  --targets-file ./configs/controller_targets.yaml
```

Equivalent compose-only controller host:

```bash
docker compose -f deploy/docker/docker-compose.controller.yml up -d
```

### 2. Prepare each collector host

Edit the collector config or override the controller endpoint through env:

- `configs/collector.yaml`
- `SRE_COLLECTOR_CONTROLLER_ENDPOINTS=controller.example.net:9090`

Build from the local checkout:

```bash
./scripts/docker-build-collector.sh
```

Build from another repository/ref:

```bash
REPO_URL=https://github.com/your-org/your-fork.git \
REPO_REF=main \
./scripts/docker-build-collector.sh
```

Run the collector:

```bash
SRE_COLLECTOR_CONTROLLER_ENDPOINTS=controller.example.net:9090 \
SRE_COLLECTOR_ID=collector-edge-a \
SRE_COLLECTOR_HOSTNAME=edge-a.example.net \
./scripts/docker-run-collector.sh --config-file ./configs/container/collector.yaml
```

If the collector must observe host kernel/runtime state more directly:

```bash
SRE_COLLECTOR_CONTROLLER_ENDPOINTS=controller.example.net:9090 \
SRE_COLLECTOR_ID=collector-edge-a \
SRE_COLLECTOR_HOSTNAME=edge-a.example.net \
./scripts/docker-run-collector.sh --config-file ./configs/container/collector.yaml --host-observer
```

`--host-observer` is the recommended production-like container mode because probe-core/perf/eBPF visibility depends on host namespaces, mounted kernel interfaces, and container capabilities. Without it, the collector can still start, but the runtime may degrade to compatibility behavior and reduced kernel visibility.

Equivalent compose-only collector host:

```bash
SRE_COLLECTOR_CONTROLLER_ENDPOINTS=controller.example.net:9090 \
docker compose -f deploy/docker/docker-compose.collector.yml up -d
```

### 3. Add or remove collectors

To add a collector:

1. Add an entry to `configs/controller_targets.yaml`
2. Start a collector container on the remote host with a unique `SRE_COLLECTOR_ID`
3. Verify it appears in:
   - `GET /api/v1/inventory/probes`
   - `GET /api/v1/fleet`

To remove a collector:

1. Stop the collector container on the remote host
2. Remove or disable the entry from `configs/controller_targets.yaml`
3. Restart or redeploy the controller if the mounted file changed outside the container image

### 4. How the controller target config is used today

Today the file is not a reverse-dial transport list for the controller's ingest plane. The collector still initiates the main telemetry connection.

The file is used for:

- known collector registration
- inventory listing even before telemetry arrives
- grouping by labels/tags
- separating enabled vs disabled collectors
- storing auth/endpoint metadata without hardcoding it in the binary

### 5. Troubleshooting basics

Controller host:

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/api/v1/inventory/status
curl -sS http://127.0.0.1:8080/api/v1/inventory/probes
curl -sS http://127.0.0.1:8080/api/v1/fleet
docker logs ai-sre-agent-controller --tail 100
```

Collector host:

```bash
curl -sS http://127.0.0.1:9464/healthz
curl -sS http://127.0.0.1:9464/metrics | head
docker logs ai-sre-agent-collector --tail 100
```

Common failure points:

- wrong `SRE_COLLECTOR_CONTROLLER_ENDPOINTS`
- controller `:9090` not reachable through host firewall or security group
- host-observer collector started without required kernel capabilities/mounts
- controller target inventory file mounted to the wrong path
- controller and collector IDs not matching the intended inventory entry
