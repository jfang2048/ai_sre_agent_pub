package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessTree_AddExec_ByPID(t *testing.T) {
	pt := NewProcessTree(1000)
	now := time.Now()

	pt.AddExecEvent(1, 0, "/sbin/init", "", now.Add(-1*time.Hour))
	pt.AddExecEvent(100, 1, "/usr/sbin/sshd", "-D", now.Add(-50*time.Minute))
	pt.AddExecEvent(200, 100, "/bin/bash", "-l", now.Add(-40*time.Minute))

	node := pt.ByPID(200)
	require.NotNil(t, node)
	assert.Equal(t, 200, node.PID)
	assert.Equal(t, 100, node.PPID)
	assert.Equal(t, "/bin/bash", node.Binary)
	assert.Equal(t, []string{"/bin/bash"}, node.ExecChain)

	assert.Nil(t, pt.ByPID(999))
}

func TestProcessTree_AddFork_ParentChild(t *testing.T) {
	pt := NewProcessTree(1000)
	now := time.Now()

	pt.AddExecEvent(1, 0, "/sbin/init", "", now)
	pt.AddForkEvent(10, 1, "/sbin/init", now.Add(time.Second))
	pt.AddForkEvent(11, 1, "/sbin/init", now.Add(2*time.Second))

	parent := pt.ByPID(1)
	require.NotNil(t, parent)
	assert.Contains(t, parent.Children, 10)
	assert.Contains(t, parent.Children, 11)
	assert.Equal(t, 3, pt.Size())
}

func TestProcessTree_ExecChain(t *testing.T) {
	pt := NewProcessTree(1000)
	now := time.Now()

	pt.AddExecEvent(500, 1, "/bin/sh", "-c script.sh", now)
	pt.AddExecEvent(500, 1, "/usr/bin/python3", "script.py", now.Add(time.Second))
	pt.AddExecEvent(500, 1, "/usr/bin/curl", "http://example.com", now.Add(2*time.Second))

	node := pt.ByPID(500)
	require.NotNil(t, node)
	assert.Equal(t, "/usr/bin/curl", node.Binary)
	assert.Equal(t, []string{"/bin/sh", "/usr/bin/python3", "/usr/bin/curl"}, node.ExecChain)
}

func TestProcessTree_ByBinary(t *testing.T) {
	pt := NewProcessTree(1000)
	now := time.Now()

	pt.AddExecEvent(100, 1, "/usr/bin/python3", "", now)
	pt.AddExecEvent(200, 1, "/usr/bin/python3", "", now)
	pt.AddExecEvent(300, 1, "/usr/bin/nginx", "", now)

	results := pt.ByBinary("python")
	assert.Len(t, results, 2)

	results = pt.ByBinary("NGINX")
	assert.Len(t, results, 1)

	results = pt.ByBinary("nonexistent")
	assert.Len(t, results, 0)
}

func TestProcessTree_ByTimeWindow(t *testing.T) {
	pt := NewProcessTree(1000)
	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	pt.AddExecEvent(1, 0, "/sbin/init", "", base)
	pt.AddExecEvent(10, 1, "/usr/bin/app1", "", base.Add(5*time.Minute))
	pt.AddExecEvent(20, 1, "/usr/bin/app2", "", base.Add(15*time.Minute))
	pt.AddExecEvent(30, 1, "/usr/bin/app3", "", base.Add(25*time.Minute))

	results := pt.ByTimeWindow(base.Add(4*time.Minute), base.Add(20*time.Minute))
	assert.Len(t, results, 2)
	assert.Equal(t, "/usr/bin/app1", results[0].Binary)
	assert.Equal(t, "/usr/bin/app2", results[1].Binary)
}

func TestProcessTree_DetectAbnormalLineage(t *testing.T) {
	pt := NewProcessTree(1000)
	now := time.Now()

	// Normal: init -> sshd -> bash
	pt.AddExecEvent(1, 0, "/sbin/init", "", now)
	pt.AddExecEvent(100, 1, "/usr/sbin/sshd", "", now)
	pt.AddExecEvent(200, 100, "/bin/bash", "", now)

	// Suspicious: nginx -> bash
	pt.AddExecEvent(300, 1, "/usr/sbin/nginx", "", now)
	pt.AddExecEvent(400, 300, "/bin/bash", "-c whoami", now)

	// Suspicious: execution from /tmp
	pt.AddExecEvent(500, 200, "/tmp/malware", "", now)

	results := pt.DetectAbnormalLineage()
	require.True(t, len(results) >= 2, "expected at least 2 abnormal lineage results, got %d", len(results))

	var foundNginxBash, foundTmpExec bool
	for _, r := range results {
		if r.PID == 400 {
			assert.Contains(t, r.Reason, "nginx")
			foundNginxBash = true
		}
		if r.PID == 500 {
			assert.Contains(t, r.Reason, "/tmp/")
			foundTmpExec = true
		}
	}
	assert.True(t, foundNginxBash, "should detect nginx->bash")
	assert.True(t, foundTmpExec, "should detect /tmp execution")
}

func TestProcessTree_DetectAbnormalLineage_LongExecChain(t *testing.T) {
	pt := NewProcessTree(1000)
	now := time.Now()

	pt.AddExecEvent(100, 1, "/bin/a", "", now)
	pt.AddExecEvent(100, 1, "/bin/b", "", now)
	pt.AddExecEvent(100, 1, "/bin/c", "", now)
	pt.AddExecEvent(100, 1, "/bin/d", "", now)
	pt.AddExecEvent(100, 1, "/bin/e", "", now)
	pt.AddExecEvent(100, 1, "/bin/f", "", now)

	results := pt.DetectAbnormalLineage()
	found := false
	for _, r := range results {
		if r.PID == 100 {
			assert.Contains(t, r.Reason, "exec chain")
			found = true
		}
	}
	assert.True(t, found, "should detect long exec chain")
}

func TestProcessTree_DetectNewPatterns(t *testing.T) {
	pt := NewProcessTree(1000)
	now := time.Now()

	pt.AddExecEvent(100, 1, "/usr/bin/nginx", "", now)
	pt.AddExecEvent(200, 1, "/usr/bin/python3", "", now)
	pt.AddExecEvent(300, 1, "/usr/bin/cryptominer", "", now)

	baseline := map[string]bool{
		"nginx":   true,
		"python3": true,
		"bash":    true,
	}

	results := pt.DetectNewPatterns(baseline)
	require.Len(t, results, 1)
	assert.Equal(t, "cryptominer", binaryBaseName(results[0].Binary))
}

func TestProcessTree_Eviction(t *testing.T) {
	pt := NewProcessTree(10)
	base := time.Now()

	for i := 1; i <= 10; i++ {
		pt.AddExecEvent(i, 0, "/bin/test", "", base.Add(time.Duration(i)*time.Second))
	}
	assert.Equal(t, 10, pt.Size())

	// Adding one more should trigger eviction
	pt.AddExecEvent(11, 0, "/bin/test", "", base.Add(11*time.Second))
	assert.LessOrEqual(t, pt.Size(), 10)
}

func TestProcessTree_ParentChain(t *testing.T) {
	pt := NewProcessTree(1000)
	now := time.Now()

	pt.AddExecEvent(1, 0, "init", "", now)
	pt.AddExecEvent(10, 1, "systemd", "", now)
	pt.AddExecEvent(100, 10, "sshd", "", now)
	pt.AddExecEvent(200, 100, "bash", "", now)

	chain := pt.ParentChain(200)
	assert.Equal(t, []string{"bash", "sshd", "systemd", "init"}, chain)
}

func TestProcessTree_UpdateFingerprint(t *testing.T) {
	pt := NewProcessTree(1000)
	now := time.Now()

	pt.AddExecEvent(100, 1, "/usr/bin/nginx", "", now)
	pt.UpdateFingerprint(100, ProcessFingerprint{
		AvgCPU:         45.2,
		AvgMemBytes:    512 * 1024 * 1024,
		NetworkConns:   150,
		PortsListening: []int{80, 443},
	})

	node := pt.ByPID(100)
	require.NotNil(t, node)
	assert.InDelta(t, 45.2, node.Fingerprint.AvgCPU, 0.01)
	assert.Equal(t, []int{80, 443}, node.Fingerprint.PortsListening)
}
