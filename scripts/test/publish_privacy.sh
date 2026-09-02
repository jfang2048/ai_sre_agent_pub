#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../publish/common.sh
source "${ROOT_DIR}/scripts/publish/common.sh"

TMP_BASE="${ROOT_DIR}/.tmpbuild"
mkdir -p "${TMP_BASE}"
TMP_DIR="$(mktemp -d "${TMP_BASE}/publish-privacy.XXXXXX")"
UNTRACKED_FILE="$(mktemp "${ROOT_DIR}/publish-untracked.XXXXXX")"

cleanup() {
  rm -f "${UNTRACKED_FILE}"
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

fail() {
  echo "[publish-privacy] $*" >&2
  exit 1
}

TARGET_DIR="${TMP_DIR}/public-tree"
publish_copy_worktree "${TARGET_DIR}"

if [[ -e "${TARGET_DIR}/$(basename "${UNTRACKED_FILE}")" ]]; then
  fail "untracked file was copied into the public tree"
fi

publish_history_audit || fail "repository history audit failed"
publish_secret_name_audit "${TARGET_DIR}" || fail "clean public tree failed filename audit"
publish_sensitive_content_audit "${TARGET_DIR}" || fail "clean public tree failed content audit"

touch "${TARGET_DIR}/private.pem"
if publish_secret_name_audit "${TARGET_DIR}" >/dev/null 2>&1; then
  fail "secret filename was accepted"
fi
rm -f "${TARGET_DIR}/private.pem"

printf 'AKIA%s\n' '1234567890ABCDEF' >"${TARGET_DIR}/credential.txt"
if publish_sensitive_content_audit "${TARGET_DIR}" >/dev/null 2>&1; then
  fail "credential-shaped content was accepted"
fi
rm -f "${TARGET_DIR}/credential.txt"

echo "[publish-privacy] ok"
