package gate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestStandaloneReleaseAttestationFailsBeforeWorkspaceExecution(t *testing.T) {
	var output bytes.Buffer
	config := executorConfig{
		sourcePath: "unreachable-source", workRoot: "unreachable-work", searchPath: "unreachable-path",
		runtimeSeedRoot: "unreachable-seed", runtimeSeedManifest: "unreachable-manifest",
		goRoot:               "unreachable-go-root",
		goBuildCacheSeedRoot: "unreachable-cache-seed",
		expectedUID:          os.Geteuid(), stdout: &output, stderr: &output,
		nowFunc: time.Now,
	}
	err := executeProgram(context.Background(), config, GateIDReleaseLayeredCheck,
		ExecutorPrograms()[GateIDReleaseLayeredCheck])
	if err == nil || !strings.Contains(err.Error(), "requires canonical prerequisites from the plan executor") {
		t.Fatalf("standalone release attestation error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("standalone release attestation executed workspace output: %q", output.String())
	}
}

func TestExecuteExecutorRoutesShardToPlanExecutor(t *testing.T) {
	var output bytes.Buffer
	err := ExecuteExecutor(context.Background(), []string{
		"run-shard", "--profile", "invalid", "--plan-digest", "sha256:" + strings.Repeat("a", 64),
		"--gates", string(GateIDWhitespaceCheck),
	}, &output, &output)
	if err == nil || !strings.Contains(err.Error(), "unsupported gate profile") {
		t.Fatalf("run-shard dispatch error = %v", err)
	}
}

func TestExecutorNonGoProgramDoesNotRequireGoBuildCacheSeed(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{"clean.txt": "clean\n"})
	config := newTestExecutorConfig(t, source)
	config.goBuildCacheSeedRoots = nil
	config.goBuildCacheSeedRoot = ""

	if err := executeProgram(context.Background(), config, GateIDWhitespaceCheck, ExecutorPrograms()[GateIDWhitespaceCheck]); err != nil {
		t.Fatalf("execute non-Go gate without Go build cache seed: %v", err)
	}
	assertDirectoryEmpty(t, config.workRoot)
}

func TestExecutorGoBuildCacheProxyCommandPreservesSeedOrder(t *testing.T) {
	command, err := executorGoBuildCacheProxyCommand("proxy", []string{"/seed/newest", "/seed/oldest"}, "/private")
	if err != nil {
		t.Fatal(err)
	}
	if command != "proxy --seed \"/seed/newest\" --seed \"/seed/oldest\" --private \"/private\"" {
		t.Fatalf("Go build cache proxy command = %q", command)
	}
}

func TestWhitespaceGateRejectsChangedTrailingWhitespace(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{"changed.txt": "clean\n"})
	runGit(t, source, "update-ref", baseSourceRef, "HEAD")
	writeTestFile(t, filepath.Join(source, "changed.txt"), "trailing  \n", 0o600)
	commitExecutorSnapshot(t, source, "introduce trailing whitespace")
	assertWhitespaceGateFails(t, source, "trusted-range whitespace check")
}

func TestWhitespaceGateIgnoresUnchangedLegacyWhitespace(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{"legacy.txt": "trailing  \n"})
	runGit(t, source, "update-ref", baseSourceRef, "HEAD")
	writeTestFile(t, filepath.Join(source, "changed.txt"), "clean\n", 0o600)
	commitExecutorSnapshot(t, source, "clean change")
	config := newTestExecutorConfig(t, source)
	if err := executeProgram(context.Background(), config, GateIDWhitespaceCheck, ExecutorPrograms()[GateIDWhitespaceCheck]); err != nil {
		t.Fatalf("execute trusted-range whitespace gate: %v", err)
	}
	assertDirectoryEmpty(t, config.workRoot)
}

func TestWhitespaceGateMissingBaseScansWholeTree(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{"legacy.txt": "trailing  \n"})
	assertWhitespaceGateFails(t, source, "trusted-range whitespace check")
}

func TestWhitespaceGateRejectsNonCommitBase(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{"clean.txt": "clean\n"})
	runGit(t, source, "update-ref", baseSourceRef, "HEAD^{tree}")
	assertWhitespaceGateFails(t, source, "resolve trusted whitespace base commit")
}

func assertWhitespaceGateFails(t *testing.T, source string, want string) {
	t.Helper()
	config := newTestExecutorConfig(t, source)
	err := executeProgram(context.Background(), config, GateIDWhitespaceCheck, ExecutorPrograms()[GateIDWhitespaceCheck])
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("whitespace gate error = %v, want containing %q", err, want)
	}
	assertDirectoryEmpty(t, config.workRoot)
}

func TestExecutorSharesGoModuleCacheAcrossWorktreesAndIsolatesBuildCache(t *testing.T) {
	script := "#!/bin/sh\nset -eu\ntest -d \"$GOMODCACHE\"\ntest ! -L \"$GOMODCACHE\"\ntest -L \"$GOMODCACHE/github.com\"\ntest -d \"$GOMODCACHE/cache/download\"\ntest ! -L \"$GOMODCACHE/cache/download\"\ntest \"$GOPROXY\" = off\ntest \"$(cat \"$GOMODCACHE/github.com/kelindar/event@v1.5.2/event.go\")\" = 'package event'\ntest \"$(cat \"$GOMODCACHE/cache/download/github.com/kelindar/event/@v/list\")\" = v1.5.2\nprintf private > \"$GOMODCACHE/cache/download/current-run\"\nif ! go_output=$(go list -mod=mod -deps ./... 2>&1); then printf '%s\\n' \"$go_output\" >&2; exit 18; fi\ncase \"$go_output\" in *'go: downloading'*) printf '%s\\n' \"$go_output\" >&2; exit 19;; esac\nprintf '%s\\n' \"$go_output\" | grep -qx github.com/kelindar/event\nreadlink \"$GOMODCACHE/github.com\"\nprintf updated > \"$GOCACHE/current-run\"\n"
	files := map[string]string{
		"cache.sh":                       script,
		"go.mod":                         "module example.com/cache-fixture\n\ngo 1.22\n\nrequire github.com/kelindar/event v1.5.2\n",
		"go.sum":                         "",
		"main.go":                        "package cachefixture\n\nimport _ \"github.com/kelindar/event\"\n",
		"frontend-app/package-lock.json": "{\"lockfileVersion\":3}\n",
	}
	firstSource := newExecutorGitSnapshot(t, files)
	if err := os.Chmod(filepath.Join(firstSource, "cache.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	runtimeRoot, manifestPath := writeRuntimeSeedFixture(t, firstSource)
	commitExecutorSnapshot(t, firstSource, "shared module cache fixture")
	files["build/gate/runtime-proxy/go.sum"] = runtimeProxyFixtureSum
	secondSource := newExecutorGitSnapshot(t, files)
	if err := os.Chmod(filepath.Join(secondSource, "cache.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	commitExecutorSnapshot(t, secondSource, "make shared cache fixture executable")
	program := ExecutorProgram{
		Strategy:      ExecutorStrategyCommands,
		Steps:         []ExecutorStep{{Argv: []string{"./cache.sh"}}},
		RequiredPaths: []string{"cache.sh"},
		NeedsGoSeed:   true,
	}
	moduleCacheRoot := filepath.Join(runtimeRoot, "go-mod-cache")
	makeRuntimeSeedTreeReadOnly(t, moduleCacheRoot)
	rewriteRuntimeGoModuleCacheDigest(t, manifestPath, moduleCacheRoot)
	beforeDigest := mustRuntimeSeedTreeDigest(t, moduleCacheRoot)
	for index, source := range []string{firstSource, secondSource} {
		var output, stderr bytes.Buffer
		config := newTestExecutorConfig(t, source)
		config.runtimeSeedRoot = runtimeRoot
		config.runtimeSeedManifest = manifestPath
		config.stdout = &output
		config.stderr = &stderr
		if err := executeProgram(context.Background(), config, GateIDBackendTestWithGuard, program); err != nil {
			t.Fatalf("execute shared module cache fixture for worktree %d: %v\n%s", index, err, stderr.String())
		}
		moduleSourceRoot := filepath.Join(moduleCacheRoot, "github.com")
		if strings.TrimSpace(output.String()) != moduleSourceRoot {
			t.Fatalf("worktree %d module cache target = %q, want %q", index, output.String(), moduleSourceRoot)
		}
		assertDirectoryEmpty(t, config.workRoot)
	}
	content, err := os.ReadFile(filepath.Join(moduleCacheRoot, "github.com", "kelindar", "event@v1.5.2", "event.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "package event\n" {
		t.Fatalf("shared module cache was mutated: %q", content)
	}
	if _, err := os.Stat(filepath.Join(moduleCacheRoot, "current-run")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private build cache write reached shared module cache: %v", err)
	}
	if afterDigest := mustRuntimeSeedTreeDigest(t, moduleCacheRoot); afterDigest != beforeDigest {
		t.Fatalf("shared module cache changed across worktrees: %s != %s", afterDigest, beforeDigest)
	}
}

func makeRuntimeSeedTreeReadOnly(t *testing.T, root string) {
	t.Helper()
	var directories []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			directories = append(directories, path)
			return os.Chmod(path, 0o555)
		}
		return os.Chmod(path, 0o444)
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, directory := range slices.Backward(directories) {
			_ = os.Chmod(directory, 0o700)
		}
	})
}

func rewriteRuntimeGoModuleCacheDigest(t *testing.T, manifestPath string, moduleCacheRoot string) {
	t.Helper()
	manifest, err := LoadRuntimeSeedManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.GoModCacheTreeSHA256 = mustRuntimeSeedTreeDigest(t, moduleCacheRoot)
	var encoded bytes.Buffer
	if err := EncodeRuntimeSeedManifest(&encoded, manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestExecutorSequentialGatesDoNotRetainPriorCache(t *testing.T) {
	script := "#!/bin/sh\nset -eu\ntest ! -e \"$GOCACHE/prewarmed\"\ntest ! -e \"$GOCACHE/previous-gate\"\nprintf data > \"$GOCACHE/previous-gate\"\n"
	source := newExecutorGitSnapshot(t, map[string]string{"cache.sh": script})
	if err := os.Chmod(filepath.Join(source, "cache.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	commitExecutorSnapshot(t, source, "sequential cache fixture")
	config := newTestExecutorConfig(t, source)
	writeTestFile(t, filepath.Join(config.goBuildCacheSeedRoot, "prewarmed"), "runner-cache\n", 0o600)
	program := ExecutorProgram{
		Strategy:      ExecutorStrategyCommands,
		Steps:         []ExecutorStep{{Argv: []string{"./cache.sh"}}},
		RequiredPaths: []string{"cache.sh"},
	}
	for run := range 2 {
		if err := executeProgram(context.Background(), config, GateIDBackendTestWithGuard, program); err != nil {
			t.Fatalf("sequential gate run %d: %v", run, err)
		}
		assertDirectoryEmpty(t, config.workRoot)
	}
}

func TestExecutorUsesSharedGoBuildCacheWithoutCopyingSeed(t *testing.T) {
	script := "#!/bin/sh\nset -eu\ntest ! -e \"$GOCACHE/prewarmed\"\ntest -n \"$GOCACHEPROG\"\nprintf updated > \"$GOCACHE/current-run\"\n"
	source := newExecutorGitSnapshot(t, map[string]string{
		"cache.sh":                       script,
		"go.sum":                         "module sum\n",
		"frontend-app/package-lock.json": "{\"lockfileVersion\":3}\n",
	})
	if err := os.Chmod(filepath.Join(source, "cache.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	runtimeRoot, manifestPath := writeRuntimeSeedFixture(t, source)
	commitExecutorSnapshot(t, source, "runner cache seed fixture")
	config := newTestExecutorConfig(t, source)
	config.runtimeSeedRoot = runtimeRoot
	config.runtimeSeedManifest = manifestPath
	config.goBuildCacheRoot = realTempDir(t)
	writeTestFile(t, filepath.Join(config.goBuildCacheSeedRoot, "prewarmed"), "runner-cache\n", 0o600)
	program := ExecutorProgram{
		Strategy:      ExecutorStrategyCommands,
		Steps:         []ExecutorStep{{Argv: []string{"./cache.sh"}}},
		RequiredPaths: []string{"cache.sh"},
		NeedsGoSeed:   true,
	}
	if err := executeProgram(context.Background(), config, GateIDBackendTestWithGuard, program); err != nil {
		t.Fatalf("execute seeded cache fixture: %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.goBuildCacheSeedRoot, "current-run")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runner cache seed was mutated: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(config.goBuildCacheRoot, "current-run")); err != nil || string(content) != "updated" {
		t.Fatalf("private cache write = %q, %v", content, err)
	}
	assertDirectoryEmpty(t, config.workRoot)
}

func TestRaceProgramRunsBoundedBackendOnlyInEachFreshExecutorWorkspace(t *testing.T) {
	guardScript := "#!/bin/sh\nset -eu\ntest \"$1\" = --with-race\nsaw_separator=0\nsaw_normal=0\nfor arg in \"$@\"; do\n  test \"$arg\" = -- && saw_separator=1\n  test \"$arg\" = ./... && saw_normal=1\ndone\ntest \"$saw_separator\" = 1\ntest \"$saw_normal\" = 1\ntest \"$GOFLAGS\" = '-p=4'\ntest \"$GOMAXPROCS\" = 4\ntest \"$GOMEMLIMIT\" = 6GiB\ntest ! -e frontend-app/node_modules\ntest \"$(cat cmd/agent-terminal/web-dist/index.html)\" = \"immutable embed\"\n"
	source := newExecutorGitSnapshot(t, map[string]string{
		".gitignore":                         "cmd/agent-terminal/web-dist/\n",
		"cmd/agent-terminal/frontend.go":     "package main\n",
		"go.sum":                             "module sum\n",
		"scripts/check_nested_go_modules.sh": "#!/bin/sh\nexit 0\n",
		"scripts/test_with_guard.sh":         guardScript,
	})
	if err := os.Chmod(filepath.Join(source, "scripts", "test_with_guard.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	commitExecutorSnapshot(t, source, "race backend workspace fixture")

	program := ExecutorPrograms()[GateIDBackendTestGuardWithRace]
	program.NeedsGoSeed = false

	config := newTestExecutorConfig(t, source)
	embedRoot := filepath.Join(filepath.Dir(config.workRoot), "frontend-embed")
	if err := os.Mkdir(embedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(embedRoot, "index.html"), "immutable embed\n", 0o600)
	config.frontendEmbedSeedRoot = embedRoot
	for run := range 2 {
		if err := executeProgram(context.Background(), config, GateIDBackendTestGuardWithRace, program); err != nil {
			t.Fatalf("execute fresh race workspace %d: %v", run, err)
		}
		assertDirectoryEmpty(t, config.workRoot)
	}
}

func TestExecutorWorkspaceCopiesCompleteGitSnapshotAndPreservesModes(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{"script.sh": "#!/bin/sh\nexit 0\n"})
	if err := os.Chmod(filepath.Join(source, "script.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	commitExecutorSnapshot(t, source, "executable")
	config := newTestExecutorConfig(t, source)
	layout, err := prepareExecutorWorkspace(config)
	if err != nil {
		t.Fatalf("prepareExecutorWorkspace: %v", err)
	}
	t.Cleanup(func() { _ = cleanupExecutorWorkspace(layout) })
	requireExecutorDirectory(t, filepath.Join(layout.sourceCopy, ".git"), 0)
	requireExecutorDirectory(t, filepath.Join(layout.home, ".codex"), 0o700)
	runGit(t, layout.sourceCopy, "rev-parse", "--verify", materializedSourceRef+"^{commit}")
	sourceInfo := mustExecutorFileInfo(t, filepath.Join(source, "script.sh"))
	copyInfo := mustExecutorFileInfo(t, filepath.Join(layout.sourceCopy, "script.sh"))
	if copyInfo.Mode().Perm() != sourceInfo.Mode().Perm() {
		t.Fatalf("copied mode = %o, want %o", copyInfo.Mode().Perm(), sourceInfo.Mode().Perm())
	}
}

func requireExecutorDirectory(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		t.Fatalf("executor directory %q is unavailable: info=%v err=%v", path, info, err)
	}
	if mode != 0 && info.Mode().Perm() != mode {
		t.Fatalf("executor directory %q mode = %o, want %o", path, info.Mode().Perm(), mode)
	}
}

func mustExecutorFileInfo(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestExecutorRejectsGitSnapshotTamper(t *testing.T) {
	tests := []struct {
		name   string
		tamper func(*testing.T, string)
	}{
		{
			name: "materialized ref mismatch",
			tamper: func(t *testing.T, source string) {
				writeTestFile(t, filepath.Join(source, "second.txt"), "second\n", 0o600)
				commitExecutorSnapshot(t, source, "second")
				runGit(t, source, "update-ref", materializedSourceRef, "HEAD^")
			},
		},
		{
			name: "symbolic HEAD",
			tamper: func(t *testing.T, source string) {
				runGit(t, source, "switch", "-q", "-c", "tampered")
			},
		},
		{
			name: "dirty snapshot",
			tamper: func(t *testing.T, source string) {
				writeTestFile(t, filepath.Join(source, "untracked.txt"), "dirty\n", 0o600)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := newExecutorGitSnapshot(t, map[string]string{"clean.txt": "clean\n"})
			test.tamper(t, source)
			config := newTestExecutorConfig(t, source)
			if err := executeProgram(context.Background(), config, GateIDWhitespaceCheck, ExecutorPrograms()[GateIDWhitespaceCheck]); err == nil {
				t.Fatal("executor unexpectedly accepted a tampered Git snapshot")
			}
			assertDirectoryEmpty(t, config.workRoot)
		})
	}
}

func TestValidateCopiedSnapshotReportsBoundedDirtyStatus(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{"clean.txt": "clean\n"})
	runGit(t, source, "update-ref", materializedSourceRef, "HEAD")
	for index := range 128 {
		writeTestFile(t, filepath.Join(source, fmt.Sprintf("dirty-%04d-xxxxxxxxxxxxxxxxxxxxxxxx.txt", index)), "dirty\n", 0o600)
	}

	err := validateCopiedSnapshot(context.Background(), "git", source, []string{"GATE_SECRET=must-not-appear"})
	if err == nil {
		t.Fatal("validateCopiedSnapshot unexpectedly accepted a dirty snapshot")
	}
	message := err.Error()
	if !strings.Contains(message, "?? dirty-0000-xxxxxxxxxxxxxxxxxxxxxxxx.txt") {
		t.Fatalf("dirty status diagnostic does not identify a file: %q", message)
	}
	if !strings.Contains(message, "truncated after 4096 bytes") {
		t.Fatalf("dirty status diagnostic is not bounded: %q", message)
	}
	if strings.Contains(message, "must-not-appear") {
		t.Fatalf("dirty status diagnostic leaked environment value: %q", message)
	}
}

func TestRepositoryIgnoresFrontendRuntimeSeedSymlink(t *testing.T) {
	ignore, err := os.ReadFile(filepath.Join("..", "..", "..", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains("\n"+string(ignore), "\n/frontend-app/node_modules\n") {
		t.Fatal("repository .gitignore does not cover the frontend runtime seed symlink")
	}
	source := newExecutorGitSnapshot(t, map[string]string{
		".gitignore":                     string(ignore),
		"frontend-app/package-lock.json": "{}\n",
	})
	runGit(t, source, "update-ref", materializedSourceRef, "HEAD")
	if err := os.Symlink(t.TempDir(), filepath.Join(source, "frontend-app", "node_modules")); err != nil {
		t.Fatal(err)
	}
	if err := validateCopiedSnapshot(context.Background(), "git", source, nil); err != nil {
		t.Fatalf("frontend runtime seed link dirtied copied snapshot: %v", err)
	}
}

func TestExecutorFailsClosedOnMissingCommandAndRequiredPath(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{"clean.txt": "clean\n"})
	tests := []struct {
		name    string
		program ExecutorProgram
	}{
		{name: "command", program: commandProgram([]string{"definitely-missing-executor-command"})},
		{name: "required path", program: requirePaths(commandProgram([]string{"git", "--version"}), "scripts/missing-gate.sh")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := newTestExecutorConfig(t, source)
			if err := executeProgram(context.Background(), config, GateID("test:missing"), test.program); err == nil {
				t.Fatal("executor unexpectedly accepted a missing runtime dependency")
			}
			assertDirectoryEmpty(t, config.workRoot)
		})
	}
}

func TestExecutorWorkspaceRejectsSymlinksAndDirtyWorkRoot(t *testing.T) {
	t.Run("source root symlink", func(t *testing.T) {
		source := newExecutorGitSnapshot(t, map[string]string{"clean.txt": "clean\n"})
		linkParent := realTempDir(t)
		link := filepath.Join(linkParent, "source-link")
		if err := os.Symlink(source, link); err != nil {
			t.Fatal(err)
		}
		config := newTestExecutorConfig(t, source)
		config.sourcePath = link
		if _, err := prepareExecutorWorkspace(config); err == nil || !strings.Contains(err.Error(), "real directory") {
			t.Fatalf("prepareExecutorWorkspace source-root symlink error = %v", err)
		}
	})

	t.Run("source symlink entry", func(t *testing.T) {
		source := newExecutorGitSnapshot(t, map[string]string{"clean.txt": "clean\n"})
		if err := os.Symlink("clean.txt", filepath.Join(source, "link.txt")); err != nil {
			t.Fatal(err)
		}
		config := newTestExecutorConfig(t, source)
		if _, err := prepareExecutorWorkspace(config); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("prepareExecutorWorkspace symlink error = %v", err)
		}
	})

	t.Run("dirty work root", func(t *testing.T) {
		source := newExecutorGitSnapshot(t, map[string]string{"clean.txt": "clean\n"})
		config := newTestExecutorConfig(t, source)
		writeTestFile(t, filepath.Join(config.workRoot, "unexpected"), "x", 0o600)
		if _, err := prepareExecutorWorkspace(config); err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("prepareExecutorWorkspace dirty-root error = %v", err)
		}
	})
}

func TestExecutorDoesNotInheritHostEnvironmentAndPropagatesExitCode(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{"clean.txt": "clean\n"})
	config := newTestExecutorConfig(t, source)
	bin := realTempDir(t)
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required")
	}
	if err := os.Symlink(gitPath, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	for _, dependency := range []string{"basename", "uname"} {
		dependencyPath, err := exec.LookPath(dependency)
		if err != nil {
			t.Skipf("%s is required by portable git wrappers", dependency)
		}
		if err := os.Symlink(dependencyPath, filepath.Join(bin, dependency)); err != nil {
			t.Fatal(err)
		}
	}
	probe := fmt.Sprintf("#!/bin/sh\n[ -z \"${LEAK_ME+x}\" ] || exit 9\n[ \"$PATH\" = %q ] || exit 8\nexit 7\n", bin)
	writeTestFile(t, filepath.Join(bin, "probe"), probe, 0o700)
	config.searchPath = bin
	t.Setenv("LEAK_ME", "must-not-leak")
	program := commandProgram([]string{"probe"})
	err = executeProgram(context.Background(), config, GateID("test:probe"), program)
	if code := ExecutorExitCode(err); code != 7 {
		t.Fatalf("executor exit code = %d, want 7; err=%v", code, err)
	}
}

func TestExecutorStepPassesOnlyAllowedResourceBounds(t *testing.T) {
	tests := []struct {
		name       string
		step       func([]string) ExecutorStep
		goFlags    string
		goMaxProc  string
		goMemLimit string
	}{
		{name: "normal backend", step: normalGoExecutorStep, goFlags: "-p=4", goMaxProc: "4", goMemLimit: "6GiB"},
		{name: "release race", step: raceGoExecutorStep, goFlags: "-p=4", goMaxProc: "4", goMemLimit: "6GiB"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe := fmt.Sprintf("#!/bin/sh\n[ \"$GOFLAGS\" = %q ] || exit 8\n[ \"$GOMAXPROCS\" = %q ] || exit 9\n[ \"$GOMEMLIMIT\" = %q ] || exit 10\n[ -z \"${LEAK_ME+x}\" ] || exit 11\n", test.goFlags, test.goMaxProc, test.goMemLimit)
			source := newExecutorGitSnapshot(t, map[string]string{"probe": probe})
			if err := os.Chmod(filepath.Join(source, "probe"), 0o700); err != nil {
				t.Fatal(err)
			}
			commitExecutorSnapshot(t, source, "resource-bound probe")
			config := newTestExecutorConfig(t, source)
			t.Setenv("LEAK_ME", "must-not-leak")
			program := ExecutorProgram{
				Strategy: ExecutorStrategyCommands,
				Steps:    []ExecutorStep{test.step([]string{"./probe"})},
			}
			if err := executeProgram(context.Background(), config, GateID("test:resource-bounds"), program); err != nil {
				t.Fatalf("execute resource-bound probe: %v", err)
			}
		})
	}
}

func TestExecutorStepEnvironmentFailsClosed(t *testing.T) {
	tests := []struct {
		name        string
		environment []string
	}{
		{name: "incomplete normal profile", environment: []string{"GOFLAGS=-p=1"}},
		{name: "incomplete race profile", environment: []string{"GOMAXPROCS=2"}},
		{name: "mixed profiles", environment: []string{"GOFLAGS=-p=2", "GOMAXPROCS=3"}},
		{name: "unregistered package parallelism", environment: []string{"GOFLAGS=-p=3", "GOMAXPROCS=3"}},
		{name: "unregistered runtime parallelism", environment: []string{"GOFLAGS=-p=1", "GOMAXPROCS=5"}},
		{name: "unrelated override", environment: []string{"DOCKER_HOST="}},
		{name: "duplicate", environment: []string{"GOFLAGS=-p=1", "GOFLAGS=-p=1"}},
		{name: "malformed", environment: []string{"GOFLAGS"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			program := commandProgram([]string{"go", "version"})
			program.Steps[0].Environment = test.environment
			if err := validateExecutorProgram(program); err == nil {
				t.Fatalf("validateExecutorProgram accepted environment %q", test.environment)
			}
		})
	}
	for _, environment := range []string{"GOFLAGS=-trimpath", "GOMAXPROCS=4", "GOMEMLIMIT=9GiB"} {
		if _, err := mergeExecutorStepEnvironment([]string{environment}, raceGoExecutorStep([]string{"go"}).Environment); err == nil {
			t.Fatalf("step environment overrode executor-owned key %q", environment)
		}
	}
}

func TestExecutorRunsCommandFromWritableSourceCopy(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{"probe": "#!/bin/sh\npwd\n"})
	if err := os.Chmod(filepath.Join(source, "probe"), 0o700); err != nil {
		t.Fatal(err)
	}
	commitExecutorSnapshot(t, source, "executable probe")
	config := newTestExecutorConfig(t, source)
	var stdout bytes.Buffer
	config.stdout = &stdout
	if err := executeProgram(context.Background(), config, GateID("test:cwd"), commandProgram([]string{"./probe"})); err != nil {
		t.Fatalf("execute cwd probe: %v", err)
	}
	workingDirectory := strings.TrimSpace(stdout.String())
	if workingDirectory == source || !strings.HasSuffix(workingDirectory, filepath.Join("run", "source")) {
		t.Fatalf("command cwd = %q, want isolated writable source copy", workingDirectory)
	}
	assertDirectoryEmpty(t, config.workRoot)
}

func TestTrustedChangedDiagnosticsUsesSnapshotBaseAndFiltersUnsupportedFiles(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{"base.go": "package base\n"})
	runGit(t, source, "update-ref", baseSourceRef, "HEAD")
	writeTestFile(t, filepath.Join(source, "internal", "changed.go"), "package changed\n", 0o600)
	writeTestFile(t, filepath.Join(source, "internal", "notes.md"), "notes\n", 0o600)
	writeTestFile(t, filepath.Join(source, "frontend-app", "asset.png"), "png\n", 0o600)
	commitExecutorSnapshot(t, source, "changed")
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required")
	}
	selection, err := trustedChangedDiagnostics(context.Background(), gitPath, source, os.Environ())
	if err != nil {
		t.Fatalf("trustedChangedDiagnostics: %v", err)
	}
	if !slices.Equal(selection.files, []string{"internal/changed.go"}) {
		t.Fatalf("changed files = %v, want [internal/changed.go]", selection.files)
	}
	if selection.unsupported != 2 {
		t.Fatalf("unsupported files = %d, want 2", selection.unsupported)
	}
}

func TestTrustedChangedDiagnosticsDeletionOnlyIsLegalSkip(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{"internal/deleted.go": "package deleted\n"})
	runGit(t, source, "update-ref", baseSourceRef, "HEAD")
	if err := os.Remove(filepath.Join(source, "internal", "deleted.go")); err != nil {
		t.Fatal(err)
	}
	commitExecutorSnapshot(t, source, "delete")
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required")
	}
	selection, err := trustedChangedDiagnostics(context.Background(), gitPath, source, os.Environ())
	if err != nil {
		t.Fatalf("trustedChangedDiagnostics: %v", err)
	}
	if len(selection.files) != 0 || selection.deleted != 1 {
		t.Fatalf("deletion selection = %+v, want one deleted and no live files", selection)
	}
}

func TestRunChangedDiagnosticsDeletionOnlySkipsBeforeToolResolution(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{"internal/deleted.go": "package deleted\n"})
	runGit(t, source, "update-ref", baseSourceRef, "HEAD")
	if err := os.Remove(filepath.Join(source, "internal", "deleted.go")); err != nil {
		t.Fatal(err)
	}
	commitExecutorSnapshot(t, source, "delete")
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required")
	}
	var stderr bytes.Buffer
	err = runChangedDiagnostics(context.Background(), gitPath, source, os.Environ(), filepath.Join(source, "missing-bin"), ioDiscard{}, &stderr)
	if err != nil {
		t.Fatalf("runChangedDiagnostics deletion-only skip: %v", err)
	}
	if !strings.Contains(stderr.String(), "deleted=1") {
		t.Fatalf("skip audit = %q, want deleted=1", stderr.String())
	}
}

func TestRunChangedDiagnosticsEmptyTrustedRangeIsLegalSkip(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{"internal/unchanged.go": "package unchanged\n"})
	runGit(t, source, "update-ref", baseSourceRef, "HEAD")
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required")
	}
	var stderr bytes.Buffer
	err = runChangedDiagnostics(context.Background(), gitPath, source, os.Environ(), filepath.Join(source, "missing-bin"), ioDiscard{}, &stderr)
	if err != nil {
		t.Fatalf("runChangedDiagnostics empty trusted range: %v", err)
	}
	if !strings.Contains(stderr.String(), "candidates=0") {
		t.Fatalf("skip audit = %q, want candidates=0", stderr.String())
	}
}

func TestTrustedChangedDiagnosticsInitialCommitFiltersFullTree(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{
		"internal/kept.go":  "package kept\n",
		"internal/notes.md": "notes\n",
		"config.json":       "{}\n",
	})
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required")
	}
	selection, err := trustedChangedDiagnostics(context.Background(), gitPath, source, os.Environ())
	if err != nil {
		t.Fatalf("trustedChangedDiagnostics initial commit: %v", err)
	}
	if !slices.Equal(selection.files, []string{"internal/kept.go"}) {
		t.Fatalf("initial diagnostic files = %v, want [internal/kept.go]", selection.files)
	}
	if selection.unsupported != 2 {
		t.Fatalf("initial unsupported = %d, want 2", selection.unsupported)
	}
}

func TestLSPDiagnosticsEligibilityMatchesMaintenanceSourceBoundary(t *testing.T) {
	for _, path := range []string{
		"cmd/tool/main.go", "internal/tool/main.ts", "pkg/tool/main.js", "frontend-app/src/App.tsx",
		"frontend-app/src/style.css", "scripts/check.go", "internal/store/query.sql",
	} {
		if !lspDiagnosticsEligible(path) {
			t.Errorf("supported source %q was omitted", path)
		}
	}
	for _, path := range []string{
		"README.md", "internal/notes.md", "internal/image.png", "internal/config.json", "scripts/check.ts", "docs/example.go",
	} {
		if lspDiagnosticsEligible(path) {
			t.Errorf("unsupported path %q was included", path)
		}
	}
}

func newTestExecutorConfig(t *testing.T, source string) executorConfig {
	t.Helper()
	root := realTempDir(t)
	workRoot := filepath.Join(root, "work")
	runtimeRoot := filepath.Join(root, "runtime")
	goBuildCacheSeedRoot := filepath.Join(root, "go-build-cache-seed")
	for _, directory := range []string{workRoot, runtimeRoot, goBuildCacheSeedRoot} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(runtimeRoot, "go-mod-cache", "example.org", "shared@v1.0.0", "module.go"), "shared-module\n", 0o444)
	return executorConfig{
		sourcePath: source, workRoot: workRoot, searchPath: executorSearchPath,
		expectedUID: os.Geteuid(), requireReadOnlySource: false,
		runtimeSeedRoot: runtimeRoot, runtimeSeedManifest: filepath.Join(runtimeRoot, "manifest.json"),
		goRoot:                testGoRoot(t),
		goBuildCacheSeedRoots: []string{goBuildCacheSeedRoot},
		goBuildCacheSeedRoot:  goBuildCacheSeedRoot,
		goBuildCacheProxy:     testGoBuildCacheProxyLauncher(),
		stdout:                ioDiscard{}, stderr: ioDiscard{},
		nowFunc: time.Now,
	}
}

func testGoRoot(t *testing.T) string {
	t.Helper()
	binary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(resolved))
	if info, err := os.Stat(filepath.Join(root, "src")); err != nil || !info.IsDir() {
		t.Fatalf("resolved Go root %q has no source tree", root)
	}
	return root
}

type ioDiscard struct{}

func (ioDiscard) Write(data []byte) (int, error) { return len(data), nil }

func newExecutorGitSnapshot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := realTempDir(t)
	runGit(t, root, "init", "-q")
	for path, content := range files {
		writeTestFile(t, filepath.Join(root, path), content, 0o600)
	}
	commitExecutorSnapshot(t, root, "initial")
	runGit(t, root, "checkout", "-q", "--detach", "HEAD")
	return root
}

func commitExecutorSnapshot(t *testing.T, root string, message string) {
	t.Helper()
	runGit(t, root, "add", "--all")
	runGit(t, root, "-c", "user.name=executor-test", "-c", "user.email=executor@example.invalid", "commit", "-q", "-m", message)
	runGit(t, root, "update-ref", materializedSourceRef, "HEAD")
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func realTempDir(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(resolved, 0o700); err != nil {
		t.Fatal(err)
	}
	return resolved
}

func writeTestFile(t *testing.T, path string, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func assertDirectoryEmpty(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory %s is not empty: %v", path, entries)
	}
}

func TestExecutorExitCodeDefaultsToFailure(t *testing.T) {
	if code := ExecutorExitCode(errors.New("validation")); code != 1 {
		t.Fatalf("validation exit code = %d, want 1", code)
	}
	if code := ExecutorExitCode(nil); code != 0 {
		t.Fatalf("success exit code = %d, want 0", code)
	}
}
