package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
)

func TestServiceStartRequiresOrchestration(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(context.Context, dto.StartSessionRequest) (contract.Session, error) {
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f607"}
			sessions.session = session
			return session, nil
		},
	}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, nil, nil).(*service)

	_, err := svc.Start(context.Background(), StartRequest{
		AgentID:           "agent-no-orchestration",
		Provider:          "codex",
		CWD:               wantStartCWD(t),
		Prompt:            "start",
		PromptAssemblyRef: promptAssemblyForTest("start"),
	})
	if err == nil || !strings.Contains(err.Error(), "orchestration service is not configured") {
		t.Fatalf("Start() error = %v, want missing orchestration error", err)
	}
	if threads.upsertCount != 0 {
		t.Fatalf("thread upsert count = %d, want 0 before failed launch is persisted", threads.upsertCount)
	}
}

func TestUpsertPublicThreadRequiresThreadStore(t *testing.T) {
	t.Parallel()

	err := (&service{}).upsertPublicThread(context.Background(), threadState{PublicThreadID: "thread-1"}, bindingWriteOutcome{})
	if err == nil || !strings.Contains(err.Error(), "thread store is not configured") {
		t.Fatalf("upsertPublicThread() error = %v, want missing thread store error", err)
	}
}

func TestStartPersistsSubagentIdentityFields(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	bindings := &stubBindingStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(context.Context, dto.StartSessionRequest) (contract.Session, error) {
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f607"}
			sessions.session = session
			return session, nil
		},
	}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, &stubThreadOrchestration{}, nil).(*service)

	_, err := svc.Start(context.Background(), StartRequest{
		AgentID:           "agent-subagent",
		Provider:          "codex",
		CWD:               wantStartCWD(t),
		Prompt:            "start",
		ParentAgentID:     "agent-root",
		AgentType:         "worker",
		AgentMemoryScope:  "project",
		Config:            map[string]any{contract.CodexHomeKey: t.TempDir(), contract.CodexInstanceKeyKey: "default", contract.CodexModelProviderKey: "openai"},
		PromptAssemblyRef: promptAssemblyForTest("start"),
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if threads.upsert.ParentAgentID != "agent-root" ||
		threads.upsert.AgentType != "worker" ||
		threads.upsert.AgentMemoryScope != "project" {
		t.Fatalf("thread upsert identity = %#v, want parent/type/scope persisted", threads.upsert)
	}
}

func TestSavePromptSnapshotRequiresThreadStore(t *testing.T) {
	t.Parallel()

	assembly := ensureStartAssemblySnapshot(contract.StartAssembly{DisplayName: "assembled"}, "codex")
	err := (&service{}).savePromptSnapshot(context.Background(), "thread-1", assembly)
	if err == nil || !strings.Contains(err.Error(), "thread store is not configured") {
		t.Fatalf("savePromptSnapshot() error = %v, want missing thread store error", err)
	}
}

func TestSavePromptSnapshotRejectsEmptySnapshot(t *testing.T) {
	t.Parallel()

	err := (&service{threadStore: &stubThreadStore{}}).savePromptSnapshot(
		context.Background(),
		"thread-1",
		contract.StartAssembly{},
	)
	if err == nil || !strings.Contains(err.Error(), "prompt snapshot is empty") {
		t.Fatalf("savePromptSnapshot() error = %v, want empty snapshot error", err)
	}
}

func TestResolveStablePromptSnapshotPropagatesStoreError(t *testing.T) {
	t.Parallel()

	cause := errors.New("snapshot store unavailable")
	svc := &service{threadStore: &stubThreadStore{promptSnapshotError: cause}}

	_, err := svc.resolveStablePromptSnapshot(
		context.Background(),
		"thread-1",
		"codex",
		contract.PromptAssemblySnapshot{DisplayName: "fallback"},
	)
	if !errors.Is(err, cause) {
		t.Fatalf("resolveStablePromptSnapshot() error = %v, want %v", err, cause)
	}
}

func TestResolveStablePromptSnapshotFailsWhenThreadMetaMissing(t *testing.T) {
	t.Parallel()

	svc := &service{threadStore: &stubThreadStore{threadByID: map[string]*ThreadRecord{}}}

	_, err := svc.resolveStablePromptSnapshot(
		context.Background(),
		"thread-missing",
		"codex",
		contract.PromptAssemblySnapshot{},
	)
	if err == nil || !strings.Contains(err.Error(), `thread "thread-missing" missing`) {
		t.Fatalf("resolveStablePromptSnapshot() error = %v, want missing thread meta error", err)
	}
}

func TestUnarchivePendingLaunchDoesNotRequireBinding(t *testing.T) {
	t.Parallel()

	bindingStore := &stubThreadBindingStore{}
	threadStore := &recordingThreadStore{
		stubThreadStore: &stubThreadStore{thread: &ThreadRecord{
			ThreadID:      "thread-pending",
			Status:        statusArchived,
			PendingLaunch: true,
		}},
	}
	var stopped threaddto.Stopped
	svc := &service{
		bindingStore: bindingStore,
		threadStore:  threadStore,
		emitStopped: func(evt threaddto.Stopped) {
			stopped = evt
		},
	}

	if err := svc.Unarchive(context.Background(), "thread-pending"); err != nil {
		t.Fatalf("Unarchive() error = %v", err)
	}
	if threadStore.status.ThreadID != "thread-pending" || threadStore.status.Status != statusCreated {
		t.Fatalf("thread status = %#v, want pending thread recreated", threadStore.status)
	}
	if len(bindingStore.archived) != 0 {
		t.Fatalf("binding archive calls = %#v, want none for pending_launch", bindingStore.archived)
	}
	if stopped.ThreadID != "thread-pending" || stopped.Status != statusCreated || stopped.Reason != "unarchived_pending_launch" {
		t.Fatalf("stopped event = %#v, want pending thread created/unarchived", stopped)
	}
}

func TestForkSavesInheritedPromptSnapshotAfterNewThreadRowExists(t *testing.T) {
	t.Parallel()

	originalSession := &stubSession{
		threadID:   "thread-parent",
		forkResult: dto.ForkResult{NewThreadID: "thread-fork"},
	}
	forkedSession := &stubSession{threadID: "019d5f6b-aaaa-7760-9d6f-54005553f5b3"}
	sessions := &stubSessionProvider{session: originalSession}
	bindings := forkParentBindingStore()
	bindings.binding.ParentAgentID = "agent-root"
	bindings.binding.AgentType = "worker"
	bindings.binding.AgentMemoryScope = "project"
	parentThreads := forkParentThreadStore()
	parentThreads.thread.ParentAgentID = "agent-root"
	parentThreads.thread.AgentType = "worker"
	parentThreads.thread.AgentMemoryScope = "project"
	threads := &snapshotRequiresThreadRowStore{
		stubThreadStore:      parentThreads,
		wantParentAgentID:    "agent-root",
		wantAgentType:        "worker",
		wantAgentMemoryScope: "project",
	}
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		sessions.session = forkedSession
		return forkedSession, nil
	}}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	if _, err := svc.Fork(context.Background(), "thread-parent"); err != nil {
		t.Fatalf("Fork() error = %v, want snapshot saved after durable fork row exists", err)
	}
	if len(threads.savePromptSnapshotIDs) != 1 || threads.savePromptSnapshotIDs[0] != "thread-fork" {
		t.Fatalf("SavePromptSnapshot IDs = %#v, want [thread-fork]", threads.savePromptSnapshotIDs)
	}
	if threads.upsertCount == 0 {
		t.Fatal("thread upsert count = 0, want fork row persisted before snapshot save")
	}
}

type snapshotRequiresThreadRowStore struct {
	*stubThreadStore
	wantParentAgentID    string
	wantAgentType        string
	wantAgentMemoryScope string
}

func (s *snapshotRequiresThreadRowStore) SavePromptSnapshot(ctx context.Context, threadID string, snapshot PromptSnapshotRecord) error {
	if s.thread == nil || s.thread.ThreadID != threadID {
		return fmt.Errorf("new thread row %q does not exist before snapshot save", threadID)
	}
	if s.wantParentAgentID != "" &&
		(s.upsert.ParentAgentID != s.wantParentAgentID ||
			s.upsert.AgentType != s.wantAgentType ||
			s.upsert.AgentMemoryScope != s.wantAgentMemoryScope) {
		return fmt.Errorf("fork creating upsert identity = %#v, want parent/type/scope", s.upsert)
	}
	return s.stubThreadStore.SavePromptSnapshot(ctx, threadID, snapshot)
}

func TestForkFailsWhenThreadMetaMissing(t *testing.T) {
	t.Parallel()
	const providerThreadID = "019d5f6b-fb3c-7760-9d6f-54005553f5b3"
	originalSession := &stubSession{threadID: providerThreadID, forkResult: dto.ForkResult{NewThreadID: "thread-fork"}}
	forkedSession := &stubSession{threadID: "019d5f6b-aaaa-7760-9d6f-54005553f5b3"}
	sessions := &stubSessionProvider{session: originalSession}
	bindings := &stubBindingStore{binding: &BindingRecord{AgentID: "thread-parent", Provider: "claude", ProviderThreadID: providerThreadID, SessionUUID: providerThreadID, CodexThreadID: "thread-parent", Cwd: "/repo"}}
	threads := &stubThreadStore{threadByID: map[string]*ThreadRecord{}, promptSnapshot: &PromptSnapshotRecord{
		DisplayName:           "Forked Thread",
		BaseInstructions:      "stored base",
		DeveloperInstructions: "stored dev",
		Provider:              "claude",
		Version:               contract.PromptAssemblySnapshotVersion,
		Hash:                  promptSnapshotHash("Forked Thread", "stored base", "stored dev", "claude", nil, nil, 0),
	}}
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		sessions.session = forkedSession
		return forkedSession, nil
	}}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)
	_, err := svc.Fork(context.Background(), "thread-parent")
	if err == nil || !strings.Contains(err.Error(), `thread "thread-parent" missing`) {
		t.Fatalf("Fork() error = %v, want missing thread meta error", err)
	}
	if originalSession.forkRequest.ThreadID != "" || orch.launch.AgentID != "" {
		t.Fatalf("side effects before meta lookup failed: forkRequest=%#v launch=%#v", originalSession.forkRequest, orch.launch)
	}
}

func TestPostSnapshotResumeRejectsMissingPromptSnapshot(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:  "thread-resume",
		AgentID:   "agent-resume",
		Prompt:    "resume name",
		Model:     "gpt-5.5",
		Cwd:       "/repo",
		CreatedAt: 123,
		Status:    statusCreated,
	}}
	const providerThreadID = "11111111-2222-3333-4444-555555555581"
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-resume",
		Provider:         "codex",
		ProviderThreadID: providerThreadID,
		CodexThreadID:    "thread-resume",
		RolloutPath:      writeExistingProviderHistoryFile(t),
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	resumeCalled := false
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		resumeCalled = true
		sessions.session = &stubSession{threadID: providerThreadID}
		return sessions.session, nil
	}}
	orch := &forkOrchestrationStub{}
	svc := NewServiceWithPromptAssembly(
		silentLogger(),
		threads,
		bindings,
		sessions,
		starter,
		nil,
		orch,
		nil,
		&resumeMetadataPromptAssembly{},
		nil,
		nil,
	).(*service)

	_, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-resume"})
	if err == nil || !strings.Contains(err.Error(), "prompt snapshot") {
		t.Fatalf("Resume() error = %v, want missing prompt snapshot error", err)
	}
	if resumeCalled {
		t.Fatal("ResumeSession was called despite missing stored prompt snapshot")
	}
	if orch.launch.AgentID != "" {
		t.Fatalf("orchestration launch = %#v, want none before snapshot preflight passes", orch.launch)
	}
}

func TestPersistResumedSessionCleansRuntimeOnThreadStoreFailure(t *testing.T) {
	t.Parallel()

	storeErr := errors.New("thread store unavailable")
	threads := &stubThreadStore{upsertErr: storeErr}
	bindings := &stubBindingStore{}
	sessions := &stubSessionProvider{session: &stubSession{threadID: "provider-thread-1"}}
	orch := &stubThreadOrchestration{}
	svc := &service{
		threadStore:    threads,
		bindingStore:   bindings,
		sessions:       sessions,
		orchestration:  orch,
		logger:         silentLogger(),
		threadAgents:   map[string]string{},
		reconnectDelay: 0,
	}

	_, err := svc.persistResumedSession(context.Background(), ResumeRequest{
		Provider:         "claude",
		AgentID:          "agent-resume",
		ThreadID:         "thread-resume",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/repo",
	}, resumeState{
		AgentID:          "agent-resume",
		PublicThreadID:   "thread-resume",
		Provider:         "claude",
		ProviderThreadID: "provider-thread-1",
		SessionUUID:      "provider-thread-1",
		CWD:              "/repo",
		CreatedAt:        123,
	}, "resume display", &stubSession{threadID: "provider-thread-1"})

	if !errors.Is(err, storeErr) {
		t.Fatalf("persistResumedSession() error = %v, want %v", err, storeErr)
	}
	if orch.stoppedAgentID != "agent-resume" {
		t.Fatalf("stopped agent = %q, want agent-resume", orch.stoppedAgentID)
	}
	if len(sessions.removed) != 1 || sessions.removed[0] != "agent-resume" {
		t.Fatalf("removed sessions = %#v, want [agent-resume]", sessions.removed)
	}
}

func TestRecoverFailsWhenThreadMetaLookupErrors(t *testing.T) {
	t.Parallel()

	storeErr := errors.New("thread metadata store unavailable")
	threads := &stubThreadStore{getErr: storeErr}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-parent",
		Provider:         "codex",
		ProviderThreadID: "provider-parent",
		CodexThreadID:    "thread-parent",
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{session: &stubSession{threadID: "provider-parent"}}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, &stubSessionStarter{}, nil, orch, nil).(*service)

	_, err := svc.Recover(context.Background(), "thread-parent")
	if !errors.Is(err, storeErr) {
		t.Fatalf("Recover() error = %v, want %v", err, storeErr)
	}
	if len(orch.recovered) != 0 || orch.launch.AgentID != "" || threads.upsertCount != 0 {
		t.Fatalf("side effects before meta lookup failed: recovered=%#v launch=%#v upserts=%d", orch.recovered, orch.launch, threads.upsertCount)
	}
}

func TestRecoverUsesPersistedSubagentIdentity(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:         "thread-parent",
		AgentID:          "agent-parent",
		ParentAgentID:    "agent-root-from-thread",
		AgentType:        "worker-from-thread",
		AgentMemoryScope: "project-from-thread",
		Prompt:           "Recovered Thread",
		Model:            "gpt-5.5",
		Cwd:              "/repo",
		CreatedAt:        123,
	}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-parent",
		Provider:         "codex",
		ProviderThreadID: "provider-parent",
		CodexThreadID:    "thread-parent",
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{session: &stubSession{threadID: "provider-parent"}}
	orch := &forkOrchestrationStub{recoverErr: contract.ErrAgentNotFound}
	svc := NewService(silentLogger(), threads, bindings, sessions, &stubSessionStarter{}, nil, orch, nil).(*service)

	if _, err := svc.Recover(context.Background(), "thread-parent"); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if orch.launch.ParentID != "agent-root-from-thread" ||
		orch.launch.AgentType != "worker-from-thread" ||
		orch.launch.MemoryScope != "project-from-thread" {
		t.Fatalf("recover launch identity = %#v, want persisted thread identity", orch.launch)
	}
	if threads.upsert.ParentAgentID != "agent-root-from-thread" ||
		threads.upsert.AgentType != "worker-from-thread" ||
		threads.upsert.AgentMemoryScope != "project-from-thread" {
		t.Fatalf("recover upsert identity = %#v, want persisted thread identity", threads.upsert)
	}
}

func TestStartFailureCleansProviderSessionBeforeStoppingAgent(t *testing.T) {
	t.Parallel()

	calls := []string{}
	sessions := &cleanupGenerationSessionProvider{generation: 7, calls: &calls}
	sessions.session = &cleanupRecordingSession{
		stubSession: &stubSession{threadID: "agent-start"},
		calls:       &calls,
	}
	starter := &startOnlySessionStarter{
		onStart: func(context.Context, dto.StartSessionRequest) (contract.Session, error) {
			return sessions.session, nil
		},
	}
	orch := &startCleanupOrchestration{calls: &calls}
	svc := NewService(silentLogger(), &stubThreadStore{}, nil, sessions, starter, nil, orch, nil).(*service)

	_, err := svc.Start(context.Background(), StartRequest{
		AgentID:           "agent-start",
		Provider:          "codex",
		CWD:               wantStartCWD(t),
		Prompt:            "start",
		PromptAssemblyRef: promptAssemblyForTest("start"),
	})
	if err == nil || !strings.Contains(err.Error(), "provider session UUID required") {
		t.Fatalf("Start() error = %v, want provider UUID failure", err)
	}
	closeIdx := callIndex(calls, "session_close:agent-start")
	removeIdx := callIndex(calls, "session_remove_generation:agent-start:7")
	stopIdx := callIndex(calls, "agent_stop:agent-start")
	if closeIdx == -1 || removeIdx == -1 || stopIdx == -1 || !(closeIdx < stopIdx && removeIdx < stopIdx) {
		t.Fatalf("cleanup calls = %#v, want close/remove before stop", calls)
	}
}

func TestStartFailureCleanupUsesGenerationGuard(t *testing.T) {
	t.Parallel()

	bindErr := errors.New("bind generation failed")
	calls := []string{}
	sessions := &cleanupGenerationSessionProvider{generation: 7, calls: &calls}
	replacement := &stubSession{threadID: "new-session"}
	sessions.session = &cleanupRecordingSession{
		stubSession: &stubSession{threadID: "agent-start"},
		calls:       &calls,
		onClose: func() {
			sessions.generation = 8
			sessions.session = replacement
		},
	}
	starter := &startOnlySessionStarter{
		onStart: func(context.Context, dto.StartSessionRequest) (contract.Session, error) {
			return sessions.session, nil
		},
	}
	orch := &startCleanupOrchestration{bindErr: bindErr, calls: &calls}
	svc := NewService(silentLogger(), &stubThreadStore{}, nil, sessions, starter, nil, orch, nil).(*service)

	_, err := svc.Start(context.Background(), StartRequest{
		AgentID:           "agent-start",
		Provider:          "codex",
		CWD:               wantStartCWD(t),
		Prompt:            "start",
		PromptAssemblyRef: promptAssemblyForTest("start"),
	})
	if !errors.Is(err, bindErr) {
		t.Fatalf("Start() error = %v, want %v", err, bindErr)
	}
	if sessions.session != replacement {
		t.Fatalf("current session = %#v, want replacement preserved by generation guard", sessions.session)
	}
	if len(sessions.removed) != 0 {
		t.Fatalf("plain RemoveSession calls = %#v, want generation guarded removal only", sessions.removed)
	}
	if len(sessions.removedGenerations) != 1 || sessions.removedGenerations[0] != 7 {
		t.Fatalf("removed generations = %#v, want [7]", sessions.removedGenerations)
	}
}

type cleanupRecordingSession struct {
	*stubSession
	calls   *[]string
	onClose func()
}

func (s *cleanupRecordingSession) Close(context.Context) error {
	recordCall(s.calls, "session_close:"+s.threadID)
	if s.onClose != nil {
		s.onClose()
	}
	return nil
}

type cleanupGenerationSessionProvider struct {
	session            contract.Session
	generation         uint64
	calls              *[]string
	removed            []string
	removedGenerations []uint64
}

func (p *cleanupGenerationSessionProvider) GetSession(agentID string) (contract.Session, error) {
	if p.session == nil {
		return nil, fmt.Errorf("%w for agent %q", contract.ErrSessionNotFound, agentID)
	}
	return p.session, nil
}

func (p *cleanupGenerationSessionProvider) RemoveSession(agentID string) {
	p.removed = append(p.removed, agentID)
	recordCall(p.calls, "session_remove:"+agentID)
	p.session = nil
}

func (p *cleanupGenerationSessionProvider) SessionGeneration(string) uint64 {
	return p.generation
}

func (p *cleanupGenerationSessionProvider) RemoveSessionGeneration(agentID string, generation uint64) {
	p.removedGenerations = append(p.removedGenerations, generation)
	recordCall(p.calls, fmt.Sprintf("session_remove_generation:%s:%d", agentID, generation))
	if generation == p.generation {
		p.session = nil
	}
}

type startCleanupOrchestration struct {
	bindErr error
	calls   *[]string
}

func (s *startCleanupOrchestration) LaunchAgent(_ context.Context, req LaunchAgentRequest) error {
	recordCall(s.calls, "agent_launch:"+req.AgentID)
	return nil
}

func (s *startCleanupOrchestration) StopAgent(_ context.Context, agentID string) error {
	recordCall(s.calls, "agent_stop:"+agentID)
	return nil
}

func (s *startCleanupOrchestration) Recover(context.Context, string) error {
	return nil
}

func (s *startCleanupOrchestration) BindSessionGeneration(_ context.Context, agentID string, _ uint64) error {
	recordCall(s.calls, "agent_bind_generation:"+agentID)
	return s.bindErr
}

func TestPostSnapshotForkRejectsInvalidPromptSnapshot(t *testing.T) {
	t.Parallel()

	originalSession := &stubSession{
		threadID:   "thread-parent",
		forkResult: dto.ForkResult{NewThreadID: "thread-fork"},
	}
	sessions := &stubSessionProvider{session: originalSession}
	bindings := forkParentBindingStore()
	snapshot := validThreadPromptSnapshotForTest("Forked Thread")
	snapshot.SectionSnapshot["identity"] = "tampered after hash"
	threads := forkParentThreadStore()
	threads.promptSnapshot = &snapshot
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		t.Fatal("ResumeSession should not be called with an invalid stored prompt snapshot")
		return nil, nil
	}}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	_, err := svc.Fork(context.Background(), "thread-parent")
	if err == nil || !strings.Contains(err.Error(), "prompt snapshot") {
		t.Fatalf("Fork() error = %v, want invalid prompt snapshot error", err)
	}
	if originalSession.forkRequest.ThreadID != "" {
		t.Fatalf("ForkThread request = %#v, want no provider fork before snapshot preflight", originalSession.forkRequest)
	}
	if orch.launch.AgentID != "" {
		t.Fatalf("orchestration launch = %#v, want none before snapshot preflight passes", orch.launch)
	}
}

func TestLegacyThreadUsesExplicitSnapshotMigrationGate(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T, config []byte, wantErr bool) {
		t.Helper()
		threads := &stubThreadStore{thread: &ThreadRecord{
			ThreadID:         "thread-legacy",
			AgentID:          "agent-legacy",
			ParentAgentID:    "agent-root",
			AgentType:        "worker",
			AgentMemoryScope: "local",
			Prompt:           "legacy name",
			Model:            "gpt-5.5",
			Cwd:              "/repo",
			ConfigOverride:   config,
			CreatedAt:        123,
			Status:           statusCreated,
		}}
		const providerThreadID = "11111111-2222-3333-4444-555555555582"
		bindings := &stubBindingStore{binding: &BindingRecord{
			AgentID:          "agent-legacy",
			ParentAgentID:    "agent-root",
			AgentType:        "worker",
			AgentMemoryScope: "local",
			Provider:         "codex",
			ProviderThreadID: providerThreadID,
			CodexThreadID:    "thread-legacy",
			RolloutPath:      writeExistingProviderHistoryFile(t),
			Cwd:              "/repo",
		}}
		sessions := &stubSessionProvider{}
		resumeCalled := false
		starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
			resumeCalled = true
			sessions.session = &stubSession{threadID: providerThreadID}
			return sessions.session, nil
		}}
		svc := NewServiceWithPromptAssembly(
			silentLogger(),
			threads,
			bindings,
			sessions,
			starter,
			nil,
			&forkOrchestrationStub{},
			nil,
			&resumeMetadataPromptAssembly{},
			nil,
			nil,
		).(*service)

		_, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-legacy"})
		if wantErr {
			if err == nil || !strings.Contains(err.Error(), "legacy") {
				t.Fatalf("Resume() error = %v, want legacy migration gate error", err)
			}
			if resumeCalled {
				t.Fatal("ResumeSession was called without explicit legacy migration gate")
			}
			return
		}
		if err != nil {
			t.Fatalf("Resume() error = %v, want nil with explicit legacy migration gate", err)
		}
		if !resumeCalled {
			t.Fatal("ResumeSession was not called with explicit legacy migration gate")
		}
	}

	t.Run("without_gate_rejects", func(t *testing.T) {
		run(t, nil, true)
	})
	t.Run("with_gate_allows_migration", func(t *testing.T) {
		config := legacyPromptSnapshotMigrationConfig(t)
		run(t, config, false)
	})
}

func TestStartDoesNotPublishStartedBeforeSnapshotSaved(t *testing.T) {
	t.Parallel()

	cause := errors.New("snapshot write failed")
	threads := &stubThreadStore{savePromptSnapshotError: cause}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(context.Context, dto.StartSessionRequest) (contract.Session, error) {
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f607"}
			sessions.session = session
			return session, nil
		},
	}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, &forkOrchestrationStub{}, nil).(*service)
	startedCount := 0
	svc.emitStarted = func(threaddto.Started) {
		startedCount++
	}

	_, err := svc.Start(context.Background(), StartRequest{
		AgentID:           "agent-start",
		Provider:          "codex",
		CWD:               wantStartCWD(t),
		Prompt:            "start",
		PromptAssemblyRef: promptAssemblyForTest("start"),
	})
	if !errors.Is(err, cause) {
		t.Fatalf("Start() error = %v, want %v", err, cause)
	}
	if startedCount != 0 {
		t.Fatalf("thread.Started emitted %d times; want 0 before prompt snapshot is saved", startedCount)
	}
}

func TestForkDoesNotPublishStartedBeforeSnapshotSaved(t *testing.T) {
	t.Parallel()

	cause := errors.New("fork snapshot write failed")
	originalSession := &stubSession{
		threadID:   "thread-parent",
		forkResult: dto.ForkResult{NewThreadID: "thread-fork"},
	}
	forkedSession := &stubSession{threadID: "019d5f6b-aaaa-7760-9d6f-54005553f5b3"}
	sessions := &stubSessionProvider{session: originalSession}
	bindings := forkParentBindingStore()
	threads := forkParentThreadStore()
	threads.savePromptSnapshotError = cause
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		sessions.session = forkedSession
		return forkedSession, nil
	}}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, &forkOrchestrationStub{}, nil).(*service)
	startedCount := 0
	svc.emitStarted = func(threaddto.Started) {
		startedCount++
	}

	_, err := svc.Fork(context.Background(), "thread-parent")
	if !errors.Is(err, cause) {
		t.Fatalf("Fork() error = %v, want %v", err, cause)
	}
	if startedCount != 0 {
		t.Fatalf("thread.Started emitted %d times; want 0 before fork prompt snapshot is saved", startedCount)
	}
}

func TestPromptSnapshotHashCoversSectionSnapshot(t *testing.T) {
	t.Parallel()

	assembly := ensureStartAssemblySnapshot(contract.StartAssembly{
		DisplayName:           "snapshot name",
		BaseInstructions:      "snapshot base",
		DeveloperInstructions: "snapshot dev",
		ResolvedSections: []contract.ResolvedPromptSection{
			{Name: "identity", Content: "You are Codex."},
			{Name: "language", Content: "Use Chinese."},
		},
		Snapshot: contract.PromptAssemblySnapshot{Generation: 9},
	}, "codex")
	snapshot := assembly.Snapshot
	if !storedPromptSnapshotValid(snapshot, "codex") {
		t.Fatalf("storedPromptSnapshotValid() = false for freshly generated snapshot: %#v", snapshot)
	}

	tampered := snapshot
	tampered.SectionSnapshot = clonePromptSectionMap(snapshot.SectionSnapshot)
	tampered.SectionSnapshot["language"] = "Use French."
	if storedPromptSnapshotValid(tampered, "codex") {
		t.Fatal("storedPromptSnapshotValid() = true after section snapshot tamper, want false")
	}
}

func validThreadPromptSnapshotForTest(displayName string) PromptSnapshotRecord {
	assembly := ensureStartAssemblySnapshot(contract.StartAssembly{
		DisplayName:           displayName,
		BaseInstructions:      "stored base",
		DeveloperInstructions: "stored dev",
		ResolvedSections: []contract.ResolvedPromptSection{
			{Name: "identity", Content: "stable identity"},
		},
		Snapshot: contract.PromptAssemblySnapshot{Generation: 7},
	}, "codex")
	return promptSnapshotRecordToStore(toStoredPromptSnapshot(assembly.Snapshot))
}

func legacyPromptSnapshotMigrationConfig(t *testing.T) []byte {
	t.Helper()
	config, err := encodeStoredThreadConfig(storedThreadConfig{
		Runtime: map[string]any{
			"legacyPromptSnapshotMigration": true,
		},
	})
	if err != nil {
		t.Fatalf("encodeStoredThreadConfig() error = %v", err)
	}
	return config
}
