package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	lspexec "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/exec"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/middleware"
)

type CodeRunTestHandler struct {
	sandbox SandboxRunner
}

func NewCodeRunTestHandler(rootDir string) (middleware.Handler, error) {
	sandbox, err := lspexec.NewSandbox(rootDir)
	if err != nil {
		return nil, err
	}
	return wrapToolHandler("code_run_test", middleware.TierExec, CodeRunTestHandler{sandbox: sandbox}.Handle), nil
}

func NewCodeRunTestHandlerWithSandbox(sandbox SandboxRunner) middleware.Handler {
	return wrapToolHandler("code_run_test", middleware.TierExec, CodeRunTestHandler{sandbox: sandbox}.Handle)
}

func HandleCodeRunTest(ctx context.Context, sandbox SandboxRunner, params json.RawMessage) (any, error) {
	return CodeRunTestHandler{sandbox: sandbox}.Handle(ctx, params)
}

func (h CodeRunTestHandler) Handle(ctx context.Context, params json.RawMessage) (any, error) {
	if h.sandbox == nil {
		return nil, errors.New("code_run_test sandbox is nil")
	}
	var req CodeRunRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("decode code_run_test request: %w", err)
	}
	if strings.TrimSpace(req.TestFunc) == "" {
		return nil, errors.New("test_func is required")
	}
	if !goTestNamePattern.MatchString(req.TestFunc) {
		return nil, errors.New("test_func contains unsupported characters")
	}
	pkg := strings.TrimSpace(req.TestPkg)
	if pkg == "" {
		pkg = "./..."
	}
	timeout := middleware.ClampTimeout(req.Timeout, defaultCodeRunTimeout(), middleware.TierExec)
	request := lspexec.Request{
		Args:    []string{"go", "test", "-run", "^" + req.TestFunc + "$", pkg},
		WorkDir: h.sandbox.RootDir(),
		Timeout: timeout,
	}
	result, err := h.sandbox.Run(ctx, request)
	if err != nil {
		return CodeRunFailure{Error: err.Error(), ExitCode: -1}, nil
	}
	return CodeRunResult{
		Success:   result.ExitCode == 0,
		Output:    result.Output,
		ExitCode:  result.ExitCode,
		Duration:  result.Duration,
		Language:  "go",
		Mode:      "test",
		Truncated: result.Truncated,
	}, nil
}
