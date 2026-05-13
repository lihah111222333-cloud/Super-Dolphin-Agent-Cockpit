package thread

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestStopInterruptsTurnAndCleansThreadState(t *testing.T) {
	t.Parallel()

	turns := &stubTurnService{}
	orch := &stubThreadOrchestration{}
	svc := &service{
		bindingStore: &stubThreadBindingStore{binding: &bindingstore.Binding{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-1",
			CodexThreadID:    "thread-1",
		}},
		threadStore: &stubThreadStore{thread: &threadstore.Thread{
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
	if !reflect.DeepEqual(turns.interruptCalls, []string{"thread-1:thread_stopped"}) {
		t.Fatalf("interrupt calls = %#v", turns.interruptCalls)
	}
	if orch.stoppedAgentID != "agent-1" {
		t.Fatalf("stopped agent = %q, want %q", orch.stoppedAgentID, "agent-1")
	}
	bindingStore := svc.bindingStore.(*stubThreadBindingStore)
	if len(bindingStore.sessionUpdates) != 1 || bindingStore.sessionUpdates[0].AgentID != "agent-1" || bindingStore.sessionUpdates[0].SessionUUID != "" {
		t.Fatalf("binding cleanup = %#v", bindingStore.sessionUpdates)
	}
	threadStore := svc.threadStore.(*stubThreadStore)
	if threadStore.status.ThreadID != "thread-1" || threadStore.status.Status != statusStopped {
		t.Fatalf("thread status update = %#v", threadStore.status)
	}
	session := svc.sessions.(*stubThreadSessions).session.(*stubThreadSession)
	if session.closeCalls != 1 {
		t.Fatalf("session close calls = %d, want 1", session.closeCalls)
	}
	sessions := svc.sessions.(*stubThreadSessions)
	if !reflect.DeepEqual(sessions.removed, []string{"agent-1"}) {
		t.Fatalf("removed sessions = %#v, want [agent-1]", sessions.removed)
	}
	wantCleanup := map[string]struct{}{
		"agent-1:thread_stopped":           {},
		"thread-1:thread_stopped":          {},
		"provider-thread-1:thread_stopped": {},
	}
	if len(turns.cleanupCalls) != len(wantCleanup) {
		t.Fatalf("cleanup calls = %#v", turns.cleanupCalls)
	}
	for _, call := range turns.cleanupCalls {
		if _, ok := wantCleanup[call]; !ok {
			t.Fatalf("unexpected cleanup call %q", call)
		}
	}
}

func TestStopUsesPublicThreadIDForStatusUpdateWhenProviderDiffers(t *testing.T) {
	t.Parallel()

	turns := &stubTurnService{}
	orch := &stubThreadOrchestration{}
	svc := &service{
		bindingStore: &stubThreadBindingStore{binding: &bindingstore.Binding{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-1",
			CodexThreadID:    "thread-1",
		}},
		threadStore: &stubThreadStore{thread: &threadstore.Thread{
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
		bindingStore: &stubThreadBindingStore{binding: &bindingstore.Binding{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-1",
			CodexThreadID:    "thread-1",
		}},
		threadStore: &stubThreadStore{thread: &threadstore.Thread{
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

func TestArchiveStopsManagedAgentBeforeArchiving(t *testing.T) {
	t.Parallel()

	calls := []string{}
	turns := &stubTurnService{calls: &calls}
	orch := &stubThreadOrchestration{calls: &calls}
	bindingStore := &stubThreadBindingStore{
		binding: &bindingstore.Binding{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-1",
			CodexThreadID:    "thread-1",
		},
		calls: &calls,
	}
	threadStore := &recordingThreadStore{
		stubThreadStore: &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-1", AgentID: "agent-1", Status: statusCreated}},
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
	if !reflect.DeepEqual(sessions.removed, []string{"agent-1"}) {
		t.Fatalf("removed sessions = %#v, want [agent-1]", sessions.removed)
	}
	if callIndex(calls, "agent_stop:agent-1") > callIndex(calls, "thread_status:thread-1:archived") {
		t.Fatalf("call order = %#v, want stop before archive status update", calls)
	}
	if callIndex(calls, "agent_stop:agent-1") > callIndex(calls, "binding_archive:agent-1:true") {
		t.Fatalf("call order = %#v, want stop before binding archive", calls)
	}
	if callIndex(calls, "turn_cleanup:thread-1:thread_archived") == -1 {
		t.Fatalf("cleanup calls = %#v, want thread_archived cleanup", calls)
	}
}

func TestArchiveFallsBackWhenBindingMissing(t *testing.T) {
	t.Parallel()

	calls := []string{}
	turns := &stubTurnService{calls: &calls}
	orch := &stubThreadOrchestration{calls: &calls}
	// No binding row (e.g. agent_provider_binding wiped during a DB rebuild
	// while agent_threads survived). GetByAgentID returns platformdb.ErrNotFound
	// — without the C2 fallback Archive() would return that error and the
	// frontend would silently swallow it, leaving the archive button
	// non-reactive.
	bindingStore := &stubThreadBindingStore{
		getByAgentIDErr: platformdb.ErrNotFound,
		calls:           &calls,
	}
	threadStore := &recordingThreadStore{
		stubThreadStore: &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-orphan", Status: statusCreated}},
		calls:           &calls,
	}
	var stopped threaddto.Stopped
	svc := &service{
		bindingStore:  bindingStore,
		threadStore:   threadStore,
		turns:         turns,
		orchestration: orch,
		emitStopped: func(evt threaddto.Stopped) {
			stopped = evt
		},
	}

	if err := svc.Archive(context.Background(), "thread-orphan"); err != nil {
		t.Fatalf("Archive() error = %v, want nil (no-binding fallback)", err)
	}
	if threadStore.status.Status != statusArchived {
		t.Fatalf("thread status = %#v, want archived", threadStore.status)
	}
	if threadStore.status.ThreadID != "thread-orphan" {
		t.Fatalf("status update target = %q, want thread-orphan", threadStore.status.ThreadID)
	}
	if len(bindingStore.archived) != 0 {
		t.Fatalf("binding archive calls = %#v, want none (no binding to update)", bindingStore.archived)
	}
	if orch.stoppedAgentID != "" {
		t.Fatalf("orchestration stop called with agent = %q, want none", orch.stoppedAgentID)
	}
	if stopped.ThreadID != "thread-orphan" || stopped.Status != statusArchived || stopped.Reason != "archived_no_binding" {
		t.Fatalf("stopped event = %#v, want thread-orphan/archived/archived_no_binding", stopped)
	}
}

func TestUnarchivePublishesCreatedLifecycleEvent(t *testing.T) {
	t.Parallel()

	bindingStore := &stubThreadBindingStore{
		binding: &bindingstore.Binding{AgentID: "agent-1", CodexThreadID: "thread-1"},
	}
	threadStore := &recordingThreadStore{
		stubThreadStore: &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-1", AgentID: "agent-1", Status: statusArchived}},
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
		binding: &bindingstore.Binding{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-1",
			CodexThreadID:    "thread-1",
		},
		calls: &calls,
	}
	threadStore := &recordingThreadStore{
		stubThreadStore: &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-1", AgentID: "agent-1", Status: statusCreated}},
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
	if !reflect.DeepEqual(sessions.removed, []string{"agent-1"}) {
		t.Fatalf("removed sessions = %#v, want [agent-1]", sessions.removed)
	}
	if callIndex(calls, "agent_stop:agent-1") > callIndex(calls, "binding_delete:agent-1") {
		t.Fatalf("call order = %#v, want stop before binding delete", calls)
	}
	if callIndex(calls, "agent_stop:agent-1") > callIndex(calls, "thread_delete:thread-1") {
		t.Fatalf("call order = %#v, want stop before thread delete", calls)
	}
	if callIndex(calls, "turn_cleanup:thread-1:thread_deleted") == -1 {
		t.Fatalf("cleanup calls = %#v, want thread_deleted cleanup", calls)
	}
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
		bindingStore: &stubThreadBindingStore{binding: &bindingstore.Binding{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-1",
			CodexThreadID:    "thread-1",
		}},
		threadStore: &stubThreadStore{thread: &threadstore.Thread{
			ThreadID:       "thread-1",
			AgentID:        "agent-1",
			Status:         statusCreated,
			ConfigOverride: raw,
		}},
		orchestration: &stubThreadOrchestration{},
	}, dir
}

type stubThreadBindingStore struct {
	binding         *bindingstore.Binding
	sessionUpdates  []bindingstore.UpdateSessionUUIDParams
	archived        []bindingstore.SetArchivedParams
	deletedAgentIDs []string
	calls           *[]string
	getByAgentIDErr error
}

func (s *stubThreadBindingStore) GetByProviderThread(context.Context, string, string) (*bindingstore.Binding, error) {
	return nil, errors.New("not found")
}
func (s *stubThreadBindingStore) Upsert(context.Context, bindingstore.UpsertParams) error { return nil }
func (s *stubThreadBindingStore) DeleteByAgentID(_ context.Context, agentID string) error {
	s.deletedAgentIDs = append(s.deletedAgentIDs, agentID)
	recordCall(s.calls, "binding_delete:"+agentID)
	return nil
}
func (s *stubThreadBindingStore) UpdateSessionUUID(_ context.Context, params bindingstore.UpdateSessionUUIDParams) error {
	s.sessionUpdates = append(s.sessionUpdates, params)
	return nil
}
func (s *stubThreadBindingStore) UpdateProviderThreadID(context.Context, bindingstore.UpdateProviderThreadIDParams) error {
	return nil
}
func (s *stubThreadBindingStore) SetArchived(_ context.Context, params bindingstore.SetArchivedParams) error {
	s.archived = append(s.archived, params)
	archived := "false"
	if params.Archived {
		archived = "true"
	}
	recordCall(s.calls, "binding_archive:"+params.AgentID+":"+archived)
	return nil
}
func (s *stubThreadBindingStore) GetByAgentID(_ context.Context, agentID string) (*bindingstore.Binding, error) {
	if s.binding != nil && s.binding.AgentID == agentID {
		return s.binding, nil
	}
	if s.getByAgentIDErr != nil {
		return nil, s.getByAgentIDErr
	}
	return nil, errors.New("not found")
}
func (s *stubThreadBindingStore) BindAgentThread(context.Context, bindingstore.BindAgentThreadParams) error {
	return nil
}
func (s *stubThreadBindingStore) UnbindAgentThread(context.Context, string) error { return nil }
func (s *stubThreadBindingStore) ListAgentThreadBindings(context.Context) ([]bindingstore.Binding, error) {
	if s.binding == nil {
		return nil, nil
	}
	return []bindingstore.Binding{*s.binding}, nil
}
func (s *stubThreadBindingStore) GetThreadByAgent(context.Context, string) (string, error) {
	if s.binding == nil {
		return "", errors.New("not found")
	}
	return s.binding.ProviderThreadID, nil
}
func (s *stubThreadBindingStore) UpdateAgentCwd(context.Context, bindingstore.UpdateAgentCwdParams) error {
	return nil
}

func (s *stubThreadBindingStore) Rebind(context.Context, bindingstore.RebindParams) error { return nil }

func (s *stubThreadBindingStore) ListProviderMap(context.Context) (map[string]string, error) {
	return nil, nil
}

func (s *stubThreadBindingStore) ListCwdMap(context.Context) (map[string]string, error) {
	return nil, nil
}

type stubThreadSessions struct {
	agentID            string
	session            contract.Session
	generation         uint64
	removed            []string
	removedGenerations []uint64
	calls              *[]string
}

func (s *stubThreadSessions) GetSession(agentID string) (contract.Session, error) {
	if s.session != nil && agentID == s.agentID {
		return s.session, nil
	}
	return nil, errors.New("not found")
}
func (s *stubThreadSessions) RemoveSession(agentID string) {
	s.removed = append(s.removed, agentID)
	recordCall(s.calls, "session_remove:"+agentID)
}
func (s *stubThreadSessions) SessionGeneration(agentID string) uint64 {
	if agentID != s.agentID {
		return 0
	}
	return s.generation
}
func (s *stubThreadSessions) RemoveSessionGeneration(agentID string, generation uint64) {
	if agentID != s.agentID {
		return
	}
	s.removedGenerations = append(s.removedGenerations, generation)
	recordCall(s.calls, "session_remove_generation:"+agentID)
}

type stubThreadSession struct {
	threadID   string
	closeCalls int
	calls      *[]string
}

func (s *stubThreadSession) ThreadID() string    { return s.threadID }
func (s *stubThreadSession) RolloutPath() string { return "" }
func (s *stubThreadSession) Capabilities() dto.CapabilitySet {
	return nil
}
func (s *stubThreadSession) StartTurn(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
	return nil, nil
}
func (s *stubThreadSession) Interrupt(context.Context, dto.InterruptRequest) error { return nil }
func (s *stubThreadSession) ForceComplete(context.Context, dto.ForceCompleteRequest) error {
	return nil
}
func (s *stubThreadSession) ListThreads(context.Context) ([]dto.ThreadRef, error) {
	return nil, nil
}
func (s *stubThreadSession) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, nil
}
func (s *stubThreadSession) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	return nil, nil
}
func (s *stubThreadSession) Configure(context.Context, dto.ThreadConfigPatch) error { return nil }
func (s *stubThreadSession) Close(context.Context) error {
	s.closeCalls++
	recordCall(s.calls, "session_close:"+s.threadID)
	return nil
}
func (s *stubThreadSession) ForceStop() error { return nil }

type stubTurnService struct {
	interruptCalls []string
	cleanupCalls   []string
	calls          *[]string
}

func (s *stubTurnService) InterruptActiveTurn(_ context.Context, session contract.Session, source string) error {
	s.interruptCalls = append(s.interruptCalls, session.ThreadID()+":"+source)
	recordCall(s.calls, "turn_interrupt:"+session.ThreadID()+":"+source)
	return nil
}
func (s *stubTurnService) CleanupThread(_ context.Context, threadID, reason string) error {
	s.cleanupCalls = append(s.cleanupCalls, threadID+":"+reason)
	recordCall(s.calls, "turn_cleanup:"+threadID+":"+reason)
	return nil
}

type stubThreadOrchestration struct {
	launchReq      LaunchAgentRequest
	stoppedAgentID string
	stopCalls      []string
	stopErr        error
	calls          *[]string
}

func (s *stubThreadOrchestration) LaunchAgent(_ context.Context, req LaunchAgentRequest) error {
	s.launchReq = req
	return nil
}
func (s *stubThreadOrchestration) StopAgent(_ context.Context, agentID string) error {
	s.stoppedAgentID = agentID
	s.stopCalls = append(s.stopCalls, agentID)
	recordCall(s.calls, "agent_stop:"+agentID)
	return s.stopErr
}
func (s *stubThreadOrchestration) Recover(context.Context, string) error { return nil }
func (s *stubThreadOrchestration) BindSessionGeneration(context.Context, string, uint64) error {
	return nil
}

type recordingThreadStore struct {
	*stubThreadStore
	calls           *[]string
	deletedThreadID string
}

func (s *recordingThreadStore) UpdateStatus(ctx context.Context, params threadstore.UpdateStatusParams) error {
	recordCall(s.calls, "thread_status:"+params.ThreadID+":"+params.Status)
	return s.stubThreadStore.UpdateStatus(ctx, params)
}

func (s *recordingThreadStore) DeleteByThreadID(_ context.Context, threadID string) error {
	s.deletedThreadID = threadID
	recordCall(s.calls, "thread_delete:"+s.deletedThreadID)
	return nil
}

func recordCall(calls *[]string, entry string) {
	if calls != nil {
		*calls = append(*calls, entry)
	}
}

func callIndex(calls []string, target string) int {
	for i, call := range calls {
		if call == target {
			return i
		}
	}
	return -1
}
