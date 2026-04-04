package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	lspexec "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/exec"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/middleware"
)

type SandboxRunner interface {
	RootDir() string
	Run(context.Context, lspexec.Request) (lspexec.Result, error)
	ShellRequest(command string, workDir string, timeout time.Duration) lspexec.Request
}

type CodeRunRequest struct {
	Mode     string `json:"mode"`
	Language string `json:"language,omitempty"`
	Code     string `json:"code,omitempty"`
	Command  string `json:"command,omitempty"`
	AutoWrap *bool  `json:"auto_wrap,omitempty"`
	WorkDir  string `json:"work_dir,omitempty"`
	Timeout  int    `json:"timeout,omitempty"`
	TestFunc string `json:"test_func,omitempty"`
	TestPkg  string `json:"test_pkg,omitempty"`
}

type CodeRunResult struct {
	Success   bool   `json:"success"`
	Output    string `json:"output"`
	ExitCode  int    `json:"exit_code"`
	Duration  int64  `json:"duration"`
	Language  string `json:"language,omitempty"`
	Mode      string `json:"mode"`
	Truncated bool   `json:"truncated,omitempty"`
}

type CodeRunFailure struct {
	Error    string `json:"error"`
	ExitCode int    `json:"exit_code"`
}

type CodeRunHandler struct {
	sandbox SandboxRunner
}

func NewCodeRunHandler(rootDir string) (middleware.Handler, error) {
	sandbox, err := lspexec.NewSandbox(rootDir)
	if err != nil {
		return nil, err
	}
	return newSandboxTool("code_run", sandbox, HandleCodeRun), nil
}

func NewCodeRunHandlerWithSandbox(sandbox SandboxRunner) middleware.Handler {
	return newSandboxTool("code_run", sandbox, HandleCodeRun)
}

func HandleCodeRun(ctx context.Context, sandbox SandboxRunner, params json.RawMessage) (any, error) {
	return CodeRunHandler{sandbox: sandbox}.Handle(ctx, params)
}

func (h CodeRunHandler) Handle(ctx context.Context, params json.RawMessage) (any, error) {
	if h.sandbox == nil {
		return nil, errors.New("code_run sandbox is nil")
	}
	req, err := decodeToolParams[CodeRunRequest](params, decodeRaw)
	if err != nil {
		return nil, fmt.Errorf("decode code_run request: %w", err)
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	switch mode {
	case "run":
		return h.handleRun(ctx, req)
	case "project_cmd":
		return h.handleProjectCommand(ctx, req)
	default:
		return nil, fmt.Errorf("unsupported code_run mode: %q", req.Mode)
	}
}

func (h CodeRunHandler) handleRun(ctx context.Context, req CodeRunRequest) (any, error) {
	language := strings.ToLower(strings.TrimSpace(req.Language))
	if strings.TrimSpace(req.Code) == "" {
		return nil, errors.New("code is required for run mode")
	}
	source, fileName, args, err := prepareSnippet(language, req.Code, autoWrapEnabled(req.AutoWrap))
	if err != nil {
		return nil, err
	}
	timeout := middleware.ClampTimeout(req.Timeout, middleware.TierExec, middleware.TierExec)
	request, cleanup, err := h.snippetRequest(fileName, source, args, timeout)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return h.execute(ctx, request, language, "run")
}

func (h CodeRunHandler) handleProjectCommand(ctx context.Context, req CodeRunRequest) (any, error) {
	if strings.TrimSpace(req.Command) == "" {
		return nil, errors.New("command is required for project_cmd mode")
	}
	timeout := middleware.ClampTimeout(req.Timeout, defaultCodeRunTimeout(), middleware.TierExec)
	request := h.sandbox.ShellRequest(req.Command, req.WorkDir, timeout)
	return h.execute(ctx, request, "", "project_cmd")
}

func (h CodeRunHandler) execute(ctx context.Context, request lspexec.Request, language string, mode string) (any, error) {
	return executeSandbox(ctx, h.sandbox, request, language, mode)
}

func (h CodeRunHandler) snippetRequest(fileName string, source string, args []string, timeout time.Duration) (lspexec.Request, func(), error) {
	tempDir, err := os.MkdirTemp(h.sandbox.RootDir(), ".mcp-lsp-run-*")
	if err != nil {
		return lspexec.Request{}, nil, fmt.Errorf("create temp dir: %w", err)
	}
	path := filepath.Join(tempDir, fileName)
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		_ = os.RemoveAll(tempDir)
		return lspexec.Request{}, nil, fmt.Errorf("write temp source: %w", err)
	}
	request := lspexec.Request{
		Args:    append([]string(nil), args...),
		WorkDir: tempDir,
		Timeout: timeout,
	}
	return request, func() { _ = os.RemoveAll(tempDir) }, nil
}

func prepareSnippet(language string, code string, autoWrap bool) (string, string, []string, error) {
	switch language {
	case "go":
		source := code
		if autoWrap {
			source = wrapGoSnippet(code)
		}
		return source, "main.go", []string{"go", "run", "main.go"}, nil
	case "javascript", "js":
		return code, "snippet.js", []string{"node", "snippet.js"}, nil
	case "typescript", "ts":
		return code, "snippet.ts", []string{"node", "--experimental-strip-types", "snippet.ts"}, nil
	default:
		return "", "", nil, fmt.Errorf("unsupported run language: %q", language)
	}
}

func wrapGoSnippet(code string) string {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" || strings.HasPrefix(trimmed, "package ") {
		return code
	}
	if strings.Contains(trimmed, "func main(") || strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "import(") {
		return "package main\n\n" + code
	}
	importBlock, body := splitLeadingGoImports(code)
	if importBlock == "" {
		return "package main\n\nfunc main() {\n" + indentBlock(code) + "\n}\n"
	}
	return "package main\n\n" + importBlock + "\n\nfunc main() {\n" + indentBlock(body) + "\n}\n"
}

func splitLeadingGoImports(code string) (string, string) {
	trimmed := strings.TrimSpace(code)
	if !hasLeadingGoImportBlock(trimmed) {
		return "", code
	}
	lines := strings.Split(strings.ReplaceAll(code, "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return "", code
	}
	importLines, bodyStart, ok := collectLeadingGoImportLines(lines)
	if !ok {
		return "", code
	}
	body := strings.Join(lines[bodyStart:], "\n")
	return strings.Join(importLines, "\n"), strings.TrimLeft(body, "\n")
}

func hasLeadingGoImportBlock(trimmed string) bool {
	return strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "import(")
}

func collectLeadingGoImportLines(lines []string) ([]string, int, bool) {
	importLines := make([]string, 0, len(lines))
	bodyStart := 0
	openParens := 0
	for idx, line := range lines {
		if idx == 0 {
			if !strings.HasPrefix(strings.TrimSpace(line), "import") {
				return nil, 0, false
			}
		} else if len(importLines) == 0 {
			return nil, 0, false
		}
		importLines = append(importLines, line)
		openParens += strings.Count(line, "(") - strings.Count(line, ")")
		bodyStart = idx + 1
		if importBlockClosed(line, openParens) {
			return importLines, bodyStart, true
		}
	}
	return importLines, bodyStart, len(importLines) > 0
}

func importBlockClosed(line string, openParens int) bool {
	if openParens > 0 {
		return false
	}
	return !strings.HasPrefix(strings.TrimSpace(line), "import (")
}

func indentBlock(code string) string {
	lines := strings.Split(strings.ReplaceAll(code, "\r\n", "\n"), "\n")
	for idx := range lines {
		if strings.TrimSpace(lines[idx]) == "" {
			lines[idx] = ""
			continue
		}
		lines[idx] = "\t" + lines[idx]
	}
	return strings.Join(lines, "\n")
}

func autoWrapEnabled(flag *bool) bool {
	return flag == nil || *flag
}

func defaultCodeRunTimeout() time.Duration {
	return middleware.TierExec
}

var goTestNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
