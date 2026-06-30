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
			ID:       "probe-static",
			Name:     "probe-static",
			Hostname: "collector-a.internal",
			Address:  "10.0.0.10",
			Port:     9090,
			Enabled:  true,
			Labels: map[string]string{
				"zone": "us-east-1a",
			},
			Tags: []string{"prod", "gpu"},
			Auth: TargetAuth{
				Mode: "mtls",
			},
			Metadata: map[string]string{
				"site": "dc-a",
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
	if !staticItem.Configured {
		t.Fatalf("staticItem.Configured = false, want true")
	}
	if !staticItem.Enabled {
		t.Fatalf("staticItem.Enabled = false, want true")
	}
	if staticItem.Address != "10.0.0.10:9090" {
		t.Fatalf("staticItem.Address = %q, want %q", staticItem.Address, "10.0.0.10:9090")
	}
	if staticItem.Hostname != "node-static" {
		t.Fatalf("staticItem.Hostname = %q, want %q", staticItem.Hostname, "node-static")
	}
	if staticItem.Port != 9090 {
		t.Fatalf("staticItem.Port = %d, want 9090", staticItem.Port)
	}
	if staticItem.Version != "v2.0.0" {
		t.Fatalf("staticItem.Version = %q, want %q", staticItem.Version, "v2.0.0")
	}
	if got := staticItem.Tags[0]; got != "prod" {
		t.Fatalf("staticItem.Tags[0] = %q, want %q", got, "prod")
	}
	if staticItem.Auth.Mode != "mtls" {
		t.Fatalf("staticItem.Auth.Mode = %q, want mtls", staticItem.Auth.Mode)
	}
	if staticItem.Metadata["site"] != "dc-a" {
		t.Fatalf("staticItem.Metadata[site] = %q, want dc-a", staticItem.Metadata["site"])
	}

	if !telemetryItem.Healthy {
		t.Fatalf("telemetryItem.Healthy = false, want true")
	}
	if telemetryItem.Configured {
		t.Fatalf("telemetryItem.Configured = true, want false")
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
	if summary.Configured != 0 {
		t.Fatalf("Summary.Configured = %d, want 0", summary.Configured)
	}
	if summary.FromTelemetry != 1 {
		t.Fatalf("Summary.FromTelemetry = %d, want 1", summary.FromTelemetry)
	}
}

func TestManagerReloadUpdatesStaticTargetsAndTTL(t *testing.T) {
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
	cfg.HeartbeatTTL = 30 * time.Second
	mgr := NewManager(cfg, []StaticProbe{
		{
			ID:      "probe-a",
			Address: "10.0.0.10",
			Port:    9090,
			Enabled: true,
		},
	}, store, zap.NewNop())

	reloaded := DefaultConfig()
	reloaded.HeartbeatTTL = 2 * time.Second
	mgr.Reload(reloaded, []StaticProbe{
		{
			ID:      "probe-b",
			Address: "10.0.0.11",
			Port:    9091,
			Enabled: true,
		},
	})

	items := mgr.List()
	if len(items) != 2 {
		t.Fatalf("List() len = %d, want 2", len(items))
	}

	var probeA, probeB *Probe
	for i := range items {
		switch items[i].ID {
		case "probe-a":
			probeA = &items[i]
		case "probe-b":
			probeB = &items[i]
		}
	}
	if probeA == nil || probeB == nil {
		t.Fatalf("expected both probe-a and probe-b after reload, got %#v", items)
	}
	if probeA.Configured {
		t.Fatalf("probe-a.Configured = true, want false after static target reload")
	}
	if probeA.Healthy {
		t.Fatalf("probe-a.Healthy = true, want false with tightened heartbeat ttl")
	}
	if !probeB.Configured {
		t.Fatalf("probe-b.Configured = false, want true")
	}
	if probeB.Address != "10.0.0.11:9091" {
		t.Fatalf("probe-b.Address = %q, want 10.0.0.11:9091", probeB.Address)
	}
}
