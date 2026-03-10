#!/usr/bin/env bash
set -euo pipefail

PUBLISH_ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PUBLISH_MAX_FILE_BYTES="${SRE_PUBLISH_MAX_FILE_BYTES:-47185920}"
PUBLISH_WARN_FILE_BYTES="${SRE_PUBLISH_WARN_FILE_BYTES:-20971520}"

publish_usage_common() {
  cat <<'USAGE'
Environment:
  SRE_PUBLISH_TARGET_DIR        Target mirror repo or prepared tree directory.
  SRE_PUBLISH_REMOTE            Remote name used when pushing (default: origin).
  SRE_PUBLISH_BRANCH            Branch used when pushing (default: main).
  SRE_PUBLISH_MAX_FILE_BYTES    Hard size limit for publish tree files (default: 45 MiB).
  SRE_PUBLISH_WARN_FILE_BYTES   Warning threshold for publish tree files (default: 20 MiB).
USAGE
}

publish_require_git_repo() {
  git -C "${PUBLISH_ROOT_DIR}" rev-parse --is-inside-work-tree >/dev/null 2>&1 || {
    echo "not inside a git repository: ${PUBLISH_ROOT_DIR}" >&2
    exit 1
  }
}

publish_copy_worktree() {
  local dst="$1"
  publish_assert_safe_destination "${dst}"
  mkdir -p "${dst}"
  find "${dst}" -mindepth 1 -maxdepth 1 ! -name '.git' -exec rm -rf {} +
  (
    cd "${PUBLISH_ROOT_DIR}"
    git ls-files -z --cached --others --exclude-standard | \
      while IFS= read -r -d '' path; do \
        if [[ -e "${path}" ]]; then \
          printf '%s\0' "${path}"; \
        fi; \
      done | \
      tar --null -T - -cf -
  ) | (
    cd "${dst}"
    tar -xf -
  )
}

publish_assert_safe_destination() {
  local dst="$1"
  local root_real dst_real
  root_real="$(cd "${PUBLISH_ROOT_DIR}" && pwd -P)"
  dst_real="$(mkdir -p "${dst}" && cd "${dst}" && pwd -P)"
  case "${dst_real}" in
    ""|"/")
      echo "refusing to use unsafe publish destination: ${dst}" >&2
      exit 1
      ;;
  esac
  if [[ "${dst_real}" == "${root_real}" ]]; then
    echo "refusing to use repository root as publish destination: ${dst}" >&2
    exit 1
  fi
}

publish_copy_tracked_metadata() {
  local dst="$1"
  mkdir -p "${dst}"
  cp -f "${PUBLISH_ROOT_DIR}/LICENSE" "${dst}/LICENSE"
}

publish_file_audit() {
  local dst="$1"
  local oversized=0
  local warned=0
  while IFS= read -r -d '' file; do
    local size
    size=$(wc -c <"${file}")
    if (( size > PUBLISH_MAX_FILE_BYTES )); then
      printf 'publish size violation: %s bytes %s\n' "${size}" "${file#${dst}/}" >&2
      oversized=1
    elif (( size > PUBLISH_WARN_FILE_BYTES )); then
      printf 'publish size warning: %s bytes %s\n' "${size}" "${file#${dst}/}" >&2
      warned=1
    fi
  done < <(find "${dst}" -type f -print0)

  if (( oversized != 0 )); then
    return 1
  fi
  return 0
}

publish_secret_name_audit() {
  local dst="$1"
  local found=0
  while IFS= read -r -d '' file; do
    case "${file}" in
      *.pem|*.key|*.p12|*.pfx|*.crt|*.csr|*.der|*.jks|*.keystore|*.secret|*.secrets|*.token|*.tokens|*.credentials)
        printf 'publish secret-name violation: %s\n' "${file#${dst}/}" >&2
        found=1
        ;;
    esac
  done < <(find "${dst}" -type f -print0)
  if (( found != 0 )); then
    return 1
  fi
  return 0
}

publish_prepare_tree() {
  local dst="$1"
  publish_require_git_repo
  publish_copy_worktree "${dst}"
  publish_copy_tracked_metadata "${dst}"
  publish_file_audit "${dst}"
  publish_secret_name_audit "${dst}"
}

publish_ensure_repo() {
  local dst="$1"
  local branch="$2"
  if [[ ! -d "${dst}/.git" ]]; then
    git init -b "${branch}" "${dst}" >/dev/null
  fi
}

publish_ensure_identity() {
  local repo_dir="$1"
  local name
  local email
  name="$(git -C "${PUBLISH_ROOT_DIR}" config user.name || true)"
  email="$(git -C "${PUBLISH_ROOT_DIR}" config user.email || true)"
  if [[ -n "${name}" && -z "$(git -C "${repo_dir}" config user.name || true)" ]]; then
    git -C "${repo_dir}" config user.name "${name}"
  fi
  if [[ -n "${email}" && -z "$(git -C "${repo_dir}" config user.email || true)" ]]; then
    git -C "${repo_dir}" config user.email "${email}"
  fi
}

publish_configure_remote_if_requested() {
  local repo_dir="$1"
  local remote_name="$2"
  local remote_url="${SRE_PUBLISH_REMOTE_URL:-}"
  if [[ -z "${remote_url}" ]]; then
    return
  fi
  if git -C "${repo_dir}" remote get-url "${remote_name}" >/dev/null 2>&1; then
    git -C "${repo_dir}" remote set-url "${remote_name}" "${remote_url}"
    return
  fi
  git -C "${repo_dir}" remote add "${remote_name}" "${remote_url}"
}
