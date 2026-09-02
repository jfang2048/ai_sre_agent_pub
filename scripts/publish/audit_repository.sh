#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./common.sh
source "${SCRIPT_DIR}/common.sh"

publish_require_git_repo
TMP_BASE="${PUBLISH_ROOT_DIR}/.tmpbuild"
mkdir -p "${TMP_BASE}"
AUDIT_TREE="$(mktemp -d "${TMP_BASE}/public-audit.XXXXXX")"
trap 'rm -rf "${AUDIT_TREE}"' EXIT

publish_history_audit
publish_copy_worktree "${AUDIT_TREE}"
publish_file_audit "${AUDIT_TREE}"
publish_secret_name_audit "${AUDIT_TREE}"
publish_sensitive_content_audit "${AUDIT_TREE}"

echo "public repository audit passed"
