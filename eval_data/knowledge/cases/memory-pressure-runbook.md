# Memory Pressure Runbook

Used by `backend/internal/controller/eval/loader.go` and `backend/internal/controller/rag/knowledge.go` as a deterministic runbook case for the retrieval and workflow tests.

Summary: sustained RSS growth with reclaim pressure usually points to a leak, unbounded cache growth, or retry amplification before the node hard-fails.

Symptoms
- memory usage climbs steadily across consecutive samples
- reclaim pressure rises before OOM
- one service process grows faster than the rest of the node

Evidence
- rss growth on checkout-api
- memory pressure and reclaim spikes
- repeated cache growth or request retry fan-out

Likely causes
- memory leak in checkout-api
- unbounded cache growth in the request path
- retry amplification keeping request state alive too long

Remediation steps
- inspect top RSS processes before restarting anything
- compare reclaim, swap, and OOM counters in the same window
- capture a heap profile or allocation summary before the process is recycled

Commands
- cat /proc/pressure/memory
- pmap -x $(pidof checkout-api) | tail -n 20
- kubectl top pod -n production | grep checkout
