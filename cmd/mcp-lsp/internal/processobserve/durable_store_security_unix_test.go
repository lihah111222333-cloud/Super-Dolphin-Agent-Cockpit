//go:build darwin || linux

package processobserve_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processobserve"
)

func TestDurableStoreRejectsSymlinkComponent(t *testing.T) {
	parent := canonicalTempRoot(t)
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("Mkdir(target): %v", err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink(link): %v", err)
	}
	if _, err := processobserve.OpenDurableStore(link, processobserve.DurableOptions{TestOnly: true}); err == nil {
		t.Fatal("OpenDurableStore() accepted a symlink path component")
	}
}

func TestDurableStoreRejectsSymlinkTargetAfterOpen(t *testing.T) {
	root := canonicalTempRoot(t)
	store, err := processobserve.OpenDurableStore(root, processobserve.DurableOptions{TestOnly: true})
	if err != nil {
		t.Fatalf("OpenDurableStore() error = %v", err)
	}
	decision, err := store.RecordGhost(context.Background(), probeMustFail(t, 9_999_994))
	if err != nil {
		t.Fatalf("RecordGhost() error = %v", err)
	}
	incident := filepath.Join(root, decision.EventID()+".incident")
	data, err := os.ReadFile(incident)
	if err != nil {
		t.Fatalf("ReadFile(incident): %v", err)
	}
	if err := os.Remove(incident); err != nil {
		t.Fatalf("Remove(incident): %v", err)
	}
	target := filepath.Join(canonicalTempRoot(t), "target-record")
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatalf("WriteFile(target): %v", err)
	}
	if err := os.Symlink(target, incident); err != nil {
		t.Fatalf("Symlink(incident): %v", err)
	}
	if _, err := store.ListDecisions(context.Background()); err == nil {
		t.Fatal("ListDecisions() accepted a symlink incident target")
	}
}

func TestDurableStoreRejectsParentRenameSwap(t *testing.T) {
	parent := canonicalTempRoot(t)
	root := filepath.Join(parent, "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("Mkdir(root): %v", err)
	}
	store, err := processobserve.OpenDurableStore(root, processobserve.DurableOptions{TestOnly: true})
	if err != nil {
		t.Fatalf("OpenDurableStore() error = %v", err)
	}
	if _, err := store.RecordGhost(context.Background(), probeMustFail(t, 9_999_993)); err != nil {
		t.Fatalf("RecordGhost() error = %v", err)
	}
	moved := filepath.Join(parent, "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatalf("Rename(root): %v", err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("Mkdir(replacement): %v", err)
	}
	if _, err := store.RecordGhost(context.Background(), probeMustFail(t, 9_999_992)); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("RecordGhost() error = %v, want root identity mismatch", err)
	}
}

func TestDurableStoreLeavesNoTemporaryRecordAfterAtomicPublish(t *testing.T) {
	root := canonicalTempRoot(t)
	store, err := processobserve.OpenDurableStore(root, processobserve.DurableOptions{TestOnly: true})
	if err != nil {
		t.Fatalf("OpenDurableStore() error = %v", err)
	}
	if _, err := store.RecordGhost(context.Background(), probeMustFail(t, 9_999_991)); err != nil {
		t.Fatalf("RecordGhost() error = %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(root): %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temporary durable record remains: %q", entry.Name())
		}
	}
}
