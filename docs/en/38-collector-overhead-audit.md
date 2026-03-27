# Collector Overhead Audit (Pre-Refactor)

Date: 2026-03-13

This note captures the pre-refactor operational cost profile of the repository as audited from the current code paths. It is intentionally focused on host impact, not architecture changes.

## Summary

The current architecture is sound: `probe-core` and eBPF preserve short-lived evidence, the collector batches and spools locally, and the controller centralizes storage and reasoning. The main production risk is not architectural shape. The risk is that several hot paths still do more synchronous work than a business-workload-first observer should.

The highest-impact issues before refactoring were:

1. The collector runs one synchronous `collect -> marshal -> enqueue -> drain` cycle and drains the full spool in the same timer tick.
2. The transport redials gRPC for every send.
3. The spool fsyncs every enqueue and rewrites the offset file on every commit.
4. Compatibility and enrichment paths still perform full `/proc`, `/sys`, and filesystem scans.
5. `probe-core` already has queue-based backoff, but its adaptive logic does not yet account for host pressure strongly enough.
6. Hardware discovery is not centralized and cached as a first-class runtime profile, so some collectors still probe unsupported hardware repeatedly.
7. Controller-side periodic agent/analysis loops do not yet defer heavy reasoning aggressively when the observed host is already under pressure.

## Hot Path Findings

### Collector cycle

File: `backend/internal/collector/collector.go`

- `Run` uses a single timer loop and executes `collectAndSend` serially every interval.
- `collectAndSend` marshals the batch, appends it to the spool, then drains the spool immediately in the same cycle.
- `collectBatch` always evaluates logs and may evaluate security/external enrichment inside the same collection tick.

Cost implications:

- CPU spikes are coupled to transport recovery because backlog drain happens inline with collection.
- A large backlog or slow controller can consume the whole collector budget.
- Expensive enrichment work is not yet load-shed early enough.

Key references:

- `backend/internal/collector/collector.go:341`
- `backend/internal/collector/collector.go:389`
- `backend/internal/collector/collector.go:438`

### Transport

File: `backend/internal/collector/transport/client.go`

- `sendToEndpoint` opens a new gRPC connection for every send and closes it immediately afterward.
- The client also unmarshals the payload back into protobuf before every send to recover the batch ID and validate the payload.

Cost implications:

- avoidable CPU, TLS, socket, and syscall overhead on every flush
- avoidable latency amplification during backlog replay

Key references:

- `backend/internal/collector/transport/client.go:301`
- `backend/internal/collector/transport/client.go:384`

### Spool

File: `backend/internal/collector/spool/spool.go`

- `Enqueue` fsyncs the spool file for every appended batch.
- `Commit` writes the offset file for every acknowledged batch.

Cost implications:

- steady-state disk write amplification
- higher syscall frequency than necessary
- replay storms can turn into small-sync storms

Key references:

- `backend/internal/collector/spool/spool.go:91`
- `backend/internal/collector/spool/spool.go:166`

### Compatibility process collection

File: `backend/internal/collector/collect/process.go`

- The compatibility top-K collector scans all numeric `/proc` entries.
- For each process it reads `/proc/<pid>/stat` and `/proc/<pid>/io`, then sorts the entire process set.

Cost implications:

- full-process-table scanning cost scales with host process count
- repeated file reads and parsing in a tight periodic path
- fallback mode is materially more expensive than the primary `probe-core` path

Key references:

- `backend/internal/collector/collect/process.go:51`
- `backend/internal/collector/collect/process.go:93`

### Security audit

File: `backend/internal/collector/security_audit.go`

- The audit is cached by interval, but when it runs it performs broad process, socket, permission, SUID, scheduler, sysctl, and posture scans.

Cost implications:

- acceptable as a slow path, but still too expensive to run unchanged during host pressure
- needs explicit load-shedding and cached topology reuse

Key references:

- `backend/internal/collector/security_audit.go:217`

### probe-core adaptive loop

File: `cpp/probe_core/main.cpp`

- `probe-core` already adapts to its own IPC queue pressure.
- The collector loop increases `effective_interval_ms_` when frame queue pressure or local collection time grows.
- Process collection still scans all PIDs, then enriches top-K rows with PSS and socket queue lookups.

Cost implications:

- strong base design, but adaptation is still biased toward self-backpressure instead of host-first pressure
- process enrichment remains the most expensive recurring `probe-core` sub-collector

Key references:

- `cpp/probe_core/main.cpp:1508`
- `cpp/probe_core/main.cpp:1523`
- `cpp/probe_core/main.cpp:2897`

### Controller periodic reasoning

Files:

- `backend/internal/controller/analysis/engine.go`
- `backend/internal/controller/agent/engine.go`

- Analysis runs on a fixed ticker and processes all nodes each cycle.
- Agent report generation also runs on a fixed ticker and can invoke RAG/LLM enrichment paths.

Cost implications:

- controller load can indirectly increase collector backlog if heavy reasoning is allowed to stay oblivious to host stress
- heavy reasoning should defer first when collectors are already protecting an overloaded node

Key references:

- `backend/internal/controller/analysis/engine.go:461`
- `backend/internal/controller/agent/engine.go:219`
- `backend/internal/controller/agent/engine.go:265`

## Repeated Discovery / Unsupported Hardware Risk

The codebase already has partial caching for:

- `probe-core` netlink refresh
- `probe-core` PSI and cgroup refresh
- `probe-core` disk queue capacity refresh
- `probe-core` ethtool refresh
- collector security audit interval cache

But there is still no single cached hardware profile that answers:

- CPU vendor, topology, NUMA layout, and heterogeneous-core presence
- storage class per device
- NIC speed, driver, and type
- GPU vendor/runtime/driver presence

Without that profile, unsupported collectors can still probe absent hardware repeatedly and threshold logic stays too generic.

## Refactor Goals Derived From The Audit

The code changes after this note should do the following without redesigning the architecture:

1. Bound collector work per interval.
2. Reuse transport connections.
3. Reduce spool sync amplification.
4. Defer non-critical collectors under pressure.
5. Add a cached hardware profile and use it to tailor collector behavior.
6. Make adaptive sampling host-aware, not just queue-aware.
7. Emit measurable self-overhead metrics.
8. Defer heavy controller-side reasoning when a node is already in self-protection mode.
