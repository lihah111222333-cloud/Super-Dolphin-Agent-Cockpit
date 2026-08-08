package trustedlauncher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestLauncherBuildCacheRootReusedByTenAgentsAcrossTwoRepositories(t *testing.T) {
	installRoot := requireSecureLauncherTestInstallRoot(t)
	identity := launcherCacheTestIdentity("a", "b")
	const agents = 10
	var group errgroup.Group
	paths := make(chan string, agents)
	for agent := range agents {
		group.Go(func() error {
			cacheRoot, err := launcherBuildCacheRoot(installRoot, identity)
			if err != nil {
				return fmt.Errorf("repository %d worktree agent %d: %w", agent/5, agent, err)
			}
			paths <- cacheRoot
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatalf("concurrent launcherBuildCacheRoot() failed: %v", err)
	}
	expected := <-paths
	for range agents - 1 {
		if cacheRoot := <-paths; cacheRoot != expected {
			t.Fatalf("cache root = %q, want shared root %q", cacheRoot, expected)
		}
	}
	assertPrivateLauncherCacheRoot(t, expected)
}

func TestLauncherBuildCacheRootSeparatesCompilerIdentity(t *testing.T) {
	installRoot := requireSecureLauncherTestInstallRoot(t)
	identity := launcherCacheTestIdentity("a", "b")
	baseRoot := requireLauncherBuildCacheRoot(t, installRoot, identity)
	changedCompiler := launcherCacheTestIdentity("e", "b")
	if changedRoot := requireLauncherBuildCacheRoot(t, installRoot, changedCompiler); changedRoot == baseRoot {
		t.Fatal("compiler digest change reused the previous launcher build cache root")
	}
	changedClosure := launcherCacheTestIdentity("a", "f")
	if changedRoot := requireLauncherBuildCacheRoot(t, installRoot, changedClosure); changedRoot == baseRoot {
		t.Fatal("compiler closure change reused the previous launcher build cache root")
	}
}

func TestLauncherBuildEnvironmentKeepsTemporaryRootSeparateFromPersistentCache(t *testing.T) {
	installRoot := requireSecureLauncherTestInstallRoot(t)
	cacheRoot := requireLauncherBuildCacheRoot(t, installRoot, launcherCacheTestIdentity("c", "d"))
	tempRoot := filepath.Join(installRoot, ".install-fixture")
	environment, err := launcherBuildEnvironment(tempRoot, cacheRoot)
	if err != nil {
		t.Fatalf("launcherBuildEnvironment() error = %v", err)
	}
	want := map[string]bool{"TMPDIR=" + tempRoot: false, "GOCACHE=" + cacheRoot: false}
	for _, entry := range environment {
		if _, tracked := want[entry]; tracked {
			want[entry] = true
		}
	}
	for entry, present := range want {
		if !present {
			t.Fatalf("launcher build environment is missing %q", entry)
		}
	}
}

func requireSecureLauncherTestInstallRoot(t *testing.T) string {
	t.Helper()
	installRoot := trustedLauncherTestInstallRoot(t)
	if _, err := secureInstallRoot(installRoot); err != nil {
		t.Fatalf("secureInstallRoot() error = %v", err)
	}
	return installRoot
}

func launcherCacheTestIdentity(compiler, closure string) LinkedIdentity {
	return LinkedIdentity{
		CompilerSHA256:        "sha256:" + strings.Repeat(compiler, 64),
		CompilerClosureSHA256: "sha256:" + strings.Repeat(closure, 64),
	}
}

func requireLauncherBuildCacheRoot(t *testing.T, installRoot string, identity LinkedIdentity) string {
	t.Helper()
	cacheRoot, err := launcherBuildCacheRoot(installRoot, identity)
	if err != nil {
		t.Fatalf("launcherBuildCacheRoot() error = %v", err)
	}
	return cacheRoot
}

func assertPrivateLauncherCacheRoot(t *testing.T, cacheRoot string) {
	t.Helper()
	info, err := os.Stat(cacheRoot)
	if err != nil {
		t.Fatalf("stat shared launcher cache root: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("shared launcher cache mode = %o, want 700", info.Mode().Perm())
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
