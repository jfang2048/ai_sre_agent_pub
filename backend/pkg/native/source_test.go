//go:build linux

package native

import (
	"context"
	"testing"
)

func TestNativeSourceCollectProducesMetrics(t *testing.T) {
	source := NewNativeSource()
	if err := source.Start(context.Background()); err != nil {
		t.Fatalf("start native source: %v", err)
	}
	defer source.Stop()

	batch, err := source.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect native metrics: %v", err)
	}
	if batch == nil {
		t.Fatalf("collect returned nil batch")
	}
	if batch.Source != "native" {
		t.Fatalf("unexpected source %q", batch.Source)
	}
	if len(batch.Metrics) < 4 {
		t.Fatalf("expected at least 4 metrics, got %d", len(batch.Metrics))
	}

	status := source.Status()
	if !status.Enabled {
		t.Fatalf("native source should be enabled")
	}
	if status.LastSeen.IsZero() {
		t.Fatalf("expected last_seen to be populated")
	}
}
