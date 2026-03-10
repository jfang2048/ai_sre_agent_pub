#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
STORE_DIR="${SRE_OPTIONAL_DATASET_STORE:-${ROOT_DIR}/data/bootstrap/datasets/archives}"
MANIFEST_PATH="${ROOT_DIR}/dataset/raw/archives/manifest.json"
README_PATH="${ROOT_DIR}/dataset/raw/archives/README.md"

usage() {
  cat <<'USAGE'
usage: scripts/bootstrap/manage_optional_datasets.sh <command> [options]

commands:
  status               Show local optional archive status.
  import --from <dir>  Copy archive files from a source directory into the local bootstrap store.
  manifest             Rebuild dataset/raw/archives/manifest.json from the local bootstrap store.

options:
  --from <dir>         Source directory containing archive inputs.
USAGE
}

ensure_layout() {
  mkdir -p "${STORE_DIR}"
  mkdir -p "$(dirname "${MANIFEST_PATH}")"
}

write_readme() {
  cat >"${README_PATH}" <<'DOC'
# Optional Archive Corpora

`dataset/raw/archives/` is intentionally publish-safe in `v0.6`.

The original large archive inputs are not kept in the public source tree because they inflate clone size,
trigger GitHub large-file warnings, and do not need to be downloaded by every contributor.

Current policy:

- `dataset/raw/structured/` remains the tracked seed knowledge base
- large archive corpora live in the local-only bootstrap store: `data/bootstrap/datasets/archives/`
- this directory keeps only a manifest and usage notes

Use `scripts/bootstrap/manage_optional_datasets.sh import --from <dir>` to import archive corpora into the local bootstrap store.
When the local bootstrap directory exists, the local RAG tooling can include it through `SRE_AGENT_RAG_SOURCE_PATHS`.

See `manifest.json` for filenames, sizes, and checksums of the local-only archive set used during development.
DOC
}

rebuild_manifest() {
  ensure_layout
  write_readme
  python3 - <<'PY' "${STORE_DIR}" "${MANIFEST_PATH}"
from pathlib import Path
import hashlib
import json
import sys
store = Path(sys.argv[1])
manifest_path = Path(sys.argv[2])
archives = []
for path in sorted(store.glob('*')):
    if not path.is_file():
        continue
    data = path.read_bytes()
    archives.append({
        'filename': path.name,
        'size_bytes': path.stat().st_size,
        'sha256': hashlib.sha256(data).hexdigest(),
        'local_bootstrap_path': f'data/bootstrap/datasets/archives/{path.name}',
        'status': 'available-in-local-bootstrap-store',
    })
manifest = {
    'version': 'v0.6',
    'archives': archives,
}
manifest_path.write_text(json.dumps(manifest, indent=2) + '\n', encoding='utf-8')
PY
}

status() {
  ensure_layout
  rebuild_manifest
  python3 - <<'PY' "${STORE_DIR}" "${MANIFEST_PATH}"
from pathlib import Path
import json
import sys
store = Path(sys.argv[1])
manifest_path = Path(sys.argv[2])
manifest = json.loads(manifest_path.read_text(encoding='utf-8'))
print(f'local optional archive store: {store}')
print(f'archive count: {len(manifest.get("archives", []))}')
for item in manifest.get('archives', []):
    print(f"- {item['filename']} ({item['size_bytes']} bytes)")
PY
}

import_from() {
  local src_dir="$1"
  ensure_layout
  if [[ ! -d "${src_dir}" ]]; then
    echo "source directory not found: ${src_dir}" >&2
    exit 1
  fi

  shopt -s nullglob
  local copied=0
  local path
  for path in "${src_dir}"/*; do
    [[ -f "${path}" ]] || continue
    case "${path}" in
      *.zip|*.tar|*.tar.gz|*.tgz|*.gz|*.bz2|*.xz|*.7z|*.zedx)
        cp -f "${path}" "${STORE_DIR}/"
        copied=1
        ;;
    esac
  done
  shopt -u nullglob

  if [[ "${copied}" != "1" ]]; then
    echo "no supported archive files found in ${src_dir}" >&2
    exit 1
  fi

  rebuild_manifest
  status
}

main() {
  local cmd="${1:-status}"
  local src_dir=""
  shift || true
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --from)
        src_dir="$2"
        shift 2
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

  case "${cmd}" in
    status)
      status
      ;;
    manifest)
      rebuild_manifest
      status
      ;;
    import)
      if [[ -z "${src_dir}" ]]; then
        echo "import requires --from <dir>" >&2
        exit 1
      fi
      import_from "${src_dir}"
      ;;
    *)
      echo "unknown command: ${cmd}" >&2
      usage
      exit 1
      ;;
  esac
}

main "$@"
