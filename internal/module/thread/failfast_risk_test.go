package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
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
		RolloutPath:      writeExistingProviderHistoryFile(t, providerThreadID),
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

func TestPostSnapshotResumeRejectsLegacyMemoryPromptSnapshot(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-resume-memory",
		AgentID:        "agent-resume-memory",
		Prompt:         "resume name",
		Model:          "gpt-5.5",
		Cwd:            "/repo",
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
	}, promptSnapshot: &PromptSnapshotRecord{
		DisplayName:           "resume name",
		BaseInstructions:      "stored base",
		DeveloperInstructions: "stored dev",
		Provider:              "codex",
		Version:               contract.PromptAssemblySnapshotVersion,
		SectionSnapshot:       map[string]string{contract.DynamicSectionMemoryContext: "legacy memory"},
		Generation:            3,
	}}
	threads.promptSnapshot.Hash = promptSnapshotHash(
		threads.promptSnapshot.DisplayName,
		threads.promptSnapshot.BaseInstructions,
		threads.promptSnapshot.DeveloperInstructions,
		threads.promptSnapshot.Provider,
		nil,
		threads.promptSnapshot.SectionSnapshot,
		threads.promptSnapshot.Generation,
	)
	const providerThreadID = "11111111-2222-3333-4444-555555555582"
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-resume-memory",
		Provider:         "codex",
		ProviderThreadID: providerThreadID,
		CodexThreadID:    "thread-resume-memory",
		RolloutPath:      writeExistingProviderHistoryFile(t, providerThreadID),
		Cwd:              "/repo",
	}}
	resumeCalled := false
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		resumeCalled = true
		return nil, nil
	}}
	svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, starter, nil, &forkOrchestrationStub{}, nil).(*service)

	_, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-resume-memory"})
	if !errors.Is(err, contract.ErrLegacyMemoryPromptInjection) &&
		(err == nil || !strings.Contains(err.Error(), "prompt snapshot")) {
		t.Fatalf("Resume() error = %v, want legacy memory prompt snapshot rejection", err)
	}
	if resumeCalled {
		t.Fatal("ResumeSession was called despite legacy memory snapshot rejection")
	}
}

func TestPersistResumedSessionCleansRuntimeOnThreadStoreFailure(t *testing.T) {
	t.Parallel()

	storeErr := errors.New("thread store unavailable")
	threads := &stubThreadStore{upsertErr: storeErr}
	bindings := &stubBindingStore{}
	sessions := &stubSessionProvider{session: &stubSession{threadID: "11111111-2222-3333-4444-555555555596"}}
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
		ProviderThreadID: "11111111-2222-3333-4444-555555555596",
		CWD:              "/repo",
	}, resumeState{
		AgentID:              "agent-resume",
		PublicThreadID:       "thread-resume",
		Provider:             "claude",
		ProviderThreadID:     "11111111-2222-3333-4444-555555555596",
		SessionUUID:          "11111111-2222-3333-4444-555555555596",
		ProviderRecoveryHome: t.TempDir(),
		CWD:                  "/repo",
		CreatedAt:            123,
	}, "resume display", &stubSession{threadID: "11111111-2222-3333-4444-555555555596"})

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
		ProviderThreadID: "11111111-2222-3333-4444-555555555593",
		CodexThreadID:    "thread-parent",
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{session: &stubSession{threadID: "11111111-2222-3333-4444-555555555593"}}
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

	const providerThreadID = "11111111-2222-3333-4444-555555555593"
	rolloutPath := writeExistingProviderHistoryFile(t, providerThreadID)
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
		ProviderThreadID: providerThreadID,
		CodexThreadID:    "thread-parent",
		RolloutPath:      rolloutPath,
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{session: &stubSession{threadID: providerThreadID, rolloutPath: rolloutPath}}
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

func TestPostSnapshotForkRejectsLegacyMemoryPromptSnapshot(t *testing.T) {
	t.Parallel()

	originalSession := &stubSession{
		threadID:   "thread-parent",
		forkResult: dto.ForkResult{NewThreadID: "thread-fork"},
	}
	sessions := &stubSessionProvider{session: originalSession}
	bindings := forkParentBindingStore()
	threads := forkParentThreadStore()
	threads.promptSnapshot.SectionSnapshot = map[string]string{contract.DynamicSectionMemory: "legacy memory"}
	threads.promptSnapshot.Hash = promptSnapshotHash(
		threads.promptSnapshot.DisplayName,
		threads.promptSnapshot.BaseInstructions,
		threads.promptSnapshot.DeveloperInstructions,
		threads.promptSnapshot.Provider,
		nil,
		threads.promptSnapshot.SectionSnapshot,
		threads.promptSnapshot.Generation,
	)
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		t.Fatal("ResumeSession should not be called with a legacy memory snapshot")
		return nil, nil
	}}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	_, err := svc.Fork(context.Background(), "thread-parent")
	if !errors.Is(err, contract.ErrLegacyMemoryPromptInjection) &&
		(err == nil || !strings.Contains(err.Error(), "prompt snapshot")) {
		t.Fatalf("Fork() error = %v, want legacy memory prompt snapshot rejection", err)
	}
	if originalSession.forkRequest.ThreadID != "" || orch.launch.AgentID != "" {
		t.Fatalf("side effects before legacy snapshot rejection: forkRequest=%#v launch=%#v", originalSession.forkRequest, orch.launch)
	}
}

func TestResumeForkSessionRejectsLegacyMemoryBeforeStarter(t *testing.T) {
	t.Parallel()

	resumeCalled := false
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		resumeCalled = true
		return nil, nil
	}}
	svc := &service{starter: starter}
	snapshot := contract.PromptAssemblySnapshot{
		DisplayName:      "fork",
		BaseInstructions: "base",
		Provider:         "codex",
		Version:          contract.PromptAssemblySnapshotVersion,
		SectionSnapshot:  map[string]string{contract.DynamicSectionMemory: "legacy memory"},
		Generation:       1,
	}
	snapshot.Hash = promptSnapshotHash(
		snapshot.DisplayName,
		snapshot.BaseInstructions,
		snapshot.DeveloperInstructions,
		snapshot.Provider,
		snapshot.Boundary,
		snapshot.SectionSnapshot,
		snapshot.Generation,
	)
	_, err := svc.resumeForkSession(context.Background(), ResumeRequest{
		Provider:         "codex",
		AgentID:          "agent-fork",
		ThreadID:         "thread-fork",
		ProviderThreadID: "provider-fork",
		CWD:              "/repo",
		PromptSnapshot:   snapshot,
	})
	if !errors.Is(err, contract.ErrLegacyMemoryPromptInjection) {
		t.Fatalf("resumeForkSession() error = %v, want legacy memory sentinel", err)
	}
	if resumeCalled {
		t.Fatal("ResumeSession starter ran before legacy memory snapshot rejection")
	}
}
