package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
		"if (Test-AllArgsAreGoFiles -Args $GuardArgs)",
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

type codeSizeGuardCLIResult struct {
	exitCode int
	stdout   string
	stderr   string
}

func runCodeSizeGuardCLI(t *testing.T, goFile string) codeSizeGuardCLIResult {
	t.Helper()
	cmd := exec.Command("go", "run", "./code_size_guard.go", "--", goFile)
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
