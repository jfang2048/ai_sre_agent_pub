# Deployment Assets

`deploy/` is organized around the maintained split runtime model:

- `docker/`: controller and collector images plus split compose assets
- `k8s/push-first/`: raw `cluster-lite` Kubernetes manifests
- `charts/sre-agent/`: Helm chart for `cluster-lite` or `distributed` rollout
- `systemd/`: source/binary-first fallback for environments outside container orchestration

The controller image carries the UI and controller defaults.
The collector image is the node-local data-plane component.

Key code and libraries behind these assets:

- controller bootstrap: `backend/cmd/controller/main.go` with `cobra`, `viper`, `zap`, and `grpc`
- collector bootstrap: `backend/cmd/collector/main.go`
- deployment default rewriting: `backend/internal/controller/deployment.go` `ApplyDeploymentDefaults()` and `backend/internal/collector/deployment.go`
- guarded startup posture: `backend/internal/controller/deployment_posture.go`

Recommended entry points:

| Scenario | Start here |
| --- | --- |
| local single-machine validation | [`../../docker-compose.yaml`](../docker-compose.yaml) |
| one controller host plus external collectors | [`docker/`](docker/) or [`systemd/`](systemd/) |
| quick Kubernetes deployment | [`k8s/push-first/`](k8s/push-first/) |
| parameterized cluster deployment | [`charts/sre-agent/`](charts/sre-agent/) |

Helm example values are now included for the two maintained cluster shapes:

- [`charts/sre-agent/examples/cluster-lite-values.yaml`](charts/sre-agent/examples/cluster-lite-values.yaml)
- [`charts/sre-agent/examples/distributed-values.yaml`](charts/sre-agent/examples/distributed-values.yaml)
