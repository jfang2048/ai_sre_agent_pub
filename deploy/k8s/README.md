# Kubernetes Deployment Assets (v0.6)

Supported paths:

- `push-first/`: raw manifests for the split controller/collector runtime
- `../charts/sre-agent/`: Helm chart for the same split runtime model

The older single-purpose `aggregator`, `analyzer`, and `federation` manifests were removed because they did not map to maintained binaries in this repository and created public-facing drift.
