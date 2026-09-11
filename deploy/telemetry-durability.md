# Telemetry durability in v0.95

The collector owns an unsent batch until it observes a matching controller ACK.
The controller commits a raw receipt before applying telemetry or sending that
ACK. This contract depends on a filesystem/database that honors durable writes;
it does not protect against permanent loss of the underlying disk.

## Delivery order

1. The collector creates a random 128-bit producer epoch at startup and a
   monotonically increasing sequence. The serialized batch keeps that identity
   through retries and spool restarts. Legacy `batch_id`-only producers remain
   supported.
2. Spool v2 appends a 16-byte header containing `SPL2`, version, header size,
   payload length and CRC32C over the immutable header and payload. An enqueue
   returns only after file sync. The effective payload limit is the smaller of
   `spool_max_record_bytes` and the available spool capacity minus its header.
3. Ingest validates payload size, identifiers and collector authorization before
   committing the raw protobuf. Identity is collector + epoch + sequence, or
   collector + opaque batch ID for legacy producers. Reusing an identity with
   different serialized contents is rejected.
4. A fenced, renewable receipt lease serializes concurrent controllers.
   Deterministic ingestion runs before the applied checkpoint is persisted.
   Duplicates of an applied receipt ACK without invoking processors again.
5. The matching ACK permits the collector to advance its cursor using a synced
   temporary file, atomic rename and parent-directory sync. A failure can replay
   the batch; it must not skip an unacknowledged record.

The startup path restores retained applied receipts into hot state and retries
unapplied receipts. Recovery continues after startup because a terminated
controller can leave a lease that has not yet expired. Followers may restore
their hot view but cannot claim/replay pending receipts without the write guard.

## Failure boundaries

An ACK transfers ownership to the durable inbox **within its retention window**.
Defaults are 24 hours, 100,000 receipts and 256 MiB of serialized payloads. The
4 MiB per-batch limit also applies to direct server calls, before persistence.
Database indexes, WAL and free pages need additional disk headroom. PostgreSQL
must have `fsync` and `synchronous_commit` enabled. Back up the database using its
supported backup procedure; do not copy a live bbolt file without a transaction.

Capacity exhaustion rejects new receipts, so the collector retains them for
retry. Retention never removes pending receipts or shortens the dedupe window
just to accept a new batch. Expired applied receipts are deleted in bounded
1,024-record passes. An old identity submitted after retention can be accepted
again: this is a bounded journal, not permanent deduplication storage.

Hot-state history uses the stable receipt timestamp to avoid adding a second
point when replay follows an interrupted application. Older pending receipts
add their own historical metrics without replacing a newer dashboard snapshot
or evicting newer points from the bounded history. The current observer
interface is not a distributed transaction: a crash after an observer runs but
before the applied checkpoint can invoke that observer again during recovery.
Observers must remain deterministic/idempotent and must not perform remediation.
This release does not claim exactly-once execution for external observer side
effects or delivery into an optional asynchronous TSDB queue. The raw inbox is
the recovery authority, independent of optional hot snapshots and downstream
exports.

The collector keeps its existing bounded oldest-unread eviction policy. A long
outage can therefore lose **unacknowledged** batches when the collector spool
fills. Watch `collector_spool_evicted_records_total`; increase capacity or restore
connectivity before reaching the limit. Do not delete the spool to clear an
error.

## Upgrade and rollback

Stop the collector before backing up or moving its spool. Opening a v1 spool
converts only unread length-prefixed records. The v2 replacement is synced before
the original is renamed to a migration backup. A durable marker makes restart
between backup, swap and cursor reset idempotent. Backups are removed only after
the replacement and cursor are durable.

The new header adds 12 bytes per v1 record. If unread records cannot fit the
configured v2 bound, startup refuses migration and preserves v1 data. Increase
the bound and retry. A migration temporarily needs space for both copies.
Downgrading a collector with unread v2 data is unsupported: drain it first or
retain the upgraded binary and its backup until delivery completes.

Torn tails preserve earlier complete records. CRC failures quarantine the known
record and advance past it; structural corruption quarantines the unread segment
when its record boundaries cannot be trusted. Quarantine storage is bounded and
can itself discard old diagnostic bytes. Corruption is observable, not repaired
by inventing telemetry. All recovery counters reset on collector process restart.

The inbox creates versioned bbolt buckets or idempotent PostgreSQL tables/indexes
at startup. Existing collector protocol and workflow/artifact schemas do not
change. Stop the controller before changing inbox backend; there is no automatic
cross-database migration. Preserve the old journal until its replay/retention
requirements have been met. A bbolt journal permits one process to own its file.

## Kubernetes and configuration

Local-dev Helm deployments may use `collector.persistence.type: emptyDir`.
Cluster-lite and distributed rendering reject this unless
`collector.persistence.allowDataLossInCluster: true` is explicitly set. Maintained
cluster examples use:

```yaml
collector:
  persistence:
    type: hostPath
    hostPath: /var/lib/ai-sre-agent/collector
    hostPathType: DirectoryOrCreate
```

The path is node-local: one DaemonSet pod per node owns one spool, and a pod
restart on that node reopens it. Node loss does not preserve a hostPath. Provision
directory ownership for the configured collector UID; the chart does not relax
the security context or add a privileged ownership-changing init container.
Multiple releases on one node must use different paths. Never set this path to
a host root, system directory or another workload's data directory.

Distributed controllers require a shared PostgreSQL inbox, or an explicit
`singleWriter` choice with exactly one PVC-backed bbolt replica. The default is
not an assertion of single-writer safety. Helm rejects an unsafe replica/storage
combination. The raw Kubernetes example now requires a default StorageClass for
its controller PVC and uses node-local collector hostPath storage.

```yaml
controller:
  ingestInbox:
    backend: postgres
    singleWriter: false
    postgres:
      dsnSecretName: sre-controller-workflow-postgres
      dsnSecretKey: dsn
    retention: 24h
    gcInterval: 5m
    maxRecords: 100000
    maxBytes: 268435456
    lease: 30s
```

Outside Helm, configure `ingest.inbox` or the
`SRE_INGEST_INBOX_BACKEND`, `PATH`, `RETENTION`, `GC_INTERVAL`, `MAX_RECORDS`,
`MAX_BYTES`, `LEASE`, and `SINGLE_WRITER` environment variables (each with the
`SRE_INGEST_INBOX_` prefix). The PostgreSQL DSN is environment-only through
`SRE_INGEST_INBOX_POSTGRES_DSN`; never put credentials in tracked YAML. The old
spool sync-interval settings remain readable, but v2 always synchronizes each
record and acknowledged cursor.

## Verification and observability

`/api/v1/ingest/status` includes inbox backend, capacity, pending/applied receipts,
duplicates, replay attempts, pruning and errors. `/metrics` exports
`sre_ingest_inbox_*`. Collector metrics separately report checksum failures, torn
tail recovery, migrations, sync failures, quarantine, discard and eviction.

| Check | Command | Environment |
| --- | --- | --- |
| Spool corruption, migration and crash matrix | `make test-spool-crash` | CPU/filesystem |
| Controller crashes and 10,000 batches over 20 restarts | `make test-ingest-restart` | CPU/filesystem |
| Concurrent controllers and PostgreSQL capacity | `make test-ingest-postgres` | Local PostgreSQL binaries or test DSN |
| Positive and negative chart rendering | `make helm-smoke` | Helm |

The 10,000-batch test checks every ACKed identity after restart, one processor
call for ordinary duplicate delivery, restored materialized history and configured
bounds. A separate subprocess exits immediately after ACK without running
cleanup. Fault injection covers receipt commit, materialization, checkpoint,
ACK and cursor boundaries. These tests do not emulate a physical disk losing
power, certify storage hardware, or prove transactional external side effects.
The dedicated CI job uses an actual PostgreSQL service. Without a test DSN,
ordinary Go tests report PostgreSQL as skipped; the required PostgreSQL target
starts an isolated database or exits unsuccessfully when it cannot run.
