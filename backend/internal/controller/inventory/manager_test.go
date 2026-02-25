package inventory

import (
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"go.uber.org/zap"
)

type fakeSnapshotStore struct {
	snapshots []*ingest.NodeSnapshot
}

func (f *fakeSnapshotStore) Snapshot() []*ingest.NodeSnapshot {
	out := make([]*ingest.NodeSnapshot, 0, len(f.snapshots))
	out = append(out, f.snapshots...)
	return out
}

func TestManagerListMergesSources(t *testing.T) {
	now := time.Now()
	store := &fakeSnapshotStore{
		snapshots: []*ingest.NodeSnapshot{
			{
				CollectorID: "probe-telemetry",
				Hostname:    "node-a",
				Version:     "v1.2.3",
				Labels: map[string]string{
					"cluster": "prod-a",
				},
				LastSeen: now.Add(-15 * time.Second),
			},
		},
	}

	cfg := DefaultConfig()
	cfg.HeartbeatTTL = 45 * time.Second

	mgr := NewManager(cfg, []StaticProbe{
		{
			ID:      "probe-static",
			Name:    "probe-static",
			Address: "10.0.0.10:9090",
			Labels: map[string]string{
				"zone": "us-east-1a",
			},
		},
	}, store, zap.NewNop())

	if ok := mgr.UpsertHeartbeat(Heartbeat{
		ProbeID:   "probe-static",
		Hostname:  "node-static",
		Version:   "v2.0.0",
		Timestamp: now.Add(-10 * time.Second),
	}); !ok {
		t.Fatalf("UpsertHeartbeat returned false")
	}

	items := mgr.List()
	if len(items) != 2 {
		t.Fatalf("List() len = %d, want 2", len(items))
	}

	var staticItem, telemetryItem *Probe
	for i := range items {
		switch items[i].ID {
		case "probe-static":
			staticItem = &items[i]
		case "probe-telemetry":
			telemetryItem = &items[i]
		}
	}
	if staticItem == nil || telemetryItem == nil {
		t.Fatalf("missing expected probes, items=%v", items)
	}

	if !staticItem.Healthy {
		t.Fatalf("staticItem.Healthy = false, want true")
	}
	if staticItem.Address != "10.0.0.10:9090" {
		t.Fatalf("staticItem.Address = %q, want %q", staticItem.Address, "10.0.0.10:9090")
	}
	if staticItem.Hostname != "node-static" {
		t.Fatalf("staticItem.Hostname = %q, want %q", staticItem.Hostname, "node-static")
	}
	if staticItem.Version != "v2.0.0" {
		t.Fatalf("staticItem.Version = %q, want %q", staticItem.Version, "v2.0.0")
	}

	if !telemetryItem.Healthy {
		t.Fatalf("telemetryItem.Healthy = false, want true")
	}
	if telemetryItem.Hostname != "node-a" {
		t.Fatalf("telemetryItem.Hostname = %q, want %q", telemetryItem.Hostname, "node-a")
	}
	if telemetryItem.Labels["cluster"] != "prod-a" {
		t.Fatalf("telemetryItem cluster label = %q, want %q", telemetryItem.Labels["cluster"], "prod-a")
	}
}

func TestManagerGetAndSummary(t *testing.T) {
	now := time.Now()
	store := &fakeSnapshotStore{
		snapshots: []*ingest.NodeSnapshot{
			{
				CollectorID: "probe-a",
				Hostname:    "node-a",
				LastSeen:    now.Add(-5 * time.Second),
			},
		},
	}

	cfg := DefaultConfig()
	cfg.HeartbeatTTL = 20 * time.Second
	mgr := NewManager(cfg, nil, store, zap.NewNop())

	got, ok := mgr.Get("probe-a")
	if !ok {
		t.Fatalf("Get(probe-a) not found")
	}
	if got.ID != "probe-a" {
		t.Fatalf("Get(probe-a).ID = %q, want probe-a", got.ID)
	}

	summary := mgr.Summary()
	if summary.Total != 1 {
		t.Fatalf("Summary.Total = %d, want 1", summary.Total)
	}
	if summary.Healthy != 1 {
		t.Fatalf("Summary.Healthy = %d, want 1", summary.Healthy)
	}
	if summary.FromTelemetry != 1 {
		t.Fatalf("Summary.FromTelemetry = %d, want 1", summary.FromTelemetry)
	}
}
