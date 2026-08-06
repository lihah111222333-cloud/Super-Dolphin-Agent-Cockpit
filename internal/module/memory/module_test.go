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
	"sync"
	"testing"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	rpcpkg "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	"go.uber.org/fx"
)

func TestNewConfigFallsBackToProjectRoot(t *testing.T) {
	t.Setenv(envMemoryRoot, "")
	cfg := NewConfig(&contract.Config{ProjectRoot: newTestGitProjectRoot(t)})
	if cfg == nil || cfg.RootDir == "" {
		t.Fatalf("expected non-empty root dir, got %#v", cfg)
	}
}

func TestServiceEnsureRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memory-root")
	svc := NewService(
		&Config{RootDir: root},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
		nil,
	)
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
	svc := NewService(
		&Config{
			RootDir:             root,
			ProjectRoot:         filepath.Join(t.TempDir(), "project"),
			AutoMemPathOverride: override,
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
		nil,
	)
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

func TestMemoryModuleInvalidProjectRootFailsConstruction(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memory-root")
	badProjectRoot := filepath.Join(t.TempDir(), "not-a-git-repo")
	if err := os.MkdirAll(badProjectRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", badProjectRoot, err)
	}

	var provider *MemoryContextProvider
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			func() *contract.Config { return &contract.Config{} },
			func() contract.PromptAssemblyService { return memoryPromptAssemblyStub{} },
			func() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) },
		),
		Module,
		fx.Replace(&Config{Enabled: true, RootDir: root, ProjectRoot: badProjectRoot}),
		fx.Populate(&provider),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err == nil || !strings.Contains(err.Error(), "resolve git root") {
		if err == nil {
			_ = app.Stop(context.Background())
		}
		t.Fatalf("app.Start(memory.Module) error = %v, want invalid memory project root to fail startup", err)
	}
}

func TestSetTeamMemoryRuntimeReadyRequiresManager(t *testing.T) {
	if err := setTeamMemoryRuntimeReady(nil, true); err == nil {
		t.Fatal("setTeamMemoryRuntimeReady(nil, true) error = nil, want required manager error")
	}
}

func TestServiceRunConsolidationWithoutHooks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memory-root")
	writeMemoryIndexFixture(t, root)
	consolidator := newAutoDreamConsolidator(NewMemoryExtractor(), func(context.Context, string) (string, error) {
		return "", nil
	})
	svc := NewService(
		&Config{RootDir: root},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		consolidator,
		nil,
	)
	if err := svc.RunConsolidation(context.Background()); err != nil {
		t.Fatalf("RunConsolidation() error = %v", err)
	}
}

func TestRootManagerEnsureRootDelegatesToService(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memory-root-manager")
	svc := NewService(
		&Config{RootDir: root},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
		nil,
	)
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
	taskCtx, started := hooks.startDreamTask(context.Background(), "thread-1")
	if !started {
		t.Fatal("startDreamTask() = false, want true")
	}
	hooks.setDreamTaskPhase(dreamTaskPhaseUpdating)

	svc := NewService(
		&Config{RootDir: root},
		nil,
		nil,
		hooks,
	)
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

func TestAutoDreamTaskInheritsParentCancellation(t *testing.T) {
	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, RootDir: newTestMemoryRoot(t)},
		NewAutoDreamConsolidator(NewMemoryExtractor()),
		nil,
		nil,
		nil,
		nil,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)
	parent, cancelParent := context.WithCancel(context.Background())
	taskCtx, started := hooks.startDreamTask(parent, "thread-1")
	if !started {
		t.Fatal("startDreamTask() = false, want true")
	}
	cancelParent()
	select {
	case <-taskCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not cancel dream task context")
	}
	hooks.finishDreamTask()

	canceledParent, cancelCanceledParent := context.WithCancel(context.Background())
	cancelCanceledParent()
	if taskCtx, started := hooks.startDreamTask(canceledParent, "thread-2"); started || taskCtx != nil {
		t.Fatalf("startDreamTask(canceled parent) = (%v, %v), want nil, false", taskCtx, started)
	}
}

func TestConsolidationHandlerDispatch(t *testing.T) {
	stub := &stubMemoryService{}
	server := rpcpkg.NewServer(rpcpkg.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewMemoryHandlers(memoryHandlerDeps{Service: stub}).Handlers)

	raw, err := server.Dispatch(context.Background(), "memory/consolidate", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch(memory/consolidate) error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(memory/consolidate) error = %v", err)
	}
	if !stub.runCalled {
		t.Fatal("RunConsolidation() was not called")
	}
	if got["status"] != "completed" {
		t.Fatalf("Dispatch(memory/consolidate) = %#v", got)
	}
}

func TestMemorySubscribersHandleTurnCompletedAsync(t *testing.T) {
	root := newTestMemoryRoot(t)
	dispatcher := event.NewDispatcher()
	hooks := newMemoryLifecycleHooks(&Config{
		Enabled:       true,
		ExtractOnStop: true,
		RootDir:       root,
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)),
		historyStub{messages: []providerdto.Message{{Role: "user", Content: "Please keep build commands guarded in this repo."}}},
		threadLookupStub{thread: &contract.ThreadMetadata{
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
		return `{"memories":[{"scope":"private","name":"Keep build commands guarded","description":"Keep build commands guarded in this repo.","content":"Keep build commands guarded in this repo.","type":"project","tags":["build"]}]}`, nil
	}

	spec := NewMemorySubscribers(nil, nil, nil, memorySubscriberParams{Hooks: hooks}).Spec
	cancel := spec.Register(dispatcher)
	if cancel == nil {
		t.Fatal("Register returned nil cancel")
	}
	defer func() {
		cancel()
		drainDreamTask(context.Background(), hooks)
	}()

	published := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		event.Publish(dispatcher, turndto.TurnCompleted{
			TurnHeader: shareddto.TurnHeader{
				AgentHeader:  shareddto.AgentHeader{ThreadHeader: shareddto.ThreadHeader{ThreadID: "thread-1"}},
				TurnIDHeader: shareddto.TurnIDHeader{TurnID: "turn-1"},
			},
			Success: true,
		})
		close(published)
	})

	select {
	case <-published:
		wg.Wait()
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

type memoryPromptAssemblyStub struct{}

func (memoryPromptAssemblyStub) AssembleStart(context.Context, contract.StartInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, nil
}

func (memoryPromptAssemblyStub) AssembleTurn(context.Context, contract.TurnInput) (contract.TurnAssembly, error) {
	return contract.TurnAssembly{}, nil
}

func (memoryPromptAssemblyStub) AssembleAgent(context.Context, contract.AgentInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, nil
}

func (memoryPromptAssemblyStub) Invalidate(context.Context, contract.InvalidateReason) error {
	return nil
}

func (s historyStub) ReadHistory(context.Context, string, int) ([]providerdto.Message, error) {
	return append([]providerdto.Message(nil), s.messages...), nil
}

type threadLookupStub struct {
	thread *contract.ThreadMetadata
}

func (s threadLookupStub) GetByThreadID(context.Context, string) (*contract.ThreadMetadata, error) {
	return s.thread, nil
}

func (s threadLookupStub) ListAll(context.Context) ([]contract.ThreadMetadata, error) {
	if s.thread == nil {
		return nil, nil
	}
	return []contract.ThreadMetadata{*s.thread}, nil
}

func mustStoredRuntimeConfig(t *testing.T, runtime map[string]any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"runtime": runtime})
	if err != nil {
		t.Fatalf("json.Marshal(runtime) error = %v", err)
	}
	return payload
}

type stubMemoryService struct {
	runCalled bool
	runErr    error
}

func (s *stubMemoryService) Config() Config                        { return Config{} }
func (s *stubMemoryService) RootDir() string                       { return "" }
func (s *stubMemoryService) EnsureRoot(context.Context) error      { return nil }
func (s *stubMemoryService) GetDreamTaskStatus() DreamTaskSnapshot { return DreamTaskSnapshot{} }
func (s *stubMemoryService) GetNestedIngestHealth() NestedIngestHealthSnapshot {
	return NestedIngestHealthSnapshot{}
}
func (s *stubMemoryService) KillDreamTask() error                    { return nil }
func (s *stubMemoryService) MemoryCoordinator() *diskLockCoordinator { return nil }
func (s *stubMemoryService) RunConsolidation(context.Context) error {
	s.runCalled = true

	return s.runErr
}
