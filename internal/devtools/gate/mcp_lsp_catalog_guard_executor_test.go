package gate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestResolveExecutorCandidateTreeCancelsInheritedPipeProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix process-group inherited-pipe regression")
	}
	git := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(git, []byte("#!/bin/sh\n/bin/sleep 1 &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := resolveExecutorCandidateTree(ctx, git, t.TempDir())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("resolveExecutorCandidateTree() error = %v, want deadline cause", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("resolveExecutorCandidateTree() elapsed = %v, inherited pipe outlived cancellation", elapsed)
	}
}

func TestBoundedTrustedGitIgnoresAmbientRepositoryAndPath(t *testing.T) {
	repository, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GIT_DIR", t.TempDir())
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	t.Setenv("GIT_OBJECT_DIRECTORY", t.TempDir())
	t.Setenv("GIT_REPLACE_REF_BASE", "refs/evil/")
	output, err := BoundedTrustedGitCombinedOutput(context.Background(), repository, "rev-parse", "--verify", "HEAD^{tree}")
	if err != nil {
		t.Fatal(err)
	}
	if tree := strings.TrimSpace(string(output)); !validCalibrationOID(tree) {
		t.Fatalf("trusted Git tree = %q, want full object ID", tree)
	}
}

func TestMcpLSPCatalogGuardUsesFixedExecutor(t *testing.T) {
	id, err := targetWorkloadID(GateIDBackendTestWithGuard, workloadTargetGoGuard, GoGuardTargetMcpLSPWorkloadCatalog)
	if err != nil {
		t.Fatal(err)
	}
	program := mustExecutorProgramForWorkload(t, id)
	if len(program.Steps) != 1 || !reflect.DeepEqual(program.Steps[0].Argv, []string{ExecutorSelfCommandName, "worker", "mcp-lsp-catalog-guard"}) {
		t.Fatalf("MCP-LSP catalog guard executor = %#v", program)
	}
	want := []string{"scripts/mcp_lsp_workload_catalog.json", "internal/devtools/mcplspguardcore", "scripts/mcp_lsp_workload_catalog"}
	if !reflect.DeepEqual(program.RequiredPaths, want) || program.NeedsGoSeed {
		t.Fatalf("MCP-LSP catalog guard paths/go seed = %v/%t, want %v/false", program.RequiredPaths, program.NeedsGoSeed, want)
	}
}

func TestInjectMcpLSPCandidateTreeBindsGuardAndMakeCommands(t *testing.T) {
	tree := strings.Repeat("a", 40)
	guard, err := injectMcpLSPCandidateTree([]string{"./scripts/check_mcp_lsp_workload_catalog.sh"}, tree)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(guard, []string{"./scripts/check_mcp_lsp_workload_catalog.sh", "--tree", tree}) {
		t.Fatalf("guard argv = %v, want explicit tree binding", guard)
	}
	makeArgs, err := injectMcpLSPCandidateTree([]string{"make", "mcp-lsp-workload-catalog-check"}, tree)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(makeArgs, []string{"make", "mcp-lsp-workload-catalog-check", "MCP_LSP_CANDIDATE_TREE=" + tree}) {
		t.Fatalf("make argv = %v, want explicit tree binding", makeArgs)
	}
	if _, err := injectMcpLSPCandidateTree([]string{"./scripts/check_mcp_lsp_workload_catalog.sh", "--tree", tree}, tree); err == nil {
		t.Fatal("injectMcpLSPCandidateTree() accepted duplicate candidate tree")
	}
}

func TestInjectMcpLSPCandidateTreeBindsCompiledWorkerToSnapshot(t *testing.T) {
	tree := strings.Repeat("b", 40)
	repository := "/absolute/candidate"
	argv, err := injectMcpLSPCandidateTreeAt([]string{ExecutorSelfCommandName, "worker", "mcp-lsp-catalog-guard"}, tree, repository)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{ExecutorSelfCommandName, "worker", "mcp-lsp-catalog-guard", "--repository", repository, "--tree", tree}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("compiled guard argv = %v, want %v", argv, want)
	}
}
