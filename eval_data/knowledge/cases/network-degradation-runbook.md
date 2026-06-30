# Network Degradation Runbook

Used by `backend/internal/controller/eval/loader.go` and `backend/internal/controller/rag/knowledge.go` as a deterministic runbook case for the retrieval and workflow tests.

Summary: retransmits and softnet drops together usually point to congestion, packet loss, or queue oversubscription rather than pure application slowness.

Symptoms
- tcp retransmit ratio rises with timeout bursts
- softnet drops increase on the same node or interface
- user-facing latency increases even when CPU remains moderate

Evidence
- retransmit ratio is above the normal baseline
- softnet drops increase with queue growth
- timeout logs cluster around the same service and time window

Likely causes
- network congestion or packet loss
- NIC receive backlog saturation
- link oversubscription or noisy-neighbor traffic

Remediation steps
- inspect retransmit and drop counters before changing the application
- confirm the affected interface and top talking process
- compare the degradation window with rollout or traffic-shift timing

Commands
- ss -s
- ip -s link
- ethtool -S eth0 | grep -E 'drop|error|timeout'
