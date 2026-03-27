package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTopProgramsRobustnessHandlesMissingStore(t *testing.T) {
	ctrl := &Controller{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/top/programs", nil)
	w := httptest.NewRecorder()

	ctrl.handleTopPrograms(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp TopProgramsResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, 0, resp.Count)
	assert.Empty(t, resp.Programs)
	assert.Contains(t, resp.ByCategory, "cpu")
	assert.Equal(t, 0, resp.Report.CategoryCounts["logs"])
}

func TestTopProgramsRobustnessDeterministicSortForEqualScores(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now()

	store.StoreProcesses("c-1", []*telemetryv1.ProcessSample{
		{Pid: 1, Name: "zeta", CpuPercent: 50, RssBytes: 512 * 1024 * 1024},
		{Pid: 2, Name: "alpha", CpuPercent: 50, RssBytes: 512 * 1024 * 1024},
	}, now)

	ctrl := &Controller{ingestStore: store}
	programs := ctrl.aggregateTopPrograms(10)
	require.Len(t, programs, 2)
	assert.Equal(t, "alpha", programs[0].Name)
	assert.Equal(t, "zeta", programs[1].Name)
}

func TestTopProgramsRobustnessCategoryTopNDefaults(t *testing.T) {
	programs := []ProgramStats{
		{Name: "a", CPUPercent: 90},
		{Name: "b", CPUPercent: 80},
		{Name: "c", CPUPercent: 70},
		{Name: "d", CPUPercent: 60},
		{Name: "e", CPUPercent: 50},
		{Name: "f", CPUPercent: 40},
	}

	byCategory := categorizeTopPrograms(programs, 0)
	assert.Len(t, byCategory["cpu"], defaultCategoryTopN)
}
