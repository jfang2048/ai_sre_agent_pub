# Security Policy

## Responsible Disclosure

If you discover a vulnerability:

1. Do not open a public issue.
2. Contact maintainers privately with:
   - impact summary
   - affected components/versions
   - reproduction steps
   - suggested remediation (if available)
3. Allow up to 72 hours for initial triage response.

## Security Architecture and Threat Model

This repository contains:

- `sre-collector`: host-side telemetry collection and push
- `sre-controller`: ingest, diagnostics, API/UI, and optional AGENT execution

Primary trust boundaries:

- monitored host runtime (`/proc`, `/sys`, GPU, logs)
- collector-to-controller transport
- controller API/UI/operator boundary
- optional remediation execution boundary

Detailed threat model:

- `docs/security/threat-model.md`

## Secure Deployment Recommendations

### Network and API Exposure

- Keep controller listeners loopback-only unless behind authenticated ingress.
- Enable controller API key auth for non-local deployments:

```yaml
auth:
  enabled: true
  api_key_env: SRE_AGENT_CONTROLLER_API_KEY
```

### Transport Security

- Enable TLS for collector transport to controller in production.
- Never use `insecure_skip_verify: true`.

```yaml
transport:
  tls:
    enabled: true
    insecure_skip_verify: false
    ca_file: /path/to/ca.pem
    cert_file: /path/to/cert.pem
    key_file: /path/to/key.pem
```

### Secrets and File Permissions

- Inject secrets via environment variables or mounted files, never via VCS.
- Keep sensitive files owner-only (`0600`).
- Run runtime audit with production-like env vars so permission checks are effective.

### Least Privilege

- Use hardened container settings (`read_only`, `no-new-privileges`, `cap_drop: [ALL]`).
- Keep RBAC read-only by default (`get/list/watch`) and scope mutating verbs to explicit overrides.
- Keep `hostPID` disabled unless explicitly required and reviewed.

## Automated Security Checks

### Local Commands

```bash
# Local-friendly mode (missing tools are skipped)
make security-scan

# Strict mode (fail on missing tools/findings; CI mode)
SECURITY_SCAN_STRICT=1 make security-scan

# Built-in runtime audit only
make security-audit
```

### What `security-scan` Runs

| Check | Tool | Purpose |
| --- | --- | --- |
| Go SCA | `govulncheck` | Known dependency/runtime vulnerabilities in Go modules |
| Go SAST | `gosec` | Go static security analysis |
| Python SCA | `pip-audit` | Known Python dependency vulnerabilities |
| Python SAST | `bandit` | Python static security analysis |
| C++ SAST | `cppcheck` | C++ static analysis for native components |
| Secret detection | `gitleaks` | Detect committed credentials/tokens/secrets |
| Dockerfile lint | `hadolint` | Dockerfile hardening and best-practice lint |
| Config lint | `yamllint` | Basic YAML configuration linting |
| Runtime audit | `cmd/security-audit` | Runtime posture checks and security report generation |

`scripts/security-scan.sh` writes reports to `build/security/`.

### Runtime Audit Checks

Runtime audit emits `SEC-RUNTIME-001` through `SEC-RUNTIME-007`:

| ID | Coverage |
| --- | --- |
| `SEC-RUNTIME-001` | Controller exposure and API authentication posture |
| `SEC-RUNTIME-002` | Collector TLS transport posture |
| `SEC-RUNTIME-003` | External metrics command safety |
| `SEC-RUNTIME-004` | Sensitive runtime file permissions |
| `SEC-RUNTIME-005` | Helm least-privilege defaults |
| `SEC-RUNTIME-006` | Placeholder/suspicious secret env values |
| `SEC-RUNTIME-007` | Docker Compose hardening and host port exposure |

Direct invocation example:

```bash
go -C backend run ./cmd/security-audit \
  -root "$(pwd)" \
  -format markdown \
  -output build/security/runtime-audit.md \
  -fail-on fail
```

## Interpreting Results

- `pass`: control is in place.
- `warn`: potential weakness or incomplete runtime context.
- `fail`: concrete high-confidence security risk.

Exit behavior:

- `security-scan` exits non-zero on findings/tool failures.
- `SECURITY_SCAN_STRICT=1` also fails when required tools are missing.
- Runtime audit threshold is controlled via `SECURITY_AUDIT_FAIL_ON` (`none|warn|fail`).

## CI Integration

Security checks run in `.github/workflows/ci.yml` (`security` job) on push and PR:

- installs scanner toolchain
- runs strict `scripts/security-scan.sh`
- uploads `build/security/` artifacts
