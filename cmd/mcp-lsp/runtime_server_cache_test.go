package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

type runtimeServerLeaseTestResult struct {
	role string
	path string
	err  error
}

func TestRuntimeServerEnvironmentSharesCacheAcrossWorktrees(t *testing.T) {
	cacheRoot := runtimeServerSecureCacheRoot(t)
	t.Setenv(agentLSPSharedCacheDirEnv, cacheRoot)
	firstRoot, secondRoot := writeRuntimeLinkedWorktreeFixture(t)
	firstBinary := writeRuntimeServerCacheFixture(t, "typescript-language-server", "#!/bin/sh\nexit 0\n")
	secondBinary := writeRuntimeServerCacheFixture(t, "typescript-language-server", "#!/bin/sh\nexit 0\n")
	command := multilsp.ServerCommand{Executable: firstBinary, Args: []string{"--stdio"}}

	first, err := runtimeServerEnvironment(command, firstBinary, firstRoot, []string{"typescript"}, []string{"WORKTREE=" + firstRoot}, true)
	if err != nil {
		t.Fatalf("runtimeServerEnvironment(first) error = %v", err)
	}
	t.Cleanup(func() { _ = multilsp.ReleaseResourceCohortLease(first) })
	second, err := runtimeServerEnvironment(command, secondBinary, secondRoot, []string{"typescript"}, []string{"WORKTREE=" + secondRoot}, true)
	if err != nil {
		t.Fatalf("runtimeServerEnvironment(second) error = %v", err)
	}
	t.Cleanup(func() { _ = multilsp.ReleaseResourceCohortLease(second) })
	firstEnv := runtimeServerEnvMap(first)
	secondEnv := runtimeServerEnvMap(second)
	assertSharedRuntimeServerEnvironment(t, firstEnv, secondEnv)
}

func assertSharedRuntimeServerEnvironment(t *testing.T, firstEnv, secondEnv map[string]string) {
	t.Helper()
	assertSharedRuntimeResourceCohort(t, firstEnv, secondEnv)
	assertRuntimeServerResourceLimits(t, firstEnv, secondEnv)
	if firstEnv["WORKTREE"] == secondEnv["WORKTREE"] {
		t.Fatal("unrelated per-worktree environment was not preserved")
	}
	assertPortableNodeCompileCache(t, firstEnv, secondEnv)
	if firstEnv["XDG_CACHE_HOME"] != "" || secondEnv["XDG_CACHE_HOME"] != "" {
		t.Fatal("generic XDG cache must not be overridden")
	}
}

func assertSharedRuntimeResourceCohort(t *testing.T, firstEnv, secondEnv map[string]string) {
	t.Helper()
	for _, key := range []string{multilsp.ResourceCohortDirEnv, multilsp.ResourceRepositoryCohortIDEnv} {
		if firstEnv[key] == "" {
			t.Fatalf("%s is empty in first environment", key)
		}
		if firstEnv[key] != secondEnv[key] {
			t.Fatalf("%s differs across worktrees: %q != %q", key, firstEnv[key], secondEnv[key])
		}
		if strings.Contains(firstEnv[key], ".worktrees") {
			t.Fatalf("%s contains worktree identity: %q", key, firstEnv[key])
		}
	}
}

func assertRuntimeServerResourceLimits(t *testing.T, firstEnv, secondEnv map[string]string) {
	t.Helper()
	if got := firstEnv["NODE_OPTIONS"]; got != "--max-old-space-size=2048" {
		t.Fatalf("primary NODE_OPTIONS = %q, want managed primary Node memory limit", got)
	}
	if got := secondEnv["NODE_OPTIONS"]; got != "--max-old-space-size=2048" {
		t.Fatalf("secondary NODE_OPTIONS = %q, want managed secondary Node memory limit", got)
	}
	if firstEnv[multilsp.ResourceCohortRoleEnv] != multilsp.ResourceCohortRolePrimary {
		t.Fatalf("first role = %q, want primary", firstEnv[multilsp.ResourceCohortRoleEnv])
	}
	if firstEnv[multilsp.ResourceCohortHardLimitMBEnv] != "15360" ||
		secondEnv[multilsp.ResourceCohortHardLimitMBEnv] != "15360" {
		t.Fatalf("canonical cohort limits = (%q, %q), want frozen default 15360 MiB",
			firstEnv[multilsp.ResourceCohortHardLimitMBEnv], secondEnv[multilsp.ResourceCohortHardLimitMBEnv])
	}
	if secondEnv[multilsp.ResourceCohortRoleEnv] != multilsp.ResourceCohortRoleSecondary {
		t.Fatalf("second role = %q, want secondary", secondEnv[multilsp.ResourceCohortRoleEnv])
	}
	if firstEnv[multilsp.ResourceProcessRSSLimitMBEnv] != "2560" || secondEnv[multilsp.ResourceProcessRSSLimitMBEnv] != "2560" {
		t.Fatalf("process RSS limits = (%q, %q), want primary and secondary 2560 MiB",
			firstEnv[multilsp.ResourceProcessRSSLimitMBEnv], secondEnv[multilsp.ResourceProcessRSSLimitMBEnv])
	}
}

func assertPortableNodeCompileCache(t *testing.T, firstEnv, secondEnv map[string]string) {
	t.Helper()
	_, portable, err := runtimeServerNodeVersion(nil)
	if err != nil {
		t.Fatalf("runtimeServerNodeVersion() error = %v", err)
	}
	for _, key := range []string{"NODE_COMPILE_CACHE", "NODE_COMPILE_CACHE_PORTABLE"} {
		if portable && (firstEnv[key] == "" || firstEnv[key] != secondEnv[key]) {
			t.Fatalf("portable %s was not shared: first=%q second=%q", key, firstEnv[key], secondEnv[key])
		}
		if !portable && firstEnv[key] != "" {
			t.Fatalf("unsupported portable %s = %q, want empty", key, firstEnv[key])
		}
	}
}

// super-dolphin-ci: compile-group-exclusive
func TestRuntimeServerEnvironmentSharesGlobalNonGoplsRSSPoolButIsolatesCaches(t *testing.T) {
	t.Setenv(agentLSPSharedCacheDirEnv, runtimeServerSecureCacheRoot(t))
	firstBinary := writeRuntimeServerCacheFixture(t, "language-server", "#!/bin/sh\nexit 0\n")
	secondBinary := writeRuntimeServerCacheFixture(t, "language-server", "#!/bin/sh\nexit 1\n")
	limits := []string{
		multilsp.ResourceCohortHardLimitMBEnv + "=1024",
		runtimePrimaryRSSLimitEnv + "=768",
		runtimeSecondaryRSSLimitEnv + "=256",
		runtimeNodePrimaryHeapEnv + "=512",
		runtimeNodeSecondaryHeapEnv + "=128",
	}

	first, err := runtimeServerEnvironment(multilsp.ServerCommand{Executable: firstBinary}, firstBinary, t.TempDir(), []string{"rust"}, limits, false)
	if err != nil {
		t.Fatalf("runtimeServerEnvironment(first) error = %v", err)
	}
	t.Cleanup(func() { _ = multilsp.ReleaseResourceCohortLease(first) })
	second, err := runtimeServerEnvironment(multilsp.ServerCommand{Executable: secondBinary}, secondBinary, t.TempDir(), []string{"rust"}, limits, false)
	if err != nil {
		t.Fatalf("runtimeServerEnvironment(second) error = %v", err)
	}
	t.Cleanup(func() { _ = multilsp.ReleaseResourceCohortLease(second) })
	firstCache := runtimeServerEnvMap(first)[multilsp.ResourceCohortDirEnv]
	secondCache := runtimeServerEnvMap(second)[multilsp.ResourceCohortDirEnv]
	if got := runtimeServerEnvMap(first)[multilsp.ResourceCohortHardLimitMBEnv]; got != "1024" {
		t.Fatalf("frozen custom cohort limit = %q, want 1024 MiB", got)
	}
	if firstCache != secondCache {
		t.Fatalf("non-gopls resource pools differ: %q != %q", firstCache, secondCache)
	}
	firstVersionCache, err := runtimeServerCacheDir(multilsp.ServerCommand{Executable: firstBinary}, firstBinary)
	if err != nil {
		t.Fatalf("runtimeServerCacheDir(first) error = %v", err)
	}
	secondVersionCache, err := runtimeServerCacheDir(multilsp.ServerCommand{Executable: secondBinary}, secondBinary)
	if err != nil {
		t.Fatalf("runtimeServerCacheDir(second) error = %v", err)
	}
	if firstVersionCache == secondVersionCache {
		t.Fatalf("incompatible binary caches share directory %q", firstVersionCache)
	}
}

// super-dolphin-ci: compile-group-exclusive
func TestRuntimeServerResourceCohortDirIsolatesGoplsDaemons(t *testing.T) {
	t.Setenv(agentLSPSharedCacheDirEnv, runtimeServerSecureCacheRoot(t))
	binary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	first, err := runtimeServerResourceCohortDir(
		multilsp.ServerCommand{Executable: binary, Args: []string{"-remote=auto;sdmcp2-first"}},
		binary,
	)
	if err != nil {
		t.Fatalf("runtimeServerResourceCohortDir(first) error = %v", err)
	}
	second, err := runtimeServerResourceCohortDir(
		multilsp.ServerCommand{Executable: binary, Args: []string{"-remote=auto;sdmcp2-second"}},
		binary,
	)
	if err != nil {
		t.Fatalf("runtimeServerResourceCohortDir(second) error = %v", err)
	}
	if first == second {
		t.Fatalf("incompatible gopls daemon cohorts share resource directory %q", first)
	}
}

// super-dolphin-ci: compile-group-exclusive
func TestRuntimeGoplsRemoteIDBindsRealpathAndResourceDirUsesRemoteID(t *testing.T) {
	t.Setenv(agentLSPSharedCacheDirEnv, runtimeServerSecureCacheRoot(t))
	firstBinary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	secondBinary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	command := multilsp.ServerCommand{
		Executable: "gopls",
		Args:       []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"},
	}
	firstArgs := mustRuntimeServerArgs(t, command, firstBinary, []string{"GOOS=darwin"})
	secondArgs := mustRuntimeServerArgs(t, command, secondBinary, []string{"GOOS=darwin"})
	firstID := runtimeServerGoplsRemoteID(firstArgs)
	if firstID == "" || firstID == runtimeServerGoplsRemoteID(secondArgs) {
		t.Fatalf("different gopls realpaths reused one remote ID: first=%v second=%v", firstArgs, secondArgs)
	}
	resourceCommand := command
	resourceCommand.Args = firstArgs
	resourceDir, err := runtimeServerResourceCohortDir(resourceCommand, firstBinary)
	if err != nil {
		t.Fatalf("runtimeServerResourceCohortDir() error = %v", err)
	}
	if filepath.Base(resourceDir) != firstID {
		t.Fatalf("resource cohort %q is not derived from remote ID %q", resourceDir, firstID)
	}
}

// super-dolphin-ci: compile-group-exclusive
func TestRuntimeGoplsCohortRehashesSamePathWithRestoredMetadata(t *testing.T) {
	binary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	info, err := os.Stat(binary)
	if err != nil {
		t.Fatalf("stat first gopls fixture: %v", err)
	}
	command := multilsp.ServerCommand{
		Executable: "gopls",
		Args:       []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"},
	}
	first := mustRuntimeServerArgs(t, command, binary, []string{"GOOS=darwin"})
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 1\n"), info.Mode()); err != nil {
		t.Fatalf("replace gopls fixture: %v", err)
	}
	if err := os.Chtimes(binary, info.ModTime(), info.ModTime()); err != nil {
		t.Fatalf("restore gopls fixture mtime: %v", err)
	}
	second := mustRuntimeServerArgs(t, command, binary, []string{"GOOS=darwin"})
	if runtimeServerGoplsRemoteID(first) == runtimeServerGoplsRemoteID(second) {
		t.Fatalf("same-path content replacement reused stale remote ID: first=%v second=%v", first, second)
	}
}

func TestRuntimeServerEnvironmentPreservesNodeOptions(t *testing.T) {
	t.Setenv(agentLSPSharedCacheDirEnv, runtimeServerSecureCacheRoot(t))
	t.Setenv("NODE_OPTIONS", "--trace-warnings")
	binary := writeRuntimeServerCacheFixture(t, "language-server", "#!/bin/sh\nexit 0\n")

	env, err := runtimeServerEnvironment(
		multilsp.ServerCommand{Executable: binary},
		binary,
		t.TempDir(),
		[]string{"typescript"},
		[]string{"NODE_OPTIONS=--enable-source-maps"},
		true,
	)
	if err != nil {
		t.Fatalf("runtimeServerEnvironment() error = %v", err)
	}
	t.Cleanup(func() { _ = multilsp.ReleaseResourceCohortLease(env) })
	if got := runtimeServerEnvMap(env)["NODE_OPTIONS"]; got != "--enable-source-maps --max-old-space-size=2048" {
		t.Fatalf("NODE_OPTIONS = %q, want adapter options plus managed memory limit", got)
	}
}

func TestRuntimeServerEnvironmentDoesNotInjectNodePolicyIntoNativeServer(t *testing.T) {
	t.Setenv(agentLSPSharedCacheDirEnv, runtimeServerSecureCacheRoot(t))
	binary := writeRuntimeServerCacheFixture(t, "native-language-server", "#!/bin/sh\nexit 0\n")
	env, err := runtimeServerEnvironment(multilsp.ServerCommand{Executable: binary}, binary, t.TempDir(), []string{"rust"}, nil, false)
	if err != nil {
		t.Fatalf("runtimeServerEnvironment() error = %v", err)
	}
	t.Cleanup(func() { _ = multilsp.ReleaseResourceCohortLease(env) })
	values := runtimeServerEnvMap(env)
	for _, key := range []string{"NODE_OPTIONS", "NODE_COMPILE_CACHE", "NODE_COMPILE_CACHE_PORTABLE", "XDG_CACHE_HOME"} {
		if values[key] != "" {
			t.Fatalf("native server %s = %q, want empty", key, values[key])
		}
	}
	if values[multilsp.ResourceCohortDirEnv] == "" {
		t.Fatal("native server resource cohort directory is empty")
	}
}

func TestRuntimeParseNodeVersionPortableBoundary(t *testing.T) {
	tests := []struct {
		version  string
		portable bool
	}{
		{version: "v22.5.0"},
		{version: "v24.11.1"},
		{version: "v24.12.0", portable: true},
		{version: "v25.0.0", portable: true},
	}
	for _, test := range tests {
		major, minor, err := runtimeParseNodeVersion(test.version)
		if err != nil {
			t.Fatalf("runtimeParseNodeVersion(%q) error = %v", test.version, err)
		}
		got := major > 24 || (major == 24 && minor >= 12)
		if got != test.portable {
			t.Fatalf("runtimeParseNodeVersion(%q) portable = %v, want %v", test.version, got, test.portable)
		}
	}
}

func TestRuntimeServerNodeVersionUsesAdapterPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX executable script")
	}
	dir := t.TempDir()
	nodePath := filepath.Join(dir, "node")
	if err := os.WriteFile(nodePath, []byte("#!/bin/sh\nprintf 'v24.11.9\\n'\n"), 0o755); err != nil {
		t.Fatalf("write fake Node runtime: %v", err)
	}
	version, portable, err := runtimeServerNodeVersion([]string{"PATH=" + dir})
	if err != nil {
		t.Fatalf("runtimeServerNodeVersion(adapter PATH) error = %v", err)
	}
	if version != "v24.11.9" || portable {
		t.Fatalf("adapter Node result = (%q, %v), want (v24.11.9, false)", version, portable)
	}
}

func TestRuntimeServerLookPathRejectsCurrentDirectoryEntries(t *testing.T) {
	for _, pathEnv := range []string{
		"relative",
		string(os.PathListSeparator) + os.Getenv("PATH"),
	} {
		if _, err := runtimeServerLookPath("node", pathEnv, ""); err == nil {
			t.Fatalf("runtimeServerLookPath() accepted unsafe PATH %q", pathEnv)
		}
	}
}

// super-dolphin-ci: compile-group-exclusive
func TestRuntimeServerAcquireResourceLeaseSerializesStalePrimaryElection(t *testing.T) {
	root := runtimeServerSecureCacheRoot(t)
	cohortID := "repo-concurrent-election"
	cohortDir := filepath.Join(root, "resource-cohorts", "repositories", cohortID)
	if err := runtimeServerEnsurePrivateDescendant(root, cohortDir); err != nil {
		t.Fatalf("prepare repository cohort directory: %v", err)
	}
	primaryPath := filepath.Join(cohortDir, "primary.json")
	stale := runtimeServerResourceLease{
		SchemaVersion:      runtimeResourceLeaseSchemaVersion,
		CohortID:           cohortID,
		Role:               multilsp.ResourceCohortRolePrimary,
		OwnerPID:           os.Getpid(),
		OwnerStartIdentity: "stale-owner-identity",
		CreatedAtUnixNano:  time.Now().Add(-time.Hour).UnixNano(),
	}
	if err := runtimeServerCreateResourceLease(primaryPath, stale); err != nil {
		t.Fatalf("write stale primary lease: %v", err)
	}

	const contenders = 12
	start := make(chan struct{})
	results := make(chan runtimeServerLeaseTestResult, contenders)
	goroutines := newTestGoroutineGroup(t)
	for range contenders {
		goroutines.Go(func() {
			<-start
			role, path, err := runtimeServerAcquireResourceLease(root, cohortID)
			results <- runtimeServerLeaseTestResult{role: role, path: path, err: err}
		})
	}
	close(start)
	goroutines.Wait()
	close(results)

	primaryCount, secondaryCount, paths := summarizeRuntimeServerLeaseResults(t, results)
	if primaryCount != 1 || secondaryCount != contenders-1 || len(paths) != contenders {
		t.Fatalf("election results: primary=%d secondary=%d unique_paths=%d", primaryCount, secondaryCount, len(paths))
	}
	lease, err := runtimeServerReadResourceLease(primaryPath)
	if err != nil {
		t.Fatalf("read elected primary lease: %v", err)
	}
	if err := runtimeServerValidatePrimaryLease(lease, cohortID, primaryPath); err != nil {
		t.Fatalf("validate elected primary lease: %v", err)
	}
}

// super-dolphin-ci: compile-group-exclusive
func TestRuntimeServerAcquireResourceLeaseWaitsForElectionLock(t *testing.T) {
	root := runtimeServerSecureCacheRoot(t)
	cohortID := "repo-lock-barrier"
	cohortDir := filepath.Join(root, "resource-cohorts", "repositories", cohortID)
	if err := runtimeServerEnsurePrivateDescendant(root, cohortDir); err != nil {
		t.Fatalf("prepare repository cohort directory: %v", err)
	}
	held, err := runtimeServerAcquireResourceLeaseLock(cohortDir)
	if err != nil {
		t.Fatalf("hold repository cohort election lock: %v", err)
	}
	t.Cleanup(func() {
		if held != nil {
			_ = runtimeServerReleaseResourceLeaseLock(held)
		}
	})

	started := make(chan struct{})
	resultCh := make(chan runtimeServerLeaseTestResult, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		close(started)
		role, path, err := runtimeServerAcquireResourceLease(root, cohortID)
		resultCh <- runtimeServerLeaseTestResult{role: role, path: path, err: err}
	})
	<-started
	assertRuntimeServerLeaseBlocked(t, resultCh)
	if err := runtimeServerReleaseResourceLeaseLock(held); err != nil {
		t.Fatalf("release repository cohort election lock: %v", err)
	}
	held = nil
	assertRuntimeServerLeaseResult(t, resultCh, multilsp.ResourceCohortRolePrimary)
	goroutines.Wait()
}

// super-dolphin-ci: compile-group-exclusive
func TestRuntimeServerAcquireResourceLeaseRejectsStableCorruptPrimary(t *testing.T) {
	root := runtimeServerSecureCacheRoot(t)
	cohortID := "repo-corrupt-primary"
	cohortDir := filepath.Join(root, "resource-cohorts", "repositories", cohortID)
	if err := runtimeServerEnsurePrivateDescendant(root, cohortDir); err != nil {
		t.Fatalf("prepare repository cohort directory: %v", err)
	}
	primaryPath := filepath.Join(cohortDir, "primary.json")
	if err := os.WriteFile(primaryPath, []byte("{"), 0o600); err != nil {
		t.Fatalf("write corrupt primary lease: %v", err)
	}
	if _, _, err := runtimeServerAcquireResourceLease(root, cohortID); err == nil {
		t.Fatal("runtimeServerAcquireResourceLease() accepted corrupt primary lease")
	}
	payload, err := os.ReadFile(primaryPath)
	if err != nil {
		t.Fatalf("read corrupt primary after rejection: %v", err)
	}
	if string(payload) != "{" {
		t.Fatalf("corrupt primary was overwritten: %q", payload)
	}
}

func TestRuntimeServerResolveResourceLimitsMatrix(t *testing.T) {
	tests := []struct {
		name    string
		env     []string
		wantErr bool
	}{
		{name: "defaults"},
		{name: "valid boundaries", env: []string{
			runtimePrimaryRSSLimitEnv + "=15360",
			runtimeSecondaryRSSLimitEnv + "=2047",
			runtimeNodePrimaryHeapEnv + "=15359",
			runtimeNodeSecondaryHeapEnv + "=2046",
		}},
		{name: "valid custom low cohort", env: []string{
			multilsp.ResourceCohortHardLimitMBEnv + "=1024",
			runtimePrimaryRSSLimitEnv + "=768",
			runtimeSecondaryRSSLimitEnv + "=256",
			runtimeNodePrimaryHeapEnv + "=512",
			runtimeNodeSecondaryHeapEnv + "=128",
		}},
		{name: "deprecated cohort owner", env: []string{
			multilsp.DeprecatedResourceCohortHardLimitMBEnv + "=15360",
		}, wantErr: true},
		{name: "secondary matches primary at 2.5 GiB", env: []string{
			runtimePrimaryRSSLimitEnv + "=2560",
			runtimeSecondaryRSSLimitEnv + "=2560",
			runtimeNodePrimaryHeapEnv + "=2048",
			runtimeNodeSecondaryHeapEnv + "=2048",
		}},
		{name: "secondary exceeds primary", env: []string{
			runtimePrimaryRSSLimitEnv + "=2560",
			runtimeSecondaryRSSLimitEnv + "=2561",
		}, wantErr: true},
		{name: "primary exceeds global", env: []string{
			runtimePrimaryRSSLimitEnv + "=15361",
			runtimeSecondaryRSSLimitEnv + "=512",
		}, wantErr: true},
		{name: "both exceed configured global", env: []string{
			runtimePrimaryRSSLimitEnv + "=1024",
			runtimeSecondaryRSSLimitEnv + "=512",
			multilsp.ResourceCohortHardLimitMBEnv + "=256",
		}, wantErr: true},
		{name: "primary heap reaches RSS", env: []string{
			runtimePrimaryRSSLimitEnv + "=1024",
			runtimeNodePrimaryHeapEnv + "=1024",
		}, wantErr: true},
		{name: "secondary heap reaches RSS", env: []string{
			runtimeSecondaryRSSLimitEnv + "=512",
			runtimeNodeSecondaryHeapEnv + "=512",
		}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := runtimeServerResolveResourceLimits(test.env)
			if (err != nil) != test.wantErr {
				t.Fatalf("runtimeServerResolveResourceLimits() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestRuntimeServerEnvironmentRejectsLimitsBeforeCreatingLease(t *testing.T) {
	root := runtimeServerSecureCacheRoot(t)
	t.Setenv(agentLSPSharedCacheDirEnv, root)
	binary := writeRuntimeServerCacheFixture(t, "native-language-server", "#!/bin/sh\nexit 0\n")
	_, err := runtimeServerEnvironment(
		multilsp.ServerCommand{Executable: binary},
		binary,
		t.TempDir(),
		[]string{"rust"},
		[]string{multilsp.ResourceCohortHardLimitMBEnv + "=256"},
		false,
	)
	if err == nil {
		t.Fatal("runtimeServerEnvironment() accepted invalid resource limits")
	}
	leaseRoot := filepath.Join(root, "resource-cohorts", "repositories")
	if _, statErr := os.Stat(leaseRoot); !os.IsNotExist(statErr) {
		t.Fatalf("invalid limits left repository lease state: stat error = %v", statErr)
	}
}

// super-dolphin-ci: compile-group-exclusive
func TestRuntimeServerCleanupResourceLeasesRemovesReusedPIDOrphans(t *testing.T) {
	root := runtimeServerSecureCacheRoot(t)
	cohortID := "repo-secondary-orphans"
	role, primaryPath, err := runtimeServerAcquireResourceLease(root, cohortID)
	if err != nil || role != multilsp.ResourceCohortRolePrimary {
		t.Fatalf("create primary lease: role=%q path=%q err=%v", role, primaryPath, err)
	}
	primary, err := runtimeServerReadResourceLease(primaryPath)
	if err != nil {
		t.Fatalf("read primary lease: %v", err)
	}
	cohortDir := filepath.Dir(primaryPath)
	writeRuntimeServerOrphanLeases(t, cohortDir, primary, 8)
	role, secondaryPath, err := runtimeServerAcquireResourceLease(root, cohortID)
	if err != nil || role != multilsp.ResourceCohortRoleSecondary {
		t.Fatalf("acquire after orphan cleanup: role=%q path=%q err=%v", role, secondaryPath, err)
	}
	secondaryCount := countRuntimeServerSecondaryLeases(t, cohortDir)
	if secondaryCount != 1 {
		t.Fatalf("active secondary lease count = %d, want only newly acquired lease", secondaryCount)
	}
	if _, err := os.Stat(secondaryPath); err != nil {
		t.Fatalf("new secondary lease is unavailable: %v", err)
	}
}

// super-dolphin-ci: compile-group-exclusive
func TestRuntimeServerCleanupResourceLeasesBoundsQuarantine(t *testing.T) {
	root := runtimeServerSecureCacheRoot(t)
	cohortID := "repo-secondary-quarantine"
	cohortDir := filepath.Join(root, "resource-cohorts", "repositories", cohortID)
	if err := runtimeServerEnsurePrivateDescendant(root, cohortDir); err != nil {
		t.Fatalf("prepare repository cohort directory: %v", err)
	}
	now := time.Now()
	malformedPath := filepath.Join(cohortDir, "secondary-malformed.json")
	if err := os.WriteFile(malformedPath, []byte("{"), 0o600); err != nil {
		t.Fatalf("write malformed secondary lease: %v", err)
	}
	if _, err := runtimeServerCleanupResourceLeases(cohortDir, cohortID, now); err == nil {
		t.Fatal("cleanup accepted malformed secondary lease")
	}
	if _, err := os.Stat(malformedPath); !os.IsNotExist(err) {
		t.Fatalf("malformed secondary remained published: %v", err)
	}
	writeRuntimeServerLeaseQuarantines(t, cohortDir, now)
	if _, err := runtimeServerCleanupResourceLeases(cohortDir, cohortID, now); err != nil {
		t.Fatalf("cleanup bounded quarantine: %v", err)
	}
	assertRuntimeServerLeaseQuarantinesBounded(t, cohortDir)
}

func summarizeRuntimeServerLeaseResults(
	t *testing.T,
	results <-chan runtimeServerLeaseTestResult,
) (int, int, map[string]struct{}) {
	t.Helper()
	primaryCount := 0
	secondaryCount := 0
	paths := make(map[string]struct{}, len(results))
	for got := range results {
		if got.err != nil {
			t.Fatalf("runtimeServerAcquireResourceLease() error = %v", got.err)
		}
		paths[got.path] = struct{}{}
		switch got.role {
		case multilsp.ResourceCohortRolePrimary:
			primaryCount++
		case multilsp.ResourceCohortRoleSecondary:
			secondaryCount++
		default:
			t.Fatalf("unexpected repository cohort role %q", got.role)
		}
	}
	return primaryCount, secondaryCount, paths
}

func assertRuntimeServerLeaseBlocked(t *testing.T, results <-chan runtimeServerLeaseTestResult) {
	t.Helper()
	select {
	case got := <-results:
		t.Fatalf("election crossed held lock early: %#v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func assertRuntimeServerLeaseResult(t *testing.T, results <-chan runtimeServerLeaseTestResult, role string) {
	t.Helper()
	select {
	case got := <-results:
		if got.err != nil || got.role != role {
			t.Fatalf("election after lock release = %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("election did not resume after lock release")
	}
}

func writeRuntimeServerOrphanLeases(
	t *testing.T,
	cohortDir string,
	primary runtimeServerResourceLease,
	count int,
) {
	t.Helper()
	for index := range count {
		orphan := primary
		orphan.Role = multilsp.ResourceCohortRoleSecondary
		orphan.OwnerStartIdentity = fmt.Sprintf("reused-pid-%d", index)
		path := filepath.Join(cohortDir, fmt.Sprintf("secondary-orphan-%02d.json", index))
		if err := runtimeServerCreateResourceLease(path, orphan); err != nil {
			t.Fatalf("write orphan secondary %d: %v", index, err)
		}
	}
}

func countRuntimeServerSecondaryLeases(t *testing.T, cohortDir string) int {
	t.Helper()
	entries, err := os.ReadDir(cohortDir)
	if err != nil {
		t.Fatalf("read cohort leases: %v", err)
	}
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "secondary-") && strings.HasSuffix(entry.Name(), ".json") {
			count++
		}
	}
	return count
}

func writeRuntimeServerLeaseQuarantines(t *testing.T, cohortDir string, now time.Time) {
	t.Helper()
	for index := range runtimeLeaseQuarantineMaxCount + 12 {
		path := filepath.Join(cohortDir, fmt.Sprintf("evidence-%02d.bad", index))
		if err := os.WriteFile(path, []byte("evidence"), 0o600); err != nil {
			t.Fatalf("write quarantine %d: %v", index, err)
		}
		modifiedAt := now.Add(time.Duration(index) * time.Millisecond)
		if index < 2 {
			modifiedAt = now.Add(-runtimeLeaseQuarantineMaxAge - time.Hour)
		}
		if err := os.Chtimes(path, modifiedAt, modifiedAt); err != nil {
			t.Fatalf("age quarantine %d: %v", index, err)
		}
	}
}

func assertRuntimeServerLeaseQuarantinesBounded(t *testing.T, cohortDir string) {
	t.Helper()
	entries, err := os.ReadDir(cohortDir)
	if err != nil {
		t.Fatalf("read bounded quarantines: %v", err)
	}
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".bad") {
			count++
		}
	}
	if count > runtimeLeaseQuarantineMaxCount {
		t.Fatalf("quarantine count = %d, limit = %d", count, runtimeLeaseQuarantineMaxCount)
	}
	for index := range 2 {
		path := filepath.Join(cohortDir, fmt.Sprintf("evidence-%02d.bad", index))
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expired quarantine %d was retained: %v", index, err)
		}
	}
}

func runtimeServerSecureCacheRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := runtimeServerHardenPrivateDirectory(root); err != nil {
		t.Fatalf("secure test cache root: %v", err)
	}
	if err := runtimeServerValidatePrivateDirectory(root); err != nil {
		t.Fatalf("validate secure test cache root: %v", err)
	}
	return root
}

func writeRuntimeLinkedWorktreeFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	commonGitDir := filepath.Join(root, "common.git")
	worktreesRoot := filepath.Join(root, "worktrees")
	if err := os.MkdirAll(filepath.Join(commonGitDir, "worktrees"), 0o700); err != nil {
		t.Fatalf("create common Git directory: %v", err)
	}
	roots := make([]string, 0, 2)
	for _, name := range []string{"one", "two"} {
		workspace := filepath.Join(worktreesRoot, name)
		gitDir := filepath.Join(commonGitDir, "worktrees", name)
		if err := os.MkdirAll(gitDir, 0o700); err != nil {
			t.Fatalf("create linked worktree Git directory: %v", err)
		}
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			t.Fatalf("create linked worktree: %v", err)
		}
		if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../..\n"), 0o600); err != nil {
			t.Fatalf("write linked worktree commondir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(workspace, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600); err != nil {
			t.Fatalf("write linked worktree Git marker: %v", err)
		}
		roots = append(roots, workspace)
	}
	return roots[0], roots[1]
}

func writeRuntimeServerCacheFixture(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write server fixture: %v", err)
	}
	return path
}

func runtimeServerEnvMap(entries []string) map[string]string {
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}
