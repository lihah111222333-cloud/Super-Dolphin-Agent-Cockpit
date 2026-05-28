package thread

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/module/threadprompt"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

// TestFoldRouterOutputIntoAssemblyInput_PreservesBlocks is a regression test
// for the match_when template-sections bug: runPendingSpawn takes a snapshot
// of *req BEFORE resolveRoutedPrompt runs, so any field the router writes to
// *req must be explicitly folded back into the assembly input. The bug was
// that BaseInstructionBlocks was missing from the fold-back, so a template
// with sections in the DB would have its sections silently dropped and the
// assembler would see an empty slice, making user edits to sections appear
// to have no effect on Claude CLI.
func TestFoldRouterOutputIntoAssemblyInput_PreservesBlocks(t *testing.T) {
	t.Parallel()

	assemblyInput := contract.StartInput{
		BaseInstructions:      "stale",
		DeveloperInstructions: "stale-dev",
		BaseInstructionBlocks: nil, // pre-router snapshot has no blocks yet
	}
	req := &StartRequest{
		BaseInstructions:      "router-picked",
		DeveloperInstructions: "router-dev",
		BaseInstructionBlocks: []contract.BaseInstructionBlock{
			{Key: "identity", Region: contract.PromptRegionStatic, Ordinal: 0, Body: "You are a super-Dolphin"},
			{Key: "zh_hint", Region: contract.PromptRegionDynamic, Ordinal: 0, Body: "Default to 中文"},
		},
	}

	foldRouterOutputIntoAssemblyInput(&assemblyInput, req)

	if assemblyInput.BaseInstructions != "router-picked" {
		t.Fatalf("BaseInstructions not folded: got %q", assemblyInput.BaseInstructions)
	}
	if assemblyInput.DeveloperInstructions != "router-dev" {
		t.Fatalf("DeveloperInstructions not folded: got %q", assemblyInput.DeveloperInstructions)
	}
	if len(assemblyInput.BaseInstructionBlocks) != 2 {
		t.Fatalf("BaseInstructionBlocks not folded: got %d blocks, want 2", len(assemblyInput.BaseInstructionBlocks))
	}
	if assemblyInput.BaseInstructionBlocks[0].Body != "You are a super-Dolphin" {
		t.Fatalf("identity block body lost: got %q", assemblyInput.BaseInstructionBlocks[0].Body)
	}
	// Ensure we copied rather than aliased the slice: mutating the source should
	// not ripple into the assembly input. This matches the append(nil, ...)
	// pattern everywhere else in the module.
	req.BaseInstructionBlocks[0].Body = "mutated-after-fold"
	if assemblyInput.BaseInstructionBlocks[0].Body == "mutated-after-fold" {
		t.Fatalf("fold aliased slice; mutation leaked into assembly input")
	}
}

// TestSpawnIfNeeded_SkipsStoppedThread asserts that SpawnIfNeeded returns
// (false, nil) for a thread whose DB row has PendingLaunch=true but
// Status="stopped". This guards against the race where Stop marks status
// but a queued turn/start could still trigger a spawn.
func TestSpawnIfNeeded_SkipsStoppedThread(t *testing.T) {
	t.Parallel()

	store := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:      "thread-pending-1",
		Status:        statusStopped,
		PendingLaunch: true,
	}}
	svc := &service{threadStore: store}

	launched, _, err := svc.SpawnIfNeeded(context.Background(), "thread-pending-1", "hello", "")
	if err != nil {
		t.Fatalf("SpawnIfNeeded() error = %v, want nil", err)
	}
	if launched {
		t.Fatal("SpawnIfNeeded() launched = true, want false for stopped thread")
	}
}

// TestSpawnIfNeeded_SkipsArchivedThread asserts the same guard for
// Status="archived" threads.
func TestSpawnIfNeeded_SkipsArchivedThread(t *testing.T) {
	t.Parallel()

	store := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:      "thread-pending-2",
		Status:        statusArchived,
		PendingLaunch: true,
	}}
	svc := &service{threadStore: store}

	launched, _, err := svc.SpawnIfNeeded(context.Background(), "thread-pending-2", "hello", "")
	if err != nil {
		t.Fatalf("SpawnIfNeeded() error = %v, want nil", err)
	}
	if launched {
		t.Fatal("SpawnIfNeeded() launched = true, want false for archived thread")
	}
}

func TestStartPendingLaunchSkipsPromptAssemblyPreflight(t *testing.T) {
	t.Parallel()

	store := &stubThreadStore{}
	startCalled := false
	svc := &service{
		threadStore: store,
		promptAssembly: errorPromptAssembly{
			err: errors.New("ClaudeMd candidate containment: safe read: path escapes root"),
		},
		starter: &startOnlySessionStarter{onStart: func(context.Context, dto.StartSessionRequest) (contract.Session, error) {
			startCalled = true
			return nil, errors.New("provider must not start for pending launch")
		}},
	}

	result, err := svc.Start(context.Background(), StartRequest{
		AgentID:    "agent-pending-preflight",
		Provider:   "claude",
		CWD:        "/tmp/project",
		DeferSpawn: true,
	})
	if err != nil {
		t.Fatalf("Start() error = %v, want pending_launch success", err)
	}
	assertFalse(t, startCalled, "provider StartSession was called for pending_launch intake thread")
	assertTrue(t, result.PendingLaunch, "StartResult.PendingLaunch")
	assertTrue(t, store.upsert.PendingLaunch, "Upsert.PendingLaunch")
	if store.upsert.ThreadID == "" {
		t.Fatal("Start() did not persist pending_launch thread")
	}
}

func TestStartPendingLaunchAllowsIntakeMetadataWithoutStartingProvider(t *testing.T) {
	t.Parallel()

	store := &stubThreadStore{}
	startCalled := false
	svc := &service{
		threadStore:    store,
		promptAssembly: promptAssemblyStub{},
		starter: &startOnlySessionStarter{onStart: func(context.Context, dto.StartSessionRequest) (contract.Session, error) {
			startCalled = true
			return nil, errors.New("provider must not start for pending launch")
		}},
	}

	result, err := svc.Start(context.Background(), StartRequest{
		AgentID:    "thread-dag-designer",
		Provider:   "claude",
		CWD:        "/tmp/project",
		DeferSpawn: true,
		Name:       "AI 设计流程",
		AgentKey:   "dag_designer",
		PromptKey:  "main/dag_designer_zh",
	})
	if err != nil {
		t.Fatalf("Start() error = %v, want pending_launch success", err)
	}
	assertFalse(t, startCalled, "provider StartSession was called for pending_launch intake thread")
	assertTrue(t, result.PendingLaunch, "StartResult.PendingLaunch")
	assertTrue(t, store.upsert.PendingLaunch, "Upsert.PendingLaunch")
	assertString(t, store.upsert.Name, "AI 设计流程", "Upsert.Name")
	assertString(t, store.upsert.Prompt, "AI 设计流程", "Upsert.Prompt")
	assertString(t, store.upsert.AgentKey, "dag_designer", "Upsert.AgentKey")
	var stored map[string]any
	if err := json.Unmarshal(store.upsert.ConfigOverride, &stored); err != nil {
		t.Fatalf("decode ConfigOverride: %v", err)
	}
	assertString(t, stringValue(stored["agent_key"]), "dag_designer", "stored.agent_key")
	assertString(t, stringValue(stored["prompt_key"]), "main/dag_designer_zh", "stored.prompt_key")
}

func TestSpawnIfNeededDeletesPendingThreadOnSpawnFailure(t *testing.T) {
	t.Parallel()

	deletedIDs := []string{}
	store := &deleteCaptureThreadStore{
		stubThreadStore: &stubThreadStore{thread: &threadstore.Thread{
			ThreadID:       "thread-pending-fail",
			Status:         statusCreated,
			PendingLaunch:  true,
			Cwd:            "/tmp/project",
			ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Provider: "codex"}),
		}},
		deletedIDs: &deletedIDs,
	}
	var stopped []threaddto.Stopped
	svc := &service{
		threadStore: store,
		promptAssembly: errorPromptAssembly{
			err: errors.New("codex pool disabled for explicit identity"),
		},
		emitStopped: func(evt threaddto.Stopped) {
			stopped = append(stopped, evt)
		},
	}
	_ = svc.acquirePendingLaunchLock("thread-pending-fail")

	launched, _, err := svc.SpawnIfNeeded(context.Background(), "thread-pending-fail", "hello", "/tmp/project")
	if err == nil {
		t.Fatal("SpawnIfNeeded() error = nil, want spawn failure")
	}
	if launched {
		t.Fatal("SpawnIfNeeded() launched = true, want false on spawn failure")
	}
	if len(deletedIDs) != 1 || deletedIDs[0] != "thread-pending-fail" {
		t.Fatalf("deletedIDs = %v, want [thread-pending-fail]", deletedIDs)
	}
	if _, loaded := svc.pendingLaunchMu.Load("thread-pending-fail"); loaded {
		t.Fatal("pendingLaunchMu still contains failed pending thread")
	}
	if len(stopped) != 1 ||
		stopped[0].ThreadID != "thread-pending-fail" ||
		stopped[0].Status != "deleted" ||
		stopped[0].Reason != "pending_launch_failed" {
		t.Fatalf("stopped events = %#v, want deleted pending_launch_failed event", stopped)
	}
}

func TestSpawnIfNeededPropagatesPromptKeyStale(t *testing.T) {
	t.Parallel()

	threadID := "thread-pending-stale"
	store := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:       threadID,
		Status:         statusCreated,
		PendingLaunch:  true,
		Cwd:            "/tmp/project",
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Provider: "claude", PromptKey: "main/missing"}),
	}}
	sessions := &stubSessionProvider{}
	svc := &service{
		threadStore:    store,
		bindingStore:   &stubBindingStore{},
		sessions:       sessions,
		promptAssembly: promptAssemblyStub{},
		promptCatalog:  threadprompt.NewRuntimeCatalog(&fakePromptStore{}, nil),
		starter: &startOnlySessionStarter{onStart: func(_ context.Context, _ dto.StartSessionRequest) (contract.Session, error) {
			session := &stubSession{threadID: threadID}
			sessions.session = session
			return session, nil
		}},
	}

	launched, routing, err := svc.SpawnIfNeeded(context.Background(), threadID, "hello", "/tmp/project")
	if err != nil {
		t.Fatalf("SpawnIfNeeded() error = %v, want nil", err)
	}
	if !launched {
		t.Fatal("SpawnIfNeeded() launched = false, want true")
	}
	if !routing.PromptKeyStale {
		t.Fatalf("SpawnRouting.PromptKeyStale = false, want true for stale prompt key: %+v", routing)
	}
	if routing.PromptKey != "main/missing" {
		t.Fatalf("SpawnRouting.PromptKey = %q, want stale prompt key preserved", routing.PromptKey)
	}
}

func TestRunPendingSpawnPropagatesRouterError(t *testing.T) {
	t.Parallel()

	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "db body", []string{"scope.cwd:/tmp/project"}),
		},
		insertErr: errors.New("version insert failed"),
	}
	svc := &service{
		threadStore:   &stubThreadStore{},
		promptCatalog: threadprompt.NewRuntimeCatalog(store, nil),
	}

	req := &StartRequest{
		AgentID:  "thread-pending-router-error",
		AgentKey: "sql_expert",
		CWD:      "/tmp/project",
		Provider: "codex",
		Prompt:   "write SQL",
	}
	err := svc.runPendingSpawn(context.Background(), req, &threadstore.Thread{}, req.AgentID, req.AgentID)
	if err == nil {
		t.Fatal("runPendingSpawn() error = nil, want routed prompt materialization error")
	}
	if !containsAll(err.Error(), "materialize prompt_versions", "version insert failed") {
		t.Fatalf("runPendingSpawn() error = %v, want routed prompt error", err)
	}
}

func TestBuildPendingSpawnRequestRejectsMissingStoredCWD(t *testing.T) {
	t.Parallel()

	row := &threadstore.Thread{
		ThreadID:      "thread-pending-missing-cwd",
		Status:        statusCreated,
		PendingLaunch: true,
	}

	_, err := buildPendingSpawnRequest(row, "thread-pending-missing-cwd", "hello", "")
	if err == nil {
		t.Fatal("buildPendingSpawnRequest() error = nil, want missing cwd error")
	}
	if !containsAll(err.Error(), "pending launch", "cwd is required") {
		t.Fatalf("buildPendingSpawnRequest() error = %v, want pending launch cwd required", err)
	}
}

func TestBuildPendingSpawnRequestRejectsRequestCWDMismatch(t *testing.T) {
	t.Parallel()

	row := &threadstore.Thread{
		ThreadID:      "thread-pending-cwd-mismatch",
		Status:        statusCreated,
		PendingLaunch: true,
		Cwd:           "/repo/stored-worktree",
	}

	_, err := buildPendingSpawnRequest(row, "thread-pending-cwd-mismatch", "hello", "/repo/active-window")
	if err == nil {
		t.Fatal("buildPendingSpawnRequest() error = nil, want cwd mismatch error")
	}
	if !containsAll(err.Error(), "cwd mismatch", "/repo/stored-worktree", "/repo/active-window") {
		t.Fatalf("buildPendingSpawnRequest() error = %v, want mismatch with both cwd values", err)
	}
}

// TestStopPendingThread_CleansLaunchMutex asserts that stopping a
// pending_launch thread cleans up the per-thread mutex from
// pendingLaunchMu, preventing a memory leak in long-running services.
func TestStopPendingThread_CleansLaunchMutex(t *testing.T) {
	t.Parallel()

	store := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:      "thread-pending-3",
		Status:        statusCreated,
		PendingLaunch: true,
	}}
	svc := &service{threadStore: store}

	// Pre-populate the mutex map as if SpawnIfNeeded had been called.
	_ = svc.acquirePendingLaunchLock("thread-pending-3")

	if err := svc.Stop(context.Background(), "thread-pending-3"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	// Verify the mutex entry was cleaned up.
	if _, loaded := svc.pendingLaunchMu.Load("thread-pending-3"); loaded {
		t.Fatal("pendingLaunchMu still contains entry after Stop, want cleaned up")
	}
}

// TestArchivePendingThread_CleansLaunchMutex asserts the same cleanup
// for the Archive path.
func TestArchivePendingThread_CleansLaunchMutex(t *testing.T) {
	t.Parallel()

	store := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:      "thread-pending-4",
		Status:        statusCreated,
		PendingLaunch: true,
	}}
	svc := &service{threadStore: store}

	// Pre-populate the mutex map.
	_ = svc.acquirePendingLaunchLock("thread-pending-4")

	if err := svc.Archive(context.Background(), "thread-pending-4"); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}

	if _, loaded := svc.pendingLaunchMu.Load("thread-pending-4"); loaded {
		t.Fatal("pendingLaunchMu still contains entry after Archive, want cleaned up")
	}
}

// TestDeletePendingThread_SkipsBindingResolution asserts that deleting a
// pending_launch thread succeeds without requiring a binding (which
// doesn't exist for pending threads) and cleans up the mutex.
func TestDeletePendingThread_SkipsBindingResolution(t *testing.T) {
	t.Parallel()

	deletedIDs := []string{}
	store := &deleteCaptureThreadStore{
		stubThreadStore: &stubThreadStore{thread: &threadstore.Thread{
			ThreadID:      "thread-pending-5",
			Status:        statusCreated,
			PendingLaunch: true,
		}},
		deletedIDs: &deletedIDs,
	}
	svc := &service{threadStore: store}

	// Pre-populate the mutex map.
	_ = svc.acquirePendingLaunchLock("thread-pending-5")

	if err := svc.Delete(context.Background(), "thread-pending-5"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Verify the thread was deleted from the store.
	if len(deletedIDs) != 1 || deletedIDs[0] != "thread-pending-5" {
		t.Fatalf("deleted thread IDs = %v, want [thread-pending-5]", deletedIDs)
	}

	// Verify mutex cleanup.
	if _, loaded := svc.pendingLaunchMu.Load("thread-pending-5"); loaded {
		t.Fatal("pendingLaunchMu still contains entry after Delete, want cleaned up")
	}
}

// deleteCaptureThreadStore wraps stubThreadStore to record DeleteByThreadID calls.
type deleteCaptureThreadStore struct {
	*stubThreadStore
	deletedIDs *[]string
}

func (s *deleteCaptureThreadStore) DeleteByThreadID(_ context.Context, threadID string) error {
	*s.deletedIDs = append(*s.deletedIDs, threadID)
	return nil
}

func containsAll(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			return false
		}
	}
	return true
}

func assertTrue(t *testing.T, got bool, label string) {
	t.Helper()
	if !got {
		t.Fatalf("%s = false, want true", label)
	}
}

func assertFalse(t *testing.T, got bool, message string) {
	t.Helper()
	if got {
		t.Fatal(message)
	}
}

func assertString(t *testing.T, got, want, label string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", label, got, want)
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

type errorPromptAssembly struct {
	err error
}

func (p errorPromptAssembly) AssembleStart(context.Context, contract.StartInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, p.err
}

func (p errorPromptAssembly) AssembleTurn(context.Context, contract.TurnInput) (contract.TurnAssembly, error) {
	return contract.TurnAssembly{}, p.err
}

func (p errorPromptAssembly) AssembleAgent(context.Context, contract.AgentInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, p.err
}

func (errorPromptAssembly) Invalidate(context.Context, contract.InvalidateReason) error {
	return nil
}
