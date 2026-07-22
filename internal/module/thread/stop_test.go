package thread

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
)

func TestStopInterruptsTurnAndCleansThreadState(t *testing.T) {
	t.Parallel()

	turns := &stubTurnService{}
	orch := &stubThreadOrchestration{}
	svc := &service{
		bindingStore: &stubThreadBindingStore{binding: &BindingRecord{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-1",
			CodexThreadID:    "thread-1",
			SessionUUID:      "019e2c67-aabc-74f2-bf7a-6872e8465908",
		}},
		threadStore: &stubThreadStore{thread: &ThreadRecord{
			ThreadID: "thread-1",
			AgentID:  "agent-1",
			Status:   statusCreated,
		}},
		sessions: &stubThreadSessions{
			agentID: "agent-1",
			session: &stubThreadSession{threadID: "thread-1"},
		},
		turns:         turns,
		orchestration: orch,
	}
	if err := svc.Stop(context.Background(), "agent-1"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	assertStopInterruptsAndCleansState(t, svc, turns, orch)
}

func TestStopReturnsInterruptTimeoutAfterRuntimeTeardown(t *testing.T) {
	t.Parallel()

	calls := []string{}
	turns := &stubTurnService{interruptErr: context.DeadlineExceeded, calls: &calls}
	orch := &stubThreadOrchestration{calls: &calls}
	svc, scratchpadDir := newScratchpadCleanupService(t)
	threadStore := &recordingThreadStore{stubThreadStore: svc.threadStore.(*stubThreadStore), calls: &calls}
	stoppedEvents := 0
	svc.threadStore = threadStore
	svc.sessions = &stubThreadSessions{
		agentID: "agent-1",
		session: &stubThreadSession{threadID: "thread-1", calls: &calls},
		calls:   &calls,
	}
	svc.turns = turns
	svc.orchestration = orch
	for _, id := range []string{"thread-1", "provider-thread-1", "agent-1"} {
		svc.rememberThreadAgent(id, "agent-1")
	}
	svc.emitStopped = func(threaddto.Stopped) {
		stoppedEvents++
	}

	err := svc.Stop(context.Background(), "thread-1")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want interrupt timeout", err)
	}
	assertStopCallPresent(t, calls, "session_close:thread-1", "session close after interrupt timeout")
	assertStopCallPresent(t, calls, "session_remove:agent-1", "session binding cleanup after interrupt timeout")
	assertStopCallPresent(t, calls, "agent_stop:agent-1", "managed agent stop after interrupt timeout")
	assertStopCallBefore(t, calls, "turn_interrupt:thread-1:thread_stopped", "session_close:thread-1", "session close")
	assertStopCallBefore(t, calls, "session_close:thread-1", "agent_stop:agent-1", "managed agent stop")
	assertStopCallBefore(t, calls, "agent_stop:agent-1", "thread_status:thread-1:stopped", "durable stopped status")
	assertStopCallPresent(t, calls, "turn_cleanup:thread-1:thread_stopped", "turn cleanup after interrupt timeout")
	if _, statErr := os.Stat(scratchpadDir); !os.IsNotExist(statErr) {
		t.Fatalf("scratchpad stat error = %v, want removed directory", statErr)
	}
	if stoppedEvents != 1 {
		t.Fatalf("stopped events = %d, want one after durable finalization", stoppedEvents)
	}
	for _, id := range []string{"thread-1", "provider-thread-1", "agent-1"} {
		if agentID := svc.lookupThreadAgent(id); agentID != "" {
			t.Fatalf("thread-agent binding %q = %q, want cleaned", id, agentID)
		}
	}
	if _, blocked := svc.resumeBlocked.Load("agent-1"); blocked {
		t.Fatal("resumeBlocked remains after interrupted Stop returned")
	}
}

func TestStopDoesNotSwallowDurableFailureAfterInterruptTimeout(t *testing.T) {
	t.Parallel()

	durableErr := errors.New("durable stopped write failed")
	svc := newResumeBlockTimingService(t, statusCreated)
	svc.turns = &stubTurnService{interruptErr: context.DeadlineExceeded}
	store := &stopFailingStatusThreadStore{
		stubThreadStore: svc.threadStore.(*stubThreadStore),
		t:               t,
		svc:             svc,
		agentID:         "agent-1",
		err:             durableErr,
	}
	svc.threadStore = store

	err := svc.Stop(context.Background(), "thread-1")
	if !errors.Is(err, durableErr) {
		t.Fatalf("Stop() error = %v, want durable failure", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want retained interrupt timeout", err)
	}
	if !store.statusObserved {
		t.Fatal("durable stopped write was not attempted")
	}
	if _, blocked := svc.resumeBlocked.Load("agent-1"); blocked {
		t.Fatal("resumeBlocked remains after failed durable write returned")
	}
}

func assertStopInterruptsAndCleansState(t *testing.T, svc *service, turns *stubTurnService, orch *stubThreadOrchestration) {
	t.Helper()
	if !reflect.DeepEqual(turns.interruptCalls, []string{"thread-1:thread_stopped"}) {
		t.Fatalf("interrupt calls = %#v", turns.interruptCalls)
	}
	if orch.stoppedAgentID != "agent-1" {
		t.Fatalf("stopped agent = %q, want %q", orch.stoppedAgentID, "agent-1")
	}
	assertStopStoresAndSessions(t, svc)
	assertCleanupCalls(t, turns.cleanupCalls, map[string]struct{}{
		"agent-1:thread_stopped":           {},
		"thread-1:thread_stopped":          {},
		"provider-thread-1:thread_stopped": {},
	})
	if _, blocked := svc.resumeBlocked.Load("agent-1"); blocked {
		t.Fatal("resumeBlocked has not been cleared for agent-1 after successful Stop")
	}
}

func assertStopStoresAndSessions(t *testing.T, svc *service) {
	t.Helper()
	bindingStore := svc.bindingStore.(*stubThreadBindingStore)
	if len(bindingStore.sessionUpdates) != 0 {
		t.Fatalf("binding session uuid updates = %#v, want preserved history locator", bindingStore.sessionUpdates)
	}
	threadStore := svc.threadStore.(*stubThreadStore)
	if threadStore.status.ThreadID != "thread-1" || threadStore.status.Status != statusStopped {
		t.Fatalf("thread status update = %#v", threadStore.status)
	}
	session := svc.sessions.(*stubThreadSessions).session.(*stubThreadSession)
	if session.closeCalls != 1 {
		t.Fatalf("session close calls = %d, want 1", session.closeCalls)
	}
	assertRemovedSessions(t, svc.sessions.(*stubThreadSessions), "agent-1")
}

func assertCleanupCalls(t *testing.T, got []string, want map[string]struct{}) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("cleanup calls = %#v", got)
	}
	for _, call := range got {
		if _, ok := want[call]; !ok {
			t.Fatalf("unexpected cleanup call %q", call)
		}
	}
}

func TestStopUsesPublicThreadIDForStatusUpdateWhenProviderDiffers(t *testing.T) {
	t.Parallel()

	turns := &stubTurnService{}
	orch := &stubThreadOrchestration{}
	svc := &service{
		bindingStore: &stubThreadBindingStore{binding: &BindingRecord{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-1",
			CodexThreadID:    "thread-1",
		}},
		threadStore: &stubThreadStore{thread: &ThreadRecord{
			ThreadID: "thread-1",
			AgentID:  "agent-1",
			Status:   statusCreated,
		}},
		sessions: &stubThreadSessions{
			agentID: "agent-1",
			session: &stubThreadSession{threadID: "provider-thread-1"},
		},
		turns:         turns,
		orchestration: orch,
	}

	if err := svc.Stop(context.Background(), "thread-1"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	threadStore := svc.threadStore.(*stubThreadStore)
	if threadStore.status.ThreadID != "thread-1" || threadStore.status.Status != statusStopped {
		t.Fatalf("thread status update = %#v", threadStore.status)
	}
	if len(turns.cleanupCalls) == 0 {
		t.Fatal("cleanup calls = nil, want cleanup for stop targets")
	}
}

func TestStopRemovesSessionByGenerationWhenAvailable(t *testing.T) {
	t.Parallel()

	sessions := &stubThreadSessions{
		agentID:    "agent-1",
		session:    &stubThreadSession{threadID: "thread-1"},
		generation: 7,
	}
	svc := &service{
		bindingStore: &stubThreadBindingStore{binding: &BindingRecord{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-1",
			CodexThreadID:    "thread-1",
		}},
		threadStore: &stubThreadStore{thread: &ThreadRecord{
			ThreadID: "thread-1",
			AgentID:  "agent-1",
			Status:   statusCreated,
		}},
		sessions:      sessions,
		turns:         &stubTurnService{},
		orchestration: &stubThreadOrchestration{},
	}

	if err := svc.Stop(context.Background(), "agent-1"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !reflect.DeepEqual(sessions.removedGenerations, []uint64{7}) {
		t.Fatalf("removed generations = %#v, want [7]", sessions.removedGenerations)
	}
	if len(sessions.removed) != 0 {
		t.Fatalf("removed current sessions = %#v, want none", sessions.removed)
	}
}

func TestStopContinuesWhenLocalSessionAlreadyGone(t *testing.T) {
	t.Parallel()

	calls := []string{}
	sessions := &stubThreadSessions{
		agentID:    "agent-1",
		getErr:     contract.ErrSessionNotFound,
		generation: 11,
		calls:      &calls,
	}
	orch := &stubThreadOrchestration{calls: &calls}
	threadStore := &recordingThreadStore{
		stubThreadStore: &stubThreadStore{thread: &ThreadRecord{
			ThreadID: "thread-1",
			AgentID:  "agent-1",
			Status:   statusCreated,
		}},
		calls: &calls,
	}
	svc := &service{
		bindingStore: &stubThreadBindingStore{binding: &BindingRecord{
			AgentID:       "agent-1",
			CodexThreadID: "thread-1",
		}},
		threadStore:   threadStore,
		sessions:      sessions,
		turns:         &stubTurnService{calls: &calls},
		orchestration: orch,
	}

	if err := svc.Stop(context.Background(), "agent-1"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	assertStopCallPresent(t, calls, "agent_stop:agent-1", "managed agent stop after stale local session")
	assertStopCallPresent(t, calls, "thread_status:thread-1:stopped", "stopped status after stale local session")
	if !reflect.DeepEqual(sessions.removedGenerations, []uint64{11}) {
		t.Fatalf("removed generations = %#v, want [11]", sessions.removedGenerations)
	}
}

func TestStopKeepsResumeBlockedUntilStatusIsDurable(t *testing.T) {
	t.Parallel()

	svc := newResumeBlockTimingService(t, statusCreated)
	store := &resumeBlockAssertingThreadStore{
		stubThreadStore: svc.threadStore.(*stubThreadStore),
		t:               t,
		svc:             svc,
		agentID:         "agent-1",
	}
	svc.threadStore = store

	if err := svc.Stop(context.Background(), "thread-1"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !store.statusObserved {
		t.Fatal("UpdateStatus was not observed")
	}
	if _, blocked := svc.resumeBlocked.Load("agent-1"); blocked {
		t.Fatal("resumeBlocked remains after Stop completed")
	}
}

func TestStopRetriesBindingAfterPendingLaunchLock(t *testing.T) {
	t.Parallel()

	calls := []string{}
	binding := &BindingRecord{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "thread-1",
		CodexThreadID:    "thread-1",
	}
	svc := &service{
		bindingStore: &lateResolveBindingStore{
			stubThreadBindingStore: &stubThreadBindingStore{binding: binding, calls: &calls},
			failLookups:            3,
		},
		threadStore: &recordingThreadStore{
			stubThreadStore: &stubThreadStore{thread: &ThreadRecord{
				ThreadID:      "thread-1",
				AgentID:       "agent-1",
				Status:        statusCreated,
				PendingLaunch: false,
			}},
			calls: &calls,
		},
		sessions:      &stubThreadSessions{agentID: "agent-1", session: &stubThreadSession{threadID: "thread-1", calls: &calls}, calls: &calls},
		turns:         &stubTurnService{calls: &calls},
		orchestration: &stubThreadOrchestration{calls: &calls},
	}

	if err := svc.Stop(context.Background(), "thread-1"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	assertStopCallPresent(t, calls, "agent_stop:agent-1", "managed agent stop after binding retry")
	assertStopCallPresent(t, calls, "thread_status:thread-1:stopped", "stopped status update after binding retry")
}

func TestArchiveStopsManagedAgentBeforeArchiving(t *testing.T) {
	t.Parallel()

	calls := []string{}
	turns := &stubTurnService{calls: &calls}
	orch := &stubThreadOrchestration{calls: &calls}
	bindingStore := &stubThreadBindingStore{
		binding: &BindingRecord{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-1",
			CodexThreadID:    "thread-1",
		},
		calls: &calls,
	}
	threadStore := &recordingThreadStore{
		stubThreadStore: &stubThreadStore{thread: &ThreadRecord{ThreadID: "thread-1", AgentID: "agent-1", Status: statusCreated}},
		calls:           &calls,
	}
	session := &stubThreadSession{threadID: "thread-1", calls: &calls}
	sessions := &stubThreadSessions{agentID: "agent-1", session: session, calls: &calls}
	svc := &service{
		bindingStore:  bindingStore,
		threadStore:   threadStore,
		sessions:      sessions,
		turns:         turns,
		orchestration: orch,
	}

	if err := svc.Archive(context.Background(), "thread-1"); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	assertArchiveStopsManagedAgent(t, calls, orch, bindingStore, threadStore, session, sessions)
	if _, blocked := svc.resumeBlocked.Load("agent-1"); blocked {
		t.Fatal("resumeBlocked has not been cleared for agent-1 after successful Archive")
	}
}

func TestArchiveKeepsResumeBlockedUntilArchiveIsDurable(t *testing.T) {
	t.Parallel()

	svc := newResumeBlockTimingService(t, statusCreated)
	store := &resumeBlockAssertingThreadStore{
		stubThreadStore: svc.threadStore.(*stubThreadStore),
		t:               t,
		svc:             svc,
		agentID:         "agent-1",
	}
	svc.threadStore = store

	if err := svc.Archive(context.Background(), "thread-1"); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	if !store.statusObserved {
		t.Fatal("UpdateStatus was not observed")
	}
	if _, blocked := svc.resumeBlocked.Load("agent-1"); blocked {
		t.Fatal("resumeBlocked remains after Archive completed")
	}
}

func TestArchiveContinuesWhenLocalSessionAlreadyGone(t *testing.T) {
	t.Parallel()

	calls := []string{}
	sessions := &stubThreadSessions{
		agentID: "agent-1",
		getErr:  contract.ErrSessionNotFound,
		calls:   &calls,
	}
	orch := &stubThreadOrchestration{calls: &calls}
	bindingStore := &stubThreadBindingStore{
		binding: &BindingRecord{
			AgentID:       "agent-1",
			CodexThreadID: "thread-1",
		},
		calls: &calls,
	}
	threadStore := &recordingThreadStore{
		stubThreadStore: &stubThreadStore{thread: &ThreadRecord{
			ThreadID: "thread-1",
			AgentID:  "agent-1",
			Status:   statusCreated,
		}},
		calls: &calls,
	}
	svc := &service{
		bindingStore:  bindingStore,
		threadStore:   threadStore,
		sessions:      sessions,
		turns:         &stubTurnService{calls: &calls},
		orchestration: orch,
	}

	if err := svc.Archive(context.Background(), "thread-1"); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	assertStopCallPresent(t, calls, "agent_stop:agent-1", "managed agent stop after stale local session")
	assertStopCallPresent(t, calls, "thread_status:thread-1:archived", "archive status after stale local session")
	if len(bindingStore.archived) != 1 || !bindingStore.archived[0].Archived {
		t.Fatalf("archived bindings = %#v", bindingStore.archived)
	}
}

func assertArchiveStopsManagedAgent(
	t *testing.T,
	calls []string,
	orch *stubThreadOrchestration,
	bindingStore *stubThreadBindingStore,
	threadStore *recordingThreadStore,
	session *stubThreadSession,
	sessions *stubThreadSessions,
) {
	t.Helper()
	if orch.stoppedAgentID != "agent-1" {
		t.Fatalf("stopped agent = %q, want agent-1", orch.stoppedAgentID)
	}
	if threadStore.status.Status != statusArchived {
		t.Fatalf("thread status = %#v, want archived", threadStore.status)
	}
	if len(bindingStore.archived) != 1 || !bindingStore.archived[0].Archived {
		t.Fatalf("archived bindings = %#v", bindingStore.archived)
	}
	if session.closeCalls != 1 {
		t.Fatalf("session close calls = %d, want 1", session.closeCalls)
	}
	assertRemovedSessions(t, sessions, "agent-1")
	assertStopCallBefore(t, calls, "agent_stop:agent-1", "thread_status:thread-1:archived", "archive status update")
	assertStopCallBefore(t, calls, "agent_stop:agent-1", "binding_archive:agent-1:true", "binding archive")
	assertStopCallPresent(t, calls, "turn_cleanup:thread-1:thread_archived", "thread_archived cleanup")
}

func TestArchiveFailsFastWhenBindingMissing(t *testing.T) {
	t.Parallel()

	calls := []string{}
	turns := &stubTurnService{calls: &calls}
	orch := &stubThreadOrchestration{calls: &calls}
	bindingStore := &stubThreadBindingStore{
		getByAgentIDErr: platformdb.ErrNotFound,
		calls:           &calls,
	}
	threadStore := &recordingThreadStore{
		stubThreadStore: &stubThreadStore{thread: &ThreadRecord{ThreadID: "thread-orphan", Status: statusCreated}},
		calls:           &calls,
	}
	svc := &service{
		bindingStore:  bindingStore,
		threadStore:   threadStore,
		turns:         turns,
		orchestration: orch,
	}

	if err := svc.Archive(context.Background(), "thread-orphan"); err != nil {
		if platformdb.IsNotFound(err) {
			return
		}
		t.Fatalf("Archive() error = %v, want not found", err)
	}
	t.Fatal("Archive() error = nil, want not found")
}

func TestUnarchivePublishesCreatedLifecycleEvent(t *testing.T) {
	t.Parallel()

	bindingStore := &stubThreadBindingStore{
		binding: &BindingRecord{AgentID: "agent-1", CodexThreadID: "thread-1"},
	}
	threadStore := &recordingThreadStore{
		stubThreadStore: &stubThreadStore{thread: &ThreadRecord{ThreadID: "thread-1", AgentID: "agent-1", Status: statusArchived}},
	}
	var stopped threaddto.Stopped
	svc := &service{
		bindingStore: bindingStore,
		threadStore:  threadStore,
		emitStopped: func(evt threaddto.Stopped) {
			stopped = evt
		},
	}

	if err := svc.Unarchive(context.Background(), "thread-1"); err != nil {
		t.Fatalf("Unarchive() error = %v", err)
	}
	if threadStore.status.Status != statusCreated {
		t.Fatalf("thread status = %#v, want created", threadStore.status)
	}
	if len(bindingStore.archived) != 1 || bindingStore.archived[0].Archived {
		t.Fatalf("archived bindings = %#v, want false", bindingStore.archived)
	}
	if stopped.ThreadID != "thread-1" || stopped.Status != statusCreated || stopped.Reason != "unarchived" {
		t.Fatalf("stopped event = %#v, want thread created/unarchived", stopped)
	}
}

func TestDeleteStopsManagedAgentBeforeDeleting(t *testing.T) {
	t.Parallel()

	calls := []string{}
	turns := &stubTurnService{calls: &calls}
	orch := &stubThreadOrchestration{calls: &calls}
	bindingStore := &stubThreadBindingStore{
		binding: &BindingRecord{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-1",
			CodexThreadID:    "thread-1",
		},
		calls: &calls,
	}
	threadStore := &recordingThreadStore{
		stubThreadStore: &stubThreadStore{thread: &ThreadRecord{ThreadID: "thread-1", AgentID: "agent-1", Status: statusCreated}},
		calls:           &calls,
	}
	session := &stubThreadSession{threadID: "thread-1", calls: &calls}
	sessions := &stubThreadSessions{agentID: "agent-1", session: session, calls: &calls}
	svc := &service{
		bindingStore:  bindingStore,
		threadStore:   threadStore,
		sessions:      sessions,
		turns:         turns,
		orchestration: orch,
	}

	if err := svc.Delete(context.Background(), "thread-1"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	assertDeleteStopsManagedAgent(t, calls, orch, bindingStore, threadStore, session, sessions)
	if _, blocked := svc.resumeBlocked.Load("agent-1"); blocked {
		t.Fatal("resumeBlocked has not been cleared for agent-1 after successful Delete")
	}
}

func TestDeleteKeepsResumeBlockedUntilDeleteIsDurable(t *testing.T) {
	t.Parallel()

	svc := newResumeBlockTimingService(t, statusCreated)
	store := &resumeBlockAssertingThreadStore{
		stubThreadStore: svc.threadStore.(*stubThreadStore),
		t:               t,
		svc:             svc,
		agentID:         "agent-1",
	}
	svc.threadStore = store

	if err := svc.Delete(context.Background(), "thread-1"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if !store.deleteObserved {
		t.Fatal("DeleteByThreadID was not observed")
	}
	if _, blocked := svc.resumeBlocked.Load("agent-1"); blocked {
		t.Fatal("resumeBlocked remains after Delete completed")
	}
}

func TestDeleteRetriesBindingAfterPendingLaunchLock(t *testing.T) {
	t.Parallel()

	calls := []string{}
	binding := &BindingRecord{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "thread-1",
		CodexThreadID:    "thread-1",
	}
	bindingStore := &lateResolveBindingStore{
		stubThreadBindingStore: &stubThreadBindingStore{binding: binding, calls: &calls},
		failLookups:            3,
	}
	threadStore := &recordingThreadStore{
		stubThreadStore: &stubThreadStore{thread: &ThreadRecord{
			ThreadID:      "thread-1",
			AgentID:       "agent-1",
			Status:        statusCreated,
			PendingLaunch: false,
		}},
		calls: &calls,
	}
	svc := &service{
		bindingStore:  bindingStore,
		threadStore:   threadStore,
		sessions:      &stubThreadSessions{agentID: "agent-1", session: &stubThreadSession{threadID: "thread-1", calls: &calls}, calls: &calls},
		turns:         &stubTurnService{calls: &calls},
		orchestration: &stubThreadOrchestration{calls: &calls},
	}

	if err := svc.Delete(context.Background(), "thread-1"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	assertStopCallPresent(t, calls, "agent_stop:agent-1", "managed agent stop after binding retry")
	assertStopCallPresent(t, calls, "binding_delete:agent-1", "binding delete after binding retry")
	assertStopCallPresent(t, calls, "thread_delete:thread-1", "thread delete after binding retry")
}

func assertDeleteStopsManagedAgent(
	t *testing.T,
	calls []string,
	orch *stubThreadOrchestration,
	bindingStore *stubThreadBindingStore,
	threadStore *recordingThreadStore,
	session *stubThreadSession,
	sessions *stubThreadSessions,
) {
	t.Helper()
	if orch.stoppedAgentID != "agent-1" {
		t.Fatalf("stopped agent = %q, want agent-1", orch.stoppedAgentID)
	}
	if len(bindingStore.deletedAgentIDs) != 1 || bindingStore.deletedAgentIDs[0] != "agent-1" {
		t.Fatalf("deleted bindings = %#v", bindingStore.deletedAgentIDs)
	}
	if threadStore.deletedThreadID != "thread-1" {
		t.Fatalf("deleted thread id = %q, want thread-1", threadStore.deletedThreadID)
	}
	if session.closeCalls != 1 {
		t.Fatalf("session close calls = %d, want 1", session.closeCalls)
	}
	assertRemovedSessions(t, sessions, "agent-1")
	assertStopCallBefore(t, calls, "agent_stop:agent-1", "binding_delete:agent-1", "binding delete")
	assertStopCallBefore(t, calls, "agent_stop:agent-1", "thread_delete:thread-1", "thread delete")
	assertStopCallPresent(t, calls, "turn_cleanup:thread-1:thread_deleted", "thread_deleted cleanup")
}

func assertRemovedSessions(t *testing.T, sessions *stubThreadSessions, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(sessions.removed, want) {
		t.Fatalf("removed sessions = %#v, want %v", sessions.removed, want)
	}
}

func assertStopCallBefore(t *testing.T, calls []string, before, after, label string) {
	t.Helper()
	beforeIndex := callIndex(calls, before)
	afterIndex := callIndex(calls, after)
	if beforeIndex == -1 || afterIndex == -1 || beforeIndex > afterIndex {
		t.Fatalf("call order = %#v, want %s before %s", calls, before, label)
	}
}

func assertStopCallPresent(t *testing.T, calls []string, want, label string) {
	t.Helper()
	if callIndex(calls, want) == -1 {
		t.Fatalf("cleanup calls = %#v, want %s", calls, label)
	}
}

type lateResolveBindingStore struct {
	*stubThreadBindingStore
	failLookups int
}

func (s *lateResolveBindingStore) shouldFailLookup() bool {
	if s.failLookups <= 0 {
		return false
	}
	s.failLookups--
	return true
}

func (s *lateResolveBindingStore) GetByAgentID(ctx context.Context, agentID string) (*BindingRecord, error) {
	if s.shouldFailLookup() {
		return nil, platformdb.ErrNotFound
	}
	return s.stubThreadBindingStore.GetByAgentID(ctx, agentID)
}

func (s *lateResolveBindingStore) GetByProviderThread(ctx context.Context, provider, providerThreadID string) (*BindingRecord, error) {
	if s.shouldFailLookup() {
		return nil, platformdb.ErrNotFound
	}
	if s.binding != nil && s.binding.Provider == provider &&
		(s.binding.ProviderThreadID == providerThreadID || s.binding.CodexThreadID == providerThreadID) {
		return s.binding, nil
	}
	return nil, platformdb.ErrNotFound
}

func TestStopCleansManagedScratchpad(t *testing.T) {
	t.Parallel()

	svc, dir := newScratchpadCleanupService(t)
	if err := svc.Stop(context.Background(), "thread-1"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scratchpad dir stat error = %v, want %v", err, os.ErrNotExist)
	}
}

func TestArchiveCleansManagedScratchpad(t *testing.T) {
	t.Parallel()

	svc, dir := newScratchpadCleanupService(t)
	if err := svc.Archive(context.Background(), "thread-1"); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scratchpad dir stat error = %v, want %v", err, os.ErrNotExist)
	}
}

func newScratchpadCleanupService(t *testing.T) (*service, string) {
	t.Helper()

	cwd := t.TempDir()
	dir, err := ensureManagedScratchpadDir(contract.BuildCtx{CWD: cwd}, StartRequest{CWD: cwd}, "thread-1", nil)
	if err != nil {
		t.Fatalf("ensureManagedScratchpadDir() error = %v", err)
	}
	marker := filepath.Join(dir, "scratch.txt")
	if err := os.WriteFile(marker, []byte("scratch"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(dir)) })

	raw, err := json.Marshal(storedThreadConfig{Runtime: map[string]any{"scratchpadDir": dir}})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	return &service{
		bindingStore: &stubThreadBindingStore{binding: &BindingRecord{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-1",
			CodexThreadID:    "thread-1",
		}},
		threadStore: &stubThreadStore{thread: &ThreadRecord{
			ThreadID:       "thread-1",
			AgentID:        "agent-1",
			Status:         statusCreated,
			ConfigOverride: raw,
		}},
		orchestration: &stubThreadOrchestration{},
	}, dir
}

func newResumeBlockTimingService(t *testing.T, status string) *service {
	t.Helper()
	return &service{
		bindingStore: &stubThreadBindingStore{binding: &BindingRecord{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-1",
			CodexThreadID:    "thread-1",
		}},
		threadStore: &stubThreadStore{thread: &ThreadRecord{
			ThreadID: "thread-1",
			AgentID:  "agent-1",
			Status:   status,
		}},
		sessions: &stubThreadSessions{
			agentID: "agent-1",
			session: &stubThreadSession{threadID: "thread-1"},
		},
		turns:         &stubTurnService{},
		orchestration: &stubThreadOrchestration{},
	}
}

type resumeBlockAssertingThreadStore struct {
	*stubThreadStore
	t              *testing.T
	svc            *service
	agentID        string
	statusObserved bool
	deleteObserved bool
}

func (s *resumeBlockAssertingThreadStore) UpdateStatus(ctx context.Context, params ThreadStatusUpdate) error {
	s.statusObserved = true
	s.assertBlocked("UpdateStatus")
	return s.stubThreadStore.UpdateStatus(ctx, params)
}

func (s *resumeBlockAssertingThreadStore) DeleteByThreadID(ctx context.Context, threadID string) error {
	s.deleteObserved = true
	s.assertBlocked("DeleteByThreadID")
	return s.stubThreadStore.DeleteByThreadID(ctx, threadID)
}

func (s *resumeBlockAssertingThreadStore) assertBlocked(stage string) {
	s.t.Helper()
	if _, blocked := s.svc.resumeBlocked.Load(s.agentID); !blocked {
		s.t.Fatalf("resumeBlocked missing during %s; concurrent Resume can pass before terminal state is durable", stage)
	}
}

type stopFailingStatusThreadStore struct {
	*stubThreadStore
	t              *testing.T
	svc            *service
	agentID        string
	err            error
	statusObserved bool
}

func (s *stopFailingStatusThreadStore) UpdateStatus(context.Context, ThreadStatusUpdate) error {
	s.statusObserved = true
	s.t.Helper()
	if _, blocked := s.svc.resumeBlocked.Load(s.agentID); !blocked {
		s.t.Fatal("resumeBlocked missing during failed durable stopped write")
	}
	return s.err
}
