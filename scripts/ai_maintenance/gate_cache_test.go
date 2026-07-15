package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGateResultCacheReusesOnlyMatchingGreenResult(t *testing.T) {
	var runs atomic.Int32
	fingerprint := strings.Repeat("a", 64)
	cache := newTestGateResultCache(t, func(string, gatePlan) (string, error) {
		return fingerprint, nil
	})
	plan := gatePlan{RequiredGates: []string{"backend:test_with_guard"}}
	run := func() error {
		runs.Add(1)
		return nil
	}

	if err := cache.run("backend:test_with_guard", plan, run); err != nil {
		t.Fatalf("first cache run: %v", err)
	}
	if err := cache.run("backend:test_with_guard", plan, run); err != nil {
		t.Fatalf("second cache run: %v", err)
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("matching green result ran %d times, want 1", got)
	}

	fingerprint = strings.Repeat("b", 64)
	if err := cache.run("backend:test_with_guard", plan, run); err != nil {
		t.Fatalf("invalidated cache run: %v", err)
	}
	if got := runs.Load(); got != 2 {
		t.Fatalf("changed fingerprint ran %d times, want 2", got)
	}
}

func TestGateResultCacheDoesNotStoreFailure(t *testing.T) {
	var runs atomic.Int32
	cache := newTestGateResultCache(t, fixedGateFingerprint("c"))
	wantErr := errors.New("gate failed")
	if err := cache.run("backend:test_with_guard", gatePlan{}, func() error {
		runs.Add(1)
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("failure = %v, want %v", err, wantErr)
	}
	if err := cache.run("backend:test_with_guard", gatePlan{}, func() error {
		runs.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("rerun after failure: %v", err)
	}
	if got := runs.Load(); got != 2 {
		t.Fatalf("failed result was cached, runs=%d", got)
	}
}

func TestGateResultCacheRejectsCorruptMarker(t *testing.T) {
	cache := newTestGateResultCache(t, fixedGateFingerprint("d"))
	fingerprint := strings.Repeat("d", 64)
	marker, err := cache.markerPath("backend:test_with_guard", fingerprint)
	if err != nil {
		t.Fatalf("marker path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatalf("mkdir marker: %v", err)
	}
	if err := os.WriteFile(marker, []byte("corrupt\n"), 0o600); err != nil {
		t.Fatalf("write corrupt marker: %v", err)
	}

	err = cache.run("backend:test_with_guard", gatePlan{}, func() error {
		t.Fatal("corrupt marker must block before executing gate")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("corrupt marker error = %v", err)
	}
}

func TestGateResultCacheExpiresAndReruns(t *testing.T) {
	var runs atomic.Int32
	cache := newTestGateResultCache(t, fixedGateFingerprint("e"))
	now := time.Now()
	cache.now = func() time.Time { return now }
	run := func() error {
		runs.Add(1)
		return nil
	}
	if err := cache.run("backend:test_with_guard", gatePlan{}, run); err != nil {
		t.Fatalf("initial cache run: %v", err)
	}
	now = now.Add(cache.maxAge + time.Second)
	if err := cache.run("backend:test_with_guard", gatePlan{}, run); err != nil {
		t.Fatalf("expired cache run: %v", err)
	}
	if got := runs.Load(); got != 2 {
		t.Fatalf("expired cache ran %d times, want 2", got)
	}
}

func TestGateResultCacheRejectsFutureMarkerAndReruns(t *testing.T) {
	var runs atomic.Int32
	cache := newTestGateResultCache(t, fixedGateFingerprint("9"))
	now := time.Now()
	cache.now = func() time.Time { return now }
	run := func() error {
		runs.Add(1)
		return nil
	}
	if err := cache.run("backend:test_with_guard", gatePlan{}, run); err != nil {
		t.Fatalf("initial cache run: %v", err)
	}
	marker, err := cache.markerPath("backend:test_with_guard", strings.Repeat("9", 64))
	if err != nil {
		t.Fatalf("marker path: %v", err)
	}
	future := now.Add(time.Hour)
	if err := os.Chtimes(marker, future, future); err != nil {
		t.Fatalf("set future marker time: %v", err)
	}
	if err := cache.run("backend:test_with_guard", gatePlan{}, run); err != nil {
		t.Fatalf("rerun future marker: %v", err)
	}
	if got := runs.Load(); got != 2 {
		t.Fatalf("future marker ran %d times, want 2", got)
	}
}

func TestGateResultCachePublishesValidMarkerConcurrently(t *testing.T) {
	cache := newTestGateResultCache(t, fixedGateFingerprint("f"))
	start := make(chan struct{})
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			<-start
			if err := cache.run("backend:test_with_guard", gatePlan{}, func() error { return nil }); err != nil {
				t.Errorf("concurrent cache run: %v", err)
			}
		})
	}
	close(start)
	group.Wait()
	if err := cache.run("backend:test_with_guard", gatePlan{}, func() error {
		t.Fatal("valid concurrent marker should be a cache hit")
		return nil
	}); err != nil {
		t.Fatalf("read concurrent marker: %v", err)
	}
}

func TestGateResultCacheRejectsInputChangeBeforeCacheHit(t *testing.T) {
	fingerprint := strings.Repeat("1", 64)
	cache := newTestGateResultCache(t, fixedGateFingerprint("1"))
	if err := cache.run("project-map:check", gatePlan{}, func() error { return nil }); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	var calls atomic.Int32
	cache.fingerprint = func(string, gatePlan) (string, error) {
		if calls.Add(1) == 1 {
			return fingerprint, nil
		}
		return strings.Repeat("2", 64), nil
	}
	err := cache.run("project-map:check", gatePlan{}, func() error {
		t.Fatal("cache hit must not execute the gate")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "inputs changed") {
		t.Fatalf("changed cache-hit input error = %v", err)
	}
}

func TestGateResultCacheDoesNotPublishWhenInputsChangeDuringRun(t *testing.T) {
	var calls atomic.Int32
	cache := newTestGateResultCache(t, func(string, gatePlan) (string, error) {
		if calls.Add(1) == 1 {
			return strings.Repeat("3", 64), nil
		}
		return strings.Repeat("4", 64), nil
	})
	err := cache.run("project-map:check", gatePlan{}, func() error { return nil })
	if err == nil || !strings.Contains(err.Error(), "inputs changed") {
		t.Fatalf("changed post-run input error = %v", err)
	}
	marker, markerErr := cache.markerPath("project-map:check", strings.Repeat("3", 64))
	if markerErr != nil {
		t.Fatalf("marker path: %v", markerErr)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("unstable run published marker: %v", statErr)
	}
}

func TestFingerprintGateInputsInvalidatesScopeAndEnvironment(t *testing.T) {
	plan := gatePlan{RequiredGates: []string{"backend:test_with_guard"}}
	scope := runGateCacheGit(t, ".", "write-tree")
	t.Setenv("SUPER_DOLPHIN_CACHE_TEST_INPUT", "first")
	first, err := fingerprintGateInputs(scope, "backend:test_with_guard", plan)
	if err != nil {
		t.Fatalf("first fingerprint: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_CACHE_TEST_INPUT", "second")
	second, err := fingerprintGateInputs(scope, "backend:test_with_guard", plan)
	if err != nil {
		t.Fatalf("environment fingerprint: %v", err)
	}
	third, err := fingerprintGateInputs(writeEmptyGateCacheTree(t, "."), "backend:test_with_guard", plan)
	if err != nil {
		t.Fatalf("scope fingerprint: %v", err)
	}
	if first == second {
		t.Fatal("environment change did not invalidate gate fingerprint")
	}
	if second == third {
		t.Fatal("staged tree change did not invalidate gate fingerprint")
	}
}

func TestFingerprintGateInputsIgnoresShellBookkeepingEnvironment(t *testing.T) {
	scope := runGateCacheGit(t, ".", "write-tree")
	first, err := fingerprintGateInputs(scope, "diff:whitespace", gatePlan{})
	if err != nil {
		t.Fatalf("initial fingerprint: %v", err)
	}
	t.Setenv("_", "/different/last/command")
	t.Setenv("SHLVL", "999")
	t.Setenv("OLDPWD", "/different/previous/directory")
	second, err := fingerprintGateInputs(scope, "diff:whitespace", gatePlan{})
	if err != nil {
		t.Fatalf("shell bookkeeping fingerprint: %v", err)
	}
	if first != second {
		t.Fatal("shell bookkeeping environment invalidated stable gate fingerprint")
	}
}

func TestStableGateEnvironmentOmitsIsolatedIndexPath(t *testing.T) {
	t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "isolated-index"))
	t.Setenv("PWD", filepath.Join(t.TempDir(), "isolated-worktree"))
	for _, entry := range stableGateEnvironment() {
		if strings.HasPrefix(entry, "GIT_INDEX_FILE=") || strings.HasPrefix(entry, "PWD=") {
			t.Fatalf("ephemeral snapshot path leaked into stable cache environment: %q", entry)
		}
	}
}

func TestProjectMapFingerprintIgnoresEquivalentIsolatedIndexPath(t *testing.T) {
	scope := runGateCacheGit(t, ".", "write-tree")
	day := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	firstIndex := filepath.Join(t.TempDir(), "index")
	writeGateCacheIndex(t, firstIndex, scope)
	t.Setenv("GIT_INDEX_FILE", firstIndex)
	first, err := fingerprintGateInputsAt(scope, "project-map:check", gatePlan{}, day)
	if err != nil {
		t.Fatalf("first isolated-index fingerprint: %v", err)
	}

	secondIndex := filepath.Join(t.TempDir(), "index")
	writeGateCacheIndex(t, secondIndex, scope)
	t.Setenv("GIT_INDEX_FILE", secondIndex)
	second, err := fingerprintGateInputsAt(scope, "project-map:check", gatePlan{}, day)
	if err != nil {
		t.Fatalf("second isolated-index fingerprint: %v", err)
	}
	if first != second {
		t.Fatal("equivalent isolated index paths produced different cache fingerprints")
	}
}

func TestProjectMapFingerprintIgnoresUntrackedFilesOutsideItsInputClosure(t *testing.T) {
	repo := t.TempDir()
	runGateCacheGit(t, repo, "init")
	tracked := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("tracked\n"), 0o600); err != nil {
		t.Fatalf("write tracked fixture: %v", err)
	}
	runGateCacheGit(t, repo, "add", "tracked.txt")
	scope := runGateCacheGit(t, repo, "write-tree")
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("get current directory: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("enter fixture repo: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	day := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	first, err := fingerprintGateInputsAt(scope, "project-map:check", gatePlan{}, day)
	if err != nil {
		t.Fatalf("initial fingerprint: %v", err)
	}
	if err := os.WriteFile("untracked.tmp", []byte("large unrelated input\n"), 0o600); err != nil {
		t.Fatalf("write untracked fixture: %v", err)
	}
	second, err := fingerprintGateInputsAt(scope, "project-map:check", gatePlan{}, day)
	if err != nil {
		t.Fatalf("fingerprint with untracked file: %v", err)
	}
	if first != second {
		t.Fatal("untracked file outside project-map input closure invalidated cache fingerprint")
	}
}

func TestFingerprintGateInputsComparesWorktreeToExplicitScope(t *testing.T) {
	repo := t.TempDir()
	runGateCacheGit(t, repo, "init")
	path := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(path, []byte("scope content\n"), 0o600); err != nil {
		t.Fatalf("write scoped file: %v", err)
	}
	runGateCacheGit(t, repo, "add", "tracked.txt")
	scope := runGateCacheGit(t, repo, "write-tree")

	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("get current directory: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("enter fixture repo: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("restore current directory: %v", err)
		}
	})
	first, err := fingerprintGateInputs(scope, "diff:whitespace", gatePlan{})
	if err != nil {
		t.Fatalf("initial fingerprint: %v", err)
	}
	if err := os.WriteFile("tracked.txt", []byte("active index content\n"), 0o600); err != nil {
		t.Fatalf("change tracked file: %v", err)
	}
	runGateCacheGit(t, repo, "add", "tracked.txt")
	second, err := fingerprintGateInputs(scope, "diff:whitespace", gatePlan{})
	if err != nil {
		t.Fatalf("changed active index fingerprint: %v", err)
	}
	if first == second {
		t.Fatal("worktree change relative to explicit scope did not invalidate fingerprint")
	}
}

func TestProjectMapFingerprintIncludesActiveIndex(t *testing.T) {
	repo := t.TempDir()
	runGateCacheGit(t, repo, "init")
	path := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(path, []byte("scope content\n"), 0o600); err != nil {
		t.Fatalf("write scoped file: %v", err)
	}
	runGateCacheGit(t, repo, "add", "tracked.txt")
	scope := runGateCacheGit(t, repo, "write-tree")

	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("get current directory: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("enter fixture repo: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("restore current directory: %v", err)
		}
	})
	dayOne := time.Date(2026, 7, 15, 23, 59, 0, 0, time.UTC)
	first, err := fingerprintGateInputsAt(scope, "project-map:check", gatePlan{}, dayOne)
	if err != nil {
		t.Fatalf("initial project-map fingerprint: %v", err)
	}
	if err := os.WriteFile("tracked.txt", []byte("active index content\n"), 0o600); err != nil {
		t.Fatalf("change active index input: %v", err)
	}
	runGateCacheGit(t, repo, "add", "tracked.txt")
	if err := os.WriteFile("tracked.txt", []byte("scope content\n"), 0o600); err != nil {
		t.Fatalf("restore worktree to scope: %v", err)
	}
	second, err := fingerprintGateInputsAt(scope, "project-map:check", gatePlan{}, dayOne)
	if err != nil {
		t.Fatalf("active-index project-map fingerprint: %v", err)
	}
	if first == second {
		t.Fatal("active index change did not invalidate project-map fingerprint")
	}
}

func TestProjectMapFingerprintIgnoresUTCDayButIncludesTrackedInputs(t *testing.T) {
	repo := t.TempDir()
	runGateCacheGit(t, repo, "init")
	path := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(path, []byte("initial project map input\n"), 0o600); err != nil {
		t.Fatalf("write initial project map input: %v", err)
	}
	runGateCacheGit(t, repo, "add", "tracked.txt")
	scope := runGateCacheGit(t, repo, "write-tree")

	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("get current directory: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("enter fixture repo: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("restore current directory: %v", err)
		}
	})
	dayOne := time.Date(2026, 7, 15, 23, 59, 0, 0, time.UTC)
	first := projectMapFingerprintAt(t, scope, dayOne, "initial")
	dayTwo := dayOne.Add(2 * time.Minute)
	second := projectMapFingerprintAt(t, scope, dayTwo, "next-day")
	if first != second {
		t.Fatal("UTC day change invalidated project-map fingerprint for identical inputs")
	}

	if err := os.WriteFile(path, []byte("changed project map input\n"), 0o600); err != nil {
		t.Fatalf("change project map input: %v", err)
	}
	runGateCacheGit(t, repo, "add", "tracked.txt")
	third := projectMapFingerprintAt(t, scope, dayTwo, "changed-input")
	if first == third {
		t.Fatal("real project-map input change did not invalidate fingerprint")
	}
}

func projectMapFingerprintAt(t *testing.T, scope string, at time.Time, description string) string {
	t.Helper()
	fingerprint, err := fingerprintGateInputsAt(scope, "project-map:check", gatePlan{}, at)
	if err != nil {
		t.Fatalf("%s project-map fingerprint: %v", description, err)
	}
	return fingerprint
}

func TestNewGateResultCacheRejectsInvalidScope(t *testing.T) {
	if _, err := newGateResultCache(t.TempDir(), time.Minute, "not-a-git-object"); err == nil {
		t.Fatal("invalid staged tree scope was accepted")
	}
	if _, err := newGateResultCache(t.TempDir(), time.Minute, strings.Repeat("0", 40)); err == nil {
		t.Fatal("missing staged tree object was accepted")
	}
	commit := runGateCacheGit(t, ".", "rev-parse", "HEAD")
	if _, err := newGateResultCache(t.TempDir(), time.Minute, commit); err == nil || !strings.Contains(err.Error(), "must be a Git tree") {
		t.Fatalf("commit object scope error = %v, want Git tree rejection", err)
	}
}

func TestNewGateResultCacheRejectsRealOrMismatchedIndex(t *testing.T) {
	scope := runGateCacheGit(t, ".", "write-tree")
	realGitDir := runGateCacheGit(t, ".", "rev-parse", "--absolute-git-dir")
	t.Setenv("GIT_INDEX_FILE", filepath.Join(realGitDir, "index"))
	if _, err := newGateResultCache(t.TempDir(), time.Minute, scope); err == nil || !strings.Contains(err.Error(), "isolated") {
		t.Fatalf("real cache index error = %v", err)
	}

	indexDir := t.TempDir()
	indexPath := filepath.Join(indexDir, "index")
	writeGateCacheIndex(t, indexPath, scope)
	t.Setenv("GIT_INDEX_FILE", indexPath)
	otherScope := writeEmptyGateCacheTree(t, ".")
	if _, err := newGateResultCache(t.TempDir(), time.Minute, otherScope); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched cache index error = %v", err)
	}
}

func newTestGateResultCache(t *testing.T, fingerprint gateFingerprinter) *gateResultCache {
	t.Helper()
	scope := runGateCacheGit(t, ".", "rev-parse", "HEAD^{tree}")
	useImmutableGateCacheIndex(t, scope)
	cache, err := newGateResultCache(t.TempDir(), time.Hour, scope)
	if err != nil {
		t.Fatalf("new gate result cache: %v", err)
	}
	cache.fingerprint = fingerprint
	return cache
}

func useImmutableGateCacheIndex(t *testing.T, scope string) {
	t.Helper()
	indexDir := filepath.Join(t.TempDir(), "immutable-index")
	if err := os.Mkdir(indexDir, 0o700); err != nil {
		t.Fatalf("mkdir immutable index: %v", err)
	}
	indexPath := filepath.Join(indexDir, "index")
	writeGateCacheIndex(t, indexPath, scope)
	t.Setenv("GIT_INDEX_FILE", indexPath)
}

func writeGateCacheIndex(t *testing.T, indexPath, scope string) {
	t.Helper()
	command := exec.Command("git", "read-tree", scope)
	command.Env = append(os.Environ(), "GIT_INDEX_FILE="+indexPath)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("write gate cache index: %v\n%s", err, out)
	}
}

func runGateCacheGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeEmptyGateCacheTree(t *testing.T, dir string) string {
	t.Helper()
	command := exec.Command("git", "mktree")
	command.Dir = dir
	command.Stdin = strings.NewReader("")
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git mktree: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func fixedGateFingerprint(char string) gateFingerprinter {
	return func(string, gatePlan) (string, error) {
		return strings.Repeat(char, 64), nil
	}
}
