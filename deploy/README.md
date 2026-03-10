# Deployment Assets (v0.6)

`deploy/` is organized around the container-first runtime model:

- `docker/`: dedicated controller/collector Dockerfiles plus compose files for single-node demo and separated role deployment
- `k8s/push-first/`: supported Kubernetes manifests for split controller/collector deployment
- `charts/sre-agent/`: Helm chart for the same split runtime model
- `systemd/`: source/binary-first fallback for environments that are not container-managed

The controller image carries the web UI and controller defaults.
The collector image is replicated across observed hosts and ships telemetry to the controller over gRPC.

Recommended entry points:

- `docker-compose.yaml`: single-node controller + collector convenience stack
- `deploy/docker/docker-compose.controller.yml`: controller-only host
- `deploy/docker/docker-compose.collector.yml`: collector-only host

Files under `deploy/k8s/` outside `push-first/` are no longer the canonical deployment path.
The supported Kubernetes entry points are the push-first manifests and the Helm chart.
