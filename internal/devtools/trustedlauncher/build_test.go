package trustedlauncher

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/sync/errgroup"
)

func TestValidateSecureAncestorsRejectsCurrentUserWritableParent(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o770); err != nil {
		t.Fatalf("make current-user ancestor group writable: %v", err)
	}
	if err := validateSecureAncestors(filepath.Join(root, "launcher-root")); err == nil {
		t.Fatal("validateSecureAncestors() accepted a group-writable ancestor")
	}
}

func TestValidateSecureAncestorsRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	realRoot := filepath.Join(root, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatalf("create real launcher ancestor: %v", err)
	}
	linkedRoot := filepath.Join(root, "linked")
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Fatalf("create launcher ancestor symlink: %v", err)
	}
	if err := validateSecureAncestors(filepath.Join(linkedRoot, "launcher-root")); err == nil {
		t.Fatal("validateSecureAncestors() accepted a symlink ancestor")
	}
}

func TestSecureInstallRootAllowsConcurrentCreateAndVerify(t *testing.T) {
	root := trustedLauncherTestInstallRoot(t)
	const builders = 8
	var ready sync.WaitGroup
	var group errgroup.Group
	ready.Add(builders)
	start := make(chan struct{})
	for range builders {
		group.Go(func() error {
			ready.Done()
			<-start
			_, err := secureInstallRoot(root)
			return err
		})
	}
	ready.Wait()
	close(start)
	if err := group.Wait(); err != nil {
		t.Fatalf("concurrent secureInstallRoot() failed: %v", err)
	}
}

func trustedLauncherTestInstallRoot(t *testing.T) string {
	t.Helper()
	// The ECI worker deliberately runs as UID 65532 without an /etc/passwd
	// entry.  Build the fixture below a trusted writable root instead of
	// resolving a login account or using the world-writable system temp dir.
	candidates := []string{"/workspace/work"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, home)
	}
	if workingDirectory, err := os.Getwd(); err == nil {
		candidates = append(candidates, workingDirectory)
	}
	for _, base := range candidates {
		if !filepath.IsAbs(base) || filepath.Clean(base) != base {
			continue
		}
		parentPath := filepath.Join(base, "launcher-root")
		if err := validateSecureAncestors(parentPath); err != nil {
			continue
		}
		parent, err := os.MkdirTemp(base, ".trusted-launcher-root-test-")
		if err != nil {
			continue
		}
		if err := os.Chmod(parent, 0o700); err != nil {
			_ = os.RemoveAll(parent)
			continue
		}
		if err := validateSecureDirectory(parent); err != nil {
			_ = os.RemoveAll(parent)
			continue
		}
		t.Cleanup(func() { _ = os.RemoveAll(parent) })
		return filepath.Join(parent, "launcher-root")
	}
	t.Fatalf("create private launcher test parent: no trusted writable root for uid %d", os.Geteuid())
	return ""
}
