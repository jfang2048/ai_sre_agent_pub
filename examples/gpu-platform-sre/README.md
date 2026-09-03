# GPU Platform SRE Demo

This directory wraps the existing AI SRE Agent with an executable Kubernetes GPU platform demo. The agent remains the node-local collector plus controller-side incident runtime; vLLM and KServe are external workloads used to produce observable GPU serving incidents.

## Execution modes

| Mode                          | Inputs                                                                                                             | State changed                                                                                                 | Success output                                                                                           | What it does not prove                                                   |
| ----------------------------- | ------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------ |
| Dry-run / CPU-safe validation | Fresh clone, shell, optional `python3`, optional `kubectl`/`helm`                                                  | None by default                                                                                               | `make gpu-platform-validate` and `make gpu-platform-smoke` complete with skipped cluster checks recorded | GPU scheduling, NVML telemetry, real Prometheus alerts, or model quality |
| Real GPU mode                 | Kubernetes cluster with NVIDIA GPU node, NVIDIA GPU Operator, Helm, Prometheus Operator CRDs, optional KServe CRDs | Namespaces, SRE Agent Helm release, observability CRDs, vLLM/KServe workloads, training Job, evidence folders | vLLM smoke response, GPU allocation visible, alerts/evidence captured, rollback commands validated       | Production readiness or zero downtime                                    |

## Fresh-clone validation

```bash
make gpu-platform-validate
make gpu-platform-smoke
make gpu-platform-evidence-template
```

Expected dry-run behavior:

- validates required files, shell syntax, Kubernetes YAML shape, and Makefile gates;
- prints exact missing dependencies such as `kubectl`, `helm`, Prometheus Operator CRDs, KServe CRDs, or GPU nodes;
- exits successfully when only real-cluster execution is skipped.

## Real GPU cluster sequence

```bash
kubectl get nodes -o wide
kubectl get nodes -o json | jq '.items[].status.allocatable'
kubectl get pods -n gpu-operator
kubectl describe node <gpu-node> | grep -E 'nvidia.com/gpu|Allocatable|Capacity'

SRE_AGENT_ENABLE_REAL_GPU_TESTS=1 examples/gpu-platform-sre/scripts/deploy-observability.sh
SRE_AGENT_ENABLE_REAL_GPU_TESTS=1 examples/gpu-platform-sre/scripts/deploy-agent.sh
SRE_AGENT_ENABLE_REAL_GPU_TESTS=1 examples/gpu-platform-sre/scripts/run-training-job.sh
SRE_AGENT_ENABLE_REAL_GPU_TESTS=1 examples/gpu-platform-sre/scripts/deploy-vllm.sh
examples/gpu-platform-sre/scripts/run-smoke.sh
```

Optional KServe path:

```bash
kubectl get crd inferenceservices.serving.kserve.io
SRE_AGENT_ENABLE_REAL_GPU_TESTS=1 examples/gpu-platform-sre/scripts/deploy-kserve-vllm.sh
```

## Incident drill loop

```bash
SRE_AGENT_ENABLE_REAL_GPU_TESTS=1 examples/gpu-platform-sre/scripts/inject-incident.sh bad-rollout
SRE_AGENT_ENABLE_REAL_GPU_TESTS=1 examples/gpu-platform-sre/scripts/rollback-serving.sh
examples/gpu-platform-sre/scripts/collect-evidence.sh bad-rollout
```

Evidence is written under `examples/gpu-platform-sre/evidence/<timestamp>-<incident-name>/`. Missing optional systems are recorded in the corresponding file instead of being hidden.

## Layout

- `manifests/` contains the Kubernetes objects.
- `scripts/` contains validation, deploy, smoke, rollback, and evidence helpers.
- runtime evidence is generated under `examples/gpu-platform-sre/evidence/` and is ignored by git.
