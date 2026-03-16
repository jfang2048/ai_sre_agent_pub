package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"go.uber.org/zap"
)

func resetConfigGlobals() {
	configPath = ""
	listenAddr = ":8080"
	grpcAddr = ":9090"
	portFile = ""
	nodes = ""
	logLevel = "info"
	logFormat = "json"
	webPath = "./web"
	showVersion = false
	listenAddrFlagSet = false
	grpcAddrFlagSet = false
	webPathFlagSet = false
	viper.Reset()
}

func TestBuildConfigEnvCanDisableGRPCListen(t *testing.T) {
	resetConfigGlobals()
	t.Cleanup(resetConfigGlobals)

	dir := t.TempDir()
	configPath = filepath.Join(dir, "controller.yaml")
	if err := os.WriteFile(configPath, []byte("grpc_listen: \"127.0.0.1:9090\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("SRE_CONTROLLER_GRPC_LISTEN", "")

	cfg, err := buildConfig(zap.NewNop())
	if err != nil {
		t.Fatalf("buildConfig returned error: %v", err)
	}
	if cfg.GRPCListenAddr != "" {
		t.Fatalf("cfg.GRPCListenAddr = %q, want empty", cfg.GRPCListenAddr)
	}
}

func TestBuildConfigHonorsExplicitEmptyGRPCFlag(t *testing.T) {
	resetConfigGlobals()
	t.Cleanup(resetConfigGlobals)

	dir := t.TempDir()
	configPath = filepath.Join(dir, "controller.yaml")
	if err := os.WriteFile(configPath, []byte("grpc_listen: \"127.0.0.1:9090\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	grpcAddr = ""
	grpcAddrFlagSet = true

	cfg, err := buildConfig(zap.NewNop())
	if err != nil {
		t.Fatalf("buildConfig returned error: %v", err)
	}
	if cfg.GRPCListenAddr != "" {
		t.Fatalf("cfg.GRPCListenAddr = %q, want empty", cfg.GRPCListenAddr)
	}
}

func TestBuildConfigParsesHAEnvOverrides(t *testing.T) {
	resetConfigGlobals()
	t.Cleanup(resetConfigGlobals)

	dir := t.TempDir()
	configPath = filepath.Join(dir, "controller.yaml")
	if err := os.WriteFile(configPath, []byte("ha:\n  enabled: false\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("SRE_CONTROLLER_HA_ENABLED", "1")
	t.Setenv("SRE_CONTROLLER_HA_BACKEND", "etcd")
	t.Setenv("SRE_CONTROLLER_HA_NODE_ID", "controller-a")
	t.Setenv("SRE_CONTROLLER_HA_ADVERTISE_HTTP", "http://controller-a:8080")
	t.Setenv("SRE_CONTROLLER_HA_ADVERTISE_GRPC", "controller-a:9090")
	t.Setenv("SRE_CONTROLLER_HA_ELECTION_KEY", "/ai-sre-agent/test/leader")
	t.Setenv("SRE_CONTROLLER_HA_LEASE_TTL", "30s")
	t.Setenv("SRE_CONTROLLER_HA_OBSERVE_INTERVAL", "4s")
	t.Setenv("SRE_CONTROLLER_HA_CAMPAIGN_TIMEOUT", "6s")
	t.Setenv("SRE_CONTROLLER_HA_ALLOW_FOLLOWER_READ", "0")
	t.Setenv("SRE_CONTROLLER_HA_ETCD_ENDPOINTS", "http://etcd-0:2379,http://etcd-1:2379")

	cfg, err := buildConfig(zap.NewNop())
	if err != nil {
		t.Fatalf("buildConfig returned error: %v", err)
	}
	if !cfg.HA.Enabled || cfg.HA.Backend != "etcd" || cfg.HA.NodeID != "controller-a" {
		t.Fatalf("unexpected ha config: %#v", cfg.HA)
	}
	if cfg.HA.AdvertiseHTTP != "http://controller-a:8080" || cfg.HA.AdvertiseGRPC != "controller-a:9090" {
		t.Fatalf("unexpected advertise config: %#v", cfg.HA)
	}
	if cfg.HA.ElectionKey != "/ai-sre-agent/test/leader" {
		t.Fatalf("cfg.HA.ElectionKey = %q", cfg.HA.ElectionKey)
	}
	if cfg.HA.LeaseTTL.String() != "30s" || cfg.HA.ObserveInterval.String() != "4s" || cfg.HA.CampaignTimeout.String() != "6s" {
		t.Fatalf("unexpected ha timing config: %#v", cfg.HA)
	}
	if cfg.HA.AllowFollowerRead {
		t.Fatalf("cfg.HA.AllowFollowerRead = true, want false")
	}
	if len(cfg.HA.EtcdEndpoints) != 2 || cfg.HA.EtcdEndpoints[1] != "http://etcd-1:2379" {
		t.Fatalf("unexpected ha etcd endpoints: %#v", cfg.HA.EtcdEndpoints)
	}
}
