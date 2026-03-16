# Deployment

中文版本：[docs/zh/15-deployment.md](../zh/15-deployment.md)

This repository now supports a cleaner split between node-local collection and centralized analysis.

> There is still no public release pipeline or published image registry in this repo. The supported path is: build from this checkout, validate locally, then roll out progressively.

For the operational behavior behind that deployment split, read these alongside this page:

- [Collector Queue and Compaction](06-collector-queue-and-compaction.md) for node-side buffering, suppression, and slow-receiver behavior
- [Control-Plane Analysis](07-control-plane-analysis.md) for controller-side trend analysis, weak-signal fusion, TSDB fallback, and recommendation generation

## Recommended Topologies

| Mode | Use it when | Main assets |
| --- | --- | --- |
| `local-dev` | you are changing code in one checkout | [`scripts/run-local.sh`](../../scripts/run-local.sh), [`configs/controller.yaml`](../../configs/controller.yaml), [`configs/collector.yaml`](../../configs/collector.yaml) |
| `standalone` | you want one central controller and a few remote collectors without Kubernetes | [`deploy/docker/`](../../deploy/docker/), [`deploy/systemd/`](../../deploy/systemd/) |
| `cluster-lite` | you want the fastest Kubernetes deployment path | [`deploy/k8s/push-first/`](../../deploy/k8s/push-first/), Helm defaults in [`deploy/charts/sre-agent/`](../../deploy/charts/sre-agent/) |
| `distributed` | you want replicated controller instances and shared backends | Helm with `controller.ha.enabled=true` plus external backend values |

## Runtime Boundary

| Plane | Runs here | State here |
| --- | --- | --- |
| data plane | collector, probe-core, eBPF, local spool | node-local spool and runtime cache |
| control plane | controller ingest, API, UI, workflows, RAG, optional TSDB | controller cache, optional embedded ingest DB, optional external TSDB/vector backend |

The runtime split is implemented in:

- [`../../backend/internal/collector/deployment.go`](../../backend/internal/collector/deployment.go)
- [`../../backend/internal/controller/deployment.go`](../../backend/internal/controller/deployment.go)

## What Changed For Cluster Deployment

The repository is less single-node-oriented than before:

- controller and collector both understand `deployment.mode`, `deployment.cluster_name`, and `deployment.data_root`
- non-local modes rewrite only built-in default-like paths into `/var/lib/ai-sre-agent/...`
- controller now exposes `/readyz` in addition to `/healthz`
- collector metrics server now exposes `/readyz` in addition to `/healthz`
- `/api/v1/status` now includes a `deployment` block
- controller RAG config now supports YAML fields for:
  - `rag_vector_backend`
  - `rag_vector_endpoint`
  - `rag_vector_collection`
  - `rag_vector_database`
  - `rag_vector_token`
  - `rag_vector_timeout`
- the raw Kubernetes path now mounts ConfigMaps instead of relying only on image-baked config
- the Helm chart now mounts ConfigMaps and exposes deployment mode, cluster name, ingress, HPA, and external-vector settings

## Deployment Modes In Config

Relevant files:

- [`../../configs/controller.yaml`](../../configs/controller.yaml)
- [`../../configs/collector.yaml`](../../configs/collector.yaml)
- [`../../configs/container/controller.yaml`](../../configs/container/controller.yaml)
- [`../../configs/container/collector.yaml`](../../configs/container/collector.yaml)

Example controller block:

```yaml
deployment:
  mode: "cluster-lite"      # local-dev | standalone | cluster-lite | distributed
  cluster_name: "prod-eu1"
  data_root: "/var/lib/ai-sre-agent"
  external_url: "https://ai-sre-agent.example.com"
```

Example collector block:

```yaml
deployment:
  mode: "cluster-lite"
  cluster_name: "prod-eu1"
  data_root: "/var/lib/ai-sre-agent"
```

In non-local modes, the loaders move default-like paths such as:

- collector spool -> `/var/lib/ai-sre-agent/collector/data/spool`
- collector eBPF socket -> `/var/lib/ai-sre-agent/collector/data/run/sre_collector_ebpf.sock`
- controller web path -> `/var/lib/ai-sre-agent/controller/web`
- controller ingest persistence -> `/var/lib/ai-sre-agent/controller/data/ingest/store.db`
- controller RAG index -> `/var/lib/ai-sre-agent/controller/data/agent/rag/index.json`

Explicit custom paths still win.

## Local Development

```bash
cp .env.example .env
make container-build
make container-up-host-observer
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:8080/api/v1/status | jq '.deployment'
```

Use this when:

- you need source checkout debugging
- you want one machine with both controller and collector
- you do not need cluster scheduling or shared backends

## Cluster-Lite Kubernetes Path

The quick cluster path is now:

- one controller `Deployment`
- one collector `DaemonSet`
- mounted ConfigMaps for controller and collector config
- optional static inventory file ConfigMap for the controller

Files:

- [`../../deploy/k8s/push-first/controller.yaml`](../../deploy/k8s/push-first/controller.yaml)
- [`../../deploy/k8s/push-first/controller-configmap.yaml`](../../deploy/k8s/push-first/controller-configmap.yaml)
- [`../../deploy/k8s/push-first/controller-targets-configmap.yaml`](../../deploy/k8s/push-first/controller-targets-configmap.yaml)
- [`../../deploy/k8s/push-first/collector-daemonset.yaml`](../../deploy/k8s/push-first/collector-daemonset.yaml)
- [`../../deploy/k8s/push-first/collector-configmap.yaml`](../../deploy/k8s/push-first/collector-configmap.yaml)

Collector identity in this path is node-derived rather than hardcoded:

- `SRE_COLLECTOR_ID <- spec.nodeName`
- `SRE_COLLECTOR_HOSTNAME <- spec.nodeName`

That is how cluster-lite rollouts avoid one-config-file-per-node drift.

Apply:

```bash
kubectl apply -k deploy/k8s/push-first
```

What this path assumes:

- controller is centralized but not HA
- collector runs once per node
- local-file RAG index is acceptable
- node-local spool is acceptable
- host-observer privileges are allowed on collector pods

## Helm Path

The chart under [`../../deploy/charts/sre-agent/`](../../deploy/charts/sre-agent/) is now the better path when you want parameterized cluster rollout.

Notable values:

| Value | Purpose |
| --- | --- |
| `global.deploymentMode` | default deployment mode for both components |
| `global.clusterName` | shared cluster identity |
| `controller.deploymentMode`, `collector.deploymentMode` | per-component override |
| `controller.externalURL` | external UI/API URL for docs and status |
| `controller.rag.vectorBackend` | `local` or `milvus` |
| `controller.rag.vectorEndpoint` | external vector endpoint |
| `controller.rag.vectorCollection` | collection name |
| `controller.rag.vectorTokenSecretName` | existing Secret name for vector backend auth |
| `controller.rag.vectorTokenSecretKey` | Secret key injected into `SRE_AGENT_RAG_VECTOR_TOKEN` |
| `controller.ingress.*` | ingress scaffold |
| `controller.autoscaling.*` | HPA scaffold for non-HA controller deployments |
| `controller.staticTargets` | static inventory file content |

Important templates:

- [`../../deploy/charts/sre-agent/templates/controller-configmap.yaml`](../../deploy/charts/sre-agent/templates/controller-configmap.yaml)
- [`../../deploy/charts/sre-agent/templates/controller-rag-secret.yaml`](../../deploy/charts/sre-agent/templates/controller-rag-secret.yaml)
- [`../../deploy/charts/sre-agent/templates/collector-configmap.yaml`](../../deploy/charts/sre-agent/templates/collector-configmap.yaml)
- [`../../deploy/charts/sre-agent/templates/controller-deployment.yaml`](../../deploy/charts/sre-agent/templates/controller-deployment.yaml)
- [`../../deploy/charts/sre-agent/templates/controller-statefulset.yaml`](../../deploy/charts/sre-agent/templates/controller-statefulset.yaml)
- [`../../deploy/charts/sre-agent/templates/collector-daemonset.yaml`](../../deploy/charts/sre-agent/templates/collector-daemonset.yaml)
- [`../../deploy/charts/sre-agent/templates/controller-ingress.yaml`](../../deploy/charts/sre-agent/templates/controller-ingress.yaml)
- [`../../deploy/charts/sre-agent/templates/controller-hpa.yaml`](../../deploy/charts/sre-agent/templates/controller-hpa.yaml)

Example values files:

- [`../../deploy/charts/sre-agent/examples/cluster-lite-values.yaml`](../../deploy/charts/sre-agent/examples/cluster-lite-values.yaml)
- [`../../deploy/charts/sre-agent/examples/distributed-values.yaml`](../../deploy/charts/sre-agent/examples/distributed-values.yaml)

## Distributed Reference Pattern

Use this when you want a more production-like split:

- collector `DaemonSet` on every node
- controller replicas managed by the Helm StatefulSet path
- HA enabled through `controller.ha.enabled=true`
- optional external vector backend such as Milvus
- optional external TSDB for longer history

Illustrative values:

```yaml
global:
  deploymentMode: distributed
  clusterName: prod-eu1

controller:
  replicas: 3
  ha:
    enabled: true
    backend: etcd
    etcdEndpoints:
      - http://etcd-0.etcd.sre.svc.cluster.local:2379
      - http://etcd-1.etcd.sre.svc.cluster.local:2379
      - http://etcd-2.etcd.sre.svc.cluster.local:2379
  rag:
    vectorBackend: milvus
    vectorEndpoint: http://milvus.monitoring.svc.cluster.local:19530
    vectorCollection: ai_sre_agent_knowledge
    vectorDatabase: ai_sre
    vectorTokenSecretName: milvus-rag-token
    vectorTokenSecretKey: token
```

This is still an incremental step, not a fully stateless controller:

- ingest hot state is still in-process
- local-file RAG still exists when `vectorBackend=local`
- report and workflow state are not yet externalized into a shared DB

## RAG Deployment Choices

There are now two realistic deployment styles for retrieval:

| Style | Config | When to use it |
| --- | --- | --- |
| local file index | `rag_vector_backend: local` plus `rag_index_path` | one controller instance or cluster-lite |
| external vector backend | `rag_vector_backend: milvus` plus endpoint/collection/database and `SRE_AGENT_RAG_VECTOR_TOKEN` from a Secret | distributed controller deployment |

Code path:

- [`../../backend/internal/controller/rag/retriever.go`](../../backend/internal/controller/rag/retriever.go)
- [`../../backend/internal/controller/rag/service.go`](../../backend/internal/controller/rag/service.go)
- [`../../backend/internal/controller/controller.go`](../../backend/internal/controller/controller.go)
- [`../../backend/internal/controller/agent/engine.go`](../../backend/internal/controller/agent/engine.go)
- [`../../backend/internal/controller/agentcore/agent.go`](../../backend/internal/controller/agentcore/agent.go)

Fallback behavior:

- if the local index is invalid, it is quarantined and rebuilt or left disabled depending on `rag_rebuild_policy`
- if the vector backend is unavailable, retrieval falls back to deterministic controller behavior instead of crashing the controller
- if retrieval confidence is too low, the controller suppresses the retrieved evidence before prompt assembly

## Health, Readiness, And Status

Use these endpoints during rollout:

| Component | Liveness | Readiness | More detail |
| --- | --- | --- | --- |
| controller | `/healthz` | `/readyz` | `/api/v1/status` |
| collector | `/healthz` | `/readyz` | `/metrics` |

Useful status fields:

- `/api/v1/status.deployment.mode`
- `/api/v1/status.deployment.cluster_name`
- `/api/v1/status.deployment.data_root`
- `/api/v1/status.ha`

## Privilege And Degraded Mode

Collector pods are still the privileged side of the system.

Expected collector capabilities and mounts remain in:

- [`../../deploy/k8s/push-first/collector-daemonset.yaml`](../../deploy/k8s/push-first/collector-daemonset.yaml)
- [`../../deploy/charts/sre-agent/templates/collector-daemonset.yaml`](../../deploy/charts/sre-agent/templates/collector-daemonset.yaml)

If those assumptions are not available:

- eBPF can degrade
- probe-core can fall back
- controller-side reasoning still works, but with lower-fidelity evidence
- RAG and LLM paths can be skipped safely when telemetry quality is weak

## Rollout Checklist

1. Build images from this checkout.
2. Run targeted tests:

```bash
cd backend
go test ./internal/collector ./internal/controller ./internal/controller/agent ./internal/controller/agentcore
```

3. Validate locally:

```bash
make container-smoke
make rag-status
```

4. If using Kubernetes, verify:

- controller `/healthz` and `/readyz`
- collector `/healthz` and `/readyz`
- `/api/v1/status` returns the expected `deployment` block
- fleet nodes appear with the right `cluster` and `deployment_mode` labels

5. Roll out to one node or one cluster subset first.

## What Remains Limited

- The repo still does not publish prebuilt images or a release service.
- The controller is not fully stateless yet.
- Helm templates were updated here, but this workspace did not have `helm` installed for a live render check.
- The distributed RAG path is currently a cleaner backend configuration story, not a new distributed retrieval framework.
