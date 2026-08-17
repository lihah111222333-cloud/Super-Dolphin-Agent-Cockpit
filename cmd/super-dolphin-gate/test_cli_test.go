package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	catalog "github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

func TestRunCLITestHelpIsLocalAndDocumentsAuthorityTargets(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"test", "--help"}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("test --help exit code = %d, stderr=%q", exitCode, stderr.String())
	}
	for _, required := range []string{
		"--target=local|auto|hybrid|remote",
		"--gate-workload <id>",
		"--target=remote explicitly",
	} {
		if !strings.Contains(stdout.String(), required) {
			t.Fatalf("test --help omitted %q:\n%s", required, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("test --help wrote protocol error: %q", stderr.String())
	}
}

func TestParseAutoTestRunOptionsRequiresSelectorsAndOwnsScenario(t *testing.T) {
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	wantDigest, err := cicontract.AgentTokenDigest(token)
	if err != nil {
		t.Fatal(err)
	}
	options, err := parseAutoTestRunOptions([]string{
		"--target", "remote",
		"--config", "/tmp/config.json",
		"--ledger", "/tmp/config.baseline-state.sqlite",
		"--agent-token", token,
		"--test", "./internal/module/turn#TestRedact",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Target != "remote" || options.Scenario != "test" || options.AgentTokenDigest != wantDigest || len(options.Tests) != 1 {
		t.Fatalf("test options = %#v", options)
	}
	if _, err := parseAutoTestRunOptions([]string{
		"--target", "remote",
		"--config", "/tmp/config.json",
		"--ledger", "/tmp/config.baseline-state.sqlite",
		"--agent-token", token,
	}); err == nil {
		t.Fatal("test command accepted no selectors")
	}
	if _, err := parseAutoTestRunOptions([]string{
		"--target", "remote",
		"--config", "/tmp/config.json",
		"--ledger", "/tmp/config.baseline-state.sqlite",
		"--agent-token", token,
		"--scenario", "test",
		"--test", "./internal/module/turn#TestRedact",
	}); err == nil {
		t.Fatal("test command accepted a caller-owned scenario")
	}
}

func TestParseRemoteRunOptionsAllowsCanonicalTestScenarioSelectors(t *testing.T) {
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseRemoteRunOptions([]string{
		"--target", "remote", "--config", "/tmp/config.json", "--ledger", "/tmp/config.baseline-state.sqlite",
		"--agent-token", token, "--scenario", "test", "--test", "./internal/module/turn#TestRedact",
	}); err != nil {
		t.Fatalf("parseRemoteRunOptions() rejected canonical test scenario selector: %v", err)
	}
}

func TestParseRemoteRunOptionsRejectsMixedGateAndTestSelectors(t *testing.T) {
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseRemoteRunOptions([]string{
		"--target", "remote", "--config", "/tmp/config.json", "--ledger", "/tmp/config.baseline-state.sqlite",
		"--agent-token", token, "--scenario", "test", "--test", "./internal/module/turn#TestRedact",
		"--gate-workload", "backend:test_with_guard",
	}); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("parseRemoteRunOptions() error = %v, want mixed selector rejection", err)
	}
}

func TestParseAutoTestRunOptionsRejectsRemoteAuthorityFlagsForLocalTarget(t *testing.T) {
	for _, authority := range [][]string{{"--config", "/tmp/config.json"}, {"--agent-token", "issued-token"}} {
		args := []string{"--target", "local", "--ledger", "/tmp/config.baseline-state.sqlite", "--test", "./internal/module/turn#TestRedact"}
		args = append(args, authority...)
		if _, err := parseAutoTestRunOptions(args); err == nil || !strings.Contains(err.Error(), "remote-only") {
			t.Fatalf("parseAutoTestRunOptions(%v) error = %v, want local remote-authority rejection", authority, err)
		}
	}
}

func TestParseAutoAndHybridTestOptionsDeferRemoteAuthorityUntilScheduler(t *testing.T) {
	for _, target := range []string{"auto", "hybrid"} {
		t.Run(target, func(t *testing.T) {
			options, err := parseAutoTestRunOptions([]string{
				"--target", target,
				"--ledger", "/tmp/config.baseline-state.sqlite",
				"--config", "/tmp/config.json",
				"--commit", "HEAD",
				"--agent-token=raw-token-deferred",
				"--gate-workload", "backend:test_with_guard",
			})
			if err != nil {
				t.Fatalf("parseAutoTestRunOptions(%s) error = %v, want deferred authority", target, err)
			}
			if options.ConfigPath != "/tmp/config.json" || options.Commit != "HEAD" || options.AgentTokenDigest != "" {
				t.Fatalf("deferred %s options = %#v", target, options)
			}
		})
	}
}

func TestDeferredAutoAndHybridAuthorityRejectsSingleDashWhenNoRemoteSubset(t *testing.T) {
	for _, target := range []string{"auto", "hybrid"} {
		for _, authority := range [][]string{{"-config", "/tmp/config.json"}, {"-agent-token=secret-not-for-errors"}} {
			err := validateDeferredRemoteAuthorityUsage(remoteRunOptions{Target: target}, authority, false)
			if err == nil || !strings.Contains(err.Error(), "unused remote authority") {
				t.Fatalf("validateDeferredRemoteAuthorityUsage(%s, %v) error = %v", target, authority, err)
			}
			if strings.Contains(err.Error(), "secret-not-for-errors") {
				t.Fatalf("authority error leaked token: %v", err)
			}
		}
	}
}

func TestPreHandshakeAuthorityScannersStopAtPositionalSentinel(t *testing.T) {
	target, err := requestedWorkloadTarget([]string{"--target", "auto", "--", "-target=remote", "--target", "remote"}, "local")
	if err != nil || target != "auto" {
		t.Fatalf("requestedWorkloadTarget() = %q, %v; want auto", target, err)
	}
	args := []string{"--agent-token", "before", "--", "--agent-token", "after"}
	filtered, err := withoutDeferredAgentToken(args)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--", "--agent-token", "after"}
	if !reflect.DeepEqual(filtered, want) {
		t.Fatalf("withoutDeferredAgentToken() = %#v, want %#v", filtered, want)
	}
	positional := []string{"--", "--agent-token", "secret-positional"}
	if hasRemoteCIAgentTokenArgument(positional) {
		t.Fatal("hasRemoteCIAgentTokenArgument crossed -- sentinel")
	}
	if value, found, err := remoteCIAgentTokenArgument(positional); err != nil || found || value != "" {
		t.Fatalf("remoteCIAgentTokenArgument(positional) = %q, %t, %v", value, found, err)
	}
	if got := withoutRemoteCIAgentTokenArgument(positional); !reflect.DeepEqual(got, positional) {
		t.Fatalf("withoutRemoteCIAgentTokenArgument(positional) = %#v", got)
	}
}

func TestPrepareRunOptionParseArgsRejectsMissingDeferredAgentTokenValue(t *testing.T) {
	for _, args := range [][]string{
		{"--agent-token", "--target=hybrid"},
		{"-agent-token", "-target=hybrid"},
		{"--agent-token", "--", "--target=hybrid"},
	} {
		filtered, deferred, err := prepareRunOptionParseArgs(args, "auto", true)
		if err == nil || !strings.Contains(err.Error(), "--agent-token requires a value") {
			t.Fatalf("prepareRunOptionParseArgs(%v) = %#v, %t, %v; want missing token value", args, filtered, deferred, err)
		}
	}
}

func TestAutoTestAuthorityFailsWhenSchedulerHasNoRemoteSubset(t *testing.T) {
	err := validateDeferredRemoteAuthorityUsage(remoteRunOptions{Target: "auto"}, []string{"--config", "/tmp/config.json"}, false)
	if err == nil || !strings.Contains(err.Error(), "unused remote authority") {
		t.Fatalf("validateDeferredRemoteAuthorityUsage() error = %v, want unused authority rejection", err)
	}
}

func TestParseRemoteRunOptionsRejectsManifestAndTestSelectors(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "workloads.json")
	if err := os.WriteFile(manifest, []byte(`["backend:test_with_guard"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	_, err = parseRemoteRunOptions([]string{
		"--target", "remote", "--config", "/tmp/config.json", "--ledger", "/tmp/config.baseline-state.sqlite",
		"--agent-token", token, "--scenario", "test", "--test", "./internal/module/turn#TestRedact",
		"--gate-workload-manifest", manifest,
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("parseRemoteRunOptions() error = %v, want manifest/test mutual exclusion", err)
	}
}

func TestDuplicateAuthorityErrorDoesNotEchoRawToken(t *testing.T) {
	secretA := "raw-secret-A-should-not-echo"
	secretB := "raw-secret-B-should-not-echo"
	_, err := parseRemoteRunOptions([]string{
		"--config", "/tmp/config.json", "--agent-token", secretA, "--agent-token=" + secretB,
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate authority flag --agent-token") {
		t.Fatalf("duplicate agent-token error = %v, want flag-only duplicate error", err)
	}
	if strings.Contains(err.Error(), secretA) || strings.Contains(err.Error(), secretB) || strings.Contains(err.Error(), "--agent-token=") {
		t.Fatalf("duplicate agent-token error leaked raw argument: %v", err)
	}
}

func TestAuthoritySingleDashAliasesCanonicalizeBeforeTargetAndDuplicateChecks(t *testing.T) {
	if _, err := parseAutoTestRunOptions([]string{
		"-target", "local", "--target", "local", "--ledger", "/tmp/config.baseline-state.sqlite",
		"--gate-workload", "backend:test_with_guard",
	}); err == nil || !strings.Contains(err.Error(), "duplicate authority flag --target") {
		t.Fatalf("parseAutoTestRunOptions() error = %v, want canonical duplicate target rejection", err)
	}
	if _, err := parseAutoTestRunOptions([]string{
		"-target", "local", "--ledger", "/tmp/config.baseline-state.sqlite",
		"--config", "/tmp/config.json", "--gate-workload", "backend:test_with_guard",
	}); err == nil || !strings.Contains(err.Error(), "remote-only") {
		t.Fatalf("parseAutoTestRunOptions() error = %v, want canonical local authority rejection", err)
	}
}

func TestParseAutoTestRunOptionsBindsDefaultMcpLSPWorkloadAndKeepsItNV(t *testing.T) {
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	_, err = parseAutoTestRunOptions([]string{
		"--target", "remote",
		"--config", "/tmp/config.json",
		"--ledger", "/tmp/config.baseline-state.sqlite",
		"--repository", "../..",
		"--agent-token", token,
		"--workload", mcpLSPDefault15mWorkloadID,
		"--completion-receipt", "/tmp/mcp-lsp-completion.json",
	})
	if err == nil || !strings.Contains(err.Error(), "default-15m workload is N/V") {
		t.Fatalf("default workload parse error = %v, want explicit N/V", err)
	}
}

func TestParseAutoTestRunOptionsRejectsLocalMcpLSPWorkloadsForRemoteCLI(t *testing.T) {
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	for _, workloadID := range []string{"mcp-lsp-idle-quick", "mcp-lsp-native-process-tree"} {
		_, err := parseAutoTestRunOptions([]string{
			"--target", "remote",
			"--config", "/tmp/config.json",
			"--ledger", "/tmp/config.baseline-state.sqlite",
			"--repository", "../..",
			"--agent-token", token,
			"--workload", workloadID,
		})
		if err == nil || !strings.Contains(err.Error(), "remote test cannot execute local runner target") || !strings.Contains(err.Error(), "local runner receipts cannot substitute remote authority") {
			t.Fatalf("local workload %q parse error = %v, want remote N/V", workloadID, err)
		}
	}
}

func TestValidateMcpLSPWorkloadBlocksDefaultAuthorityBeforeExecution(t *testing.T) {
	receiptPath := filepath.Join(t.TempDir(), "completion.json")
	workload := catalog.Workload{
		ID:                           mcpLSPDefault15mWorkloadID,
		ImplementationStatus:         "implemented",
		ProducerImplementationStatus: "implemented",
		Platforms:                    []string{catalog.RemoteTestGOOS},
		TriggerClass:                 "default-15m-source-e2e",
	}
	err := validateMcpLSPWorkload(workload, remoteRunOptions{CompletionReceiptPath: receiptPath})
	if err == nil || !strings.Contains(err.Error(), "remote run/job/artifact authority") {
		t.Fatalf("validateMcpLSPWorkload() error = %v, want authority N/V before execution", err)
	}
}

func TestResolveMcpLSPCandidateIdentityUsesExplicitTargetAfterWorkingTreeDrift(t *testing.T) {
	root := t.TempDir()
	runMcpLSPTestGit(t, root, "init", "--quiet")
	runMcpLSPTestGit(t, root, "config", "user.name", "gate-test")
	runMcpLSPTestGit(t, root, "config", "user.email", "gate-test@example.invalid")
	path := filepath.Join(root, "candidate.txt")
	if err := os.WriteFile(path, []byte("candidate-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runMcpLSPTestGit(t, root, "add", "candidate.txt")
	runMcpLSPTestGit(t, root, "commit", "--quiet", "-m", "候选 A")
	firstHead := mcpLSPTestGitOutput(t, root, "rev-parse", "HEAD^{commit}")
	firstTree := mcpLSPTestGitOutput(t, root, "rev-parse", "HEAD^{tree}")

	// A mutable working tree must not replace the explicit candidate object.
	if err := os.WriteFile(path, []byte("working-tree-drift\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	head, tree, err := resolveMcpLSPCandidateIdentity(root, remoteRunOptions{Commit: firstHead})
	if err != nil {
		t.Fatalf("resolveMcpLSPCandidateIdentity(commit) error = %v", err)
	}
	if head != firstHead || tree != firstTree {
		t.Fatalf("candidate identity = (%q, %q), want (%q, %q)", head, tree, firstHead, firstTree)
	}

	head, tree, err = resolveMcpLSPCandidateIdentity(root, remoteRunOptions{Tree: firstTree, ParentCommit: firstHead})
	if err != nil {
		t.Fatalf("resolveMcpLSPCandidateIdentity(tree) error = %v", err)
	}
	if head != firstHead || tree != firstTree {
		t.Fatalf("tree candidate identity = (%q, %q), want (%q, %q)", head, tree, firstHead, firstTree)
	}
}

func runMcpLSPTestGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func mcpLSPTestGitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}
