// Package sources provides metric collection implementations.
//
// Each source collects metrics from a specific Linux subsystem following
// the UNIX principle of doing one thing well. Sources are independent
// and can be composed together by the collector.
//
// Standard sources:
//   - proc: System-wide metrics from /proc (CPU, memory, load)
//   - process: Per-process metrics from /proc/[pid]
//   - ebpf: eBPF-based deep system tracing (optional, requires CAP_BPF)
//
// Design principles:
//  1. Zero external dependencies (only stdlib + kernel interfaces)
//  2. Non-blocking collection (timeout via context)
//  3. Graceful degradation (return partial results on errors)
//  4. Minimal allocations (reuse buffers where possible)
//
// All sources implement the MetricSource interface:
//
//	type MetricSource interface {
//	    Name() string
//	    Collect(ctx context.Context) (*proto.MetricBatch, error)
//	}
//
// Example usage:
//
//	procSource := sources.NewProcSource(sources.ProcConfig{Enabled: true})
//	batch, err := procSource.Collect(context.Background())
//	if err != nil {
//	    log.Printf("collection failed: %v", err)
//	}
//	for _, metric := range batch.Metrics {
//	    fmt.Printf("%s = %f\n", metric.Name, metric.Points[0].Value)
//	}
package sources
