package team

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTeamSyncWatcherEnsureStableRootFailClosedOnDrift(t *testing.T) {
	base := t.TempDir()
	rootA := filepath.Join(base, "root-a")
	rootB := filepath.Join(base, "root-b")
	link := filepath.Join(base, "team")
	if err := os.MkdirAll(rootA, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", rootA, err)
	}
	if err := os.MkdirAll(rootB, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", rootB, err)
	}
	if err := os.Symlink(rootA, link); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink(%q,%q) error = %v", rootA, link, err)
	}
	watcher := &teamSyncWatcher{root: link, canonicalRoot: rootA}
	if err := os.Remove(link); err != nil {
		t.Fatalf("Remove(%q) error = %v", link, err)
	}
	if err := os.Symlink(rootB, link); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink(%q,%q) error = %v", rootB, link, err)
	}
	if err := watcher.ensureStableRoot(); err == nil {
		t.Fatal("ensureStableRoot() error = nil, want root drift failure")
	}
}

func TestTeamSyncWatcherDetectDirtyOnlyForTeamMarkdown(t *testing.T) {
	root := filepath.Join(t.TempDir(), teamMemoryRootDirName)
	writeTeamSyncTestFile(t, filepath.Join(root, "keep.md"), "one")
	writeTeamSyncTestFile(t, filepath.Join(root, "ignore.txt"), "x")
	writeTeamSyncTestFile(t, filepath.Join(root, "nested", "child.md"), "two")
	outside := filepath.Join(filepath.Dir(root), "outside.md")
	writeTeamSyncTestFile(t, outside, "outside")
	files, err := scanTeamMarkdownFiles(root)
	if err != nil {
		t.Fatalf("scanTeamMarkdownFiles() error = %v", err)
	}
	state := SyncState{LastKnownChecksum: checksumTree(localChecksumMap(files))}
	svc := &TeamSyncService{root: root, state: state}
	watcher, err := newTeamSyncWatcher(svc, root, nil)
	if err != nil {
		t.Fatalf("newTeamSyncWatcher() error = %v", err)
	}
	assertTeamSyncDirtyState(t, watcher, "baseline", false)
	writeTeamSyncTestFile(t, filepath.Join(root, "ignore.txt"), "y")
	assertTeamSyncDirtyState(t, watcher, "txt change", false)
	writeTeamSyncTestFile(t, outside, "outside-2")
	assertTeamSyncDirtyState(t, watcher, "outside change", false)
	writeTeamSyncTestFile(t, filepath.Join(root, "keep.md"), "three")
	assertTeamSyncDirtyState(t, watcher, "md change", true)
}

func TestTeamSyncWatcherRootCapsFailClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), teamMemoryRootDirName)
	writeTeamSyncTestFile(t, filepath.Join(root, "nested", "child.md"), "child")
	svc := &TeamSyncService{root: root}

	_, err := newTeamSyncWatcherWithCaps(svc, root, nil, teamSyncWatcherRootCaps{MaxDirs: 1, MaxFiles: 10, MaxBytes: 1024})
	if err == nil || !strings.Contains(err.Error(), "root cap") {
		t.Fatalf("newTeamSyncWatcherWithCaps() error = %v, want root cap violation", err)
	}
}

func TestTeamSyncWatcherHealthSnapshotReportsCapViolation(t *testing.T) {
	root := filepath.Join(t.TempDir(), teamMemoryRootDirName)
	writeTeamSyncTestFile(t, filepath.Join(root, "keep.md"), "one")
	svc := &TeamSyncService{root: root}
	watcher, err := newTeamSyncWatcherWithCaps(svc, root, nil, teamSyncWatcherRootCaps{MaxDirs: 10, MaxFiles: 10, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("newTeamSyncWatcherWithCaps() error = %v", err)
	}
	t.Cleanup(watcher.closeWatcher)

	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll nested: %v", err)
	}
	watcher.caps = teamSyncWatcherRootCaps{MaxDirs: 1, MaxFiles: 10, MaxBytes: 1024}
	if err := watcher.addRecursive(root); err == nil {
		t.Fatal("addRecursive() error = nil, want cap violation")
	}
	snapshot := watcher.HealthSnapshot()
	if snapshot.WatchedDirs == 0 || !strings.Contains(snapshot.LastCapViolation, "root cap") {
		t.Fatalf("HealthSnapshot() = %+v, want watched dirs and cap violation", snapshot)
	}
}

func assertTeamSyncDirtyState(t *testing.T, watcher *teamSyncWatcher, label string, want bool) {
	t.Helper()

	changed, err := watcher.detectDirty()
	if err != nil {
		t.Fatalf("detectDirty() %s error = %v", label, err)
	}
	if changed != want {
		t.Fatalf("detectDirty() %s = %t, want %t", label, changed, want)
	}
}
