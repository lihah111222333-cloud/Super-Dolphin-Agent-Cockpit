package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	catalog "github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

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
		"--config", "/tmp/config.json",
		"--ledger", "/tmp/config.baseline-state.sqlite",
		"--agent-token", token,
		"--test", "./internal/module/turn#TestRedact",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Scenario != "test" || options.AgentTokenDigest != wantDigest || len(options.Tests) != 1 {
		t.Fatalf("test options = %#v", options)
	}
	if _, err := parseAutoTestRunOptions([]string{
		"--config", "/tmp/config.json",
		"--ledger", "/tmp/config.baseline-state.sqlite",
		"--agent-token", token,
	}); err == nil {
		t.Fatal("test command accepted no selectors")
	}
	if _, err := parseAutoTestRunOptions([]string{
		"--config", "/tmp/config.json",
		"--ledger", "/tmp/config.baseline-state.sqlite",
		"--agent-token", token,
		"--scenario", "test",
		"--test", "./internal/module/turn#TestRedact",
	}); err == nil {
		t.Fatal("test command accepted a caller-owned scenario")
	}
}

func TestParseAutoTestRunOptionsBindsDefaultMcpLSPWorkloadAndKeepsItNV(t *testing.T) {
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	_, err = parseAutoTestRunOptions([]string{
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
		Platforms:                    []string{runtime.GOOS},
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
