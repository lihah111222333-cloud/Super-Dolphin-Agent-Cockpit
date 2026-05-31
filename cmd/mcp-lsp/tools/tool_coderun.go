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

	lspexec "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/exec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/multilsp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
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

type CodeRunTestRequest struct {
	TestFunc string `json:"test_func"`
	TestPkg  string `json:"test_pkg,omitempty"`
	Timeout  int    `json:"timeout,omitempty"`
}

type CodeRunResult struct {
	Success   bool   `json:"success"`
	Output    string `json:"output"`
	ExitCode  int    `json:"exit_code"`
	Duration  int64  `json:"duration"`
	Language  string `json:"language,omitempty"`
	Mode      string `json:"mode"`
	Truncated bool   `json:"truncated,omitempty"`
	Hint      string `json:"hint,omitempty"`
}

type CodeRunFailure struct {
	Success  bool   `json:"success"`
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

func NewCodeRunTestHandler(rootDir string) (middleware.Handler, error) {
	sandbox, err := lspexec.NewSandbox(rootDir)
	if err != nil {
		return nil, err
	}
	return newSandboxTool("code_run_test", sandbox, HandleCodeRunTest), nil
}

func NewCodeRunTestHandlerWithSandbox(sandbox SandboxRunner) middleware.Handler {
	return newSandboxTool("code_run_test", sandbox, HandleCodeRunTest)
}

func HandleCodeRun(ctx context.Context, sandbox SandboxRunner, params json.RawMessage) (any, error) {
	return CodeRunHandler{sandbox: sandbox}.Handle(ctx, params)
}

func HandleCodeRunTest(ctx context.Context, sandbox SandboxRunner, params json.RawMessage) (any, error) {
	return CodeRunHandler{sandbox: sandbox}.HandleTest(ctx, params)
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

func (h CodeRunHandler) HandleTest(ctx context.Context, params json.RawMessage) (any, error) {
	if h.sandbox == nil {
		return nil, errors.New("code_run_test sandbox is nil")
	}
	req, err := decodeToolParams[CodeRunTestRequest](params, decodeRaw)
	if err != nil {
		return nil, fmt.Errorf("decode code_run_test request: %w", err)
	}
	testFunc := strings.TrimSpace(req.TestFunc)
	if testFunc == "" {
		return nil, errors.New("test_func is required")
	}
	if !goTestNamePattern.MatchString(testFunc) {
		return nil, fmt.Errorf("test_func must contain only letters, numbers, and underscores: %q", req.TestFunc)
	}
	workDir, pkgArgs, err := goTestPackageWorkDir(ctx, req.TestPkg)
	if err != nil {
		return nil, err
	}
	timeout := middleware.ClampTimeout(req.Timeout, middleware.TierExec, middleware.TierExec)
	args := []string{"go", "test", "-run", "^" + regexp.QuoteMeta(testFunc) + "$"}
	args = append(args, pkgArgs...)
	request := lspexec.Request{
		Args:      args,
		WorkDir:   workDir,
		Timeout:   timeout,
		TraceTool: "code_run_test",
		TraceMode: "test",
	}
	return h.execute(ctx, request, "go", "test")
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
	request, cleanup, err := h.snippetRequest(ctx, fileName, source, args, timeout)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	request.TraceTool = "code_run"
	request.TraceMode = "run"
	return h.execute(ctx, request, language, "run")
}

func (h CodeRunHandler) handleProjectCommand(ctx context.Context, req CodeRunRequest) (any, error) {
	if strings.TrimSpace(req.Command) == "" {
		return nil, errors.New("command is required for project_cmd mode")
	}
	req.WorkDir = strings.TrimSpace(req.WorkDir)
	if req.WorkDir == "" {
		root, err := toolWorkspaceRoot(ctx)
		if err != nil {
			return nil, err
		}
		req.WorkDir = root
	} else if filepath.IsAbs(req.WorkDir) {
		var err error
		ctx, req.WorkDir, err = contextWithExplicitAbsoluteWorkDir(ctx, req.WorkDir)
		if err != nil {
			return nil, err
		}
	} else if !filepath.IsAbs(req.WorkDir) {
		root, err := toolWorkspaceRoot(ctx)
		if err != nil {
			return nil, err
		}
		req.WorkDir = filepath.Join(root, req.WorkDir)
	}
	timeout := middleware.ClampTimeout(req.Timeout, defaultCodeRunTimeout(), middleware.TierExec)
	request := h.sandbox.ShellRequest(req.Command, req.WorkDir, timeout)
	request.TraceTool = "code_run"
	request.TraceMode = "project_cmd"
	return h.execute(ctx, request, "", "project_cmd")
}

func contextWithExplicitAbsoluteWorkDir(ctx context.Context, workDir string) (context.Context, string, error) {
	normalized, err := normalizeExplicitAbsoluteWorkDir(workDir)
	if err != nil {
		return ctx, "", err
	}
	scope, _ := common.ToolScopeFromContext(ctx)
	scope.CWD = normalized
	scope.WorkspaceRoots = append(scope.WorkspaceRoots, normalized)
	if strings.TrimSpace(scope.Family) == "" {
		scope.Family = "lsp"
	}
	return common.WithToolScope(ctx, scope), normalized, nil
}

func normalizeExplicitAbsoluteWorkDir(workDir string) (string, error) {
	trimmed := strings.TrimSpace(workDir)
	if trimmed == "" {
		return "", errors.New("work_dir is required")
	}
	if !filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("work_dir must be absolute: %q", workDir)
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve work_dir: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(absolute))
	if err != nil {
		return "", fmt.Errorf("resolve work_dir: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat work_dir: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("work_dir is not a directory: %s", resolved)
	}
	return filepath.Clean(resolved), nil
}

func (h CodeRunHandler) execute(ctx context.Context, request lspexec.Request, language string, mode string) (any, error) {
	return executeSandbox(ctx, h.sandbox, request, language, mode)
}

func (h CodeRunHandler) snippetRequest(ctx context.Context, fileName string, source string, args []string, timeout time.Duration) (lspexec.Request, func(), error) {
	tempRoot, err := toolWorkspaceRoot(ctx)
	if err != nil {
		return lspexec.Request{}, nil, err
	}
	tempDir, err := os.MkdirTemp(tempRoot, ".mcp-lsp-run-*")
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

var goImportHintRe = regexp.MustCompile(`\b([a-z][a-z0-9]*)\.[A-Z]`)

var goStdlibPackages = map[string]string{
	"fmt":      "fmt",
	"strings":  "strings",
	"strconv":  "strconv",
	"math":     "math",
	"sort":     "sort",
	"os":       "os",
	"io":       "io",
	"time":     "time",
	"regexp":   "regexp",
	"bytes":    "bytes",
	"bufio":    "bufio",
	"encoding": "encoding",
	"json":     "encoding/json",
	"xml":      "encoding/xml",
	"csv":      "encoding/csv",
	"http":     "net/http",
	"url":      "net/url",
	"filepath": "path/filepath",
	"path":     "path",
	"reflect":  "reflect",
	"errors":   "errors",
	"log":      "log",
	"sync":     "sync",
	"atomic":   "sync/atomic",
	"context":  "context",
	"rand":     "math/rand",
	"unicode":  "unicode",
	"utf8":     "unicode/utf8",
	"base64":   "encoding/base64",
	"hex":      "encoding/hex",
	"binary":   "encoding/binary",
	"hash":     "hash",
}

func wrapGoSnippet(code string) string {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" || strings.HasPrefix(trimmed, "package ") {
		return code
	}
	// Strip comments for scanning, keep original code for output.
	var scanLines []string
	for _, line := range strings.Split(trimmed, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "//") {
			scanLines = append(scanLines, line)
		}
	}
	scanCode := strings.Join(scanLines, "\n")
	imports := detectGoStdlibImports(scanCode)
	var sb strings.Builder
	sb.WriteString("package main\n\n")
	if len(imports) > 0 {
		sb.WriteString("import (\n")
		for _, imp := range imports {
			sb.WriteString("\t")
			fmt.Fprintf(&sb, "%q", imp)
			sb.WriteString("\n")
		}
		sb.WriteString(")\n\n")
	}
	if strings.Contains(trimmed, "func main(") {
		sb.WriteString(trimmed)
	} else {
		sb.WriteString("func main() {\n")
		sb.WriteString(trimmed)
		sb.WriteString("\n}\n")
	}
	return sb.String()
}

func detectGoStdlibImports(code string) []string {
	matches := goImportHintRe.FindAllStringSubmatch(code, -1)
	seen := make(map[string]bool, len(matches))
	var imports []string
	for _, m := range matches {
		alias := m[1]
		if seen[alias] {
			continue
		}
		seen[alias] = true
		if fullPath, ok := goStdlibPackages[alias]; ok {
			imports = append(imports, fullPath)
		}
	}
	return imports
}

func autoWrapEnabled(flag *bool) bool {
	return flag == nil || *flag
}

func defaultCodeRunTimeout() time.Duration {
	return middleware.TierExec
}

var goTestNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func goTestPackageWorkDir(ctx context.Context, testPkg string) (string, []string, error) {
	root, _, err := toolWorkspaceRoots(ctx)
	if err != nil {
		return "", nil, err
	}
	pkg := strings.TrimSpace(testPkg)
	if pkg == "" {
		pkg = "./..."
	}
	if filepath.IsAbs(pkg) {
		workDir := cleanGoTestPackagePath(pkg)
		if _, err := common.WorkspaceRootForPathFromContextStrict(ctx, workDir); err != nil {
			return "", nil, err
		}
		return workDir, []string{goTestPackageArgForAbsolute(pkg)}, nil
	}
	if !isRelativeGoTestPackage(pkg) {
		return "", nil, fmt.Errorf("test_pkg must be a relative ./ package pattern or an absolute path inside workspace roots: %q", testPkg)
	}
	candidate := filepath.Join(root, cleanGoTestPackagePath(pkg))
	if _, err := common.WorkspaceRootForPathFromContextStrict(ctx, candidate); err != nil {
		return "", nil, err
	}
	return resolveRelativeGoTestPackage(ctx, root, pkg, candidate)
}

func resolveRelativeGoTestPackage(ctx context.Context, root string, pkg string, candidate string) (string, []string, error) {
	info, err := multilsp.ResolveGoRoot(multilsp.GoRootRequest{
		CWD:      root,
		FilePath: candidate,
	})
	if err != nil {
		return "", nil, err
	}
	if info.ModuleRoot != "" && pathWithinGoTestRoot(info.ModuleRoot, candidate) {
		return goTestPackageSelection(ctx, info.ModuleRoot, candidate, pkg)
	}
	if isRelativeGoTestAllPackage(pkg) {
		return resolveRelativeGoTestAllPackage(ctx, root, pkg, info)
	}
	if len(info.ModuleRoots) == 1 {
		moduleRoot := info.ModuleRoots[0]
		moduleCandidate := filepath.Join(moduleRoot, cleanGoTestPackagePath(pkg))
		return goTestPackageSelection(ctx, moduleRoot, moduleCandidate, pkg)
	}
	if len(info.ModuleRoots) > 1 {
		return "", nil, ambiguousGoTestPackageError(pkg, info.ModuleRoots)
	}
	return root, []string{normalizeRelativeGoTestPackageArg(pkg)}, nil
}

func resolveRelativeGoTestAllPackage(ctx context.Context, root string, pkg string, info multilsp.GoRootInfo) (string, []string, error) {
	if info.ModuleRoot != "" {
		return goTestPackageSelection(ctx, info.ModuleRoot, info.ModuleRoot, pkg)
	}
	switch len(info.ModuleRoots) {
	case 0:
		return root, []string{normalizeRelativeGoTestPackageArg(pkg)}, nil
	case 1:
		moduleRoot := info.ModuleRoots[0]
		return goTestPackageSelection(ctx, moduleRoot, moduleRoot, pkg)
	default:
		if info.GoWorkPath == "" {
			return "", nil, ambiguousGoTestPackageError(pkg, info.ModuleRoots)
		}
		workDir := info.WorkspaceRoot
		if workDir == "" {
			workDir = root
		}
		if err := validateGoTestWorkDir(ctx, workDir); err != nil {
			return "", nil, err
		}
		args, err := goTestWorkspaceModuleArgs(workDir, info.ModuleRoots)
		if err != nil {
			return "", nil, err
		}
		return workDir, args, nil
	}
}

func goTestPackageSelection(ctx context.Context, workDir string, candidate string, pkg string) (string, []string, error) {
	if err := validateGoTestWorkDir(ctx, workDir); err != nil {
		return "", nil, err
	}
	args, err := goTestPackageArgsRelativeToWorkDir(workDir, candidate, pkg)
	if err != nil {
		return "", nil, err
	}
	return workDir, args, nil
}

func validateGoTestWorkDir(ctx context.Context, workDir string) error {
	_, err := common.WorkspaceRootForPathFromContextStrict(ctx, workDir)
	return err
}

func goTestPackageArgsRelativeToWorkDir(workDir string, candidate string, pkg string) ([]string, error) {
	normalizedWorkDir, normalizedCandidate, err := normalizeGoTestPackageRelPaths(workDir, candidate)
	if err != nil {
		return nil, err
	}
	if !pathWithinGoTestRoot(normalizedWorkDir, normalizedCandidate) {
		return nil, fmt.Errorf("test_pkg %q resolves outside Go module root %s", pkg, workDir)
	}
	rel, err := filepath.Rel(normalizedWorkDir, normalizedCandidate)
	if err != nil {
		return nil, fmt.Errorf("resolve test_pkg relative to module root: %w", err)
	}
	if relativePathEscapesRoot(rel) {
		return nil, fmt.Errorf("test_pkg %q resolves outside Go module root %s", pkg, workDir)
	}
	return []string{formatGoTestPackageArg(rel, pkg)}, nil
}

func normalizeGoTestPackageRelPaths(workDir string, candidate string) (string, string, error) {
	normalizedWorkDir, err := platformshared.NormalizeAbsolutePath(workDir)
	if err != nil {
		return "", "", fmt.Errorf("normalize Go test work dir: %w", err)
	}
	normalizedCandidate, err := platformshared.NormalizeAbsolutePath(candidate)
	if err != nil {
		return "", "", fmt.Errorf("normalize Go test package path: %w", err)
	}
	return normalizedWorkDir, normalizedCandidate, nil
}

func formatGoTestPackageArg(rel string, pkg string) string {
	arg := filepath.ToSlash(filepath.Clean(rel))
	if arg == "." {
		if isRelativeGoTestAllPackage(pkg) {
			return "./..."
		}
		return "."
	}
	if !strings.HasPrefix(arg, "./") {
		arg = "./" + arg
	}
	if isRelativeGoTestAllPackage(pkg) {
		arg = strings.TrimSuffix(arg, "/") + "/..."
	}
	return arg
}

func relativePathEscapesRoot(rel string) bool {
	cleaned := filepath.ToSlash(filepath.Clean(rel))
	return cleaned == ".." || strings.HasPrefix(cleaned, "../")
}

func goTestWorkspaceModuleArgs(workDir string, moduleRoots []string) ([]string, error) {
	args := make([]string, 0, len(moduleRoots))
	for _, moduleRoot := range moduleRoots {
		moduleArgs, err := goTestPackageArgsRelativeToWorkDir(workDir, moduleRoot, "./...")
		if err != nil {
			return nil, err
		}
		args = append(args, moduleArgs...)
	}
	return args, nil
}

func pathWithinGoTestRoot(root string, target string) bool {
	return platformshared.ContainsPath(root, target)
}

func isRelativeGoTestAllPackage(pkg string) bool {
	return strings.HasSuffix(filepath.ToSlash(strings.TrimSpace(pkg)), "/...")
}

func ambiguousGoTestPackageError(pkg string, moduleRoots []string) error {
	return fmt.Errorf(
		"test_pkg %q is ambiguous across multiple Go modules; use a module-relative package path or an absolute package path inside one module: [%s]",
		pkg,
		strings.Join(moduleRoots, ", "),
	)
}

func isRelativeGoTestPackage(pkg string) bool {
	slashed := filepath.ToSlash(strings.TrimSpace(pkg))
	if slashed == "." || strings.HasPrefix(slashed, "./") {
		cleaned := filepath.ToSlash(filepath.Clean(pkg))
		return cleaned != ".." && !strings.HasPrefix(cleaned, "../")
	}
	return false
}

func normalizeRelativeGoTestPackageArg(pkg string) string {
	cleaned := filepath.ToSlash(filepath.Clean(pkg))
	if cleaned == "." {
		return "."
	}
	if cleaned == "..." {
		return "./..."
	}
	if strings.HasPrefix(cleaned, ".") {
		return cleaned
	}
	return "./" + cleaned
}

func cleanGoTestPackagePath(pkg string) string {
	normalized := filepath.FromSlash(strings.TrimSpace(pkg))
	if strings.HasSuffix(filepath.ToSlash(normalized), "/...") {
		normalized = filepath.FromSlash(strings.TrimSuffix(filepath.ToSlash(normalized), "/..."))
	}
	if normalized == "" {
		return "."
	}
	return filepath.Clean(normalized)
}

func goTestPackageArgForAbsolute(pkg string) string {
	if strings.HasSuffix(filepath.ToSlash(strings.TrimSpace(pkg)), "/...") {
		return "./..."
	}
	return "."
}

func (r CodeRunResult) ToPlainText() string {
	var sb strings.Builder
	status := "SUCCESS"
	if !r.Success {
		status = "FAILED"
	}
	sb.WriteString(fmt.Sprintf("Code Run Status: %s (Exit Code: %d, Duration: %dms)\n", status, r.ExitCode, r.Duration))
	if r.Mode != "" {
		sb.WriteString(fmt.Sprintf("Mode: %s", r.Mode))
		if r.Language != "" {
			sb.WriteString(fmt.Sprintf(", Language: %s", r.Language))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\nOutput:\n")
	sb.WriteString("```\n")
	sb.WriteString(r.Output)
	if !strings.HasSuffix(r.Output, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("```\n")
	if r.Truncated {
		sb.WriteString("Warning: output was truncated due to buffer limits.\n")
	}
	return strings.TrimSpace(sb.String())
}
