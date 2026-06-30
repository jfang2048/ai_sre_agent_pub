#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CAPTURE_SCRIPT="${ROOT_DIR}/scripts/capture_ui_screenshots.mjs"
TMP_BASE="${ROOT_DIR}/.tmpbuild"
mkdir -p "${TMP_BASE}"
TMP_DIR="$(mktemp -d "${TMP_BASE}/screenshot_tooling.XXXXXX")"

cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

if ! command -v node >/dev/null 2>&1; then
  echo "[screenshot-tooling] node not found, skipping" >&2
  exit 0
fi

run() {
  (cd "${ROOT_DIR}" && "$@")
}

expect_fail_strict() {
  local name="$1"
  local pattern="$2"
  shift 2

  local log="${TMP_DIR}/${name}.log"
  local rc=0
  set +e
  run "$@" >"${log}" 2>&1
  rc=$?
  set -e

  if [[ "${rc}" -eq 0 ]]; then
    echo "expected ${name} to fail" >&2
    cat "${log}" >&2 || true
    exit 1
  fi
  if [[ "${rc}" -ne 2 ]]; then
    echo "expected ${name} to exit with code 2, got ${rc}" >&2
    cat "${log}" >&2 || true
    exit 1
  fi
  if ! grep -q "${pattern}" "${log}"; then
    echo "expected ${name} output to contain: ${pattern}" >&2
    cat "${log}" >&2 || true
    exit 1
  fi
}

echo "[screenshot-tooling] list keys"
keys_output="$(run node "${CAPTURE_SCRIPT}" --list-capture-keys)"
echo "${keys_output}" | grep -q 'dashboard_live'
echo "${keys_output}" | grep -q 'trends_memory'
echo "${keys_output}" | grep -q 'trends_nic'

echo "[screenshot-tooling] print plan"
plan_output="$(run env CAPTURE_ONLY=dashboard_live,trends_nic CAPTURE_BREAKDOWN_VARIANTS=nic node "${CAPTURE_SCRIPT}" --print-plan)"
echo "${plan_output}" | grep -q 'Selected captures (2): dashboard_live, trends_nic'

echo "[screenshot-tooling] non-strict warning path"
warning_output="$(run env CAPTURE_ONLY=bad_key node "${CAPTURE_SCRIPT}" --print-plan 2>&1)"
echo "${warning_output}" | grep -q 'Unknown CAPTURE_ONLY keys'

echo "[screenshot-tooling] strict unknown key"
expect_fail_strict strict_unknown_key 'Unknown CAPTURE_ONLY keys' \
  env CAPTURE_STRICT=1 CAPTURE_ONLY=bad_key node "${CAPTURE_SCRIPT}" --print-plan

echo "[screenshot-tooling] strict unknown variant"
expect_fail_strict strict_unknown_variant 'Unknown CAPTURE_BREAKDOWN_VARIANTS values' \
  env CAPTURE_STRICT=1 CAPTURE_BREAKDOWN_VARIANTS=bad_variant node "${CAPTURE_SCRIPT}" --print-plan

echo "[screenshot-tooling] strict mismatch"
expect_fail_strict strict_mismatch 'CAPTURE_ONLY requests breakdown keys not enabled' \
  env CAPTURE_STRICT=1 CAPTURE_ONLY=trends_cpu CAPTURE_BREAKDOWN_VARIANTS=nic node "${CAPTURE_SCRIPT}" --print-plan

echo "[screenshot-tooling] readme capture minimum stabilize wait"
grep -q 'const MIN_POST_READY_WAIT_MS = 5000' "${ROOT_DIR}/scripts/capture_readme_screenshots.mjs"
grep -q 'routeStabilizeMs' "${ROOT_DIR}/scripts/capture_readme_screenshots.mjs"

echo "[screenshot-tooling] ok"
