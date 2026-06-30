package evidence

import "testing"

func TestCloneRecordsDeepCopiesNestedFields(t *testing.T) {
	records := []Record{{
		SchemaVersion: SchemaVersionV1,
		ID:            "ev-1",
		Kind:          "metric_signal",
		Summary:       "memory pressure rising",
		Subject: &Subject{
			CollectorID: "collector-a",
			Scope:       "node",
			Entity:      "node-a",
		},
		Attributes: map[string]string{
			"metric.name": "memory_used_mb",
		},
		RawReferences: []RawReference{{Kind: "metric", ID: "memory_used_mb"}},
		DerivedFrom:   []string{"ev-legacy-1"},
	}}

	cloned := CloneRecords(records)
	if len(cloned) != 1 {
		t.Fatalf("expected one cloned record, got %d", len(cloned))
	}
	cloned[0].Summary = "mutated"
	cloned[0].Subject.Entity = "node-b"
	cloned[0].Attributes["metric.name"] = "cpu_usage"
	cloned[0].RawReferences[0].ID = "cpu_usage"
	cloned[0].DerivedFrom[0] = "other"

	if records[0].Summary != "memory pressure rising" {
		t.Fatalf("original summary was mutated: %q", records[0].Summary)
	}
	if records[0].Subject.Entity != "node-a" {
		t.Fatalf("original subject entity was mutated: %q", records[0].Subject.Entity)
	}
	if records[0].Attributes["metric.name"] != "memory_used_mb" {
		t.Fatalf("original attributes were mutated: %q", records[0].Attributes["metric.name"])
	}
	if records[0].RawReferences[0].ID != "memory_used_mb" {
		t.Fatalf("original raw reference was mutated: %q", records[0].RawReferences[0].ID)
	}
	if records[0].DerivedFrom[0] != "ev-legacy-1" {
		t.Fatalf("original derived_from was mutated: %q", records[0].DerivedFrom[0])
	}
}
