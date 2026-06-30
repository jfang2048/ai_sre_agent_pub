#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="$ROOT_DIR/sources/web"
mkdir -p "$OUT_DIR"

curl -L \
  "https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/troubleshooting.html" \
  -o "$OUT_DIR/gpu-operator-troubleshooting.html"

curl -L \
  "https://docs.nvidia.com/datacenter/dcgm/latest/user-guide/debugging-and-troubleshooting.html" \
  -o "$OUT_DIR/dcgm-debugging-and-troubleshooting.html"
