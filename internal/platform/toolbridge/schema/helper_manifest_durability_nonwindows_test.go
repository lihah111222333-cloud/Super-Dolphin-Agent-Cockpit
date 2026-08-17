//go:build !windows

package schema

import "testing"

// TestSyncFilesystemSnapshotDirectoryNonWindows 锁定非 Windows 的 os.Open 目录 fsync 路径。
func TestSyncFilesystemSnapshotDirectoryNonWindows(t *testing.T) {
	if err := syncFilesystemSnapshotDirectory(t.TempDir()); err != nil {
		t.Fatalf("syncFilesystemSnapshotDirectory() error = %v", err)
	}
}
