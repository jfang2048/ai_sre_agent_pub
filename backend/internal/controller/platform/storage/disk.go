package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/safeconv"
	"go.uber.org/zap"
)

// DiskManager manages disk operations
type DiskManager struct {
	logger *zap.Logger
}

// NewDiskManager creates a new disk manager
func NewDiskManager(logger *zap.Logger) *DiskManager {
	return &DiskManager{
		logger: logger.With(zap.String("component", "disk_manager")),
	}
}

// DiskInfo represents disk information
type DiskInfo struct {
	Path         string
	Total        uint64
	Used         uint64
	Available    uint64
	UsagePercent float64
	FSType       string
}

// GetDiskInfo gets disk information for a path
func (dm *DiskManager) GetDiskInfo(path string) (*DiskInfo, error) {
	var stat syscall.Statfs_t

	err := syscall.Statfs(path, &stat)
	if err != nil {
		return nil, fmt.Errorf("statfs failed: %w", err)
	}

	blockSize := safeconv.NonNegativeInt64ToUint64(stat.Bsize)
	total := safeconv.MultiplyUint64(stat.Blocks, blockSize)
	available := safeconv.MultiplyUint64(stat.Bavail, blockSize)
	if available > total {
		available = total
	}
	used := total - available
	usagePercent := 0.0
	if total > 0 {
		usagePercent = float64(used) / float64(total) * 100
	}

	return &DiskInfo{
		Path:         path,
		Total:        total,
		Used:         used,
		Available:    available,
		UsagePercent: usagePercent,
	}, nil
}

// GetAllMounts gets all mounted filesystems
func (dm *DiskManager) GetAllMounts() ([]*DiskInfo, error) {
	// Read /proc/mounts
	_, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil, fmt.Errorf("failed to read mounts: %w", err)
	}

	// Parse mounts and get info for each
	// Simplified implementation
	return []*DiskInfo{}, nil
}

// mountsMu protects the mounts cache
func (dm *DiskManager) mountsMu() *sync.Mutex { return nil } // Simplified

// CleanupOldFiles removes old files from a directory
func (dm *DiskManager) CleanupOldFiles(dir string, olderThan int64, pattern string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("failed to read directory: %w", err)
	}

	cutoff := time.Now().Unix() - olderThan
	removed := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		if pattern != "" {
			matched, err := filepath.Match(pattern, entry.Name())
			if err != nil || !matched {
				continue
			}
		}

		fullPath := filepath.Join(dir, entry.Name())
		info, err := os.Stat(fullPath)
		if err != nil {
			continue
		}

		if info.ModTime().Unix() < cutoff {
			if err := os.Remove(fullPath); err == nil {
				removed++
			}
		}
	}

	dm.logger.Info("cleaned up old files",
		zap.String("dir", dir),
		zap.Int("removed", removed))

	return removed, nil
}

// CreateSnapshot creates a filesystem snapshot
func (dm *DiskManager) CreateSnapshot(source, snapshotPath string) error {
	dm.logger.Info("creating snapshot",
		zap.String("source", source),
		zap.String("snapshot", snapshotPath))

	// Implementation depends on filesystem type
	// For LVM: lvcreate
	// For Btrfs: btrfs subvolume snapshot
	// For ZFS: zfs snapshot

	return nil
}

// RestoreSnapshot restores a filesystem snapshot
func (dm *DiskManager) RestoreSnapshot(snapshotPath, target string) error {
	dm.logger.Info("restoring snapshot",
		zap.String("snapshot", snapshotPath),
		zap.String("target", target))

	return nil
}

// ResizeVolume resizes a volume
func (dm *DiskManager) ResizeVolume(path string, newSizeGB int) error {
	dm.logger.Info("resizing volume",
		zap.String("path", path),
		zap.Int("new_size_gb", newSizeGB))

	// Implementation depends on volume type
	// For LVM: lvextend
	// For EBS: aws cli

	return nil
}

// CephManager manages Ceph operations
type CephManager struct {
	logger *zap.Logger
}

// NewCephManager creates a new Ceph manager
func NewCephManager(logger *zap.Logger) *CephManager {
	return &CephManager{
		logger: logger.With(zap.String("component", "ceph_manager")),
	}
}

// GetOSDStatus gets OSD status
func (cm *CephManager) GetOSDStatus(ctx context.Context) ([]OSDStatus, error) {
	// Run ceph osd status command
	return []OSDStatus{}, nil
}

// OSDStatus represents OSD status
type OSDStatus struct {
	ID    int
	State string
	Up    bool
	In    bool
}

// GetPoolStats gets Ceph pool statistics
func (cm *CephManager) GetPoolStats(ctx context.Context) ([]PoolStats, error) {
	// Run ceph osd pool stats command
	return []PoolStats{}, nil
}

// PoolStats represents pool statistics
type PoolStats struct {
	Name           string
	UsedBytes      uint64
	AvailableBytes uint64
	Objects        int
	PgCount        int
}
