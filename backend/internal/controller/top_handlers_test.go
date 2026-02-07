package controller

import (
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/gpuobs"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/assert"
)

func TestAggregateTopProgramsRanksByScore(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now()

	// Two processes: one CPU heavy, one IO heavy.
	store.StoreProcesses("c-1", []*telemetryv1.ProcessSample{
		{Pid: 1, Name: "cpu-burn", CpuPercent: 95, RssBytes: 512 * 1024 * 1024},
		{Pid: 2, Name: "io-hog", CpuPercent: 10, RssBytes: 128 * 1024 * 1024, IoReadBps: 80 * 1024 * 1024},
	}, now)

	// Network + logs for pid 2
	store.StoreMetrics("c-1", []*telemetryv1.Metric{
		{Name: "rca_net_process_connections", Value: 60, Labels: []*telemetryv1.Label{{Key: "pid", Value: "2"}, {Key: "name", Value: "io-hog"}}},
		{Name: "rca_net_process_queued_bytes", Value: 2 * 1024 * 1024, Labels: []*telemetryv1.Label{{Key: "pid", Value: "2"}}},
	}, now)
	store.StoreLogs("c-1", []*telemetryv1.LogFingerprint{
		{Fingerprint: "f1", Count: 3, Example: "io-hog: ERROR disk full"},
	}, now)

	ctrl := &Controller{ingestStore: store}

	programs := ctrl.aggregateTopPrograms(10)
	assert.Len(t, programs, 2)

	// First should be cpu-burn
	assert.Equal(t, "cpu-burn", programs[0].Name)
	assert.Contains(t, programs[0].Categories, "cpu")
	assert.Greater(t, programs[0].Score, programs[1].Score)

	// Second should include network & logs categories
	ioProg := programs[1]
	assert.Equal(t, "io-hog", ioProg.Name)
	assert.Contains(t, ioProg.Categories, "network")
	assert.Contains(t, ioProg.Categories, "logs")
	assert.Greater(t, ioProg.LogErrors, 0)
}

func TestSummarizeTopProgramsPicksPerCategory(t *testing.T) {
	p := []ProgramStats{
		{Name: "cpu", CPUPercent: 90},
		{Name: "mem", MemoryBytes: 5 * 1024 * 1024 * 1024},
		{Name: "disk", DiskReadBps: 100},
		{Name: "net", NetQueuedBytes: 5},
		{Name: "gpu", GPUMemMiB: 4000},
		{Name: "logs", LogErrors: 2},
	}
	summary := summarizeTopPrograms(p)
	assert.Equal(t, "cpu", summary["cpu"].Name)
	assert.Equal(t, "mem", summary["memory"].Name)
	assert.Equal(t, "disk", summary["disk"].Name)
	assert.Equal(t, "net", summary["network"].Name)
	assert.Equal(t, "gpu", summary["gpu"].Name)
	assert.Equal(t, "logs", summary["logs"].Name)
}

func TestAggregateTopProgramsMergesLogsAndGPU(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now()
	store.StoreProcesses("c-1", []*telemetryv1.ProcessSample{
		{Pid: 10, Name: "gpu-app", CpuPercent: 10},
	}, now)
	store.StoreLogs("c-1", []*telemetryv1.LogFingerprint{
		{Fingerprint: "l1", Count: 2, Example: "gpu-app: warning throttling"},
	}, now)

	// Simulate GPU metrics
	gcfg := gpuobs.DefaultConfig()
	gcfg.PersistDir = t.TempDir()
	gpuStore := gpuobs.New(gcfg)
	_ = gpuStore.Start()
	defer gpuStore.Stop()

	batch := &telemetryv1.TelemetryBatch{
		Collector: &telemetryv1.CollectorInfo{CollectorId: "c-1", Hostname: "node-1"},
		Metrics: []*telemetryv1.Metric{
			{Name: "node_gpu_count", Value: 1},
			{Name: "node_gpu_info", Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}, {Key: "name", Value: "H100"}, {Key: "uuid", Value: "u1"}}},
			{Name: "node_gpu_process_memory_mib", Value: 3000, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}, {Key: "pid", Value: "10"}, {Key: "process", Value: "gpu-app"}}},
			{Name: "node_gpu_process_sm_util_percent", Value: 70, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}, {Key: "pid", Value: "10"}, {Key: "process", Value: "gpu-app"}}},
		},
	}
	gpuStore.ProcessBatch("c-1", batch, now)

	ctrl := &Controller{ingestStore: store, gpuStore: gpuStore}
	programs := ctrl.aggregateTopPrograms(5)

	var gpuProg ProgramStats
	for _, p := range programs {
		if p.Name == "gpu-app" {
			gpuProg = p
		}
	}
	if assert.NotZero(t, gpuProg.Name, "gpu-app should be present") {
		assert.Greater(t, gpuProg.GPUMemMiB, 0.0)
		assert.Greater(t, gpuProg.GPUUtilSMPct, 0.0)
		cats := deriveCategories(gpuProg)
		foundGPU := false
		foundLogs := false
		for _, c := range cats {
			if c == "gpu" {
				foundGPU = true
			}
			if c == "logs" {
				foundLogs = true
			}
		}
		assert.True(t, foundGPU, "expected gpu category")
		assert.True(t, foundLogs, "expected logs category")
	}
}

func TestParseTopProgramsLimitClampsAndDefaults(t *testing.T) {
	assert.Equal(t, defaultTopProgramsLimit, parseTopProgramsLimit(""))
	assert.Equal(t, defaultTopProgramsLimit, parseTopProgramsLimit("abc"))
	assert.Equal(t, defaultTopProgramsLimit, parseTopProgramsLimit("-3"))
	assert.Equal(t, 7, parseTopProgramsLimit("7"))
	assert.Equal(t, maxTopProgramsLimit, parseTopProgramsLimit("9999"))
}

func TestCategorizeTopProgramsByCategoryLimit(t *testing.T) {
	input := []ProgramStats{
		{Name: "cpu-a", CPUPercent: 95, Score: 6},
		{Name: "cpu-b", CPUPercent: 80, Score: 5},
		{Name: "cpu-c", CPUPercent: 70, Score: 4},
		{Name: "gpu-a", GPUMemMiB: 5000, GPUUtilSMPct: 80, Score: 6},
		{Name: "gpu-b", GPUMemMiB: 3000, GPUUtilSMPct: 40, Score: 4},
	}

	got := categorizeTopPrograms(input, 2)
	assert.Len(t, got["cpu"], 2)
	assert.Equal(t, "cpu-a", got["cpu"][0].Name)
	assert.Equal(t, "cpu-b", got["cpu"][1].Name)
	assert.Len(t, got["gpu"], 2)
	assert.Equal(t, "gpu-a", got["gpu"][0].Name)
}

func TestBuildTopProgramsReportTracksHotspotsAndProblematic(t *testing.T) {
	input := []ProgramStats{
		{Name: "hot", Score: 7.5, Categories: []string{"cpu", "memory"}},
		{Name: "logs", Score: 5.0, LogErrors: 3, LogWarnings: 1, Categories: []string{"logs"}},
		{Name: "gpu", Score: 4.0, GPUMemMiB: 2000, Categories: []string{"gpu"}},
	}

	report := buildTopProgramsReport(input, 2)
	if assert.NotNil(t, report.TopOverall) {
		assert.Equal(t, "hot", report.TopOverall.Name)
	}
	if assert.NotNil(t, report.MostProblematic) {
		assert.Equal(t, "logs", report.MostProblematic.Name)
	}
	assert.Len(t, report.Hotspots, 2)
	assert.Equal(t, 1, report.CategoryCounts["logs"])
	assert.Equal(t, 1, report.CategoryTopN["logs"])
}

func TestBuildResourceCategoryPagesSortsByOverallThenFrequency(t *testing.T) {
	input := []ProgramStats{
		{
			Name:              "cpu-a",
			CPUPercent:        85,
			CategoryTotals:    map[string]float64{"cpu": 320},
			CategoryFrequency: map[string]uint64{"cpu": 6},
			SignalValues:      map[string]float64{"rca_cpu_process_percent": 85},
		},
		{
			Name:              "cpu-b",
			CPUPercent:        90,
			CategoryTotals:    map[string]float64{"cpu": 300},
			CategoryFrequency: map[string]uint64{"cpu": 9},
			SignalValues:      map[string]float64{"rca_cpu_process_percent": 90},
		},
		{
			Name:              "diskio-a",
			DiskReadBps:       12 * 1024 * 1024,
			DiskWriteBps:      8 * 1024 * 1024,
			CategoryTotals:    map[string]float64{"disk_io": 1000},
			CategoryFrequency: map[string]uint64{"disk_io": 5},
			SignalValues:      map[string]float64{"rca_io_process_read_bytes_per_second": 12 * 1024 * 1024},
		},
	}

	pages := buildResourceCategoryPages(input, 3)
	cpuPage, ok := pages["cpu"]
	if assert.True(t, ok) {
		assert.Equal(t, "cpu", cpuPage.Category)
		assert.NotEmpty(t, cpuPage.KernelSignals)
		if assert.Len(t, cpuPage.Ranked, 2) {
			assert.Equal(t, "cpu-a", cpuPage.Ranked[0].Name) // higher overall first
			assert.Equal(t, "cpu-b", cpuPage.Ranked[1].Name)
		}
	}

	diskIOPage, ok := pages["disk_io"]
	if assert.True(t, ok) {
		assert.Equal(t, "Disk I/O (Rate & Syscalls)", diskIOPage.Title)
		assert.Len(t, diskIOPage.Ranked, 1)
		assert.Equal(t, "diskio-a", diskIOPage.Ranked[0].Name)
	}
}

func TestBuildResourceCategoryPagesIncludeCollectorFallbackSignals(t *testing.T) {
	pages := buildResourceCategoryPages([]ProgramStats{
		{Name: "fallback", CPUPercent: 5, MemoryBytes: 256},
	}, 1)

	cpuPage, ok := pages["cpu"]
	if assert.True(t, ok) {
		names := make([]string, 0, len(cpuPage.KernelSignals))
		for _, s := range cpuPage.KernelSignals {
			names = append(names, s.Name)
		}
		assert.Contains(t, names, "node_process_cpu_percent")
	}

	memPage, ok := pages["memory"]
	if assert.True(t, ok) {
		names := make([]string, 0, len(memPage.KernelSignals))
		for _, s := range memPage.KernelSignals {
			names = append(names, s.Name)
		}
		assert.Contains(t, names, "node_process_memory_rss_bytes")
	}

	diskIOPage, ok := pages["disk_io"]
	if assert.True(t, ok) {
		names := make([]string, 0, len(diskIOPage.KernelSignals))
		for _, s := range diskIOPage.KernelSignals {
			names = append(names, s.Name)
		}
		assert.Contains(t, names, "node_process_io_read_bytes_per_second")
		assert.Contains(t, names, "node_process_io_write_bytes_per_second")
	}
}

func TestAggregateTopProgramsMergesRowsByCollectorAndPID(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now()

	store.StoreProcesses("c-1", []*telemetryv1.ProcessSample{
		{Pid: 77, Name: "worker", CpuPercent: 55},
	}, now)

	// Name differs across sources; PID should still merge to one row.
	store.StoreMetrics("c-1", []*telemetryv1.Metric{
		{Name: "rca_net_process_connections", Value: 9, Labels: []*telemetryv1.Label{{Key: "pid", Value: "77"}, {Key: "name", Value: "worker-v2"}}},
	}, now.Add(time.Second))
	store.StoreLogs("c-1", []*telemetryv1.LogFingerprint{
		{Fingerprint: "f1", Count: 2, Example: "worker-v2[77]: ERROR timeout"},
	}, now.Add(2*time.Second))

	ctrl := &Controller{ingestStore: store}
	programs := ctrl.aggregateTopPrograms(10)
	if assert.Len(t, programs, 1) {
		assert.Equal(t, "77", programs[0].PID)
		assert.Greater(t, programs[0].CPUPercent, 0.0)
		assert.Greater(t, programs[0].NetConnections, 0)
		assert.Greater(t, programs[0].LogErrors, 0)
	}
}

func TestAggregateTopProgramsFallsBackToLogParsingWhenAttributionMissing(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now()

	// Populate process resources so the node isn't empty, but keep logs unattributed.
	store.StoreProcesses("c-1", []*telemetryv1.ProcessSample{
		{Pid: 9, Name: "worker", CpuPercent: 12},
	}, now)
	store.StoreLogs("c-1", []*telemetryv1.LogFingerprint{
		{Fingerprint: "l1", Count: 4, Example: "ERROR"},
	}, now.Add(time.Second))

	ctrl := &Controller{ingestStore: store}
	programs := ctrl.aggregateTopPrograms(10)

	var found bool
	for _, p := range programs {
		if p.Name == "unknown" && p.LogErrors > 0 {
			found = true
			break
		}
	}
	assert.True(t, found, "expected fallback log attribution entry")
}

func TestGuessProgramNameHandlesCommonFormats(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{line: "nginx[1234]: error while reading upstream", want: "nginx"},
		{line: "Oct 10 12:00:00 host kubelet[811]: warning node pressure", want: "kubelet"},
		{line: "/usr/bin/python3[42]: panic: boom", want: "python3"},
		{line: "WARN: something happened", want: ""},
		{line: "", want: ""},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, guessProgramName(tt.line))
	}
}
