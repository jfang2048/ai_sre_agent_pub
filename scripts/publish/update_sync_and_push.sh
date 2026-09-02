#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./common.sh
source "${SCRIPT_DIR}/common.sh"

TARGET_DIR="${SRE_PUBLISH_TARGET_DIR:-${PUBLISH_ROOT_DIR}_pub}"
REMOTE_NAME="${SRE_PUBLISH_REMOTE:-origin}"
TARGET_BRANCH="${SRE_PUBLISH_BRANCH:-v0.95}"
COMMIT_MESSAGE="${SRE_PUBLISH_COMMIT_MESSAGE:-snapshot v0.95 $(date '+%Y-%m-%d %H:%M:%S')}"
NO_PUSH=0

usage() {
  cat <<'USAGE'
usage: update_sync_and_push.sh [options]

options:
  --target-dir <dir>   Target mirror repository directory.
  --remote <name>      Remote name to push to (default: origin).
  --branch <name>      Version branch to replace (default: v0.95).
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
      TARGET_BRANCH="$2"
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

publish_ensure_repo "${TARGET_DIR}" "${TARGET_BRANCH}"
publish_ensure_identity "${TARGET_DIR}"
publish_configure_remote_if_requested "${TARGET_DIR}" "${REMOTE_NAME}"

cd "${TARGET_DIR}"
if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "target publish repository has uncommitted changes: ${TARGET_DIR}" >&2
  exit 1
fi

git checkout --orphan "__clean_${TARGET_BRANCH}" >/dev/null 2>&1
find . -mindepth 1 -maxdepth 1 ! -name '.git' -exec rm -rf {} +
publish_prepare_tree "${TARGET_DIR}"
git add -A
git commit -m "${COMMIT_MESSAGE}"
git branch -M "${TARGET_BRANCH}"
if [[ "${NO_PUSH}" == "1" ]]; then
  echo "snapshot branch created locally at ${TARGET_DIR} (push skipped)"
  exit 0
fi
if git remote get-url "${REMOTE_NAME}" >/dev/null 2>&1; then
  git push --force "${REMOTE_NAME}" "${TARGET_BRANCH}"
  echo "published snapshot branch ${TARGET_BRANCH} via ${TARGET_DIR}"
else
  echo "remote '${REMOTE_NAME}' is not configured for ${TARGET_DIR}; commit created locally only" >&2
fi
