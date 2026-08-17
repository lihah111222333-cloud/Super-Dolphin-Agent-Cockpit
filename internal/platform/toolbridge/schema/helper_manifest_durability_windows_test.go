//go:build windows

package schema

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestSyncFilesystemSnapshotDirectoryWindows 验证 owner-only 临时目录可用可写句柄完成目录 flush。
func TestSyncFilesystemSnapshotDirectoryWindows(t *testing.T) {
	if err := syncFilesystemSnapshotDirectory(t.TempDir()); err != nil {
		t.Fatalf("syncFilesystemSnapshotDirectory() error = %v", err)
	}
}

// TestSyncFilesystemSnapshotDirectoryWindowsPreservesErrors 验证 CreateFile、FlushFileBuffers、CloseHandle 的拒绝错误不被吞掉。
func TestSyncFilesystemSnapshotDirectoryWindowsPreservesErrors(t *testing.T) {
	tests := []struct {
		name       string
		openErr    error
		flushErr   error
		closeErr   error
		wantClosed bool
	}{
		{name: "create", openErr: windows.ERROR_ACCESS_DENIED},
		{name: "flush", flushErr: windows.ERROR_ACCESS_DENIED, wantClosed: true},
		{name: "close", closeErr: windows.ERROR_ACCESS_DENIED, wantClosed: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			closed := false
			err := syncFilesystemSnapshotDirectoryWithOps("ignored", filesystemSnapshotDirectoryWindowsOps{
				open: func(string) (windows.Handle, error) {
					if test.openErr != nil {
						return windows.InvalidHandle, test.openErr
					}
					return windows.Handle(1), nil
				},
				flush: func(windows.Handle) error { return test.flushErr },
				close: func(windows.Handle) error {
					closed = true
					return test.closeErr
				},
			})
			if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				t.Fatalf("error = %v, want ERROR_ACCESS_DENIED in chain", err)
			}
			if closed != test.wantClosed {
				t.Fatalf("close called = %v, want %v", closed, test.wantClosed)
			}
		})
	}
}

// TestWindowsHelperManifestAndSnapshotPublish 验证 manifest、staging/final 发布及中间文件清理。
func TestWindowsHelperManifestAndSnapshotPublish(t *testing.T) {
	root := t.TempDir()
	helper := filepath.Join(root, HelperFileName("windows"))
	if err := os.WriteFile(helper, []byte{'M', 'Z', 'w', 'i', 'n'}, 0o700); err != nil {
		t.Fatal(err)
	}
	identity := HelperIdentity{
		AppCommit: "windows-durability-test",
		GoVersion: runtime.Version(),
		GOOS:      "windows",
		GOARCH:    runtime.GOARCH,
	}
	manifest := helper + HelperManifestSuffix
	if err := WriteHelperManifest(helper, manifest, identity); err != nil {
		t.Fatalf("WriteHelperManifest() error = %v", err)
	}
	if err := VerifyHelperPackage(helper, manifest, identity); err != nil {
		t.Fatalf("VerifyHelperPackage() error = %v", err)
	}
	assertWindowsSnapshotPathAbsent(t, manifest+filesystemSnapshotPublishSuffix)

	t.Setenv("TMP", root)
	t.Setenv("TEMP", root)
	t.Setenv("TMPDIR", root)
	token := strings.Repeat("a", filesystemSnapshotTokenBytes*2)
	snapshot := filesystemSnapshotIdentity{
		Version:         filesystemSnapshotVersion,
		Directory:       filepath.Join(os.TempDir(), filesystemSnapshotPrefix+token),
		Token:           token,
		HelperGOOS:      "windows",
		OwnerPID:        os.Getpid(),
		OwnerStartToken: "windows-durability-test",
		OwnerExecutable: os.Args[0],
	}
	t.Cleanup(func() { _ = removeOwnedFilesystemSnapshot(snapshot) })
	path, err := writeExecutableSnapshot([]byte("durable-helper"), snapshot)
	if err != nil {
		t.Fatalf("writeExecutableSnapshot() error = %v", err)
	}
	wantPath := filepath.Join(snapshot.Directory, HelperFileName("windows"))
	if path != wantPath {
		t.Fatalf("snapshot path = %q, want %q", path, wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("published snapshot = %v", err)
	}
	assertWindowsSnapshotPathAbsent(t, filesystemSnapshotStagingDirectory(snapshot))
	assertWindowsSnapshotPathAbsent(t, filepath.Join(snapshot.Directory, filesystemSnapshotMarker)+filesystemSnapshotPublishSuffix)
	assertWindowsSnapshotPathAbsent(t, wantPath+filesystemSnapshotPublishSuffix)
}

func assertWindowsSnapshotPathAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path %q = %v, want absent", path, err)
	}
}
