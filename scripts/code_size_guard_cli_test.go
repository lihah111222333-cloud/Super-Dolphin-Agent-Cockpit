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

func TestGuardRaceOnlyModeRunsGuardsAndOneRaceInvocation(t *testing.T) {
	result := runTestWithGuardFakeGo(t, "--race-only", "./internal/devtools/gate")
	if result.err != nil {
		t.Fatalf("race-only guard failed: %v: %s", result.err, result.output)
	}
	for _, required := range []string{
		"run ./scripts/code_size_guard.go",
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

func TestCanonicalBackendModeExcludesOnlyExactArchtestPackage(t *testing.T) {
	result := runTestWithGuardFakeGoWithListOutput(t, strings.Join([]string{
		"example.test/cmd/control",
		"example.test/internal/archtest",
		"example.test/internal/archtest/child",
		"example.test/pkg/api",
		"example.test/scripts/tool",
	}, "\n"), "--canonical-backend", "./cmd/...", "./internal/...", "./pkg/...", "./scripts/...")
	if result.err != nil {
		t.Fatalf("canonical backend guard failed: %v: %s", result.err, result.output)
	}
	if !strings.Contains(result.invocations, "list ./cmd/... ./internal/... ./pkg/... ./scripts/...") {
		t.Fatalf("canonical backend mode did not resolve the complete package target set:\n%s", result.invocations)
	}
	if !strings.Contains(result.invocations, "test ./internal/archtest -count=1") {
		t.Fatalf("canonical backend mode omitted the full archtest guard:\n%s", result.invocations)
	}
	want := "test example.test/cmd/control example.test/internal/archtest/child example.test/pkg/api example.test/scripts/tool -count=1 -timeout=180s"
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

func TestTestWithGuardProductionDockerE2ETimeoutContract(t *testing.T) {
	normalHook := captureTestWithGuardGoTestInvocation(
		t,
		"./cmd/super-dolphin-gate",
		"-run", "^TestProductionProvisionBootstrapOwnerHookDockerE2E$",
		"-count=1",
	)
	if !strings.Contains(normalHook, "-timeout=15m") || strings.Contains(normalHook, "-timeout=40m") {
		t.Fatalf("normal Docker E2E go test invocation = %q, want only -timeout=15m", normalHook)
	}

	release := captureTestWithGuardGoTestInvocation(
		t,
		"./cmd/super-dolphin-gate",
		"-run", "^TestProductionProvisionBootstrapOwnerReleaseCLIDockerE2E$",
		"-count=1",
	)
	if !strings.Contains(release, "-timeout=40m") {
		t.Fatalf("release Docker E2E go test invocation = %q, want -timeout=40m", release)
	}

	ordinary := captureTestWithGuardGoTestInvocation(
		t,
		"./cmd/super-dolphin-gate",
		"-run", "^TestProductionProvisionExecutionDeadlineObservation$",
		"-count=1",
	)
	if strings.Contains(ordinary, "-timeout") {
		t.Fatalf("ordinary go test invocation unexpectedly changed timeout: %q", ordinary)
	}

	nearMiss := captureTestWithGuardGoTestInvocation(
		t, "./cmd/super-dolphin-gate",
		"-run", "^TestProductionProvisionBootstrapOwnerHookDockerE2EExtra$",
		"-count=1",
	)
	if strings.Contains(nearMiss, "-timeout") {
		t.Fatalf("near-miss Docker test unexpectedly received a wrapper timeout: %q", nearMiss)
	}

	for _, target := range []string{
		"^TestProductionProvisionBootstrapOwnerHookDockerE2E$",
		"^TestProductionProvisionBootstrapOwnerReleaseCLIDockerE2E$",
	} {
		blocked := runTestWithGuardFakeGo(
			t, "./cmd/super-dolphin-gate", "-run", target, "-count=1", "-timeout=10m",
		)
		if blocked.err == nil || !strings.Contains(blocked.output, "timeout is wrapper-owned") {
			t.Fatalf("explicit timeout for %s error = %v, output = %q", target, blocked.err, blocked.output)
		}
		if strings.Contains(blocked.invocations, "test ./cmd/super-dolphin-gate ") {
			t.Fatalf("target %s executed despite conflicting timeout:\n%s", target, blocked.invocations)
		}
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

	result := runTestWithGuardCLI(t, path)
	if result.exitCode != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", result.exitCode, result.stdout, result.stderr)
	}
	if result.stdout != "" {
		t.Fatalf("single-file wrapper should not write stdout, got:\n%s", result.stdout)
	}
	if !strings.Contains(result.stderr, "bad_identifier_with_too_many_underscores") {
		t.Fatalf("stderr missing concrete violation, got:\n%s", result.stderr)
	}
	if strings.Contains(result.stderr, "exit status") {
		t.Fatalf("stderr should filter go run status noise, got:\n%s", result.stderr)
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
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.exitCode = exitErr.ExitCode()
		return result
	}
	t.Fatalf("run code_size_guard.go: %v", err)
	return result
}

func runTestWithGuardCLI(t *testing.T, goFile string) codeSizeGuardCLIResult {
	t.Helper()
	cmd := exec.Command("bash", "scripts/test_with_guard.sh", filepath.ToSlash(goFile))
	cmd.Dir = ".."
	if realGo, err := exec.LookPath("go"); err == nil {
		env := upsertEnv(os.Environ(), "REAL_GO_BIN", bashAbsolutePath(realGo))
		env = upsertEnv(env, "PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
		cmd.Env = appendWSLEnvKeysWithGitWorktree(t, env, "REAL_GO_BIN", "PATH")
	}
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
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.exitCode = exitErr.ExitCode()
		return result
	}
	t.Fatalf("run test_with_guard.sh: %v", err)
	return result
}

func captureTestWithGuardGoTestInvocation(t *testing.T, args ...string) string {
	t.Helper()
	result := runTestWithGuardFakeGo(t, args...)
	if result.err != nil {
		t.Fatalf("run test_with_guard.sh with fake go: %v: %s", result.err, result.output)
	}
	for invocation := range strings.SplitSeq(strings.TrimSpace(result.invocations), "\n") {
		if strings.Contains(invocation, "test ./cmd/super-dolphin-gate ") &&
			strings.Contains(invocation, args[2]) {
			return invocation
		}
	}
	t.Fatalf("target go test invocation not found in log:\n%s", result.invocations)
	return ""
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
		"printf '%s ' \"$@\" >> \"$FAKE_GO_LOG\"\n" +
		"printf '\\n' >> \"$FAKE_GO_LOG\"\n" +
		"if [[ \"$1\" == list ]]; then printf '%s\\n' \"$FAKE_GO_LIST_OUTPUT\"; fi\n"
	if err := os.WriteFile(fakeGo, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
	cmd := exec.Command("bash", append([]string{"scripts/test_with_guard.sh"}, args...)...)
	cmd.Dir = ".."
	environment := upsertEnv(os.Environ(), "REAL_GO_BIN", bashAbsolutePath(fakeGo))
	environment = upsertEnv(environment, "FAKE_GO_LOG", bashAbsolutePath(logPath))
	environment = upsertEnv(environment, "FAKE_GO_LIST_OUTPUT", listOutput)
	environment = upsertEnv(environment, "SUPER_DOLPHIN_GATE_PRODUCTION_DOCKER_E2E", "1")
	cmd.Env = appendWSLEnvKeysWithGitWorktree(
		t, environment, "REAL_GO_BIN", "FAKE_GO_LOG", "FAKE_GO_LIST_OUTPUT", "SUPER_DOLPHIN_GATE_PRODUCTION_DOCKER_E2E",
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
