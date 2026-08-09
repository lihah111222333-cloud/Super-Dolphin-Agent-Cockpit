// Package main 是 mcp-lsp sidecar 进程的入口，通过 MCP stdio 协议暴露 LSP 工具能力。
package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

// TestMain 让 mcp-lsp 测试二进制复用生产监管入口，进程型测试覆盖真实 Darwin 路径。
func TestMain(m *testing.M) {
	if handled, exitCode := hiddenexec.RunProcessSupervisorIfRequested(os.Args); handled {
		os.Exit(exitCode)
	}
	os.Exit(m.Run())
}

func TestOrphanWatchdogStatus(t *testing.T) {
	t.Parallel()

	t.Run("ppid not 1 returns nil", func(t *testing.T) {
		t.Parallel()
		runner := &orphanWatchdogRunner{}
		err := runner.checkOrphanStatus(
			func() int { return 100 },
			func() (string, error) { return "/valid/path", nil },
			func(string) (os.FileInfo, error) { return nil, nil },
		)
		if err != nil {
			t.Fatalf("expected nil error when PPID!=1, got: %v", err)
		}
	})

	t.Run("ppid 1 with valid cwd returns nil", func(t *testing.T) {
		t.Parallel()
		runner := &orphanWatchdogRunner{}
		err := runner.checkOrphanStatus(
			func() int { return 1 },
			func() (string, error) { return "/valid/path", nil },
			func(string) (os.FileInfo, error) { return nil, nil },
		)
		if err != nil {
			t.Fatalf("expected nil error when PPID=1 and CWD valid, got: %v", err)
		}
	})

	t.Run("ppid 1 with cwd error triggers self termination", func(t *testing.T) {
		t.Parallel()
		runner := &orphanWatchdogRunner{}
		err := runner.checkOrphanStatus(
			func() int { return 1 },
			func() (string, error) { return "", os.ErrNotExist },
			func(string) (os.FileInfo, error) { return nil, nil },
		)
		if err == nil {
			t.Fatal("expected error when CWD get fails, got nil")
		}
		if !errors.Is(err, errOrphanProcessSelfTerminated) {
			t.Fatalf("expected errOrphanProcessSelfTerminated, got: %v", err)
		}
	})

	t.Run("ppid 1 with cwd stat error triggers self termination", func(t *testing.T) {
		t.Parallel()
		runner := &orphanWatchdogRunner{}
		err := runner.checkOrphanStatus(
			func() int { return 1 },
			func() (string, error) { return "/deleted/path", nil },
			func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
		)
		if err == nil {
			t.Fatal("expected error when CWD stat fails, got nil")
		}
		if !errors.Is(err, errOrphanProcessSelfTerminated) {
			t.Fatalf("expected errOrphanProcessSelfTerminated, got: %v", err)
		}
	})

	t.Run("ppid 1 with uncertain cwd probe does not authorize self termination", func(t *testing.T) {
		t.Parallel()
		runner := &orphanWatchdogRunner{}
		err := runner.checkOrphanStatus(
			func() int { return 1 },
			func() (string, error) { return "/protected/path", nil },
			func(string) (os.FileInfo, error) { return nil, os.ErrPermission },
		)
		if err == nil {
			t.Fatal("expected an observable probe error, got nil")
		}
		if errors.Is(err, errOrphanProcessSelfTerminated) {
			t.Fatalf("uncertain CWD probe authorized self termination: %v", err)
		}
	})
}

func TestOrphanWatchdogRunLoop(t *testing.T) {
	t.Parallel()

	t.Run("context cancel exits run loop", func(t *testing.T) {
		t.Parallel()
		runner := &orphanWatchdogRunner{
			interval: 10 * time.Millisecond,
			getPpid:  func() int { return 100 },
			getCwd:   func() (string, error) { return "/valid", nil },
			statPath: func(string) (os.FileInfo, error) { return nil, nil },
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := runner.Run(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
	})

	t.Run("ticker trigger returns orphan error", func(t *testing.T) {
		t.Parallel()
		runner := &orphanWatchdogRunner{
			interval: 10 * time.Millisecond,
			getPpid:  func() int { return 1 },
			getCwd:   func() (string, error) { return "/deleted", nil },
			statPath: func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
		}
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		err := runner.Run(ctx)
		if err == nil || !errors.Is(err, errOrphanProcessSelfTerminated) {
			t.Fatalf("expected errOrphanProcessSelfTerminated, got: %v", err)
		}
	})

	t.Run("uncertain probe remains observable without self termination", func(t *testing.T) {
		t.Parallel()
		runner := &orphanWatchdogRunner{
			interval: 5 * time.Millisecond,
			getPpid:  func() int { return 1 },
			getCwd:   func() (string, error) { return "/protected", nil },
			statPath: func(string) (os.FileInfo, error) { return nil, os.ErrPermission },
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()

		err := runner.Run(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("uncertain probe Run() error = %v, want context deadline", err)
		}
		if errors.Is(err, errOrphanProcessSelfTerminated) {
			t.Fatalf("uncertain probe self-terminated: %v", err)
		}
	})
}
