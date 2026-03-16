#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BINARY="${ROOT_DIR}/build/sre-probe-core"

DURATION_SECONDS="${1:-20}"
INTERVAL_MS="${2:-200}"

if [[ ! -x "${BINARY}" ]]; then
  echo "probe-core binary not found, building..."
  make -C "${ROOT_DIR}" build-probe-core >/dev/null
fi

run_case() {
  local name="$1"
  shift
  local tmp
  tmp="$(mktemp)"
  local rc=0

  if command -v /usr/bin/time >/dev/null 2>&1; then
    if ! /usr/bin/time -f "user=%U sys=%S elapsed=%e rss_kb=%M" \
      timeout "${DURATION_SECONDS}" "${BINARY}" \
        --interval-ms "${INTERVAL_MS}" \
        --max-interval-ms "$((INTERVAL_MS * 6))" \
        --collectors host,disk,network,netlink,perf,process \
        --process-interval-samples 3 \
        --host-proc-fallback-interval-samples 10 \
        --pressure-interval-samples 3 \
        "$@" \
        >/dev/null 2>"${tmp}"; then
      rc=$?
    fi
  else
    TIMEFORMAT='user=%3U sys=%3S elapsed=%3R rss_kb=NA'
    if ! { time timeout "${DURATION_SECONDS}" "${BINARY}" \
      --interval-ms "${INTERVAL_MS}" \
      --max-interval-ms "$((INTERVAL_MS * 6))" \
      --collectors host,disk,network,netlink,perf,process \
      --process-interval-samples 3 \
      --host-proc-fallback-interval-samples 10 \
      --pressure-interval-samples 3 \
      "$@" \
      >/dev/null; } 2>"${tmp}"; then
      rc=$?
    fi
  fi

  if [[ ${rc} -ne 0 && ${rc} -ne 124 ]]; then
    echo "${name}: failed (exit ${rc})"
    sed -n '1,120p' "${tmp}"
    rm -f "${tmp}"
    return 1
  fi

  local line
  line="$(grep -E 'user=|sys=|elapsed=|rss_kb=' "${tmp}" | tail -n1)"
  echo "${name}: ${line}"
  rm -f "${tmp}"
}

echo "probe-core benchmark (duration=${DURATION_SECONDS}s interval=${INTERVAL_MS}ms)"
run_case "kernel-first(auto)" --host-mode auto
run_case "proc-primary" --host-mode proc
