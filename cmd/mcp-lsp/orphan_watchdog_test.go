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
	if handled, exitCode := runWindowsGoplsBrokerIfRequested(os.Args); handled {
		os.Exit(exitCode)
	}
	os.Exit(m.Run())
}

func TestOrphanWatchdogParentProbe(t *testing.T) {
	t.Parallel()

	t.Run("parent alive and cwd valid returns nil", func(t *testing.T) {
		t.Parallel()
		runner := &orphanWatchdogRunner{}
		err := runner.checkOrphanStatus(
			func() int { return 100 },
			func(int) bool { return false },
			func() (string, error) { return "/valid/path", nil },
			func(string) (os.FileInfo, error) { return nil, nil },
		)
		if err != nil {
			t.Fatalf("expected nil error when parent alive and CWD valid, got: %v", err)
		}
	})

	t.Run("parent orphaned triggers self termination even with valid cwd", func(t *testing.T) {
		t.Parallel()
		runner := &orphanWatchdogRunner{}
		err := runner.checkOrphanStatus(
			func() int { return 1 },
			func(ppid int) bool { return ppid <= 1 },
			func() (string, error) { return "/valid/path", nil },
			func(string) (os.FileInfo, error) { return nil, nil },
		)
		if err == nil {
			t.Fatal("expected error when parent is orphaned, got nil")
		}
		if !errors.Is(err, errOrphanProcessSelfTerminated) {
			t.Fatalf("expected errOrphanProcessSelfTerminated, got: %v", err)
		}
	})

	t.Run("parent dead by probe triggers self termination", func(t *testing.T) {
		t.Parallel()
		runner := &orphanWatchdogRunner{}
		err := runner.checkOrphanStatus(
			func() int { return 99999 },
			func(int) bool { return true },
			func() (string, error) { return "/valid/path", nil },
			func(string) (os.FileInfo, error) { return nil, nil },
		)
		if err == nil {
			t.Fatal("expected error when parent probe reports dead, got nil")
		}
		if !errors.Is(err, errOrphanProcessSelfTerminated) {
			t.Fatalf("expected errOrphanProcessSelfTerminated, got: %v", err)
		}
	})
}

func TestOrphanWatchdogCwdProbe(t *testing.T) {
	t.Parallel()

	t.Run("parent alive with cwd get error triggers self termination", func(t *testing.T) {
		t.Parallel()
		runner := &orphanWatchdogRunner{}
		err := runner.checkOrphanStatus(
			func() int { return 100 },
			func(int) bool { return false },
			func() (string, error) { return "", os.ErrNotExist },
			func(string) (os.FileInfo, error) { return nil, nil },
		)
		if err == nil {
			t.Fatal("expected error when CWD get fails with ErrNotExist, got nil")
		}
		if !errors.Is(err, errOrphanProcessSelfTerminated) {
			t.Fatalf("expected errOrphanProcessSelfTerminated, got: %v", err)
		}
	})

	t.Run("parent alive with cwd stat error triggers self termination", func(t *testing.T) {
		t.Parallel()
		runner := &orphanWatchdogRunner{}
		err := runner.checkOrphanStatus(
			func() int { return 100 },
			func(int) bool { return false },
			func() (string, error) { return "/deleted/path", nil },
			func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
		)
		if err == nil {
			t.Fatal("expected error when CWD stat fails with ErrNotExist, got nil")
		}
		if !errors.Is(err, errOrphanProcessSelfTerminated) {
			t.Fatalf("expected errOrphanProcessSelfTerminated, got: %v", err)
		}
	})

	t.Run("parent alive with uncertain cwd probe does not authorize self termination", func(t *testing.T) {
		t.Parallel()
		runner := &orphanWatchdogRunner{}
		err := runner.checkOrphanStatus(
			func() int { return 100 },
			func(int) bool { return false },
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
			interval:         10 * time.Millisecond,
			getPpid:          func() int { return 100 },
			isParentOrphaned: func(int) bool { return false },
			getCwd:           func() (string, error) { return "/valid", nil },
			statPath:         func(string) (os.FileInfo, error) { return nil, nil },
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := runner.Run(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
	})

	t.Run("ticker trigger returns orphan error on parent dead", func(t *testing.T) {
		t.Parallel()
		runner := &orphanWatchdogRunner{
			interval:         10 * time.Millisecond,
			getPpid:          func() int { return 1 },
			isParentOrphaned: func(ppid int) bool { return ppid <= 1 },
			getCwd:           func() (string, error) { return "/valid", nil },
			statPath:         func(string) (os.FileInfo, error) { return nil, nil },
		}
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		err := runner.Run(ctx)
		if err == nil || !errors.Is(err, errOrphanProcessSelfTerminated) {
			t.Fatalf("expected errOrphanProcessSelfTerminated, got: %v", err)
		}
	})

	t.Run("ticker trigger returns orphan error on deleted cwd", func(t *testing.T) {
		t.Parallel()
		runner := &orphanWatchdogRunner{
			interval:         10 * time.Millisecond,
			getPpid:          func() int { return 100 },
			isParentOrphaned: func(int) bool { return false },
			getCwd:           func() (string, error) { return "/deleted", nil },
			statPath:         func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
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
			interval:         5 * time.Millisecond,
			getPpid:          func() int { return 100 },
			isParentOrphaned: func(int) bool { return false },
			getCwd:           func() (string, error) { return "/protected", nil },
			statPath:         func(string) (os.FileInfo, error) { return nil, os.ErrPermission },
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
