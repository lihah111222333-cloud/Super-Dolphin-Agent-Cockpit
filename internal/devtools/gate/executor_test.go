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
)

func TestStandaloneReleaseAttestationFailsBeforeWorkspaceExecution(t *testing.T) {
	var output bytes.Buffer
	config := executorConfig{
		sourcePath: "unreachable-source", workRoot: "unreachable-work", searchPath: "unreachable-path",
		runtimeSeedRoot: "unreachable-seed", runtimeSeedManifest: "unreachable-manifest",
		expectedUID: os.Geteuid(), stdout: &output, stderr: &output,
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

func TestExecutorCleansReadOnlyGoModuleCache(t *testing.T) {
	for _, test := range []struct {
		name     string
		exitLine string
		wantErr  bool
	}{
		{name: "success", exitLine: "exit 0"},
		{name: "gate failure remains primary", exitLine: "exit 23", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := "#!/bin/sh\nset -eu\nmkdir -p \"$GOMODCACHE/example\"\nprintf data > \"$GOMODCACHE/example/module.go\"\nchmod 0555 \"$GOMODCACHE/example\"\n" + test.exitLine + "\n"
			source := newExecutorGitSnapshot(t, map[string]string{"cache.sh": script})
			if err := os.Chmod(filepath.Join(source, "cache.sh"), 0o755); err != nil {
				t.Fatal(err)
			}
			commitExecutorSnapshot(t, source, "executable cache fixture")
			config := newTestExecutorConfig(t, source)
			program := ExecutorProgram{
				Strategy:      ExecutorStrategyCommands,
				Steps:         []ExecutorStep{{Argv: []string{"./cache.sh"}}},
				RequiredPaths: []string{"cache.sh"},
			}
			err := executeProgram(context.Background(), config, GateIDBackendTestWithGuard, program)
			if test.wantErr && (err == nil || !strings.Contains(err.Error(), "exit status 23")) {
				t.Fatalf("gate error = %v, want exit status 23", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("execute cache fixture: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "remove executor workspace") {
				t.Fatalf("cleanup error obscured gate result: %v", err)
			}
			assertDirectoryEmpty(t, config.workRoot)
		})
	}
}

func TestExecutorSequentialGatesDoNotRetainPriorCache(t *testing.T) {
	script := "#!/bin/sh\nset -eu\ntest ! -e \"$GOCACHE/previous-gate\"\nprintf data > \"$GOCACHE/previous-gate\"\n"
	source := newExecutorGitSnapshot(t, map[string]string{"cache.sh": script})
	if err := os.Chmod(filepath.Join(source, "cache.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	commitExecutorSnapshot(t, source, "sequential cache fixture")
	config := newTestExecutorConfig(t, source)
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

func TestRaceProgramRunsBoundedBackendOnlyInEachFreshExecutorWorkspace(t *testing.T) {
	guardScript := "#!/bin/sh\nset -eu\ntest \"$1\" = --with-race\nsaw_separator=0\nsaw_normal=0\nfor arg in \"$@\"; do\n  test \"$arg\" = -- && saw_separator=1\n  test \"$arg\" = ./cmd/... && saw_normal=1\ndone\ntest \"$saw_separator\" = 1\ntest \"$saw_normal\" = 1\ntest \"$GOFLAGS\" = -p=1\ntest \"$GOMAXPROCS\" = 1\ntest \"$GOMEMLIMIT\" = 1GiB\ntest ! -e frontend-app/node_modules\ntest \"$(cat cmd/agent-terminal/web-dist/index.html)\" = \"immutable embed\"\n"
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

func TestExecutorEnvironmentIsClosedAndWritable(t *testing.T) {
	layout := newExecutorLayout("/workspace/work")
	environment := executorEnvironment(layout, executorSearchPath)
	joined := "\n" + strings.Join(environment, "\n") + "\n"
	for _, want := range []string{
		"\nHOME=/workspace/work/home\n",
		"\nGIT_AUTHOR_NAME=Super Dolphin Gate Executor\n",
		"\nGIT_AUTHOR_EMAIL=gate-executor@super-dolphin.invalid\n",
		"\nGIT_AUTHOR_DATE=946684800 +0000\n",
		"\nGIT_COMMITTER_NAME=Super Dolphin Gate Executor\n",
		"\nGIT_COMMITTER_EMAIL=gate-executor@super-dolphin.invalid\n",
		"\nGIT_COMMITTER_DATE=946684800 +0000\n",
		"\nGOCACHE=/workspace/work/go-cache\n",
		"\nGOMODCACHE=/workspace/work/go-mod-cache\n",
		"\nGOPROXY=file:///opt/super-dolphin-gate/runtime/go-proxy\n",
		"\nGOTMPDIR=/workspace/work/tmp\n",
		"\nnpm_config_cache=/workspace/work/npm-cache\n",
		"\nPLAYWRIGHT_BROWSERS_PATH=/opt/super-dolphin-gate/runtime/frontend/node_modules/.cache/ms-playwright\n",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("executor environment missing %q", strings.TrimSpace(want))
		}
	}
	for _, forbidden := range []string{"\nGIT_CONFIG_GLOBAL=", "\nGOFLAGS=", "\nGOWORK="} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("executor environment leaks child-process policy %q", strings.TrimSpace(forbidden))
		}
	}
	if strings.Contains(joined, "SECRET") {
		t.Fatal("executor environment inherited an undeclared secret")
	}
	keys := environmentKeys(environment)
	if compacted := slices.Compact(slices.Clone(keys)); len(compacted) != len(keys) {
		t.Fatal("executor environment contains duplicate keys")
	}
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
		{name: "normal backend", step: normalGoExecutorStep, goFlags: "-p=2", goMaxProc: "2", goMemLimit: "1GiB"},
		{name: "release race", step: raceGoExecutorStep, goFlags: "-p=1", goMaxProc: "1", goMemLimit: "1GiB"},
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
	for _, directory := range []string{workRoot, runtimeRoot} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return executorConfig{
		sourcePath: source, workRoot: workRoot, searchPath: executorSearchPath,
		expectedUID: os.Geteuid(), requireReadOnlySource: false,
		runtimeSeedRoot: runtimeRoot, runtimeSeedManifest: filepath.Join(runtimeRoot, "manifest.json"),
		stdout: ioDiscard{}, stderr: ioDiscard{},
	}
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

func TestExecutorAuditOutputContainsNoHostEnvironmentValues(t *testing.T) {
	layout := newExecutorLayout("/workspace/work")
	var output bytes.Buffer
	fmt.Fprintf(&output, "%s", strings.Join(environmentKeys(executorEnvironment(layout, executorSearchPath)), ","))
	if strings.Contains(output.String(), os.Getenv("HOME")) {
		t.Fatal("audit output contains a host environment value")
	}
}
