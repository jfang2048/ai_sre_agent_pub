# Push-First Kubernetes Deployment (Controller + Collector)

This folder contains baseline manifests for the push-first architecture.

## Components

- `sre-controller`: control plane (HTTP API/UI + gRPC ingest + `/metrics`)
- `sre-collector`: node daemon (host/process/log/GPU collection + push)

## Important current note

`collector-daemonset.yaml` currently includes collector args:

```yaml
args:
  - "--level"
  - "2"
```

Current collector binary does not support a `--level` CLI flag. Before deployment, remove these args and set level via config/env (`SRE_COLLECTOR_LEVEL`).

Example patch after apply:

```bash
kubectl -n sre-agent patch daemonset sre-collector --type='json' -p='[
  {"op":"remove","path":"/spec/template/spec/containers/0/args"},
  {"op":"add","path":"/spec/template/spec/containers/0/env/-","value":{"name":"SRE_COLLECTOR_LEVEL","value":"5"}}
]'
```

## Build images

```bash
docker build -f deploy/docker/Dockerfile --target controller -t sre-agent/sre-controller:latest .
docker build -f deploy/docker/Dockerfile --target collector -t sre-agent/sre-collector:latest .
```

## Apply manifests

```bash
kubectl apply -f deploy/k8s/push-first/namespace.yaml
kubectl apply -f deploy/k8s/push-first/controller.yaml
kubectl apply -f deploy/k8s/push-first/collector-daemonset.yaml
```

Then apply the daemonset patch above.

## Verify

```bash
kubectl -n sre-agent rollout status deploy/sre-controller
kubectl -n sre-agent rollout status ds/sre-collector
kubectl -n sre-agent port-forward svc/sre-controller 8080:8080
curl -s http://127.0.0.1:8080/api/v1/fleet | head
curl -s http://127.0.0.1:8080/api/v1/top/programs?limit=20 | head
```

## GPU notes

- GPU metrics require `nvidia-smi` availability in collector runtime.
- NVIDIA runtime configuration is cluster-specific (GPU Operator/device plugin/runtimeClass).
- The daemonset sets common runtime env hints:
  - `NVIDIA_VISIBLE_DEVICES=all`
  - `NVIDIA_DRIVER_CAPABILITIES=utility,compute`
