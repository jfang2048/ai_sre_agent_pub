package collector

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDiscoverHardwareProfileDetectsTopologyAndCapabilities(t *testing.T) {
	root := t.TempDir()
	paths := hardwarePathRoots{
		procRoot: filepath.Join(root, "proc"),
		sysRoot:  filepath.Join(root, "sys"),
	}

	writeTestFile(t, filepath.Join(paths.procRoot, "cpuinfo"), `
processor   : 0
vendor_id   : GenuineIntel
model name  : Intel(R) Xeon(R)
`)
	writeTestFile(t, filepath.Join(paths.procRoot, "driver/nvidia/version"), "NVRM version: NVIDIA\n")

	for _, cpu := range []struct {
		cpu      string
		socket   string
		core     string
		capacity string
		coreType string
	}{
		{cpu: "cpu0", socket: "0", core: "0", capacity: "1024", coreType: "1"},
		{cpu: "cpu1", socket: "0", core: "1", capacity: "768", coreType: "2"},
		{cpu: "cpu2", socket: "1", core: "0", capacity: "1024", coreType: "1"},
		{cpu: "cpu3", socket: "1", core: "1", capacity: "768", coreType: "2"},
	} {
		base := filepath.Join(paths.sysRoot, "devices/system/cpu", cpu.cpu)
		writeTestFile(t, filepath.Join(base, "topology/physical_package_id"), cpu.socket)
		writeTestFile(t, filepath.Join(base, "topology/core_id"), cpu.core)
		writeTestFile(t, filepath.Join(base, "topology/core_type"), cpu.coreType)
		writeTestFile(t, filepath.Join(base, "cpu_capacity"), cpu.capacity)
	}
	require.NoError(t, os.MkdirAll(filepath.Join(paths.sysRoot, "devices/system/node/node0"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(paths.sysRoot, "devices/system/node/node1"), 0o755))

	for _, dev := range []string{"nvme0n1", "nvme1n1"} {
		base := filepath.Join(paths.sysRoot, "block", dev)
		writeTestFile(t, filepath.Join(base, "queue/rotational"), "0")
		writeTestFile(t, filepath.Join(base, "queue/nr_requests"), "1024")
	}

	netBase := filepath.Join(paths.sysRoot, "class/net/ib0")
	writeTestFile(t, filepath.Join(netBase, "type"), "32")
	require.NoError(t, os.MkdirAll(filepath.Join(netBase, "device"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "drivers", "mlx5_core"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(root, "drivers", "mlx5_core"), filepath.Join(netBase, "device/driver")))

	gpuBase := filepath.Join(paths.sysRoot, "class/drm/card0")
	writeTestFile(t, filepath.Join(gpuBase, "device/vendor"), "0x10de")
	require.NoError(t, os.MkdirAll(filepath.Join(gpuBase, "device"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "drivers", "nvidia"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(root, "drivers", "nvidia"), filepath.Join(gpuBase, "device/driver")))

	profile := discoverHardwareProfile(paths, time.Unix(1700000000, 0))
	require.True(t, profile.Discovered)
	require.Equal(t, "intel", profile.CPU.Vendor)
	require.Equal(t, 2, profile.CPU.Sockets)
	require.Equal(t, 4, profile.CPU.Cores)
	require.Equal(t, 4, profile.CPU.Threads)
	require.Equal(t, 2, profile.CPU.NUMANodes)
	require.True(t, profile.CPU.Hybrid)
	require.Equal(t, 2, profile.Storage.DeviceCount)
	require.Equal(t, "nvme", profile.Storage.DominantClass)
	require.Equal(t, 1024, profile.Storage.MaxQueueDepth)
	require.Equal(t, "rdma", profile.Network.DominantType)
	require.True(t, profile.Network.RDMACapable)
	require.Equal(t, "mlx5_core", profile.Network.Driver)
	require.Equal(t, 1, profile.GPU.DeviceCount)
	require.Equal(t, "nvidia", profile.GPU.Vendor)
	require.Equal(t, "nvidia", profile.GPU.Runtime)
	require.GreaterOrEqual(t, profile.Sampling.ProcessIntervalSamples, 3)
	require.Equal(t, 1, profile.Sampling.GPUIntervalSamples)
	require.LessOrEqual(t, profile.Threshold.DiskLatencySeconds, 0.015)
}

func TestDiscoverHardwareProfileFallsBackWhenDataUnavailable(t *testing.T) {
	root := t.TempDir()
	paths := hardwarePathRoots{
		procRoot: filepath.Join(root, "proc"),
		sysRoot:  filepath.Join(root, "sys"),
	}

	profile := discoverHardwareProfile(paths, time.Unix(1700000000, 0))
	require.True(t, profile.Discovered)
	require.Equal(t, runtimeArch(), profile.CPU.Architecture)
	require.Equal(t, "unknown", profile.CPU.Vendor)
	require.Equal(t, "unknown", profile.Storage.DominantClass)
	require.Equal(t, "unknown", profile.Network.DominantType)
	require.Equal(t, "none", profile.GPU.Vendor)
	require.GreaterOrEqual(t, profile.Sampling.GPUIntervalSamples, 8)
}

func writeTestFile(t *testing.T, path string, contents string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
}

func runtimeArch() string {
	return defaultHardwareProfile(time.Time{}).CPU.Architecture
}
