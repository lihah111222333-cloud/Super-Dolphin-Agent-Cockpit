package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	catalog "github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

const (
	mcpLSPWorkloadCatalogPath = "scripts/mcp_lsp_workload_catalog.json"
	mcpLSPWorkloadRunnerPath  = "scripts/run_mcp_lsp_workload.sh"
)

func TestMcpLSPWorkloadRunnerConsumesCatalogIDsAndFailsClosed(t *testing.T) {
	repoRoot, tree, document := loadMcpLSPWorkloadCatalogGuardContext(t)
	runner := filepath.Join(repoRoot, mcpLSPWorkloadRunnerPath)
	implementedLocal := implementedLocalWorkloads(document)
	if len(implementedLocal) == 0 {
		t.Fatal("catalog has no implemented local runner workload")
	}
	for _, workload := range implementedLocal {
		cmd := exec.Command("bash", runner, "--tree", tree, "--id", workload.ID, "--print-plan")
		cmd.Dir = repoRoot
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("runner --id %s failed: %v\n%s", workload.ID, err, output)
		}
	}
	for _, args := range [][]string{{"--tree", tree, "--id", "mcp-lsp-renamed", "--print-plan"}, {"--tree", tree, "--print-plan"}} {
		cmd := exec.Command("bash", append([]string{runner}, args...)...)
		cmd.Dir = repoRoot
		if output, err := cmd.CombinedOutput(); err == nil {
			t.Fatalf("runner args %v unexpectedly succeeded: %s", args, output)
		}
	}
}

func TestMcpLSPWorkloadRunnerMissingImplementationFailsClosedWithExitOne(t *testing.T) {
	repoRoot, tree, document := loadMcpLSPWorkloadCatalogGuardContext(t)
	runner := filepath.Join(repoRoot, mcpLSPWorkloadRunnerPath)
	found := false
	for _, workload := range document.Workloads {
		if workload.ImplementationStatus != "missing" {
			continue
		}
		found = true
		cmd := exec.Command("bash", runner, "--tree", tree, "--id", workload.ID)
		cmd.Dir = repoRoot
		output, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("missing workload %s unexpectedly succeeded: %s", workload.ID, output)
		}
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
			t.Fatalf("missing workload %s exit = %v, want exit 1; output=%s", workload.ID, err, output)
		}
		if !strings.Contains(string(output), "implementation_status=missing") {
			t.Fatalf("missing workload %s output omitted implementation status: %s", workload.ID, output)
		}
	}
	if !found {
		t.Fatal("catalog has no missing implementation workload")
	}
}

func TestMcpLSPWorkloadRunnerRejectsDirtyExecutionCandidate(t *testing.T) {
	repoRoot, tree, document := loadMcpLSPWorkloadCatalogGuardContext(t)
	workload := firstImplementedShortLocalWorkload(document)
	if workload.ID == "" {
		t.Fatal("catalog has no short implemented local runner workload")
	}
	runner := filepath.Join(repoRoot, mcpLSPWorkloadRunnerPath)
	receipt := filepath.Join(t.TempDir(), "quick-receipt.json")
	if err := os.WriteFile(filepath.Join(repoRoot, "explicit-dirty-source.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := exec.Command("bash", runner, "--tree", tree, "--id", workload.ID, "--receipt", receipt)
	run.Dir = repoRoot
	if output, err := run.CombinedOutput(); err == nil || !strings.Contains(string(output), "worktree is dirty") {
		t.Fatalf("runner output=%s err=%v, want dirty candidate rejection", output, err)
	}
}

func TestMcpLSPWrappersRequireExplicitCandidateTree(t *testing.T) {
	root := repoRootForMcpLSPWorkloadCatalogGuard(t)
	for _, test := range []struct {
		name   string
		path   string
		args   []string
		stderr string
	}{
		{name: "runner missing", path: mcpLSPWorkloadRunnerPath, args: []string{"--id", "mcp-lsp-idle-quick", "--print-plan"}, stderr: "--tree is required"},
		{name: "guard missing", path: "scripts/check_mcp_lsp_workload_catalog.sh", args: []string{"--receipt-only", "--receipt", filepath.Join(t.TempDir(), "missing.json"), "--id", "mcp-lsp-idle-quick"}, stderr: "--tree is required"},
		{name: "runner empty", path: mcpLSPWorkloadRunnerPath, args: []string{"--tree=", "--id", "mcp-lsp-idle-quick", "--print-plan"}, stderr: "requires exactly one candidate tree"},
		{name: "guard duplicate", path: "scripts/check_mcp_lsp_workload_catalog.sh", args: []string{"--tree", strings.Repeat("a", 40), "--tree=" + strings.Repeat("b", 40)}, stderr: "exactly one candidate tree"},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command("bash", append([]string{filepath.Join(root, test.path)}, test.args...)...)
			command.Dir = root
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), test.stderr) {
				t.Fatalf("wrapper output=%s err=%v, want %q", output, err, test.stderr)
			}
		})
	}
}

func TestMcpLSPGuardWrapperRequiresExplicitTrustedSelf(t *testing.T) {
	root := repoRootForMcpLSPWorkloadCatalogGuard(t)
	wrapper := filepath.Join(root, "scripts/check_mcp_lsp_workload_catalog.sh")
	fakeDir := t.TempDir()
	fake := filepath.Join(fakeDir, "super-dolphin-gate")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", wrapper, "--tree", strings.Repeat("a", 40))
	command.Dir = root
	command.Env = append(os.Environ(), "PATH="+fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"), "SUPER_DOLPHIN_GATE_SELF=")
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "SUPER_DOLPHIN_GATE_SELF") {
		t.Fatalf("guard wrapper used PATH fake: output=%s err=%v", output, err)
	}
}

func TestMcpLSPGuardWrapperLabelsDirectSelfAsNonAuthoritative(t *testing.T) {
	root := repoRootForMcpLSPWorkloadCatalogGuard(t)
	fake := filepath.Join(t.TempDir(), "fake-gate")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", filepath.Join(root, "scripts/check_mcp_lsp_workload_catalog.sh"), "--tree", strings.Repeat("a", 40))
	command.Dir = root
	command.Env = []string{"PATH=/usr/bin:/bin", "SUPER_DOLPHIN_GATE_SELF=" + fake}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("wrapper error = %v output=%s", err, output)
	}
	if !strings.Contains(string(output), "LOCAL_NON_AUTHORITATIVE NOT_VERIFIED") {
		t.Fatalf("direct wrapper output = %q, want N/V marker", output)
	}
}

func TestMcpLSPMakeTargetPassesExactCandidateTree(t *testing.T) {
	repoRoot, tree, _ := loadMcpLSPWorkloadCatalogGuardContext(t)
	capturePath := filepath.Join(t.TempDir(), "runner-argv.txt")
	runnerPath := filepath.Join(repoRoot, mcpLSPWorkloadRunnerPath)
	stub := "#!/bin/sh\nset -eu\nprintf '%s\\n' \"$@\" > \"$CAPTURE_PATH\"\n"
	if err := os.WriteFile(runnerPath, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("make", "-C", repoRoot, "test-e2e-mcp-lsp-idle-quick", "MCP_LSP_CANDIDATE_TREE="+tree)
	command.Env = append(os.Environ(), "CAPTURE_PATH="+capturePath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("make exact-tree fixture failed: %v\n%s", err, output)
	}
	argv, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	want := "--tree\n" + tree + "\n--id\nmcp-lsp-idle-quick\n"
	if string(argv) != want {
		t.Fatalf("Make runner argv = %q, want %q", argv, want)
	}
	missing := exec.Command("make", "-C", repoRoot, "test-e2e-mcp-lsp-idle-quick", "MCP_LSP_CANDIDATE_TREE=")
	missing.Env = append(os.Environ(), "CAPTURE_PATH="+capturePath)
	if output, err := missing.CombinedOutput(); err == nil || !strings.Contains(string(output), "MCP_LSP_CANDIDATE_TREE is required") {
		t.Fatalf("Make missing-tree output=%s err=%v", output, err)
	}
}

func TestMcpLSPCatalogMakeTargetRequiresExplicitSelfOwner(t *testing.T) {
	repoRoot, tree, _ := loadMcpLSPWorkloadCatalogGuardContext(t)
	missing := exec.Command("make", "-C", repoRoot, "mcp-lsp-workload-catalog-check", "MCP_LSP_CANDIDATE_TREE="+tree)
	missing.Env = []string{"PATH=/usr/bin:/bin"}
	if output, err := missing.CombinedOutput(); err == nil || !strings.Contains(string(output), "SUPER_DOLPHIN_GATE_SELF is required") {
		t.Fatalf("Make missing self output=%s err=%v", output, err)
	}
	fake := filepath.Join(t.TempDir(), "diagnostic-gate")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("make", "-C", repoRoot, "mcp-lsp-workload-catalog-check",
		"MCP_LSP_CANDIDATE_TREE="+tree, "SUPER_DOLPHIN_GATE_SELF="+fake)
	command.Env = []string{"PATH=/usr/bin:/bin"}
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "LOCAL_NON_AUTHORITATIVE NOT_VERIFIED") {
		t.Fatalf("Make explicit self output=%s, want diagnostic N/V", output)
	}
}

func loadMcpLSPWorkloadCatalogGuardContext(t *testing.T) (string, string, catalog.Catalog) {
	t.Helper()
	repoRoot := materializeMcpLSPCandidateFixture(t, repoRootForMcpLSPWorkloadCatalogGuard(t))
	tree := mcpLSPWorkloadCatalogGuardTree(t, repoRoot)
	document, err := catalog.LoadAt(repoRoot, tree)
	if err != nil {
		t.Fatal(err)
	}
	return repoRoot, tree, document
}

func materializeMcpLSPCandidateFixture(t *testing.T, sourceRoot string) string {
	t.Helper()
	repoRoot := filepath.Join(t.TempDir(), "candidate")
	tree := snapshotMcpLSPCandidateTree(t, sourceRoot)
	archive := exec.Command("git", "-C", sourceRoot, "archive", "--format=tar", tree)
	content, err := archive.Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	extract := exec.Command("tar", "-xf", "-", "-C", repoRoot)
	extract.Stdin = bytes.NewReader(content)
	if output, err := extract.CombinedOutput(); err != nil {
		t.Fatalf("extract candidate tree %s: %v\n%s", tree, err, output)
	}
	runMcpLSPFixtureGit(t, repoRoot, nil, "init", "--quiet")
	runMcpLSPFixtureGit(t, repoRoot, nil, "config", "gc.auto", "0")
	runMcpLSPFixtureGit(t, repoRoot, nil, "config", "maintenance.auto", "false")
	runMcpLSPFixtureGit(t, repoRoot, nil, "config", "maintenance.autoDetach", "false")
	runMcpLSPFixtureGit(t, repoRoot, nil, "config", "user.name", "mcp-lsp-candidate-test")
	runMcpLSPFixtureGit(t, repoRoot, nil, "config", "user.email", "mcp-lsp-candidate-test@example.invalid")
	runMcpLSPFixtureGit(t, repoRoot, nil, "add", "-A")
	runMcpLSPFixtureGit(t, repoRoot, nil, "commit", "--quiet", "-m", "候选树")
	return repoRoot
}

func snapshotMcpLSPCandidateTree(t *testing.T, sourceRoot string) string {
	t.Helper()
	indexPath := filepath.Join(t.TempDir(), "candidate.index")
	environment := append(os.Environ(), "GIT_INDEX_FILE="+indexPath)
	for _, args := range [][]string{{"read-tree", "HEAD"}, {"add", "-A", "--", "."}} {
		command := exec.Command("git", append([]string{"-C", sourceRoot}, args...)...)
		command.Env = environment
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("snapshot candidate git %v: %v\n%s", args, err, output)
		}
	}
	command := exec.Command("git", "-C", sourceRoot, "write-tree")
	command.Env = environment
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	tree := strings.TrimSpace(string(output))
	if tree == "" {
		t.Fatal("candidate snapshot tree is empty")
	}
	return tree
}

func runMcpLSPFixtureGit(t *testing.T, repoRoot string, stdin []byte, args ...string) {
	t.Helper()
	commandArgs := append([]string(nil), args...)
	if repoRoot != "" {
		commandArgs = append([]string{"-C", repoRoot}, commandArgs...)
	}
	command := exec.Command("git", commandArgs...)
	command.Stdin = bytes.NewReader(stdin)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", commandArgs, err, output)
	}
}

func implementedLocalWorkloads(document catalog.Catalog) []catalog.Workload {
	result := make([]catalog.Workload, 0)
	for _, workload := range document.Workloads {
		if workload.ImplementationStatus == "implemented" && workload.RunnerTarget == "local-go-test" {
			result = append(result, workload)
		}
	}
	return result
}

func firstImplementedShortLocalWorkload(document catalog.Catalog) catalog.Workload {
	for _, workload := range implementedLocalWorkloads(document) {
		if workload.TriggerClass != "default-15m-source-e2e" {
			return workload
		}
	}
	return catalog.Workload{}
}

func mcpLSPWorkloadCatalogGuardTree(t *testing.T, root string) string {
	t.Helper()
	output, err := exec.Command("git", "-C", root, "rev-parse", "--verify", "--end-of-options", "HEAD^{tree}").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

func repoRootForMcpLSPWorkloadCatalogGuard(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return filepath.Clean(filepath.Join(wd, ".."))
	}
	root := filepath.Clean(wd)
	for {
		if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
			return root
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal("repository root not found")
		}
		root = parent
	}
}
