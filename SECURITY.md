# Security Policy

## Public repository hygiene

Before publishing any branch or tag, run:

```bash
make public-repo-audit
make test-publish-privacy
```

The public-tree publisher copies tracked files only and rejects sensitive filenames, credential-shaped content, private-key material, workstation-specific absolute paths, and forbidden historical data paths. The history audit also rejects non-`noreply` commit identities by default; set `SRE_PUBLISH_ALLOW_PUBLIC_EMAILS=1` only when exposing contributor emails is intentional and documented.

## Responsible disclosure

Report vulnerabilities privately to repository maintainers with:

- affected paths/components
- reproduction steps
- impact summary

Do not publish details publicly until a fix is available.

## Security baseline (v0.95)

Implemented controls include:

- signed bearer-token controller auth plus compatibility API-key mode in [`backend/cmd/controller/main.go`](backend/cmd/controller/main.go) and [`backend/internal/pkg/identity/token.go`](backend/internal/pkg/identity/token.go)
- TLS/mTLS for collector transport in [`backend/internal/controller/ingest_transport.go`](backend/internal/controller/ingest_transport.go) and [`backend/internal/collector/transport/client.go`](backend/internal/collector/transport/client.go)
- ingest payload size and cardinality limits in [`backend/internal/collector/transport/client.go`](backend/internal/collector/transport/client.go)
- external command parsing checks in [`backend/internal/collector/collector.go`](backend/internal/collector/collector.go)
- execution approval and idempotency guardrails in [`backend/internal/controller/agent/incident_actions.go`](backend/internal/controller/agent/incident_actions.go) and [`backend/internal/controller/agentcore/workflow_tools.go`](backend/internal/controller/agentcore/workflow_tools.go)

## Validation commands

```bash
make security-audit
make security-scan
```

`security-scan` orchestrates third-party tools plus the built-in runtime audit.

The runtime audit entry point is `backend/internal/pkg/security/runtime_audit.go` `RunRuntimeSecurityAudit()`.

`govulncheck` is kept in the pipeline as a reachability report. By default it is
report-only because it can surface standard-library and dependency call chains
that are sensitive to Go patch level and scanner database timing. Blocking
checks remain enabled for the built-in runtime audit, secret detection, static
analysis, and configuration linting.

## Production hardening checklist

- keep non-production listeners on loopback
- enable TLS for non-loopback transport
- keep secrets in env or secret mounts
- run workloads with least privilege

## Result interpretation

- `pass`: no issue detected
- `warn`: review before production rollout
- `fail`: block rollout until fixed
