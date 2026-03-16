package controller

import (
	"net/http"
	"sort"
	"time"
)

type finopsNodeSignal struct {
	CollectorID           string   `json:"collector_id"`
	Hostname              string   `json:"hostname"`
	CPUUsagePercent       float64  `json:"cpu_usage_percent"`
	MemoryUsagePercent    float64  `json:"memory_usage_percent"`
	GPUUtilizationPercent float64  `json:"gpu_utilization_percent"`
	GPUProcesses          float64  `json:"gpu_processes"`
	IdleCPUHint           bool     `json:"idle_cpu_hint"`
	OversizedMemoryHint   bool     `json:"oversized_memory_hint"`
	GPUWasteHint          bool     `json:"gpu_waste_hint"`
	PotentialWasteScore   float64  `json:"potential_waste_score"`
	Recommendations       []string `json:"recommendations,omitempty"`
}

type finopsSummary struct {
	NodesAnalyzed     int     `json:"nodes_analyzed"`
	IdleCPUHints      int     `json:"idle_cpu_hints"`
	OversizedMemHints int     `json:"oversized_memory_hints"`
	GPUWasteHints     int     `json:"gpu_waste_hints"`
	AverageWasteScore float64 `json:"average_waste_score"`
}

func (c *Controller) handleFinOpsSignals(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if c.ingestStore == nil {
		http.Error(w, "ingest store unavailable", http.StatusServiceUnavailable)
		return
	}

	nodes := c.ingestStore.Snapshot()
	out := make([]finopsNodeSignal, 0, len(nodes))
	summary := finopsSummary{}
	totalScore := 0.0

	for _, node := range nodes {
		if node == nil {
			continue
		}
		cpu := firstPositiveMetric(node.Metrics,
			"node_cpu_usage_percent",
			"probe_core_cpu_usage_percent",
			"system.cpu.usage",
		)
		memUsage := memoryUsagePercent(node.Metrics)
		gpuUtil := firstPositiveMetric(node.Metrics, "node_gpu_utilization_sm_avg_percent")
		gpuProcesses := firstPositiveMetric(node.Metrics, "node_gpu_process_total")

		idleCPU := cpu > 0 && cpu < 20
		oversizedMem := memUsage > 0 && memUsage < 35
		gpuWaste := gpuProcesses > 0 && gpuUtil >= 0 && gpuUtil < 30

		score := 0.0
		recommendations := make([]string, 0, 3)
		if idleCPU {
			score += 0.35
			recommendations = append(recommendations, "CPU appears underutilized; consider rightsizing or workload consolidation.")
			summary.IdleCPUHints++
		}
		if oversizedMem {
			score += 0.30
			recommendations = append(recommendations, "Memory headroom is consistently high; review requests/limits for oversized allocations.")
			summary.OversizedMemHints++
		}
		if gpuWaste {
			score += 0.35
			recommendations = append(recommendations, "GPU processes are active with low utilization; inspect feeder bottlenecks or scheduling waste.")
			summary.GPUWasteHints++
		}
		if score > 1 {
			score = 1
		}

		out = append(out, finopsNodeSignal{
			CollectorID:           node.CollectorID,
			Hostname:              node.Hostname,
			CPUUsagePercent:       cpu,
			MemoryUsagePercent:    memUsage,
			GPUUtilizationPercent: gpuUtil,
			GPUProcesses:          gpuProcesses,
			IdleCPUHint:           idleCPU,
			OversizedMemoryHint:   oversizedMem,
			GPUWasteHint:          gpuWaste,
			PotentialWasteScore:   score,
			Recommendations:       recommendations,
		})
		totalScore += score
		summary.NodesAnalyzed++
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].PotentialWasteScore != out[j].PotentialWasteScore {
			return out[i].PotentialWasteScore > out[j].PotentialWasteScore
		}
		return out[i].CollectorID < out[j].CollectorID
	})
	if summary.NodesAnalyzed > 0 {
		summary.AverageWasteScore = totalScore / float64(summary.NodesAnalyzed)
	}

	writeJSON(w, map[string]interface{}{
		"summary":      summary,
		"nodes":        out,
		"count":        len(out),
		"generated_at": time.Now().UTC(),
	})
}

func firstPositiveMetric(metrics map[string]float64, names ...string) float64 {
	for _, name := range names {
		if v, ok := metrics[name]; ok {
			return v
		}
	}
	return 0
}

func memoryUsagePercent(metrics map[string]float64) float64 {
	used, okUsed := metrics["node_memory_Used_bytes"]
	total, okTotal := metrics["node_memory_MemTotal_bytes"]
	if okUsed && okTotal && total > 0 {
		return (used / total) * 100.0
	}
	if v, ok := metrics["memory_used_percent"]; ok {
		return v
	}
	if v, ok := metrics["system.memory.usage"]; ok {
		return v
	}
	return 0
}
