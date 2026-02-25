#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
README_PATH="${ROOT_DIR}/README.md"

if [[ ! -f "${README_PATH}" ]]; then
  echo "README not found: ${README_PATH}" >&2
  exit 1
fi

mapfile -t refs < <(
  sed -nE 's/.*!\[[^]]*\]\((screenshot\/[^)]+)\).*/\1/p' "${README_PATH}" | sort -u
)

if [[ "${#refs[@]}" -eq 0 ]]; then
  echo "No screenshot references found in README.md" >&2
  exit 1
fi

missing=0
invalid=0

for rel in "${refs[@]}"; do
  path="${ROOT_DIR}/${rel}"
  if [[ ! -f "${path}" ]]; then
    echo "Missing screenshot: ${rel}" >&2
    missing=1
    continue
  fi
  if [[ ! -s "${path}" ]]; then
    echo "Empty screenshot file: ${rel}" >&2
    invalid=1
    continue
  fi

  if command -v file >/dev/null 2>&1; then
    if ! file "${path}" | grep -q 'PNG image data'; then
      echo "Not a PNG image file: ${rel}" >&2
      invalid=1
      continue
    fi
  fi
done

if [[ "${missing}" -ne 0 || "${invalid}" -ne 0 ]]; then
  exit 1
fi

echo "README screenshot references verified (${#refs[@]} files)."
