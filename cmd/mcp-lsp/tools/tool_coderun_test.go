package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	lspexec "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/exec"
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

	_, err = handler(context.Background(), payload)
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
