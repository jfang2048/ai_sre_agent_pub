#!/usr/bin/env bash
# security-scan.sh — Orchestrates repo security scanners for CI and local use.
# Exit code: 0 = clean, 1 = findings/tooling failure.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${SECURITY_SCAN_OUT_DIR:-${ROOT_DIR}/build/security}"
GO_CACHE="${GO_CACHE:-${ROOT_DIR}/.gocache}"
AUDIT_FAIL_ON="${SECURITY_AUDIT_FAIL_ON:-fail}"
GOVULNCHECK_MODE="${SECURITY_SCAN_GOVULNCHECK_MODE:-report}"

FAIL=0

mkdir -p "${OUT_DIR}" "${GO_CACHE}"

section() { printf '\n==> %s\n' "$1"; }
pass()    { printf '  [PASS] %s\n' "$1"; }
warn()    { printf '  [WARN] %s\n' "$1"; }
fail()    { printf '  [FAIL] %s\n' "$1"; FAIL=1; }
skip()    { printf '  [SKIP] %s\n' "$1"; }

is_truthy() {
  case "$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]')" in
    1|true|yes|on) return 0 ;;
    *) return 1 ;;
  esac
}

strict_mode() {
  is_truthy "${SECURITY_SCAN_STRICT:-0}"
}

normalize_mode() {
  case "$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]')" in
    ""|report) printf 'report' ;;
    fail) printf 'fail' ;;
    off|skip|disabled) printf 'off' ;;
    *) printf 'report' ;;
  esac
}

require_tool() {
  local tool="$1"
  local label="$2"
  if command -v "${tool}" >/dev/null 2>&1; then
    return 0
  fi
  if strict_mode; then
    fail "${label} (missing tool: ${tool})"
  else
    skip "${label} (missing tool: ${tool})"
  fi
  return 1
}

show_failure_output() {
  local file="$1"
  if [[ -f "${file}" ]]; then
    sed -n '1,120p' "${file}"
  fi
}

run_check() {
  local label="$1"
  local output_file="$2"
  shift 2

  if "$@" >"${output_file}" 2>&1; then
    pass "${label}"
    return 0
  fi

  show_failure_output "${output_file}"
  fail "${label}"
  return 1
}

# ---------------------------------------------------------------------------
# 1. Go dependency vulnerability check (SCA)
# ---------------------------------------------------------------------------
section "Go dependency vulnerability check (govulncheck)"
GOVULNCHECK_MODE="$(normalize_mode "${GOVULNCHECK_MODE}")"
if [[ "${GOVULNCHECK_MODE}" == "off" ]]; then
  skip "govulncheck (disabled via SECURITY_SCAN_GOVULNCHECK_MODE=off)"
elif require_tool govulncheck "govulncheck"; then
  GOVULNCHECK_OUT="${OUT_DIR}/govulncheck.txt"
  if {
    printf 'go version: '
    go version
    printf 'mode: %s\n\n' "${GOVULNCHECK_MODE}"
    env GOCACHE="${GO_CACHE}" govulncheck -C "${ROOT_DIR}/backend" ./...
  } >"${GOVULNCHECK_OUT}" 2>&1; then
    pass "govulncheck"
  else
    show_failure_output "${GOVULNCHECK_OUT}"
    if [[ "${GOVULNCHECK_MODE}" == "fail" ]]; then
      fail "govulncheck"
    else
      warn "govulncheck findings recorded (report-only mode)"
    fi
  fi
fi

# ---------------------------------------------------------------------------
# 2. Go static security analysis (SAST)
# ---------------------------------------------------------------------------
section "Go static security analysis (gosec)"
if require_tool gosec "gosec"; then
  # Block merge/release on actionable high-severity findings. Keep the full
  # report as an artifact so lower-severity hardening work remains visible
  # without making every advisory or intentional systems call a release gate.
  run_check "gosec high-severity gate" "${OUT_DIR}/gosec.txt" \
    gosec -quiet -severity=high -confidence=medium \
      -nosec-require-rules -nosec-require-justification \
      -exclude-dir=vendor "${ROOT_DIR}/backend/..." || true
  if gosec -quiet -no-fail -exclude-dir=vendor "${ROOT_DIR}/backend/..." \
      >"${OUT_DIR}/gosec-all.txt" 2>&1; then
    pass "gosec full advisory report"
  else
    fail "gosec full advisory report generation"
  fi
fi

# ---------------------------------------------------------------------------
# 3. Python dependency vulnerability check (SCA)
# ---------------------------------------------------------------------------
section "Python dependency vulnerability check (pip-audit)"
if [[ -f "${ROOT_DIR}/python/pyproject.toml" || -f "${ROOT_DIR}/python/setup.py" ]]; then
  if require_tool python3 "python3 for dependency extraction" && require_tool pip-audit "pip-audit"; then
    REQ_FILE="${OUT_DIR}/python-requirements.txt"
    if python3 - "${ROOT_DIR}" "${REQ_FILE}" >"${OUT_DIR}/python-requirements.log" 2>&1 <<'PY'
import ast
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
out = pathlib.Path(sys.argv[2])
deps = []

pyproject = root / "python" / "pyproject.toml"
if pyproject.exists():
    parser = None
    try:
        import tomllib as parser  # py3.11+
    except ModuleNotFoundError:
        try:
            import tomli as parser  # py3.10 fallback when installed
        except ModuleNotFoundError:
            parser = None
    if parser is not None:
        data = parser.loads(pyproject.read_text(encoding="utf-8"))
        for dep in data.get("project", {}).get("dependencies", []):
            if isinstance(dep, str) and dep.strip():
                deps.append(dep.strip())

if not deps:
    setup_py = root / "python" / "setup.py"
    if setup_py.exists():
        tree = ast.parse(setup_py.read_text(encoding="utf-8"))
        for node in ast.walk(tree):
            if not isinstance(node, ast.Call):
                continue
            func = node.func
            is_setup = (
                isinstance(func, ast.Name) and func.id == "setup"
            ) or (
                isinstance(func, ast.Attribute) and func.attr == "setup"
            )
            if not is_setup:
                continue
            for kw in node.keywords:
                if kw.arg != "install_requires":
                    continue
                if isinstance(kw.value, (ast.List, ast.Tuple)):
                    for item in kw.value.elts:
                        if isinstance(item, ast.Constant) and isinstance(item.value, str) and item.value.strip():
                            deps.append(item.value.strip())

seen = set()
ordered = []
for dep in deps:
    if dep not in seen:
        seen.add(dep)
        ordered.append(dep)

if not ordered:
    raise SystemExit("no Python dependencies discovered from pyproject.toml or setup.py")

out.write_text("\n".join(ordered) + "\n", encoding="utf-8")
PY
    then
      run_check "pip-audit" "${OUT_DIR}/pip-audit.txt" \
        pip-audit -r "${REQ_FILE}" --progress-spinner off || true
    else
      show_failure_output "${OUT_DIR}/python-requirements.log"
      fail "python dependency extraction for pip-audit"
    fi
  fi
else
  pass "pip-audit (no python package metadata found)"
fi

# ---------------------------------------------------------------------------
# 4. Python static analysis (SAST)
# ---------------------------------------------------------------------------
section "Python static security analysis (bandit)"
if [[ -d "${ROOT_DIR}/python" ]]; then
  if require_tool bandit "bandit"; then
    run_check "bandit" "${OUT_DIR}/bandit.txt" \
      bandit -r "${ROOT_DIR}/python" -q --severity-level medium || true
  fi
else
  pass "bandit (python/ not present)"
fi

# ---------------------------------------------------------------------------
# 5. C++ static analysis (SAST)
# ---------------------------------------------------------------------------
section "C++ static security analysis (cppcheck)"
if [[ -d "${ROOT_DIR}/cpp" ]]; then
  if require_tool cppcheck "cppcheck"; then
    run_check "cppcheck" "${OUT_DIR}/cppcheck.txt" \
      cppcheck --quiet --std=c++20 --enable=warning,portability,performance --error-exitcode=1 \
      --suppress=missingIncludeSystem --suppress=missingInclude --suppress=syntaxError \
      --suppress=unknownMacro --suppress=*:*/generated/* "${ROOT_DIR}/cpp" || true
  fi
else
  pass "cppcheck (cpp/ not present)"
fi

# ---------------------------------------------------------------------------
# 6. Secret detection
# ---------------------------------------------------------------------------
section "Secret detection (gitleaks)"
if require_tool gitleaks "gitleaks"; then
  run_check "gitleaks" "${OUT_DIR}/gitleaks.txt" \
    gitleaks detect --source="${ROOT_DIR}" --no-banner --no-color || true
fi

# ---------------------------------------------------------------------------
# 7. Public repository privacy audit
# ---------------------------------------------------------------------------
section "Public repository privacy audit"
run_check "public repository privacy audit" "${OUT_DIR}/public-repo-audit.txt" \
  "${ROOT_DIR}/scripts/publish/audit_repository.sh" || true

# ---------------------------------------------------------------------------
# 8. Dockerfile linting
# ---------------------------------------------------------------------------
section "Dockerfile linting (hadolint)"
if require_tool hadolint "hadolint"; then
  dockerfiles=()
  while IFS= read -r -d '' f; do
    dockerfiles+=("$f")
  done < <(find "${ROOT_DIR}" -name 'Dockerfile*' \
      -not -path '*/vendor/*' \
      -not -path '*/.gomodcache/*' \
      -not -path '*/.gocache/*' \
      -not -path '*/node_modules/*' \
      -print0 2>/dev/null)

  if [[ ${#dockerfiles[@]} -eq 0 ]]; then
    pass "hadolint (no Dockerfiles found)"
  else
    hadolint_ok=true
    : > "${OUT_DIR}/hadolint.txt"
    for df in "${dockerfiles[@]}"; do
      if ! hadolint "${df}" >>"${OUT_DIR}/hadolint.txt" 2>&1; then
        hadolint_ok=false
      fi
    done
    if "${hadolint_ok}"; then
      pass "hadolint"
    else
      show_failure_output "${OUT_DIR}/hadolint.txt"
      fail "hadolint"
    fi
  fi
fi

# ---------------------------------------------------------------------------
# 9. Basic configuration linting
# ---------------------------------------------------------------------------
section "Configuration linting (yamllint)"
if require_tool yamllint "yamllint"; then
  lint_targets=(
    "${ROOT_DIR}/configs"
    "${ROOT_DIR}/.github/workflows"
    "${ROOT_DIR}/docker-compose.yaml"
    "${ROOT_DIR}/deploy/docker/docker-compose-tsdb.yml"
    "${ROOT_DIR}/deploy/docker/docker-compose.host-observer.yml"
    "${ROOT_DIR}/deploy/charts/sre-agent/values.yaml"
  )
  existing_targets=()
  for target in "${lint_targets[@]}"; do
    if [[ -e "${target}" ]]; then
      existing_targets+=("${target}")
    fi
  done
  if [[ ${#existing_targets[@]} -eq 0 ]]; then
    pass "yamllint (no YAML targets found)"
  else
    run_check "yamllint" "${OUT_DIR}/yamllint.txt" \
      yamllint -c "${ROOT_DIR}/.yamllint.yml" "${existing_targets[@]}" || true
  fi
fi

# ---------------------------------------------------------------------------
# 10. Runtime security audit (built-in)
# ---------------------------------------------------------------------------
section "Runtime security audit (security-audit CLI)"
if run_check "security-audit build" "${OUT_DIR}/security-audit-build.txt" \
  env GOCACHE="${GO_CACHE}" go -C "${ROOT_DIR}/backend" build -o /dev/null ./cmd/security-audit; then

  run_check "security-audit json report" "${OUT_DIR}/security-audit-json.log" \
    env GOCACHE="${GO_CACHE}" go -C "${ROOT_DIR}/backend" run ./cmd/security-audit \
    -root "${ROOT_DIR}" -format json -output "${OUT_DIR}/runtime-audit.json" -fail-on none || true

  if run_check "security-audit markdown report" "${OUT_DIR}/security-audit-md.log" \
    env GOCACHE="${GO_CACHE}" go -C "${ROOT_DIR}/backend" run ./cmd/security-audit \
    -root "${ROOT_DIR}" -format markdown -output "${OUT_DIR}/runtime-audit.md" -fail-on "${AUDIT_FAIL_ON}"; then
    pass "security-audit fail threshold (${AUDIT_FAIL_ON})"
  else
    show_failure_output "${OUT_DIR}/runtime-audit.md"
    fail "security-audit fail threshold (${AUDIT_FAIL_ON})"
  fi
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo ""
echo "Security reports: ${OUT_DIR}"
if [[ "${FAIL}" -ne 0 ]]; then
  echo "Security scan completed with findings."
  exit 1
fi

echo "All security checks passed."
