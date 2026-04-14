package memory

import (
	"os"
	"path/filepath"
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
		t.Fatalf("Symlink(%q,%q) error = %v", rootA, link, err)
	}
	watcher := &teamSyncWatcher{root: link, canonicalRoot: rootA}
	if err := os.Remove(link); err != nil {
		t.Fatalf("Remove(%q) error = %v", link, err)
	}
	if err := os.Symlink(rootB, link); err != nil {
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
	changed, err := watcher.detectDirty()
	if err != nil {
		t.Fatalf("detectDirty() baseline error = %v", err)
	}
	if changed {
		t.Fatal("detectDirty() baseline = true, want false")
	}
	writeTeamSyncTestFile(t, filepath.Join(root, "ignore.txt"), "y")
	changed, err = watcher.detectDirty()
	if err != nil {
		t.Fatalf("detectDirty() txt change error = %v", err)
	}
	if changed {
		t.Fatal("detectDirty() txt change = true, want false")
	}
	writeTeamSyncTestFile(t, outside, "outside-2")
	changed, err = watcher.detectDirty()
	if err != nil {
		t.Fatalf("detectDirty() outside change error = %v", err)
	}
	if changed {
		t.Fatal("detectDirty() outside change = true, want false")
	}
	writeTeamSyncTestFile(t, filepath.Join(root, "keep.md"), "three")
	changed, err = watcher.detectDirty()
	if err != nil {
		t.Fatalf("detectDirty() md change error = %v", err)
	}
	if !changed {
		t.Fatal("detectDirty() md change = false, want true")
	}
}
