//go:build darwin || linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeServerEnsurePrivateRootRejectsBroadPermissions(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatalf("chmod broad cache root: %v", err)
	}
	if err := runtimeServerEnsurePrivateRoot(root); err == nil {
		t.Fatal("runtimeServerEnsurePrivateRoot() accepted group/world-accessible root")
	}
}

func TestRuntimeServerEnsurePrivateDescendantRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(root, "linked-resource")
	if err := os.Symlink(outside, target); err != nil {
		t.Fatalf("create resource directory symlink: %v", err)
	}
	if err := runtimeServerEnsurePrivateDescendant(root, target); err == nil {
		t.Fatal("runtimeServerEnsurePrivateDescendant() accepted symlink")
	}
}
