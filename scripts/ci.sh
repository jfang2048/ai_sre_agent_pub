#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FRONTEND_BUILD_OUTDIR="${SRE_FRONTEND_BUILD_OUTDIR:-/tmp/ai_sre_agent_frontend_build}"

if [[ "${SRE_SKIP_FMT_CHECK:-0}" == "1" ]]; then
  echo "[ci] backend format check skipped (SRE_SKIP_FMT_CHECK=1)"
else
  echo "[ci] backend format check"
  make -C "${ROOT_DIR}" fmt-check
fi

echo "[ci] backend vet"
make -C "${ROOT_DIR}" vet

echo "[ci] layered stability tests (backend + integration + e2e + python + frontend)"
make -C "${ROOT_DIR}" test-stability

echo "[ci] README screenshot references"
make -C "${ROOT_DIR}" verify-readme-screenshots

echo "[ci] screenshot tooling checks"
make -C "${ROOT_DIR}" test-screenshot-tools

if command -v npm >/dev/null 2>&1; then
  echo "[ci] frontend build (outDir=${FRONTEND_BUILD_OUTDIR})"
  npm -C "${ROOT_DIR}/frontend" run build -- --outDir "${FRONTEND_BUILD_OUTDIR}"
else
  echo "[ci] npm not found, skipping frontend checks"
fi

echo "[ci] all checks passed"
