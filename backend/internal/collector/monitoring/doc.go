// Package monitoring provides the metric collection subsystem.
//
// This package follows the UNIX philosophy:
// - Do one thing well: Collect system metrics from kernel interfaces
// - Modularity: Each source is independent and can be enabled/disabled
// - Text streams: Metrics exposed as structured data (JSON/Protobuf)
// - Composition: Sources aggregate into a unified collector
//
// Architecture:
//   - Source: Interface for metric providers (proc, disk, network, etc.)
//   - Collector: Orchestrates multiple sources
//   - Aggregator: Combines and transforms metrics (optional)
//
// Sources read directly from Linux kernel interfaces:
//   - /proc/stat, /proc/meminfo - System-wide metrics
//   - /proc/diskstats - Disk I/O statistics
//   - /proc/net/dev - Network interface statistics
//   - /proc/[pid]/* - Process-specific metrics
//
// Design principles:
//   - No external dependencies (only stdlib + protobuf)
//   - Zero-copy where possible
//   - Fail gracefully (missing metrics != fatal error)
//   - Minimal memory allocation (reuse buffers)
package monitoring
