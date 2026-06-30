package inventory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTargetsFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "controller_targets.yaml")
	content := `---
collectors:
  - id: "collector-a"
    hostname: "node-a.example.net"
    address: "10.10.0.11"
    port: 9464
    labels:
      env: "prod"
    tags: ["gpu", "primary"]
    auth:
      mode: "mtls"
      token_env: "SRE_COLLECTOR_TOKEN"
  - hostname: "node-b.example.net"
    address: "10.10.0.12:9464"
    enabled: false
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	targets, err := LoadTargetsFile(path)
	if err != nil {
		t.Fatalf("LoadTargetsFile() error = %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("len(targets) = %d, want 2", len(targets))
	}
	if targets[0].ID != "collector-a" {
		t.Fatalf("targets[0].ID = %q, want collector-a", targets[0].ID)
	}
	if targets[0].Port != 9464 {
		t.Fatalf("targets[0].Port = %d, want 9464", targets[0].Port)
	}
	if targets[0].Auth.Mode != "mtls" {
		t.Fatalf("targets[0].Auth.Mode = %q, want mtls", targets[0].Auth.Mode)
	}
	if targets[1].Enabled {
		t.Fatalf("targets[1].Enabled = true, want false")
	}
	if targets[1].ID != "node-b.example.net" {
		t.Fatalf("targets[1].ID = %q, want node-b.example.net", targets[1].ID)
	}
}
