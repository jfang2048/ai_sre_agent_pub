#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_BASE="${ROOT_DIR}/.tmpbuild"
mkdir -p "${TMP_BASE}"
TMP_DIR="$(mktemp -d "${TMP_BASE}/dataset-fetch.XXXXXX")"
trap 'rm -rf "${TMP_DIR}"' EXIT

SOURCE_LIST="${TMP_DIR}/sources.txt"
OUT_DIR="${TMP_DIR}/out"
printf '%s\n' '# comment' '' 'https://example.com/one.html' '  https://example.com/two.txt  ' >"${SOURCE_LIST}"

output="$(
  SRE_DATASET_SOURCE_LIST="${SOURCE_LIST}" \
  SRE_DATASET_WEB_OUT_DIR="${OUT_DIR}" \
    "${ROOT_DIR}/dataset/scripts/fetch_web_sources.sh" --dry-run
)"

expected="${OUT_DIR}/one.html
${OUT_DIR}/two.txt"
if [[ "${output}" != "${expected}" ]]; then
  echo "unexpected dry-run output" >&2
  printf 'expected:\n%s\nactual:\n%s\n' "${expected}" "${output}" >&2
  exit 1
fi

printf '%s\n' 'http://example.com/private.txt' >"${SOURCE_LIST}"
if SRE_DATASET_SOURCE_LIST="${SOURCE_LIST}" SRE_DATASET_WEB_OUT_DIR="${OUT_DIR}" \
  "${ROOT_DIR}/dataset/scripts/fetch_web_sources.sh" --dry-run >/dev/null 2>&1; then
  echo "non-HTTPS source was accepted" >&2
  exit 1
fi

echo "[dataset-fetch] ok"
