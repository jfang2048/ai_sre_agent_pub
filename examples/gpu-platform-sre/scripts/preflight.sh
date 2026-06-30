#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
REQUIRE_CLUSTER=0
REQUIRE_GPU=0
REQUIRE_HELM=0
REQUIRE_PROM=0
REQUIRE_KSERVE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --require-cluster) REQUIRE_CLUSTER=1 ;;
    --require-gpu) REQUIRE_GPU=1 ;;
    --require-helm) REQUIRE_HELM=1 ;;
    --require-prometheus-operator) REQUIRE_PROM=1 ;;
    --require-kserve) REQUIRE_KSERVE=1 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

fail=0
info() { printf '[preflight] %s\n' "$*"; }
miss() { printf '[preflight] MISSING: %s\n' "$*"; fail=1; }

have() { command -v "$1" >/dev/null 2>&1; }
cluster_ok() { have kubectl && kubectl version --request-timeout=5s >/dev/null 2>&1; }
crd_exists() { cluster_ok && kubectl get crd "$1" >/dev/null 2>&1; }

info "repo=${ROOT_DIR}"

if have kubectl; then
  info "kubectl=$(command -v kubectl)"
else
  if [[ "${REQUIRE_CLUSTER}" -eq 1 ]]; then miss "kubectl command"; else info "kubectl not found; cluster checks skipped"; fi
fi

if cluster_ok; then
  info "cluster reachable"
  kubectl get nodes -o wide || true
else
  if [[ "${REQUIRE_CLUSTER}" -eq 1 ]]; then miss "reachable Kubernetes cluster"; else info "Kubernetes cluster not reachable; real-cluster checks skipped"; fi
fi

if [[ "${REQUIRE_HELM}" -eq 1 ]]; then
  have helm && info "helm=$(command -v helm)" || miss "helm command"
else
  have helm && info "helm=$(command -v helm)" || info "helm not found; Helm validation skipped"
fi

if cluster_ok; then
  if have jq; then
    gpu_count="$(kubectl get nodes -o json | jq '[.items[].status.allocatable["nvidia.com/gpu"] // "0" | tonumber] | add' 2>/dev/null || echo 0)"
  else
    gpu_count="$(kubectl get nodes -o jsonpath='{range .items[*]}{.status.allocatable.nvidia\.com/gpu}{"\n"}{end}' 2>/dev/null | awk '{s+=$1} END {print s+0}')"
  fi
  if [[ "${gpu_count:-0}" -gt 0 ]]; then
    info "allocatable nvidia.com/gpu=${gpu_count}"
  else
    if [[ "${REQUIRE_GPU}" -eq 1 ]]; then miss "allocatable nvidia.com/gpu on at least one node"; else info "no allocatable nvidia.com/gpu; GPU execution skipped"; fi
  fi
fi

if [[ "${REQUIRE_PROM}" -eq 1 ]]; then
  crd_exists servicemonitors.monitoring.coreos.com || miss "ServiceMonitor CRD"
  crd_exists podmonitors.monitoring.coreos.com || miss "PodMonitor CRD"
  crd_exists prometheusrules.monitoring.coreos.com || miss "PrometheusRule CRD"
else
  if crd_exists servicemonitors.monitoring.coreos.com; then info "Prometheus Operator CRDs detected"; else info "Prometheus Operator CRDs not detected; monitor/rule apply skipped"; fi
fi

if [[ "${REQUIRE_KSERVE}" -eq 1 ]]; then
  crd_exists inferenceservices.serving.kserve.io || miss "KServe InferenceService CRD"
else
  if crd_exists inferenceservices.serving.kserve.io; then info "KServe CRD detected"; else info "KServe CRD not detected; KServe demo skipped"; fi
fi

if [[ "${fail}" -ne 0 ]]; then
  exit 1
fi
info "preflight complete"
