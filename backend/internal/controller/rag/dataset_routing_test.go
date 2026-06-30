package rag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestDiscoverSourceUnitsSkipsCorpusNoiseAndRawGPUDuplicates(t *testing.T) {
	root := t.TempDir()
	writeDatasetFile(t, root, "sources/web/gpu-operator-troubleshooting.html", "<html><body><h1>GPU Operator Troubleshooting</h1><p>Raw HTML duplicate.</p></body></html>")
	writeDatasetFile(t, root, "processed/gpu-docs/gpu-operator-troubleshooting.md", "# GPU Operator Troubleshooting\n\nSymptoms\n- gpu operator pods crashloop")
	writeDatasetFile(t, root, "sources/git/scoutflo-sre-playbooks/README.md", "# Scoutflo Playbooks\n\nRepository overview only.")
	writeDatasetFile(t, root, "sources/git/scoutflo-sre-playbooks/K8s Playbooks/02-Nodes/NodeNetworkReceiveErrors-node.md", "# NodeNetworkReceiveErrors\n\nSymptoms\n- receive errors rising")

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.DatasetPath = root
	cfg.IndexPath = filepath.Join(t.TempDir(), "rag", "index.json")

	units, quarantine, err := discoverSourceUnits(cfg)
	require.NoError(t, err)
	require.NotEmpty(t, units)
	require.NotEmpty(t, quarantine)

	paths := make([]string, 0, len(units))
	for _, unit := range units {
		paths = append(paths, filepath.ToSlash(unit.SourcePath))
	}
	joined := strings.Join(paths, "\n")
	require.Contains(t, joined, "processed/gpu-docs/gpu-operator-troubleshooting.md")
	require.Contains(t, joined, "K8s Playbooks/02-Nodes/NodeNetworkReceiveErrors-node.md")
	require.NotContains(t, joined, "sources/web/gpu-operator-troubleshooting.html")
	require.NotContains(t, joined, "sources/git/scoutflo-sre-playbooks/README.md")

	reasons := make([]string, 0, len(quarantine))
	for _, item := range quarantine {
		reasons = append(reasons, item.Reason)
	}
	joinedReasons := strings.Join(reasons, "\n")
	require.Contains(t, joinedReasons, "processed markdown counterpart preferred")
	require.Contains(t, joinedReasons, "excluded low-value repository metadata")
}

func TestQueryPrefersOperationalRunbooksOverAWSAndHelpdeskNoise(t *testing.T) {
	root := t.TempDir()
	writeDatasetFile(t, root, "sources/git/prometheus-operator-runbooks/content/runbooks/node/NodeNetworkReceiveErrs.md", `
# NodeNetworkReceiveErrs

Symptoms
- node network receive errors are increasing

Evidence
- packet receive errors and drops rise together

Remediation steps
- inspect node NIC counters
`)
	writeDatasetFile(t, root, "sources/git/scoutflo-sre-playbooks/K8s Playbooks/02-Nodes/NodeNetworkReceiveErrors-node.md", `
# NodeNetworkReceiveErrors

Symptoms
- kubernetes node network receive errors rising

Likely causes
- host NIC issues or driver problems

Remediation steps
- inspect node status and network device counters
`)
	writeDatasetFile(t, root, "sources/git/scoutflo-sre-playbooks/AWS Playbooks/04-Networking/Latency-Higher-Than-Expected-Direct-Connect.md", `
# Latency Higher Than Expected Direct Connect

Symptoms
- network latency and packet issues

Remediation steps
- inspect Direct Connect virtual interface metrics
`)
	writeDatasetFile(t, root, "raw/structured/helpdesk_dataset.csv", "Question,LinkToAnswer\nHow do I fix node network receive errors?,http://faq/node-errors\n")

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.DatasetPath = root
	cfg.IndexPath = filepath.Join(t.TempDir(), "rag", "index.json")
	cfg.RetrievalMode = "lexical"

	service, err := NewService(cfg, zap.NewNop())
	require.NoError(t, err)
	_, err = service.Rebuild(context.Background())
	require.NoError(t, err)

	result, err := service.Query(context.Background(), QueryRequest{
		Query: "kubernetes node network receive errors packet drops",
		TopK:  4,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Hits)

	topFamily := result.Hits[0].Metadata["source_family"]
	require.Contains(t, []string{"prometheus_operator_runbook", "scoutflo_k8s_playbook"}, topFamily)

	for _, hit := range result.Hits[:minInt(len(result.Hits), 2)] {
		require.NotEqual(t, "scoutflo_aws_playbook", hit.Metadata["source_family"])
		require.NotEqual(t, "structured_helpdesk", hit.Metadata["source_family"])
	}
}

func TestQueryPrefersProcessedGPUDocsForGPUIncidents(t *testing.T) {
	root := t.TempDir()
	writeDatasetFile(t, root, "sources/web/gpu-operator-troubleshooting.html", "<html><body><h1>GPU Operator Troubleshooting</h1><p>Raw duplicate copy.</p></body></html>")
	writeDatasetFile(t, root, "processed/gpu-docs/gpu-operator-troubleshooting.md", `
# GPU Operator Troubleshooting

Symptoms
- gpu operator pods are crashlooping after node update

Evidence
- dcgm exporter is not healthy

Remediation steps
- inspect gpu operator daemonset and dcgm exporter logs
`)
	writeDatasetFile(t, root, "sources/git/scoutflo-sre-playbooks/K8s Playbooks/03-Pods/CrashLoopBackOff-pod.md", `
# CrashLoopBackOff

Symptoms
- pods restart repeatedly

Remediation steps
- inspect pod events and previous logs
`)

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.DatasetPath = root
	cfg.IndexPath = filepath.Join(t.TempDir(), "rag", "index.json")
	cfg.RetrievalMode = "lexical"

	service, err := NewService(cfg, zap.NewNop())
	require.NoError(t, err)
	_, err = service.Rebuild(context.Background())
	require.NoError(t, err)

	result, err := service.Query(context.Background(), QueryRequest{
		Query: "gpu operator dcgm exporter crashloop troubleshooting",
		TopK:  3,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Hits)
	require.Equal(t, "nvidia_gpu_doc_processed", result.Hits[0].Metadata["source_family"])
	for _, hit := range result.Hits {
		require.NotContains(t, filepath.ToSlash(hit.SourcePath), "sources/web/gpu-operator-troubleshooting.html")
	}
}

func writeDatasetFile(t *testing.T, root, relative, content string) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(relative))
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
	require.NoError(t, os.WriteFile(fullPath, []byte(strings.TrimSpace(content)+"\n"), 0o644))
}
