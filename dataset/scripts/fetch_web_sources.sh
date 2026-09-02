#!/usr/bin/env bash
set -euo pipefail

DATASET_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_LIST="${SRE_DATASET_SOURCE_LIST:-${DATASET_DIR}/metadata/web_sources.txt}"
OUT_DIR="${SRE_DATASET_WEB_OUT_DIR:-${DATASET_DIR}/sources/web}"
DRY_RUN=0

usage() {
  cat <<'USAGE'
usage: dataset/scripts/fetch_web_sources.sh [--dry-run]

Reads one HTTPS URL per line from SRE_DATASET_SOURCE_LIST and writes each page
to SRE_DATASET_WEB_OUT_DIR. Blank lines and lines beginning with # are ignored.
Successful output paths are written to stdout; diagnostics are written to stderr.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ ! -r "${SOURCE_LIST}" ]]; then
  echo "source list is not readable: ${SOURCE_LIST}" >&2
  exit 1
fi

mkdir -p "${OUT_DIR}"

while IFS= read -r raw || [[ -n "${raw}" ]]; do
  url="${raw%%#*}"
  url="${url#"${url%%[![:space:]]*}"}"
  url="${url%"${url##*[![:space:]]}"}"
  [[ -n "${url}" ]] || continue

  case "${url}" in
    https://*) ;;
    *)
      echo "only HTTPS sources are accepted: ${url}" >&2
      exit 2
      ;;
  esac

  url_path="${url%%\?*}"
  filename="${url_path##*/}"
  case "${filename}" in
    ""|.|..|*[!A-Za-z0-9._-]*)
      echo "source URL has no safe output filename: ${url}" >&2
      exit 2
      ;;
  esac

  output="${OUT_DIR}/${filename}"
  if [[ "${DRY_RUN}" == "1" ]]; then
    printf '%s\n' "${output}"
    continue
  fi

  partial="${output}.partial"
  if ! curl --fail --show-error --location --proto '=https' --retry 2 --output "${partial}" "${url}"; then
    rm -f "${partial}"
    exit 1
  fi
  mv "${partial}" "${output}"
  printf '%s\n' "${output}"
done <"${SOURCE_LIST}"
