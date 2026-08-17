package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	catalog "github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

func TestConfigureRunnerCommandKillsInheritedPipeProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix process-group inherited-pipe regression")
	}
	script := filepath.Join(t.TempDir(), "fork")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n/bin/sleep 2 &\nwait\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	command := exec.CommandContext(ctx, script)
	if err := configureRunnerCommand(command, []string{"PATH=/usr/bin:/bin"}); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err := runRunnerCommand(ctx, command)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("bounded runner command error=%v elapsed=%v", err, time.Since(started))
	}
}

func TestParseOptionsRequiresExplicitCandidateTree(t *testing.T) {
	if _, err := parseOptions([]string{"--id", "mcp-lsp-idle-quick"}); err == nil || !strings.Contains(err.Error(), "--tree is required") {
		t.Fatalf("parseOptions() error = %v, want explicit candidate tree", err)
	}
}

func TestParseOptionsRejectsDuplicateCandidateTree(t *testing.T) {
	_, err := parseOptions([]string{"--tree", strings.Repeat("a", 40), "--tree=" + strings.Repeat("b", 40), "--id", "mcp-lsp-idle-quick"})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("parseOptions() error = %v, want duplicate tree rejection", err)
	}
	_, err = parseOptions([]string{"--tree", "--tree=" + strings.Repeat("b", 40), "--id", "mcp-lsp-idle-quick"})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("parseOptions() mixed tree error = %v, want duplicate tree rejection", err)
	}
	_, err = parseOptions([]string{"-tree", strings.Repeat("a", 40), "--tree", strings.Repeat("b", 40), "--id", "mcp-lsp-idle-quick"})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("parseOptions() single-dash tree error = %v, want duplicate tree rejection", err)
	}
}

func TestValidateRunnerCommandRejectsUnownedShellInvocation(t *testing.T) {
	workload := catalog.Workload{ID: "mcp-lsp-mutated", Command: []string{"sh", "-c", "go test ./cmd/mcp-lsp"}}
	if err := validateRunnerCommand(t.TempDir(), strings.Repeat("a", 40), workload); err == nil || !strings.Contains(err.Error(), "catalog-owned go test command") {
		t.Fatalf("validateRunnerCommand() error = %v, want shell command rejection", err)
	}
}

func TestValidateRunnerPlatformViewRejectsExecutionMutation(t *testing.T) {
	canonical := catalog.Workload{
		ID: "mcp-lsp-idle-quick", RunnerTarget: "local-go-test", ProducerImplementationStatus: "missing",
		Platforms: []string{runtime.GOOS}, TimeoutSeconds: 240, Command: []string{"go", "test", "./canonical", "-run", "^TestCanonical$"},
	}
	execution := canonical
	execution.RunnerTarget = "platform-adapter"
	execution.TimeoutSeconds = 30
	execution.Command = []string{"go", "version"}
	if err := validateRunnerPlatformView(canonical, execution); err == nil {
		t.Fatal("validateRunnerPlatformView accepted command/timeout/target mutation")
	}
	if err := validateRunnerPlatformView(canonical, cloneRunnerWorkload(canonical)); err != nil {
		t.Fatalf("validateRunnerPlatformView rejected canonical clone: %v", err)
	}
}

func TestCanonicalRunnerEnvironmentRejectsGoSemanticOverrides(t *testing.T) {
	for _, entry := range []string{
		"GOFLAGS=-overlay=evil.json", "GOWORK=/tmp/evil.work", "GOENV=/tmp/evil.env", "GOTOOLCHAIN=auto", "CGO_ENABLED=1",
	} {
		if _, err := canonicalRunnerEnvironment([]string{"PATH=/usr/bin", entry}); err == nil || !strings.Contains(err.Error(), strings.SplitN(entry, "=", 2)[0]) {
			t.Fatalf("canonicalRunnerEnvironment(%q) error = %v", entry, err)
		}
	}
	for _, environment := range [][]string{
		{"PATH=/usr/bin", "PATH=/usr/bin"},
		{"PATH=/usr/bin", "PATH=/tmp/attacker"},
	} {
		if _, err := canonicalRunnerEnvironment(environment); err == nil || !strings.Contains(err.Error(), "duplicate PATH") {
			t.Fatalf("canonicalRunnerEnvironment(%v) error = %v, want duplicate rejection", environment, err)
		}
	}
	environment, err := canonicalRunnerEnvironment([]string{"PATH=/usr/bin", "UNRELATED=ignored"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(environment, "GOTOOLCHAIN=local") || slices.Contains(environment, "UNRELATED=ignored") {
		t.Fatalf("canonicalRunnerEnvironment() = %#v", environment)
	}
}

func TestCanonicalRunnerEnvironmentRejectsWindowsCaseInsensitiveConflicts(t *testing.T) {
	for _, environment := range [][]string{
		{"Path=C:\\Windows", "PATH=C:\\attacker"},
		{"PATH=C:\\Windows", "gotoolchain=auto"},
	} {
		if _, err := canonicalRunnerEnvironmentForPlatform("windows", environment); err == nil {
			t.Fatalf("canonicalRunnerEnvironmentForPlatform(windows, %v) accepted case-insensitive conflict", environment)
		}
	}
}

func TestVerifyRunnerExecutionSourceRejectsDirtyCandidate(t *testing.T) {
	root, tree := runnerExecutionFixture(t)
	if _, err := verifyRunnerExecutionSource(root, tree); err != nil {
		t.Fatalf("verifyRunnerExecutionSource() clean fixture error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyRunnerExecutionSource(root, tree); err == nil || !strings.Contains(err.Error(), "worktree is dirty") {
		t.Fatalf("verifyRunnerExecutionSource() error = %v, want dirty candidate rejection", err)
	}
}

func TestVerifyRunnerExecutionSourceRejectsIgnoredGoSource(t *testing.T) {
	root, tree := runnerExecutionFixture(t)
	if err := os.WriteFile(filepath.Join(root, "ignored.go"), []byte("package ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyRunnerExecutionSource(root, tree); err == nil || !strings.Contains(err.Error(), "ignored execution source") {
		t.Fatalf("verifyRunnerExecutionSource() error = %v, want ignored Go rejection", err)
	}
}

func TestVerifyRunnerExecutionSourceRejectsIgnoredRuntimeAsset(t *testing.T) {
	root, tree := runnerExecutionFixture(t)
	if err := os.WriteFile(filepath.Join(root, "ignored.asset"), []byte("mutable fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyRunnerExecutionSource(root, tree); err == nil || !strings.Contains(err.Error(), "ignored execution source") {
		t.Fatalf("verifyRunnerExecutionSource() error = %v, want ignored asset rejection", err)
	}
}

func TestVerifyRunnerExecutionSourceRejectsIgnoredEmbeddedCacheAsset(t *testing.T) {
	root, tree := runnerExecutionFixture(t)
	cacheRoot := filepath.Join(root, ".build-cache")
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheRoot, "embedded.txt"), []byte("mutable embedded input\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyRunnerExecutionSource(root, tree); err == nil || !strings.Contains(err.Error(), "ignored execution source") {
		t.Fatalf("verifyRunnerExecutionSource() error = %v, want ignored embedded cache rejection", err)
	}
}

func TestExecuteLocalHostCommandRejectsPostExecutionSourceMutation(t *testing.T) {
	for _, name := range []string{"CGO_ENABLED", "GOENV", "GOFLAGS", "GOMODCACHE", "GOPATH", "GOROOT", "GOTOOLCHAIN", "GOTMPDIR", "GOWORK"} {
		value, present := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
		if present {
			t.Cleanup(func() { _ = os.Setenv(name, value) })
		}
	}
	for _, test := range []struct {
		name    string
		command []string
	}{
		{name: "tracked", command: []string{"sh", "-c", "printf mutated > candidate.txt"}},
		{name: "untracked", command: []string{"sh", "-c", "printf new > untracked.txt"}},
		{name: "head", command: []string{"git", "commit", "--allow-empty", "--quiet", "-m", "执行漂移"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, tree := runnerExecutionFixture(t)
			headOutput, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
			if err != nil {
				t.Fatal(err)
			}
			err = executeLocalHostCommand(root, "fixture", test.command, strings.TrimSpace(string(headOutput)), tree)
			if err == nil || !strings.Contains(err.Error(), "runner execution source") && !strings.Contains(err.Error(), "runner HEAD changed") {
				t.Fatalf("executeLocalHostCommand() error = %v, want post-execution source rejection", err)
			}
		})
	}
}

func runnerExecutionFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{{"init", "--quiet"}, {"config", "user.name", "runner-test"}, {"config", "user.email", "runner-test@example.invalid"}} {
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "candidate.txt"), []byte("candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.go\nignored.asset\n.build-cache/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "embed.go"), []byte("package fixture\nimport _ \"embed\"\n//go:embed .build-cache/embedded.txt\nvar embedded string\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "candidate.txt", ".gitignore", "embed.go"}, {"commit", "--quiet", "-m", "候选"}} {
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	command := exec.Command("git", "-C", root, "rev-parse", "HEAD^{tree}")
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	return root, strings.TrimSpace(string(output))
}

func TestExecuteRunnerWorkloadRejectsCompletionReceiptAuthority(t *testing.T) {
	workload := catalog.Workload{ID: "mcp-lsp-default-15m"}
	err := executeRunnerWorkload(runnerOptions{completionReceipt: "/tmp/completion.json"}, "/tmp", catalog.Catalog{}, workload, workload, "")
	if err == nil || !strings.Contains(err.Error(), "cannot produce an authoritative receipt") {
		t.Fatalf("executeRunnerWorkload() error = %v", err)
	}
}

func TestExecuteRunnerWorkloadRejectsLocalPassReceipt(t *testing.T) {
	workload := catalog.Workload{ID: "mcp-lsp-native-process-tree"}
	err := executeRunnerWorkload(runnerOptions{receipt: "/tmp/receipt.json"}, "/tmp", catalog.Catalog{}, workload, workload, "")
	if err == nil || !strings.Contains(err.Error(), "cannot produce an authoritative receipt") {
		t.Fatalf("executeRunnerWorkload() error = %v", err)
	}
}

func TestValidateRunnerWorkloadClassifiesLocalAndRemoteProducerGates(t *testing.T) {
	for _, local := range []catalog.Workload{
		{ID: "mcp-lsp-idle-quick", ImplementationStatus: "implemented", ProducerImplementationStatus: "missing", RunnerTarget: "local-go-test", Platforms: []string{runtime.GOOS}, TriggerClass: "quick"},
		{ID: "mcp-lsp-native-process-tree", ImplementationStatus: "implemented", ProducerImplementationStatus: "missing", RunnerTarget: "local-go-test", Platforms: []string{runtime.GOOS}, TriggerClass: "native"},
	} {
		if err := validateRunnerWorkload(local); err != nil {
			t.Fatalf("validateRunnerWorkload(%q) error = %v, want local workload allowed", local.ID, err)
		}
	}
	local := catalog.Workload{ID: "mcp-lsp-idle-quick", ImplementationStatus: "implemented", ProducerImplementationStatus: "missing", RunnerTarget: "local-go-test", Platforms: []string{runtime.GOOS}, TriggerClass: "quick"}
	remote := local
	remote.ID = "remote-soak"
	remote.RunnerTarget = "remote-gate-test"
	remote.TriggerClass = "soak"
	if err := validateRunnerWorkload(remote); err == nil || !strings.Contains(err.Error(), "producer_implementation_status=missing") {
		t.Fatalf("validateRunnerWorkload(remote) error = %v, want producer N/V", err)
	}
}

func TestRunnerPlatformViewPreservesRemoteOnlyAndMissingAuthority(t *testing.T) {
	quick := catalog.Workload{
		ID: "mcp-lsp-idle-quick", ImplementationStatus: "implemented", ProducerImplementationStatus: "missing",
		RunnerTarget: "remote-go-test", Platforms: []string{"darwin", "linux", "windows"}, TimeoutSeconds: 240,
		Command: []string{"go", "test", "./cmd/mcp-lsp/multilsp", "./cmd/mcp-lsp/internal/hiddenexec", "-run", "^Test(?:Quick|Native)$"},
	}
	view := runnerPlatformWorkload(quick)
	if view.RunnerTarget != quick.RunnerTarget || view.TimeoutSeconds != quick.TimeoutSeconds || !slices.Equal(view.Command, quick.Command) {
		t.Fatalf("platform view changed canonical execution fields: %#v", view)
	}
	if err := validateRunnerWorkload(view); err == nil || !strings.Contains(err.Error(), "producer_implementation_status=missing") {
		t.Fatalf("validateRunnerWorkload() error = %v, want remote authority N/V", err)
	}
	if _, err := catalog.LocalHostTestCommand(view); err == nil || !strings.Contains(err.Error(), "exactly one package") {
		t.Fatalf("LocalHostTestCommand() error = %v, want multi-package host rejection", err)
	}

	missing := quick
	missing.ID = "mcp-lsp-default-15m"
	missing.ImplementationStatus = "missing"
	missing.RunnerTarget = "not-implemented"
	if err := validateRunnerWorkload(missing); err == nil {
		t.Fatal("missing remote workload unexpectedly passed runner validation")
	}
}

func TestLocalHostInvocationRejectsWindowsWithoutTrustedHostWrapper(t *testing.T) {
	_, _, err := localHostInvocationForPlatform("windows", []string{"./scripts/test_with_guard.sh", "--host-test", "light"})
	if err == nil || !strings.Contains(err.Error(), "NOT_VERIFIED") {
		t.Fatalf("localHostInvocationForPlatform(windows) error = %v, want explicit N/V", err)
	}
}
