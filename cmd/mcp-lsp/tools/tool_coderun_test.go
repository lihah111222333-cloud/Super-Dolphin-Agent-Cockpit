package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	lspexec "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/exec"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

type codeRunTestSandbox struct {
	root string
}

func (s codeRunTestSandbox) RootDir() string { return s.root }

func (codeRunTestSandbox) Run(context.Context, lspexec.Request) (lspexec.Result, error) {
	return lspexec.Result{ExitCode: 0}, nil
}

func (s codeRunTestSandbox) ShellRequest(command string, workDir string, timeout time.Duration) lspexec.Request {
	return lspexec.Request{Args: []string{"sh", "-c", command}, WorkDir: workDir, Timeout: timeout}
}

func TestCodeRunUnsupportedLanguageReturnsCapabilityError(t *testing.T) {
	handler := NewCodeRunHandlerWithSandbox(codeRunTestSandbox{root: t.TempDir()})
	payload, err := json.Marshal(CodeRunRequest{
		Mode:     "run",
		Language: "python",
		Code:     "print('not supported by code_run helper')",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	_, err = handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), payload)
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

func TestCodeRunProjectCommandUsesTrustedToolScopeCWD(t *testing.T) {
	startupRoot := t.TempDir()
	trustedRoot := t.TempDir()
	handler, err := NewCodeRunHandler(startupRoot)
	if err != nil {
		t.Fatalf("new code_run handler: %v", err)
	}
	payload, err := json.Marshal(CodeRunRequest{
		Mode:    "project_cmd",
		Command: "pwd",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ctx := context.WithValue(context.Background(), common.ToolScopeContextKey, common.ToolScope{
		CWD:    trustedRoot,
		Family: "lsp",
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
		Command: "pwd",
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
		Command: "pwd",
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
