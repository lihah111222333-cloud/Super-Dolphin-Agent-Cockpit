//go:build darwin || linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sync/errgroup"
)

func TestRuntimeServerEnsurePrivateDescendantAllowsConcurrentCreators(t *testing.T) {
	root := runtimeServerSecureCacheRoot(t)
	target := filepath.Join(root, "gopls-root-cohorts", strings.Repeat("a", 64))

	const creators = 32
	start := make(chan struct{})
	errs := make(chan error, creators)
	var group errgroup.Group
	for range creators {
		group.Go(func() error {
			<-start
			errs <- runtimeServerEnsurePrivateDescendant(root, target)
			return nil
		})
	}
	close(start)
	if err := group.Wait(); err != nil {
		t.Fatalf("wait concurrent creators: %v", err)
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent private descendant creation failed: %v", err)
		}
	}
}

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
