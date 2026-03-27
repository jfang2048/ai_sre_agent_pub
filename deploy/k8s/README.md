# Kubernetes Deployment Assets

Supported paths:

- `push-first/`: raw `cluster-lite` manifests for one controller `Deployment` plus collector `DaemonSet`
- `../charts/sre-agent/`: parameterized Helm chart for `cluster-lite` or `distributed` rollout

Use `push-first/` when you want the most direct manifest path.
Use the Helm chart when you need HA, ingress, HPA, or externalized RAG/vector settings.

If you stay on the raw `push-first/` path but need an external vector backend token, use the optional example secret:

- [`push-first/controller-rag-secret.example.yaml`](push-first/controller-rag-secret.example.yaml)
