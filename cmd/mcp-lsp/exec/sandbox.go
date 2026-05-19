package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const defaultOutputLimit = 256 * 1024

type Sandbox struct {
	rootDir     string
	outputLimit int
}

type Request struct {
	Args      []string
	Command   string
	WorkDir   string
	Env       []string
	Stdin     string
	Timeout   time.Duration
	TraceTool string
	TraceMode string
}

type Result struct {
	Output    string `json:"output"`
	ExitCode  int    `json:"exit_code"`
	Duration  int64  `json:"duration"`
	Truncated bool   `json:"truncated,omitempty"`
}

func NewSandbox(rootDir string) (*Sandbox, error) {
	if strings.TrimSpace(rootDir) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve sandbox root: %w", err)
		}
		rootDir = cwd
	}
	normalized, err := normalizePath(rootDir)
	if err != nil {
		return nil, err
	}
	return &Sandbox{
		rootDir:     normalized,
		outputLimit: defaultOutputLimit,
	}, nil
}

func (s *Sandbox) RootDir() string {
	if s == nil {
		return ""
	}
	return s.rootDir
}

func (s *Sandbox) Run(ctx context.Context, req Request) (Result, error) {
	if s == nil {
		return Result{}, errors.New("sandbox is nil")
	}
	args, err := normalizeArgs(req)
	if err != nil {
		return Result{}, err
	}
	workDir, err := s.resolveWorkDir(ctx, req.WorkDir)
	if err != nil {
		return Result{}, err
	}
	s.warnExecCWDTrace(ctx, req, args, workDir)
	runCtx, cancel := withTimeout(ctx, req.Timeout)
	defer cancel()

	command := osexec.CommandContext(runCtx, args[0], args[1:]...)
	setSandboxProcessAttrs(command)
	command.Dir = workDir
	command.Env = append(os.Environ(), req.Env...)
	writer := newLimitedBuffer(s.outputLimit)
	command.Stdout = writer
	command.Stderr = writer
	if strings.TrimSpace(req.Stdin) != "" {
		command.Stdin = strings.NewReader(req.Stdin)
	}

	start := time.Now()
	if err := command.Start(); err != nil {
		return Result{}, err
	}
	guard := attachSandboxGuard(command)
	defer guard.close()
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- command.Wait()
	}()
	err = waitForCommand(runCtx, command, waitCh, guard)
	result := Result{
		Output:    writer.String(),
		ExitCode:  exitCode(err),
		Duration:  time.Since(start).Milliseconds(),
		Truncated: writer.Truncated(),
	}
	if err == nil {
		return result, nil
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return result, fmt.Errorf("command timed out after %s", req.Timeout)
	}
	var exitErr *osexec.ExitError
	if errors.As(err, &exitErr) {
		return result, nil
	}
	return result, err
}

func (s *Sandbox) ShellRequest(command string, workDir string, timeout time.Duration) Request {
	return Request{
		Args:    shellRequestArgs(command),
		Command: command,
		WorkDir: workDir,
		Timeout: timeout,
	}
}

// effectiveRoot picks the workspace root that bounds work_dir.
// When the MCP toolbridge has injected a per-call _cwd into ctx (see
// internal/mcpserver/common/server.go + cmd/mcp-lsp/fx.go OnToolsCall),
// the sandbox MUST follow that cwd instead of the build-time s.rootDir,
// otherwise calls from agents bound to a different project root than the
// mcp-lsp startup directory get a stale "work_dir must stay within …"
// rejection even though the upstream binding has already authorised the
// target cwd.
func (s *Sandbox) effectiveRoot(ctx context.Context) (string, error) {
	if ctx != nil {
		root, err := common.WorkspaceRootFromContextStrict(ctx)
		if err != nil {
			return "", err
		}
		if normalized, err := normalizePath(strings.TrimSpace(root)); err == nil && normalized != "" {
			return normalized, nil
		}
	}
	return "", errors.New("strict context enforcement failed: missing context")
}

func (s *Sandbox) resolveWorkDir(ctx context.Context, workDir string) (string, error) {
	root, err := s.effectiveRootForWorkDir(ctx, workDir)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(workDir) == "" {
		return root, nil
	}
	candidate := workDir
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	normalized, err := normalizePath(candidate)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, normalized)
	if err != nil {
		return "", fmt.Errorf("validate work_dir: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("work_dir must stay within %s", root)
	}
	info, err := os.Stat(normalized)
	if err != nil {
		return "", fmt.Errorf("stat work_dir: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("work_dir is not a directory: %s", normalized)
	}
	return normalized, nil
}

func (s *Sandbox) effectiveRootForWorkDir(ctx context.Context, workDir string) (string, error) {
	if strings.TrimSpace(workDir) != "" && filepath.IsAbs(workDir) {
		root, err := common.WorkspaceRootForPathFromContextStrict(ctx, workDir)
		if err == nil && strings.TrimSpace(root) != "" {
			return normalizePath(root)
		}
		return "", err
	}
	return s.effectiveRoot(ctx)
}

func (s *Sandbox) warnExecCWDTrace(ctx context.Context, req Request, args []string, workDir string) {
	pkglogger.Warn("mcp-lsp: sandbox exec cwd trace",
		"tool", req.TraceTool,
		"mode", req.TraceMode,
		"root_dir", s.rootDir,
		"meta_cwd", contextCWD(ctx),
		"effective_root", func() string { r, _ := s.effectiveRoot(ctx); return r }(),
		"requested_work_dir", req.WorkDir,
		"exec_dir", workDir,
		"command", req.Command,
		"args", args,
		"timeout_ms", req.Timeout.Milliseconds(),
	)
}

func contextCWD(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	cwd, _ := ctx.Value(common.CwdContextKey).(string)
	return strings.TrimSpace(cwd)
}

func normalizeArgs(req Request) ([]string, error) {
	if len(req.Args) > 0 {
		return append([]string(nil), req.Args...), nil
	}
	if strings.TrimSpace(req.Command) == "" {
		return nil, errors.New("command is required")
	}
	return []string{req.Command}, nil
}

func withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return platformconfig.WithTimeout(ctx, timeout)
}

func normalizePath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path: %w", err)
	}
	cleaned := filepath.Clean(absPath)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		cleaned = filepath.Clean(resolved)
	}
	return cleaned, nil
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *osexec.ExitError
	if !errors.As(err, &exitErr) {
		return -1
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if ok {
		return status.ExitStatus()
	}
	return exitErr.ExitCode()
}

func waitForCommand(ctx context.Context, command *osexec.Cmd, waitCh <-chan error, guard *sandboxGuard) error {
	select {
	case err := <-waitCh:
		return err
	case <-ctx.Done():
		killSandboxProcess(command.Process, guard)
		return <-waitCh
	}
}

type limitedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func newLimitedBuffer(limit int) *limitedBuffer {
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	return &limitedBuffer{limit: limit}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	written := len(p)
	if b.truncated {
		return written, nil
	}
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = true
		return written, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, _ = b.buffer.Write(p)
	return written, nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.truncated {
		return b.buffer.String()
	}
	var out strings.Builder
	_, _ = io.Copy(&out, bytes.NewReader(b.buffer.Bytes()))
	out.WriteString("\n... output truncated ...")
	return out.String()
}

func (b *limitedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}
