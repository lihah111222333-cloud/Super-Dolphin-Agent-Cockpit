package thread

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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

	launched, _, err := svc.SpawnIfNeeded(context.Background(), "thread-pending-1", "hello")
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

	launched, _, err := svc.SpawnIfNeeded(context.Background(), "thread-pending-2", "hello")
	if err != nil {
		t.Fatalf("SpawnIfNeeded() error = %v, want nil", err)
	}
	if launched {
		t.Fatal("SpawnIfNeeded() launched = true, want false for archived thread")
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
