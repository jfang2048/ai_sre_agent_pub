package inferenceobs_test

import (
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/inferenceobs"
)

func TestInferenceCapacityRetentionAndNativeThrottleFlags(t *testing.T) {
	cfg := inferenceobs.DefaultConfig()
	cfg.MaxEndpoints, cfg.MaxModelsPerEndpoint, cfg.MaxGPUs = 1, 1, 1
	s := inferenceobs.New(cfg)
	now := time.Now()
	acceptanceBatch(s, "a", now,
		acceptanceInference("requests_waiting", "one", "one", "", 0, now),
		acceptanceInference("requests_waiting", "one", "two", "", 50, now),
		acceptanceScrape("two", 0, now),
		acceptanceGPUReading("throttle_thermal_any", "0", 1, now),
		acceptanceGPUReading("throttle_power_any", "0", 1, now),
		acceptanceGPUReading("temperature_celsius", "1", 45, now))
	r := s.Snapshot("", now)
	if !r.CapacityLimited || len(r.Endpoints) != 1 || len(r.Endpoints[0].Models) != 1 || len(r.GPUs) != 1 {
		t.Fatalf("capacity must bound state and disclose truncation: %+v", r)
	}
	if len(r.GPUs[0].Findings) != 2 {
		t.Fatalf("native throttle flags not recognized: %+v", r.GPUs[0])
	}
	later := now.Add(cfg.Retention + time.Second)
	r = s.Snapshot("", later)
	if len(r.Endpoints) != 0 || len(r.GPUs) != 0 {
		t.Fatalf("retention did not prune: %+v", r)
	}
	acceptanceBatch(s, "a", later, acceptanceScrape("replacement", 0, later))
	r = s.Snapshot("", later)
	if len(r.Endpoints) != 1 || r.Endpoints[0].Endpoint != "replacement" {
		t.Fatalf("expired capacity not reusable: %+v", r)
	}
}

func TestInferenceModelIdentityPreservesLabelWhitespace(t *testing.T) {
	s := inferenceobs.New(inferenceobs.DefaultConfig())
	now := time.Now()
	acceptanceBatch(s, "a", now,
		acceptanceInference("requests_waiting", "one", "model", "0", 1, now),
		acceptanceInference("requests_waiting", "one", " model", "0", 2, now),
		acceptanceInference("requests_waiting", "one", "model", " 0", 3, now))
	r := s.Snapshot("", now)
	if len(r.Endpoints) != 1 || len(r.Endpoints[0].Models) != 3 {
		t.Fatalf("distinct exporter identities collapsed: %+v", r)
	}
}
