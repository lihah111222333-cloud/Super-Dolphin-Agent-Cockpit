package memory

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

func TestNewConfigFallsBackToProjectRoot(t *testing.T) {
	t.Setenv(envMemoryRoot, "")
	cfg := NewConfig(&platformconfig.Config{ProjectRoot: t.TempDir()})
	if cfg == nil || cfg.RootDir == "" {
		t.Fatalf("expected non-empty root dir, got %#v", cfg)
	}
}

func TestServiceEnsureRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memory-root")
	svc := NewService(&Config{RootDir: root}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.EnsureRoot(context.Background()); err != nil {
		t.Fatalf("EnsureRoot() error = %v", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", root, err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", root)
	}
}

func TestServiceEnsureRootUsesAutoMemPathOverride(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memory-root")
	override := filepath.Join(t.TempDir(), "override", "memory")
	svc := NewService(&Config{
		RootDir:             root,
		ProjectRoot:         filepath.Join(t.TempDir(), "project"),
		AutoMemPathOverride: override,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.EnsureRoot(context.Background()); err != nil {
		t.Fatalf("EnsureRoot() error = %v", err)
	}
	info, err := os.Stat(override)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", override, err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", override)
	}
}

func TestRootManagerEnsureRootDelegatesToService(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memory-root-manager")
	svc := NewService(&Config{RootDir: root}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	manager := NewRootManager(svc)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := manager.EnsureRoot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("EnsureRoot() error = %v, want %v", err, context.Canceled)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %q to stay absent, got err=%v", root, err)
	}
	if got := manager.RootDir(); got != root {
		t.Fatalf("RootDir() = %q, want %q", got, root)
	}
}

func TestRegisterMemoryHooksSubscribesThreadStoppedAsync(t *testing.T) {
	root := newTestMemoryRoot(t)
	store := newTestDiskStore(t, root)
	written, err := store.Create(testMemoryEntry(
		"Stop hook seed note",
		"seed",
		MemoryTypeProject,
		"Stop hook seed note\nWhy: created before stop.",
	))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	dispatcher := event.NewDispatcher()
	hooks := NewMemoryLifecycleHooks(&Config{
		Enabled:       true,
		ExtractOnStop: true,
		RootDir:       root,
	}, NewAutoDreamConsolidator(NewMemoryExtractor()), slog.New(slog.NewTextHandler(io.Discard, nil)))

	started := make(chan struct{})
	release := make(chan struct{})
	hooks.extractFn = func(_ context.Context, prompt string) (string, error) {
		if prompt == "" {
			t.Fatal("extract prompt should not be empty")
		}
		close(started)
		<-release
		return `{"memories":[{"content":"Stop hook seed note\nWhy: preserve durable stop-hook output.\nHow to apply: keep this note consolidated after thread shutdown.","type":"project","tags":["stop-hook"]}]}`, nil
	}

	lifecycle := &testLifecycle{}
	registerMemoryHooks(memoryHookParams{
		Lifecycle:  lifecycle,
		Dispatcher: dispatcher,
		Hooks:      hooks,
	})
	if len(lifecycle.hooks) != 1 {
		t.Fatalf("len(lifecycle.hooks) = %d, want 1", len(lifecycle.hooks))
	}
	if err := lifecycle.hooks[0].OnStart(context.Background()); err != nil {
		t.Fatalf("OnStart() error = %v", err)
	}
	defer func() {
		if err := lifecycle.hooks[0].OnStop(context.Background()); err != nil {
			t.Fatalf("OnStop() error = %v", err)
		}
	}()

	published := make(chan struct{})
	go func() {
		event.Publish(dispatcher, threaddto.Stopped{ThreadID: "thread-1"})
		close(published)
	}()

	select {
	case <-published:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("thread stopped publish blocked; want async extract")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("extract hook not triggered on thread stopped")
	}

	close(release)
	waitForExtractedContent(t, written.FilePath, "How to apply: keep this note consolidated")
}

type testLifecycle struct {
	hooks []fx.Hook
}

func (l *testLifecycle) Append(hook fx.Hook) {
	l.hooks = append(l.hooks, hook)
}

func waitForExtractedContent(t *testing.T, path, needle string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entry, err := readMemoryEntryFile(path)
		if err == nil && strings.Contains(entry.Content, needle) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	entry, err := readMemoryEntryFile(path)
	t.Fatalf("readMemoryEntryFile(%q) final err=%v content=%q, want %q", path, err, entry.Content, needle)
}
