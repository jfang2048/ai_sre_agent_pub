#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./common.sh
source "${SCRIPT_DIR}/common.sh"

TARGET_DIR="${SRE_PUBLISH_TARGET_DIR:-${PUBLISH_ROOT_DIR}_pub}"
REMOTE_NAME="${SRE_PUBLISH_REMOTE:-origin}"
BRANCH="${SRE_PUBLISH_BRANCH:-main}"
COMMIT_MESSAGE="${SRE_PUBLISH_COMMIT_MESSAGE:-Publish AI SRE Agent v0.95 $(date '+%Y-%m-%d %H:%M:%S')}"
NO_PUSH=0

usage() {
  cat <<'USAGE'
usage: update_github.sh [options]

options:
  --target-dir <dir>   Target mirror repository directory.
  --remote <name>      Remote name to push to (default: origin).
  --branch <name>      Branch to update (default: main).
  --message <msg>      Commit message.
  --no-push            Prepare and commit locally without pushing.
  --help               Show this message.
USAGE
  publish_usage_common
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target-dir)
      TARGET_DIR="$2"
      shift 2
      ;;
    --remote)
      REMOTE_NAME="$2"
      shift 2
      ;;
    --branch)
      BRANCH="$2"
      shift 2
      ;;
    --message)
      COMMIT_MESSAGE="$2"
      shift 2
      ;;
    --no-push)
      NO_PUSH=1
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

publish_ensure_repo "${TARGET_DIR}" "${BRANCH}"
publish_ensure_identity "${TARGET_DIR}"
publish_configure_remote_if_requested "${TARGET_DIR}" "${REMOTE_NAME}"

git -C "${TARGET_DIR}" checkout "${BRANCH}" >/dev/null 2>&1 || git -C "${TARGET_DIR}" checkout -b "${BRANCH}" >/dev/null 2>&1
if ! git -C "${TARGET_DIR}" diff --quiet || ! git -C "${TARGET_DIR}" diff --cached --quiet; then
  echo "target publish repository has uncommitted changes: ${TARGET_DIR}" >&2
  exit 1
fi
if git -C "${TARGET_DIR}" remote get-url "${REMOTE_NAME}" >/dev/null 2>&1; then
  git -C "${TARGET_DIR}" pull --rebase --autostash "${REMOTE_NAME}" "${BRANCH}" >/dev/null 2>&1 || true
fi

publish_prepare_tree "${TARGET_DIR}"
git -C "${TARGET_DIR}" add -A
if git -C "${TARGET_DIR}" diff --cached --quiet; then
  echo "no publish changes detected in ${TARGET_DIR}"
  exit 0
fi

git -C "${TARGET_DIR}" commit -m "${COMMIT_MESSAGE}"
if [[ "${NO_PUSH}" == "1" ]]; then
  echo "publish commit created locally at ${TARGET_DIR} (push skipped)"
  exit 0
fi

if git -C "${TARGET_DIR}" remote get-url "${REMOTE_NAME}" >/dev/null 2>&1; then
  git -C "${TARGET_DIR}" push "${REMOTE_NAME}" "${BRANCH}"
  echo "published ${BRANCH} via ${TARGET_DIR}"
else
  echo "remote '${REMOTE_NAME}' is not configured for ${TARGET_DIR}; commit created locally only" >&2
fi
