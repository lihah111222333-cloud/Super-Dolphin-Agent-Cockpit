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
)

const defaultOutputLimit = 256 * 1024

type Sandbox struct {
	rootDir     string
	outputLimit int
}

type Request struct {
	Args    []string
	Command string
	WorkDir string
	Env     []string
	Stdin   string
	Timeout time.Duration
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
	workDir, err := s.resolveWorkDir(req.WorkDir)
	if err != nil {
		return Result{}, err
	}
	runCtx, cancel := withTimeout(ctx, req.Timeout)
	defer cancel()

	command := osexec.CommandContext(runCtx, args[0], args[1:]...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- command.Wait()
	}()
	err = waitForCommand(runCtx, command, waitCh)
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
	shell := os.Getenv("SHELL")
	if strings.TrimSpace(shell) == "" {
		shell = "/bin/sh"
	}
	return Request{
		Args:    []string{shell, "-lc", command},
		Command: command,
		WorkDir: workDir,
		Timeout: timeout,
	}
}

func (s *Sandbox) resolveWorkDir(workDir string) (string, error) {
	if strings.TrimSpace(workDir) == "" {
		return s.rootDir, nil
	}
	candidate := workDir
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(s.rootDir, candidate)
	}
	normalized, err := normalizePath(candidate)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(s.rootDir, normalized)
	if err != nil {
		return "", fmt.Errorf("validate work_dir: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("work_dir must stay within %s", s.rootDir)
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
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
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
	status, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus)
	if ok {
		return status.ExitStatus()
	}
	return exitErr.ExitCode()
}

func waitForCommand(ctx context.Context, command *osexec.Cmd, waitCh <-chan error) error {
	select {
	case err := <-waitCh:
		return err
	case <-ctx.Done():
		killProcessGroup(command.Process)
		return <-waitCh
	}
}

func killProcessGroup(process *os.Process) {
	if process == nil {
		return
	}
	_ = syscall.Kill(-process.Pid, syscall.SIGKILL)
	_ = process.Kill()
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
