# Security Policy

## Responsible disclosure

Report vulnerabilities privately to repository maintainers with:
- affected paths/components
- reproduction steps
- impact summary

Do not publish details publicly until a fix is available.

## Security baseline (v0.7)

Implemented controls include:
- optional API key auth for controller APIs
- optional TLS/mTLS for collector transport
- ingest payload size/cardinality limits
- log ingest request/body limits
- external command parsing safety checks
- agent execution approval/idempotency guardrails

## Validation commands

```bash
make security-audit
make security-scan
```

`security-scan` orchestrates third-party tools plus the built-in runtime audit.

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
