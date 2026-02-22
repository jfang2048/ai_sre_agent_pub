# AI SRE Agent Threat Model

## Scope

This threat model covers:

- `sre-collector` on monitored hosts
- `sre-controller` ingest/API/UI surfaces
- Optional AGENT execution and remediation paths
- Local/Docker/Helm deployment defaults in this repository

## Assets

- Telemetry integrity and availability (metrics, logs, diagnostics)
- Controller API and AGENT execution interfaces
- Secrets in environment variables and runtime-mounted files
- Runtime host and container privileges

## Trust Boundaries

```text
Monitored Host
  collector -> local kernel/proc/sys/log/GPU data
  collector -> optional external command execution
  collector -> gRPC push

Control Plane
  controller ingest -> validation/store
  controller REST/UI -> operator access
  controller AGENT/remediation -> controlled execution path

Operator / Automation
  browser/curl/CI/CD -> controller endpoints
```

## Primary Threats and Mitigations

| Threat | Example | Mitigations in repo |
| --- | --- | --- |
| Unauthorized API access | Exposed controller HTTP without auth | Loopback default listener, API key auth option, middleware auth/RBAC checks |
| Telemetry tampering or MITM | Collector traffic interception | TLS support with cert verification, runtime audit for insecure TLS flags |
| Command injection | Shell metacharacters in `external_metrics_cmd` | Non-shell `exec`, shell-operator blocking, config/runtime validation |
| Privilege escalation | Overly privileged container defaults | Compose hardening (`read_only`, `no-new-privileges`, `cap_drop`), Helm least-privilege defaults |
| Secret leakage | Sensitive values in logs/config | PII masking in audit logger, secret detection in CI, placeholder-secret audit checks |
| Dangerous runtime drift | Insecure config/permissions | Runtime security audit (`SEC-RUNTIME-*` checks), strict CI security scan mode |

## Residual Risks

- Local development may intentionally run without full auth/TLS in isolated environments.
- Optional advanced collectors (eBPF/probe-core) may require elevated capabilities in dedicated hardened deployments.
- Runtime file-permission checks depend on production-like env vars being set when running `security-audit`.

## Validation Workflow

```bash
make security-scan
SECURITY_SCAN_STRICT=1 make security-scan
make security-audit
```

Use `build/security/runtime-audit.md` and `build/security/runtime-audit.json` as the baseline report artifacts.
