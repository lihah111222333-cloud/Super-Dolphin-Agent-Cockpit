package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	lspexec "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/exec"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

type codeRunTestSandbox struct {
	root string
	err  error
}

func (s codeRunTestSandbox) RootDir() string { return s.root }

func (s codeRunTestSandbox) Run(context.Context, lspexec.Request) (lspexec.Result, error) {
	if s.err != nil {
		return lspexec.Result{}, s.err
	}
	return lspexec.Result{ExitCode: 0}, nil
}

func (s codeRunTestSandbox) ShellRequest(command string, workDir string, timeout time.Duration) lspexec.Request {
	return lspexec.Request{Args: []string{"sh", "-c", command}, WorkDir: workDir, Timeout: timeout}
}

type recordingCodeRunTestSandbox struct {
	root string
	req  lspexec.Request
}

func (s *recordingCodeRunTestSandbox) RootDir() string { return s.root }

func (s *recordingCodeRunTestSandbox) Run(_ context.Context, req lspexec.Request) (lspexec.Result, error) {
	s.req = req
	return lspexec.Result{ExitCode: 0, Output: "ok"}, nil
}

func (s *recordingCodeRunTestSandbox) ShellRequest(command string, workDir string, timeout time.Duration) lspexec.Request {
	return lspexec.Request{Args: []string{"sh", "-c", command}, WorkDir: workDir, Timeout: timeout}
}

func TestCodeRunUnsupportedLanguageReturnsCapabilityError(t *testing.T) {
	root := t.TempDir()
	handler := NewCodeRunHandlerWithSandbox(codeRunTestSandbox{root: root})
	payload, err := json.Marshal(CodeRunRequest{
		Mode:     "run",
		Language: "python",
		Code:     "print('not supported by code_run helper')",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	_, err = handler(testToolContext(root), payload)
	if err == nil {
		t.Fatal("code_run python error = nil, want unsupported capability")
	}
	envelope := newToolErrorEnvelope("code_run", "python", err)
	if envelope.Code != "capability_unsupported" {
		t.Fatalf("envelope code = %q, want capability_unsupported (err=%v)", envelope.Code, err)
	}
	if envelope.Retryable {
		t.Fatalf("envelope retryable = true, want false for unsupported helper capability")
	}
	if !strings.Contains(strings.ToLower(envelope.Hint), "supported") {
		t.Fatalf("envelope hint = %q, want supported-language guidance", envelope.Hint)
	}
}

func TestCodeRunTestBuildsGoTestRequestWithoutShell(t *testing.T) {
	root := t.TempDir()
	sandbox := &recordingCodeRunTestSandbox{root: root}
	handler := NewCodeRunTestHandlerWithSandbox(sandbox)
	payload, err := json.Marshal(CodeRunTestRequest{
		TestFunc: "TestTarget",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		CWD:            root,
		WorkspaceRoots: []string{root},
		Family:         "lsp",
	})

	got, err := handler(ctx, payload)
	if err != nil {
		t.Fatalf("code_run_test returned error: %v", err)
	}
	result, ok := got.(CodeRunResult)
	if !ok {
		t.Fatalf("code_run_test result = %#v, want CodeRunResult", got)
	}
	if !result.Success {
		t.Fatalf("code_run_test success = false, output=%q exit=%d", result.Output, result.ExitCode)
	}
	wantArgs := []string{"go", "test", "-run", "^TestTarget$", "./..."}
	if !reflect.DeepEqual(sandbox.req.Args, wantArgs) {
		t.Fatalf("code_run_test args = %#v, want %#v", sandbox.req.Args, wantArgs)
	}
	if sandbox.req.Command != "" {
		t.Fatalf("code_run_test command = %q, want empty command to avoid shell execution", sandbox.req.Command)
	}
	if sandbox.req.WorkDir != root {
		t.Fatalf("code_run_test work_dir = %q, want %q", sandbox.req.WorkDir, root)
	}
	if sandbox.req.TraceTool != "code_run_test" || sandbox.req.TraceMode != "test" {
		t.Fatalf("code_run_test trace = %q/%q, want code_run_test/test", sandbox.req.TraceTool, sandbox.req.TraceMode)
	}
}

func TestCodeRunTestRelativePackageInsideBackendSubmoduleUsesModuleRoot(t *testing.T) {
	root := t.TempDir()
	backend := filepath.Join(root, "backend")
	pkgDir := filepath.Join(backend, "internal", "service")
	if err := os.MkdirAll(pkgDir, 0o700); err != nil {
		t.Fatalf("mkdir package dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backend, "go.mod"), []byte("module example.test/backend\n\ngo 1.25.0\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	sandbox := &recordingCodeRunTestSandbox{root: root}
	handler := NewCodeRunTestHandlerWithSandbox(sandbox)
	payload, err := json.Marshal(CodeRunTestRequest{
		TestFunc: "TestTarget",
		TestPkg:  "./backend/internal/service/...",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		CWD:            root,
		WorkspaceRoots: []string{root},
		Family:         "lsp",
	})

	if _, err := handler(ctx, payload); err != nil {
		t.Fatalf("code_run_test returned error: %v", err)
	}
	wantArgs := []string{"go", "test", "-run", "^TestTarget$", "./internal/service/..."}
	if !reflect.DeepEqual(sandbox.req.Args, wantArgs) {
		t.Fatalf("code_run_test args = %#v, want %#v", sandbox.req.Args, wantArgs)
	}
	wantWorkDir, err := filepath.EvalSymlinks(backend)
	if err != nil {
		t.Fatalf("eval backend module root: %v", err)
	}
	if sandbox.req.WorkDir != wantWorkDir {
		t.Fatalf("code_run_test work_dir = %q, want backend module root %q", sandbox.req.WorkDir, wantWorkDir)
	}
}

func TestCodeRunProjectCommandUsesTrustedToolScopeCWD(t *testing.T) {
	startupRoot := t.TempDir()
	trustedRoot := t.TempDir()
	handler, err := NewCodeRunHandler(startupRoot)
	if err != nil {
		t.Fatalf("new code_run handler: %v", err)
	}
	payload, err := json.Marshal(CodeRunRequest{
		Mode:    "project_cmd",
		Command: printWorkingDirectoryCommand(),
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ctx := testToolContext(trustedRoot)

	got, err := handler(ctx, payload)
	if err != nil {
		t.Fatalf("code_run returned error: %v", err)
	}
	result, ok := got.(CodeRunResult)
	if !ok {
		t.Fatalf("code_run result = %#v, want CodeRunResult", got)
	}
	if !result.Success {
		t.Fatalf("code_run success = false, output=%q exit=%d", result.Output, result.ExitCode)
	}
	wantRoot, err := filepath.EvalSymlinks(trustedRoot)
	if err != nil {
		t.Fatalf("eval trusted root: %v", err)
	}
	if strings.TrimSpace(result.Output) != filepath.Clean(wantRoot) {
		t.Fatalf("pwd output = %q, want trusted cwd %q", strings.TrimSpace(result.Output), wantRoot)
	}
}

func TestCodeRunProjectCommandAllowsAbsoluteWorkDirInAdditionalWorkspaceRoot(t *testing.T) {
	startupRoot := t.TempDir()
	primaryRoot := t.TempDir()
	extraRoot := t.TempDir()
	handler, err := NewCodeRunHandler(startupRoot)
	if err != nil {
		t.Fatalf("new code_run handler: %v", err)
	}
	payload, err := json.Marshal(CodeRunRequest{
		Mode:    "project_cmd",
		Command: printWorkingDirectoryCommand(),
		WorkDir: extraRoot,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		CWD:            primaryRoot,
		WorkspaceRoots: []string{extraRoot},
		Family:         "lsp",
	})

	got, err := handler(ctx, payload)
	if err != nil {
		t.Fatalf("code_run returned error: %v", err)
	}
	result, ok := got.(CodeRunResult)
	if !ok {
		t.Fatalf("code_run result = %#v, want CodeRunResult", got)
	}
	if !result.Success {
		t.Fatalf("code_run success = false, output=%q exit=%d", result.Output, result.ExitCode)
	}
	wantRoot, err := filepath.EvalSymlinks(extraRoot)
	if err != nil {
		t.Fatalf("eval extra root: %v", err)
	}
	if strings.TrimSpace(result.Output) != filepath.Clean(wantRoot) {
		t.Fatalf("pwd output = %q, want extra root %q", strings.TrimSpace(result.Output), wantRoot)
	}
}

func TestCodeRunProjectCommandAllowsExplicitAbsoluteWorkDirOutsideWorkspaceRoots(t *testing.T) {
	startupRoot := t.TempDir()
	staleRoot := t.TempDir()
	explicitRoot := t.TempDir()
	handler, err := NewCodeRunHandler(startupRoot)
	if err != nil {
		t.Fatalf("new code_run handler: %v", err)
	}
	payload, err := json.Marshal(CodeRunRequest{
		Mode:    "project_cmd",
		Command: printWorkingDirectoryCommand(),
		WorkDir: explicitRoot,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		CWD:            staleRoot,
		WorkspaceRoots: []string{staleRoot},
		Family:         "lsp",
	})

	got, err := handler(ctx, payload)
	if err != nil {
		t.Fatalf("code_run returned error: %v", err)
	}
	result, ok := got.(CodeRunResult)
	if !ok {
		t.Fatalf("code_run result = %#v, want CodeRunResult", got)
	}
	if !result.Success {
		t.Fatalf("code_run success = false, output=%q exit=%d", result.Output, result.ExitCode)
	}
	wantRoot, err := filepath.EvalSymlinks(explicitRoot)
	if err != nil {
		t.Fatalf("eval explicit root: %v", err)
	}
	if strings.TrimSpace(result.Output) != filepath.Clean(wantRoot) {
		t.Fatalf("pwd output = %q, want explicit root %q", strings.TrimSpace(result.Output), wantRoot)
	}
}

func printWorkingDirectoryCommand() string {
	if runtime.GOOS == "windows" {
		return "cd"
	}
	return "pwd"
}

func TestCodeRunProjectCommandAllowsCanonicalWorkDirInAdditionalWorkspaceRoot(t *testing.T) {
	startupRoot := t.TempDir()
	primaryRoot := t.TempDir()
	realExtraRoot := t.TempDir()
	linkParent := t.TempDir()
	linkedExtraRoot := filepath.Join(linkParent, "extra")
	if err := os.Symlink(realExtraRoot, linkedExtraRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	handler, err := NewCodeRunHandler(startupRoot)
	if err != nil {
		t.Fatalf("new code_run handler: %v", err)
	}
	payload, err := json.Marshal(CodeRunRequest{
		Mode:    "project_cmd",
		Command: printWorkingDirectoryCommand(),
		WorkDir: realExtraRoot,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		CWD:            primaryRoot,
		WorkspaceRoots: []string{linkedExtraRoot},
		Family:         "lsp",
	})

	got, err := handler(ctx, payload)
	if err != nil {
		t.Fatalf("code_run returned error: %v", err)
	}
	result, ok := got.(CodeRunResult)
	if !ok {
		t.Fatalf("code_run result = %#v, want CodeRunResult", got)
	}
	if !result.Success {
		t.Fatalf("code_run success = false, output=%q exit=%d", result.Output, result.ExitCode)
	}
	wantRoot, err := filepath.EvalSymlinks(realExtraRoot)
	if err != nil {
		t.Fatalf("eval real extra root: %v", err)
	}
	if strings.TrimSpace(result.Output) != filepath.Clean(wantRoot) {
		t.Fatalf("pwd output = %q, want real extra root %q", strings.TrimSpace(result.Output), wantRoot)
	}
}

func TestCodeRunSandboxErrorReturnsToolError(t *testing.T) {
	root := t.TempDir()
	handler := NewCodeRunHandlerWithSandbox(codeRunTestSandbox{
		root: root,
		err:  errors.New("sandbox unavailable"),
	})
	payload, err := json.Marshal(CodeRunRequest{
		Mode:    "project_cmd",
		Command: "go test ./...",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	got, err := handler(testToolContext(root), payload)
	if err == nil || !strings.Contains(err.Error(), "sandbox unavailable") {
		t.Fatalf("code_run error = %v, want sandbox unavailable", err)
	}
	if got != nil {
		t.Fatalf("code_run result = %#v, want nil on tool error", got)
	}
}
