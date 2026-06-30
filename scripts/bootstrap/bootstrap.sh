#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
INSTALL_UI=1
INSTALL_UI_TESTS=1
INSTALL_PYTHON=1
INSTALL_LFS=1

usage() {
  cat <<'USAGE'
usage: scripts/bootstrap/bootstrap.sh [options]

options:
  --skip-ui            Skip frontend npm install.
  --skip-ui-tests      Skip Playwright test dependency install.
  --skip-python        Skip python editable install.
  --skip-lfs           Skip git-lfs local hook installation.
  --help               Show this message.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-ui)
      INSTALL_UI=0
      shift
      ;;
    --skip-ui-tests)
      INSTALL_UI_TESTS=0
      shift
      ;;
    --skip-python)
      INSTALL_PYTHON=0
      shift
      ;;
    --skip-lfs)
      INSTALL_LFS=0
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

mkdir -p \
  "${ROOT_DIR}/build" \
  "${ROOT_DIR}/data/controller/ingest" \
  "${ROOT_DIR}/data/controller/tsdb" \
  "${ROOT_DIR}/data/agent/rag" \
  "${ROOT_DIR}/data/bootstrap/datasets/archives"

if [[ "${INSTALL_LFS}" == "1" ]]; then
  if command -v git-lfs >/dev/null 2>&1; then
    git -C "${ROOT_DIR}" lfs install --local >/dev/null
    echo "[bootstrap] git-lfs hooks installed locally"
  else
    echo "[bootstrap] git-lfs not installed; continuing without it"
  fi
fi

if [[ "${INSTALL_UI}" == "1" ]]; then
  if command -v npm >/dev/null 2>&1; then
    if [[ ! -d "${ROOT_DIR}/frontend/node_modules" ]]; then
      echo "[bootstrap] installing frontend dependencies"
      npm -C "${ROOT_DIR}/frontend" ci
    else
      echo "[bootstrap] frontend dependencies already present"
    fi
  else
    echo "[bootstrap] npm not found; skipping frontend dependency install"
  fi
fi

if [[ "${INSTALL_UI_TESTS}" == "1" ]]; then
  if command -v npm >/dev/null 2>&1; then
    if [[ ! -d "${ROOT_DIR}/tests/ui/node_modules" ]]; then
      echo "[bootstrap] installing UI test dependencies"
      npm -C "${ROOT_DIR}/tests/ui" ci
    else
      echo "[bootstrap] UI test dependencies already present"
    fi
  else
    echo "[bootstrap] npm not found; skipping UI test dependency install"
  fi
fi

if [[ "${INSTALL_PYTHON}" == "1" ]]; then
  if command -v python3 >/dev/null 2>&1; then
    if python3 -m pip --version >/dev/null 2>&1; then
      echo "[bootstrap] ensuring python package is available in editable mode"
      python3 -m pip install -e "${ROOT_DIR}/python" >/dev/null
    else
      echo "[bootstrap] python3 pip not available; skipping python install"
    fi
  else
    echo "[bootstrap] python3 not found; skipping python install"
  fi
fi

"${ROOT_DIR}/scripts/bootstrap/manage_optional_datasets.sh" status >/dev/null

echo "[bootstrap] complete"
