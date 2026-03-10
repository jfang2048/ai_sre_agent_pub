# Code Review Guidelines

Use this checklist for reliability-sensitive changes in `v0.6`.

## Collector and probe changes

- Confirm capability, namespace, and degraded-mode behavior explicitly.
- Verify fallback metrics or labels remain machine-consumable by the controller.
- Require targeted tests for spool, retry, or runtime-mode changes.

## Controller and ingest changes

- Review replay, idempotency, and retention effects first.
- Check that read paths still behave correctly on follower/standby nodes.
- Require a concrete fallback story for optional dependencies such as TSDB or RAG backends.

## Agent and action changes

- Treat model output and retrieved text as untrusted input.
- Verify schema validation, approval requirements, and rollback expectations.
- Block merges if action execution semantics changed without tests.

## UI and API changes

- Check partial-data, stale-data, and degraded-mode rendering paths.
- Fail the review if the change only improves the happy path and leaves placeholder states ambiguous.

## Deployment and config changes

- Call out which settings are hot-reloadable and which still require restart.
- For HA or Kubernetes changes, describe failover and storage behavior in the PR.
