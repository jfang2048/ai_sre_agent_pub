# Disk Latency Runbook

Summary: rising disk latency together with queue depth or IO pressure usually means storage contention, writeback pressure, or a single IO-heavy job.

Symptoms
- request latency rises before the service times out
- IO pressure stays elevated across several windows
- checkpoints, flushes, or backup jobs overlap with customer traffic

Evidence
- p99 disk latency is rising
- IO pressure full avg10 is rising
- checkpoint or flush logs become more frequent

Likely causes
- storage io bottleneck on the busiest device
- writeback pressure from a bursty job
- compaction, backup, or checkpoint overlap with foreground traffic

Remediation steps
- inspect disk queue depth and latency on the same device
- identify the process or filesystem driving the IO backlog
- delay invasive mitigation until the busiest writer is confirmed

Commands
- iostat -x 1 5
- cat /proc/pressure/io
- kubectl logs deploy/postgres -n production | tail -n 50
