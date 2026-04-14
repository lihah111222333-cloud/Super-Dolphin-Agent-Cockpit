package memory

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
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
	svc := NewService(serviceParams{
		Config: &Config{RootDir: root},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
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
	svc := NewService(serviceParams{
		Config: &Config{
			RootDir:             root,
			ProjectRoot:         filepath.Join(t.TempDir(), "project"),
			AutoMemPathOverride: override,
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
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
	svc := NewService(serviceParams{
		Config: &Config{RootDir: root},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
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

func TestAutoDreamServiceExposesDreamTaskLifecycle(t *testing.T) {
	root := newTestMemoryRoot(t)
	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, RootDir: root},
		NewAutoDreamConsolidator(NewMemoryExtractor()),
		nil,
		nil,
		nil,
		nil,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)
	taskCtx, started := hooks.startDreamTask("thread-1")
	if !started {
		t.Fatal("startDreamTask() = false, want true")
	}
	hooks.setDreamTaskPhase(dreamTaskPhaseUpdating)

	svc := NewService(serviceParams{
		Config: &Config{RootDir: root},
		Hooks:  hooks,
	})
	got := svc.GetDreamTaskStatus()
	if !got.Running || got.ThreadID != "thread-1" || got.Phase != dreamTaskPhaseUpdating {
		t.Fatalf("GetDreamTaskStatus() = %#v", got)
	}
	if err := svc.KillDreamTask(); err != nil {
		t.Fatalf("KillDreamTask() error = %v", err)
	}
	select {
	case <-taskCtx.Done():
	default:
		t.Fatal("KillDreamTask() did not cancel the dream task context")
	}
	hooks.finishDreamTask()
	if got := svc.GetDreamTaskStatus(); got.Running {
		t.Fatalf("GetDreamTaskStatus() after finish = %#v, want idle", got)
	}
	if err := svc.KillDreamTask(); !errors.Is(err, ErrDreamTaskNotRunning) {
		t.Fatalf("KillDreamTask() after finish error = %v, want %v", err, ErrDreamTaskNotRunning)
	}
}

func TestRegisterMemoryHooksSubscribesTurnCompletedAsync(t *testing.T) {
	root := newTestMemoryRoot(t)
	dispatcher := event.NewDispatcher()
	hooks := newMemoryLifecycleHooks(&Config{
		Enabled:       true,
		ExtractOnStop: true,
		RootDir:       root,
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)),
		historyStub{messages: []providerdto.Message{{Role: "user", Content: "Please keep build commands guarded in this repo."}}},
		threadLookupStub{thread: &threadstore.Thread{
			ThreadID:       "thread-1",
			ConfigOverride: mustStoredRuntimeConfig(t, map[string]any{"threadKind": "main"}),
		}},
		nil,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)

	started := make(chan struct{})
	release := make(chan struct{})
	hooks.extractFn = func(_ context.Context, prompt string) (string, error) {
		if prompt == "" {
			t.Fatal("extract prompt should not be empty")
		}
		if !strings.Contains(prompt, "Conversation transcript:") {
			t.Fatalf("extract prompt missing transcript: %q", prompt)
		}
		close(started)
		<-release
		return `{"memories":[{"content":"Keep build commands guarded in this repo.","type":"project","tags":["build"]}]}`, nil
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
		event.Publish(dispatcher, turndto.TurnCompleted{
			TurnHeader: shareddto.TurnHeader{
				AgentHeader:  shareddto.AgentHeader{ThreadHeader: shareddto.ThreadHeader{ThreadID: "thread-1"}},
				TurnIDHeader: shareddto.TurnIDHeader{TurnID: "turn-1"},
			},
			Success: true,
		})
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
		t.Fatal("extract hook not triggered on turn completed")
	}

	close(release)
	waitForExtractedMemory(t, root, "Keep build commands guarded in this repo.")
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

func waitForExtractedMemory(t *testing.T, root, needle string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := scanMemoryEntries(root)
		if err == nil {
			for _, entry := range entries {
				if strings.Contains(entry.Content, needle) {
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	entries, err := scanMemoryEntries(root)
	t.Fatalf("scanMemoryEntries(%q) final err=%v entries=%#v, want %q", root, err, entries, needle)
}

type historyStub struct {
	messages []providerdto.Message
}

func (s historyStub) ReadHistory(context.Context, string, int) ([]providerdto.Message, error) {
	return append([]providerdto.Message(nil), s.messages...), nil
}

type threadLookupStub struct {
	thread *threadstore.Thread
}

func (s threadLookupStub) GetByThreadID(context.Context, string) (*threadstore.Thread, error) {
	return s.thread, nil
}

func mustStoredRuntimeConfig(t *testing.T, runtime map[string]any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"runtime": runtime})
	if err != nil {
		t.Fatalf("json.Marshal(runtime) error = %v", err)
	}
	return payload
}
