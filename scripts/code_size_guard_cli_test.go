package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardRaceOnlyModeUsesShortTestScope(t *testing.T) {
	body := readScript(t, "test_with_guard.sh")
	assertScriptContains(t, body, `run_go_test "$real_go" "$@" -race -short -count=1`)
}

func TestHostTestWrappersRequireRemoteAdmission(t *testing.T) {
	for _, command := range [][]string{
		{"scripts/test_with_guard.sh", "--ci-package-test", "./internal/devtools/gate", "TestBoundary"},
		{"scripts/test_with_guard.sh", "--ci-compile-package", "./internal/devtools/gate"},
		{"scripts/go_with_guard.sh", "test", "./internal/devtools/gate", "-run", "^TestBoundary$"},
	} {
		cmd := exec.Command("bash", command...)
		cmd.Dir = ".."
		environment := upsertEnv(os.Environ(), "SUPER_DOLPHIN_TEST_BACKEND", "")
		cmd.Env = appendWSLEnvKeysWithGitWorktree(t, environment, "SUPER_DOLPHIN_TEST_BACKEND")
		output, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("%s unexpectedly admitted an unfiltered host test", command[0])
		}
		if !strings.Contains(string(output), "super-dolphin-gate test") {
			t.Fatalf("%s rejection omitted the trusted test entrypoint:\n%s", command[0], output)
		}
	}
}

func TestCICompilePackageModeCompilesWithoutRunning(t *testing.T) {
	result := runTestWithGuardFakeGoWithListOutput(t, "example.test/scripts", "--ci-compile-package", "./scripts")
	if result.err != nil {
		t.Fatalf("compile-only package mode failed: %v: %s", result.err, result.output)
	}
	for _, required := range []string{"list ./scripts", "test -c -o ", " ./scripts"} {
		if !strings.Contains(result.invocations, required) {
			t.Fatalf("compile-only package mode omitted %q:\n%s", required, result.invocations)
		}
	}
	if strings.Contains(result.invocations, "-run") || strings.Contains(result.invocations, "-json") {
		t.Fatalf("compile-only package mode executed a test runner:\n%s", result.invocations)
	}
}

func TestCIPackageModeAllowsExactScriptsPackageForRemoteWorker(t *testing.T) {
	result := runTestWithGuardFakeGoWithListOutput(
		t,
		"example.test/scripts",
		"--ci-package-test",
		"./scripts",
		"TestHostTestWrappersRequireRemoteAdmission",
	)
	if result.err != nil {
		t.Fatalf("exact scripts package was rejected: %v: %s", result.err, result.output)
	}
	for _, required := range []string{
		"list ./scripts",
		"test ./scripts -json -run ^TestHostTestWrappersRequireRemoteAdmission$ -count=1 -timeout=0",
	} {
		if !strings.Contains(result.invocations, required) {
			t.Fatalf("exact scripts package omitted %q:\n%s", required, result.invocations)
		}
	}
}

func TestCIPackageModesDelegateTimeoutToWorker(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		args     []string
		required string
	}{
		{
			name:     "package",
			args:     []string{"--ci-package", "./scripts"},
			required: "test ./scripts -json -count=1 -timeout=0",
		},
		{
			name:     "race test",
			args:     []string{"--ci-race-package-test", "./scripts", "TestBoundary"},
			required: "test ./scripts -json -run ^TestBoundary$ -race -short -count=1 -timeout=0",
		},
		{
			name:     "benchmark",
			args:     []string{"--ci-package-benchmark", "./scripts", "BenchmarkBoundary"},
			required: "test ./scripts -json -run ^$ -bench ^BenchmarkBoundary$ -count=1 -timeout=0",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result := runTestWithGuardFakeGoWithListOutput(t, "example.test/scripts", testCase.args...)
			if result.err != nil {
				t.Fatalf("CI package mode failed: %v: %s", result.err, result.output)
			}
			if !strings.Contains(result.invocations, testCase.required) {
				t.Fatalf("CI package mode did not delegate timeout to the worker %q:\n%s", testCase.required, result.invocations)
			}
		})
	}
}

func TestCIPackageModeAcceptsAnyCanonicalPackageResolvedByGoList(t *testing.T) {
	for _, target := range []string{"./build/gate", "./build/gate/closure", "./new-root/tool"} {
		result := runTestWithGuardFakeGoWithListOutput(t, "example.test/"+strings.TrimPrefix(target, "./"), "--ci-package", target)
		if result.err != nil {
			t.Fatalf("exact root-module package %q was rejected: %v: %s", target, result.err, result.output)
		}
		for _, required := range []string{"list " + target, "test " + target + " -json -count=1 -timeout=0"} {
			if !strings.Contains(result.invocations, required) {
				t.Fatalf("exact root-module package %q omitted %q:\n%s", target, required, result.invocations)
			}
		}
	}
	t.Setenv("FAKE_GO_FAIL_PATTERN", "list ./build/gate/runtime-tools")
	result := runTestWithGuardFakeGoWithListOutput(t, "example.test/build/gate/runtime-tools", "--ci-package", "./build/gate/runtime-tools")
	if result.err == nil || !strings.Contains(result.output, "CI package mode failed to resolve exactly one package") {
		t.Fatalf("package rejected by root-module go list was admitted: err=%v output=%q", result.err, result.output)
	}
}

func TestGuardRaceOnlyModeRunsGuardsAndOneRaceInvocation(t *testing.T) {
	result := runTestWithGuardFakeGo(t, "--race-only", "./internal/devtools/gate")
	if result.err != nil {
		t.Fatalf("race-only guard failed: %v: %s", result.err, result.output)
	}
	for _, required := range []string{
		"test ./internal/archtest -count=1",
		"vet -copylocks ./internal/provider/... ./internal/platform/... ./internal/module/thread/...",
		"list ./...",
	} {
		if !strings.Contains(result.invocations, required) {
			t.Errorf("race-only mode omitted guard invocation %q:\n%s", required, result.invocations)
		}
	}
	target := "test ./internal/devtools/gate -race -short -count=1"
	if strings.Count(result.invocations, "test ./internal/devtools/gate") != 1 || !strings.Contains(result.invocations, target) {
		t.Fatalf("race-only target invocation is not unique or canonical:\n%s", result.invocations)
	}
}

func TestGuardCombinedRaceModeRunsGuardsOnceAndBothTestLanes(t *testing.T) {
	result := runTestWithGuardFakeGo(
		t,
		"--with-race", "./internal/provider/codexapp",
		"--", "./internal/devtools/gate", "-count=1",
	)
	if result.err != nil {
		t.Fatalf("combined race guard failed: %v: %s", result.err, result.output)
	}
	for _, invocation := range []string{
		"test ./internal/archtest -count=1",
		"vet -copylocks ./internal/provider/... ./internal/platform/... ./internal/module/thread/...",
		"test ./internal/devtools/gate -count=1",
		"test ./internal/provider/codexapp -race -short -count=1 -timeout=180s",
	} {
		if strings.Count(result.invocations, invocation) != 1 {
			t.Fatalf("combined race mode invocation %q is not unique:\n%s", invocation, result.invocations)
		}
	}
	if !strings.Contains(result.invocations, "list ./...") {
		t.Fatalf("combined race mode omitted nested-module discovery:\n%s", result.invocations)
	}
}

func TestCanonicalBackendModeExcludesOnlyExactArchtestPackage(t *testing.T) {
	result := runTestWithGuardFakeGoWithListOutput(t, strings.Join([]string{
		"example.test/build/gate/closure",
		"example.test/cmd/control",
		"example.test/internal/archtest",
		"example.test/internal/archtest/child",
		"example.test/new-root/tool",
		"example.test/pkg/api",
		"example.test/scripts/tool",
	}, "\n"), "--canonical-backend", "./...")
	if result.err != nil {
		t.Fatalf("canonical backend guard failed: %v: %s", result.err, result.output)
	}
	if !strings.Contains(result.invocations, "list ./...") {
		t.Fatalf("canonical backend mode did not resolve the complete package target set:\n%s", result.invocations)
	}
	if !strings.Contains(result.invocations, "test ./internal/archtest -count=1") {
		t.Fatalf("canonical backend mode omitted the full archtest guard:\n%s", result.invocations)
	}
	want := "test example.test/build/gate/closure example.test/cmd/control example.test/internal/archtest/child example.test/new-root/tool example.test/pkg/api example.test/scripts/tool -count=1 -timeout=180s"
	if !strings.Contains(result.invocations, want) {
		t.Fatalf("canonical backend package set missing %q:\n%s", want, result.invocations)
	}
	if strings.Contains(result.invocations, "test example.test/internal/archtest ") {
		t.Fatalf("canonical backend mode reran exact archtest package:\n%s", result.invocations)
	}
}

func TestCanonicalBackendModeAllowsArchtestOnlyBecauseGuardAlreadyCoveredIt(t *testing.T) {
	result := runTestWithGuardFakeGoWithListOutput(t, "example.test/internal/archtest", "--canonical-backend", "./internal/archtest")
	if result.err != nil {
		t.Fatalf("archtest-only canonical backend guard failed: %v: %s", result.err, result.output)
	}
	if !strings.Contains(result.invocations, "test ./internal/archtest -count=1") {
		t.Fatalf("archtest-only canonical backend mode omitted the guard coverage:\n%s", result.invocations)
	}
	if strings.Contains(result.invocations, "test example.test/internal/archtest ") {
		t.Fatalf("archtest-only canonical backend mode repeated the covered package:\n%s", result.invocations)
	}
}

func TestCanonicalBackendModeFailsClosedForInvalidTargetsAndEmptyResolution(t *testing.T) {
	for _, target := range []string{"-count=1", "./internal/../cmd/...", "./internal/..hidden", "./internal/arch test"} {
		t.Run(target, func(t *testing.T) {
			invalid := runTestWithGuardFakeGo(t, "--canonical-backend", "./internal/...", target)
			if invalid.err == nil || !strings.Contains(invalid.output, "rejects non-backend package target") {
				t.Fatalf("invalid canonical target result = %v, output = %q", invalid.err, invalid.output)
			}
			if invalid.invocations != "" {
				t.Fatalf("invalid canonical target invoked go before rejection:\n%s", invalid.invocations)
			}
		})
	}

	empty := runTestWithGuardFakeGoWithListOutput(t, "", "--canonical-backend", "./internal/...")
	if empty.err == nil || !strings.Contains(empty.output, "resolved no packages") {
		t.Fatalf("empty canonical package resolution result = %v, output = %q", empty.err, empty.output)
	}
	if !strings.Contains(empty.invocations, "list ./internal/...") || strings.Contains(empty.invocations, "test ./internal/archtest -count=1") {
		t.Fatalf("empty canonical package resolution did not fail before guards:\n%s", empty.invocations)
	}
}

func TestCanonicalBackendModePropagatesParallelLaneFailure(t *testing.T) {
	t.Setenv("FAKE_GO_FAIL_PATTERN", "test example.test/cmd/control")
	result := runTestWithGuardFakeGoWithListOutput(t, "example.test/cmd/control", "--canonical-backend", "./cmd/...")
	if result.err == nil {
		t.Fatalf("canonical backend mode accepted a failed package-test lane:\n%s", result.invocations)
	}
	if !strings.Contains(result.output, "canonical backend lanes failed: guard=0 test=7") {
		t.Fatalf("canonical backend failure did not preserve lane status: %q", result.output)
	}
}

func TestCodeSizeGuardSingleGoFileIsQuietWhenClean(t *testing.T) {
	path := writeGuardFixture(t, `package sample

func ok() {}
`)

	result := runCodeSizeGuardCLI(t, path)
	if result.exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", result.exitCode, result.stdout, result.stderr)
	}
	if result.stdout != "" || result.stderr != "" {
		t.Fatalf("single clean Go file should produce no output\nstdout:\n%s\nstderr:\n%s", result.stdout, result.stderr)
	}
}

func TestCodeSizeGuardSingleGoFileReportsOnlyViolations(t *testing.T) {
	path := writeGuardFixture(t, `package sample

func bad_identifier_with_too_many_underscores() {}
`)

	result := runCodeSizeGuardCLI(t, path)
	if result.exitCode != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", result.exitCode, result.stdout, result.stderr)
	}
	if result.stdout != "" {
		t.Fatalf("single-file violation mode should not write stdout, got:\n%s", result.stdout)
	}
	if !strings.Contains(result.stderr, "bad_identifier_with_too_many_underscores") {
		t.Fatalf("stderr missing concrete violation, got:\n%s", result.stderr)
	}
	for _, noisy := range []string{"代码守卫", "baseline", "文件≤", "全部通过"} {
		if strings.Contains(result.stderr, noisy) {
			t.Fatalf("stderr contains noisy summary %q:\n%s", noisy, result.stderr)
		}
	}
}

func TestCodeSizeGuardFreezeRequiresCompleteApproval(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "missing approval", args: []string{"--freeze"}, wantErr: "invalid freeze approval"},
		{name: "approval without freeze", args: []string{"--freeze-owner", "owner"}, wantErr: "require --freeze"},
		{name: "missing flag value", args: []string{"--freeze", "--freeze-owner"}, wantErr: "acceptance.owner"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := runCodeSizeGuardArgs(t, test.args...)
			if result.exitCode != 1 || !strings.Contains(result.stderr, test.wantErr) {
				t.Fatalf("code_size_guard %v exit=%d stderr=%q, want failure containing %q",
					test.args, result.exitCode, result.stderr, test.wantErr)
			}
		})
	}
}

func TestCodeSizeGuardRejectsConflictingModesAndHonorsTerminator(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "conflicting modes", args: []string{"--freeze", "--strict"}, wantErr: "conflicting mode"},
		{name: "duplicate mode", args: []string{"--strict", "--strict"}, wantErr: "duplicate mode"},
		{name: "flag after terminator is file", args: []string{"--", "--strict"}, wantErr: "expected Go file path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := runCodeSizeGuardArgs(t, test.args...)
			if result.exitCode != 1 || !strings.Contains(result.stderr, test.wantErr) {
				t.Fatalf("code_size_guard %v exit=%d stderr=%q, want failure containing %q",
					test.args, result.exitCode, result.stderr, test.wantErr)
			}
		})
	}
}

func TestCodeSizeGuardFreezeExposesAllApprovalFlags(t *testing.T) {
	guard := readScript(t, "code_size_guard.go")
	for _, flag := range []string{
		"--freeze-owner", "--freeze-reason", "--freeze-reviewed-at", "--freeze-review-by", "--freeze-fail-first",
	} {
		assertScriptContains(t, guard, flag)
	}
}

func TestTestWithGuardSingleGoFileWrapperFiltersGoRunExitStatus(t *testing.T) {
	path := writeGuardFixture(t, `package sample

func bad_identifier_with_too_many_underscores() {}
`)
	fakeGo := filepath.Join(t.TempDir(), "fake-go")
	fakeScript := "#!/usr/bin/env bash\n" +
		"if [[ \"$1\" == version ]]; then printf 'go version go1.26.5 linux/amd64\\n'; exit 0; fi\n" +
		"printf 'bad_identifier_with_too_many_underscores\\nexit status 1\\n' >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(fakeGo, []byte(fakeScript), 0o700); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
	cmd := exec.Command("bash", "scripts/test_with_guard.sh", filepath.ToSlash(path))
	cmd.Dir = ".."
	environment := upsertEnv(os.Environ(), "REAL_GO_BIN", bashAbsolutePath(fakeGo))
	environment = upsertEnv(environment, "SUPER_DOLPHIN_TEST_BACKEND", "remote-worker")
	cmd.Env = appendWSLEnvKeysWithGitWorktree(t, environment, "REAL_GO_BIN", "SUPER_DOLPHIN_TEST_BACKEND")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("exit error = %v, want exit code 1\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("single-file wrapper should not write stdout, got:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "bad_identifier_with_too_many_underscores") {
		t.Fatalf("stderr missing concrete violation, got:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "exit status") {
		t.Fatalf("stderr should filter go run status noise, got:\n%s", stderr.String())
	}
}

func TestTestWithGuardPowerShellWrapperMatchesBashContract(t *testing.T) {
	script := readScript(t, "test_with_guard.ps1")

	for _, want := range []string{
		"param(",
		"[Parameter(ValueFromRemainingArguments = $true)]",
		"[string[]]$GuardArgs",
		"$ErrorActionPreference = 'Stop'",
		"Set-StrictMode -Version Latest",
		"function Resolve-RealGo",
		"function Assert-RemoteTestExecution",
		"$env:SUPER_DOLPHIN_TEST_BACKEND -eq 'remote-worker'",
		"$env:REAL_GO_BIN",
		"Get-Command go -All",
		"function Invoke-RawGoTestGuard",
		"Makefile",
		".github/workflows",
		"go\\s+test",
		"function Invoke-Guard",
		"code_size_guard.go",
		"./internal/archtest",
		"function Test-AllArgsAreGoFiles",
		"function Invoke-SingleFileGuard",
		"[System.IO.Path]::GetFullPath",
		"exit $status",
		"if (Test-AllArgsAreGoFiles -Args $argsForRun)",
		"& $realGo test @GuardArgs",
	} {
		assertScriptContains(t, script, want)
	}
}

func TestTestWithGuardGuardOnlyRunsFullArchtest(t *testing.T) {
	for _, scriptName := range []string{"test_with_guard.sh", "test_with_guard.ps1"} {
		t.Run(scriptName, func(t *testing.T) {
			script := readScript(t, scriptName)
			assertScriptContains(t, script, "test ./internal/archtest -count=1")
			if strings.Contains(script, "-run TestCodeSizeGuard") {
				t.Fatalf("%s still narrows guard-only to TestCodeSizeGuard", scriptName)
			}
		})
	}
}

func TestTestWithGuardQuickModeSkipsHistoricalScansAndScopesCopylocks(t *testing.T) {
	bash := readScript(t, "test_with_guard.sh")
	for _, want := range []string{
		"--quick-guard",
		"TestDependencyDirection",
		"TestValidateDefaultBackendBoundaryGovernance",
		"TestBackendBoundaryRuleFactsHaveOneSource",
		"--archtest-only",
		"collect_copylocks_packages",
		"--race-only",
	} {
		assertScriptContains(t, bash, want)
	}
	assertScriptDoesNotContain(t, bash, `vet -copylocks ./internal/provider/... ./internal/platform/... ./internal/module/thread/...`)

	powershell := readScript(t, "test_with_guard.ps1")
	for _, want := range []string{"--quick-guard", "--archtest-only", "--race-only", "Get-CopylocksPackages", "[string[]]$TestArgs", "-TestArgs $argsForRun"} {
		assertScriptContains(t, powershell, want)
	}
	assertScriptDoesNotContain(t, powershell, `vet -copylocks ./internal/provider/... ./internal/platform/... ./internal/module/thread/...`)
	assertScriptDoesNotContain(t, powershell, `Invoke-GuardedTests -realGo $realGo -Args`)
}

type codeSizeGuardCLIResult struct {
	exitCode int
	stdout   string
	stderr   string
}

func runCodeSizeGuardCLI(t *testing.T, goFile string) codeSizeGuardCLIResult {
	t.Helper()
	return runCodeSizeGuardArgs(t, "--", goFile)
}

func runCodeSizeGuardArgs(t *testing.T, args ...string) codeSizeGuardCLIResult {
	t.Helper()
	cmdArgs := append([]string{"run", "./code_size_guard.go"}, args...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Dir = "."
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := codeSizeGuardCLIResult{
		exitCode: 0,
		stdout:   stdout.String(),
		stderr:   stderr.String(),
	}
	if err == nil {
		return result
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		result.exitCode = exitErr.ExitCode()
		return result
	}
	t.Fatalf("run code_size_guard.go: %v", err)
	return result
}

type testWithGuardFakeGoResult struct {
	invocations string
	output      string
	err         error
}

func runTestWithGuardFakeGo(t *testing.T, args ...string) testWithGuardFakeGoResult {
	t.Helper()
	return runTestWithGuardFakeGoWithListOutput(t, "example.test/internal/archtest", args...)
}

func runTestWithGuardFakeGoWithListOutput(t *testing.T, listOutput string, args ...string) testWithGuardFakeGoResult {
	t.Helper()
	root := t.TempDir()
	fakeGo := filepath.Join(root, "fake-go")
	logPath := filepath.Join(root, "go-invocations.log")
	script := "#!/usr/bin/env bash\n" +
		"if [[ \"$1\" == version ]]; then printf 'go version go1.26.5 linux/amd64\\n'; exit 0; fi\n" +
		"printf '%s ' \"$@\" >> \"$FAKE_GO_LOG\"\n" +
		"printf '\\n' >> \"$FAKE_GO_LOG\"\n" +
		"if [[ \"$1\" == list ]]; then printf '%s\\n' \"$FAKE_GO_LIST_OUTPUT\"; fi\n" +
		"if [[ -n \"${FAKE_GO_FAIL_PATTERN:-}\" && \"$*\" == *\"$FAKE_GO_FAIL_PATTERN\"* ]]; then exit 7; fi\n"
	if err := os.WriteFile(fakeGo, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
	cmd := exec.Command("bash", append([]string{"scripts/test_with_guard.sh"}, args...)...)
	cmd.Dir = ".."
	environment := upsertEnv(os.Environ(), "REAL_GO_BIN", bashAbsolutePath(fakeGo))
	environment = upsertEnv(environment, "FAKE_GO_LOG", bashAbsolutePath(logPath))
	environment = upsertEnv(environment, "FAKE_GO_LIST_OUTPUT", listOutput)
	environment = upsertEnv(environment, "FAKE_GO_FAIL_PATTERN", os.Getenv("FAKE_GO_FAIL_PATTERN"))
	environment = upsertEnv(environment, "SUPER_DOLPHIN_TEST_BACKEND", "remote-worker")
	cmd.Env = appendWSLEnvKeysWithGitWorktree(
		t, environment, "REAL_GO_BIN", "FAKE_GO_LOG", "FAKE_GO_LIST_OUTPUT", "FAKE_GO_FAIL_PATTERN",
		"SUPER_DOLPHIN_TEST_BACKEND",
	)
	output, runErr := cmd.CombinedOutput()
	data, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		data = nil
	} else if err != nil {
		t.Fatalf("read fake go log: %v", err)
	}
	return testWithGuardFakeGoResult{
		invocations: string(data),
		output:      strings.TrimSpace(string(output)),
		err:         runErr,
	}
}

func writeGuardFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.go")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestAgentDocsRequireSingleFileGuardAfterGoEdits(t *testing.T) {
	for _, path := range []string{"../CLAUDE.md"} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			body := readRepoFile(t, path)
			assertScriptContains(t, body, "./scripts/test_with_guard.sh <file.go>")
			assertScriptContains(t, body, "每改完一个 Go 文件")
			assertScriptContains(t, body, "0 表示无违规")
			assertScriptContains(t, body, "1 表示有违规")
		})
	}
}
