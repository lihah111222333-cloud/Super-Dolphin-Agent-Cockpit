package thread

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestBackgroundResumeIfNeededSkipsInvalidProviderThreadID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		providerThreadID string
	}{
		{name: "empty", providerThreadID: ""},
		{name: "agent placeholder", providerThreadID: "agent-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			threads := &stubThreadStore{thread: &ThreadRecord{
				ThreadID:  "thread-1",
				AgentID:   "agent-1",
				Prompt:    "resume",
				Model:     "gpt-5.5",
				Cwd:       "/repo",
				CreatedAt: 123,
				Status:    statusCreated,
			}}
			bindings := &stubBindingStore{binding: &BindingRecord{
				AgentID:          "agent-1",
				Provider:         "claude",
				ProviderThreadID: tt.providerThreadID,
				CodexThreadID:    "thread-1",
				Cwd:              "/repo",
			}}
			resumeReqCh := make(chan dto.ResumeSessionRequest, 1)
			starter := &stubSessionStarter{
				onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
					resumeReqCh <- req
					return &stubSession{threadID: tt.providerThreadID}, nil
				},
			}

			svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, starter, nil, nil, nil).(*service)
			svc.backgroundResumeIfNeeded(context.Background(), "thread-1")

			assertNoResumeStarted(t, resumeReqCh)
			if _, ok := svc.resumeInFlight.Load("agent-1"); ok {
				t.Fatalf("resumeInFlight set for provider_thread_id %q", tt.providerThreadID)
			}
		})
	}
}

func TestBackgroundResumeIfNeededSkipsArchivedBinding(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:  "thread-1",
		AgentID:   "agent-1",
		Prompt:    "resume",
		Model:     "gpt-5.5",
		Cwd:       "/repo",
		CreatedAt: 123,
		Status:    statusCreated,
	}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "22222222-3333-4444-5555-666666666666",
		CodexThreadID:    "thread-1",
		Cwd:              "/repo",
		Archived:         true,
	}}
	resumeReqCh := make(chan dto.ResumeSessionRequest, 1)
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			resumeReqCh <- req
			return &stubSession{threadID: "provider-thread-1"}, nil
		},
	}

	svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, starter, nil, nil, nil).(*service)
	svc.backgroundResumeIfNeeded(context.Background(), "thread-1")

	assertNoResumeStarted(t, resumeReqCh)
	if _, ok := svc.resumeInFlight.Load("agent-1"); ok {
		t.Fatal("resumeInFlight set for archived binding")
	}
}

func TestBackgroundResumeIfNeededSkipsStoppedThread(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:  "thread-1",
		AgentID:   "agent-1",
		Prompt:    "resume",
		Model:     "gpt-5.5",
		Cwd:       "/repo",
		CreatedAt: 123,
		Status:    statusStopped,
	}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "33333333-4444-5555-6666-777777777777",
		CodexThreadID:    "thread-1",
		Cwd:              "/repo",
	}}
	resumeReqCh := make(chan dto.ResumeSessionRequest, 1)
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			resumeReqCh <- req
			return &stubSession{threadID: "33333333-4444-5555-6666-777777777777"}, nil
		},
	}

	svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, starter, nil, nil, nil).(*service)
	svc.backgroundResumeIfNeeded(context.Background(), "thread-1")

	assertNoResumeStarted(t, resumeReqCh)
}

func TestResumeRejectsLifecycleBlockedAgent(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:  "thread-1",
		AgentID:   "agent-1",
		Prompt:    "resume",
		Model:     "gpt-5.5",
		Cwd:       "/repo",
		CreatedAt: 123,
		Status:    statusCreated,
	}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-1",
		CodexThreadID:    "thread-1",
		Cwd:              "/repo",
	}}
	svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, &stubSessionStarter{}, nil, nil, nil).(*service)
	svc.blockResumeForAgent("agent-1")

	_, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-1"})
	if !errors.Is(err, errResumeLifecycleBlocked) {
		t.Fatalf("Resume() err = %v, want errResumeLifecycleBlocked", err)
	}
}

func TestProcessSessionRecoverySkipsArchivedBinding(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:  "thread-1",
		AgentID:   "agent-1",
		Prompt:    "resume",
		Model:     "gpt-5.5",
		Cwd:       "/repo",
		CreatedAt: 123,
		Status:    statusArchived,
	}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-1",
		CodexThreadID:    "thread-1",
		Cwd:              "/repo",
		Archived:         true,
	}}
	resumeReqCh := make(chan dto.ResumeSessionRequest, 1)
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			resumeReqCh <- req
			return &stubSession{threadID: "provider-thread-1"}, nil
		},
	}
	svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, starter, nil, nil, nil).(*service)

	svc.processSessionRecovery(context.Background(), newAgentFailedForWorker("agent-1", "thread-1", true))

	assertNoResumeStarted(t, resumeReqCh)
}

func TestSetModelReturnsFriendlyCapabilityError(t *testing.T) {
	t.Parallel()

	sessions := &stubSessionProvider{session: &stubSession{
		threadID:      "thread-1",
		allowedModels: []string{"sonnet"},
		configureErr:  contract.NewCapabilityError(dto.CapModelSwitch, "claude"),
	}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-1",
		Provider:         "claude",
		ProviderThreadID: "thread-1",
		CodexThreadID:    "thread-1",
	}}
	svc := NewService(silentLogger(), nil, bindings, sessions, nil, nil, nil, nil)

	_, err := svc.SetModel(context.Background(), "thread-1", "sonnet")
	if err == nil {
		t.Fatal("SetModel() error = nil, want capability error")
	}
	if err.Error() != errRuntimeModelSwitchUnsupported {
		t.Fatalf("error = %q, want %q", err.Error(), errRuntimeModelSwitchUnsupported)
	}
	var capErr *contract.CapabilityError
	if !errors.As(err, &capErr) {
		t.Fatalf("error = %v, want CapabilityError", err)
	}
	if capErr.Capability != dto.CapModelSwitch || capErr.Driver != "claude" {
		t.Fatalf("capability error = %#v, want model_switch/claude", capErr)
	}
}

func TestCompactReturnsFriendlyCapabilityError(t *testing.T) {
	t.Parallel()

	sessions := &stubSessionProvider{session: &stubSession{threadID: "thread-1"}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-1",
		Provider:         "claude",
		ProviderThreadID: "thread-1",
		CodexThreadID:    "thread-1",
	}}
	svc := NewService(silentLogger(), nil, bindings, sessions, nil, nil, nil, nil)

	_, err := svc.Compact(context.Background(), "thread-1", "")
	if err == nil {
		t.Fatal("Compact() error = nil, want capability error")
	}
	if err.Error() != errContextCompactUnsupported {
		t.Fatalf("error = %q, want %q", err.Error(), errContextCompactUnsupported)
	}
	var capErr *contract.CapabilityError
	if !errors.As(err, &capErr) {
		t.Fatalf("error = %v, want CapabilityError", err)
	}
	if capErr.Capability != dto.CapContextCompact || capErr.Driver != "claude" {
		t.Fatalf("capability error = %#v, want context_compact/claude", capErr)
	}
}

func assertNoResumeStarted(t *testing.T, ch <-chan dto.ResumeSessionRequest) {
	t.Helper()
	select {
	case req := <-ch:
		t.Fatalf("unexpected ResumeSession request: %#v", req)
	case <-time.After(100 * time.Millisecond):
	}
}

type stubSessionStarter struct {
	onStart  func(context.Context, dto.StartSessionRequest) (contract.Session, error)
	onResume func(context.Context, dto.ResumeSessionRequest) (contract.Session, error)
}

func (s *stubSessionStarter) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	if s.onStart != nil {
		session, err := s.onStart(ctx, req)
		if err != nil {
			return nil, err
		}
		return attachStartedCodexRuntimeIdentityForTest(req, session), nil
	}
	return nil, errors.New("unexpected start session")
}

func (s *stubSessionStarter) ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	return s.onResume(ctx, req)
}

type stubSessionProvider struct {
	session    contract.Session
	sessions   map[string]contract.Session
	removed    []string
	generation uint64
}

func (p *stubSessionProvider) GetSession(agentID string) (contract.Session, error) {
	// Check multi-agent sessions map first (used by eviction tests).
	if p.sessions != nil {
		if s, ok := p.sessions[agentID]; ok {
			return s, nil
		}
	}
	if p.session == nil {
		return nil, fmt.Errorf("%w for agent %q", contract.ErrSessionNotFound, agentID)
	}
	return p.session, nil
}

func (p *stubSessionProvider) RemoveSession(agentID string) {
	p.removed = append(p.removed, agentID)
	if p.sessions != nil {
		delete(p.sessions, agentID)
	}
	p.session = nil
}

func (p *stubSessionProvider) SessionGeneration(string) uint64 {
	if p.generation != 0 {
		return p.generation
	}
	return 1
}

type stubSession struct {
	stubSessionUnusedMethods

	threadID           string
	rolloutPath        string
	allowedModels      []string
	configureErr       error
	configurePatch     dto.ThreadConfigPatch
	configureCalls     int
	readConfigResult   dto.ThreadConfig
	runtimeConfig      map[string]any
	forkResult         dto.ForkResult
	forkRequest        dto.ForkRequest
	caps               dto.CapabilitySet
	setThreadNameCalls []string
	setThreadNameErr   error
}

func (s *stubSession) ThreadID() string    { return s.threadID }
func (s *stubSession) RolloutPath() string { return s.rolloutPath }

func (s *stubSession) Capabilities() dto.CapabilitySet { return s.caps }

type stubSessionUnusedMethods struct{}

func (stubSessionUnusedMethods) StartTurn(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
	return nil, errors.New("not implemented")
}

func (stubSessionUnusedMethods) Steer(context.Context, dto.SteerRequest) error { return nil }

func (stubSessionUnusedMethods) Interrupt(context.Context, dto.InterruptRequest) error { return nil }

func (stubSessionUnusedMethods) ForceComplete(context.Context, dto.ForceCompleteRequest) error {
	return nil
}

func (stubSessionUnusedMethods) ListThreads(context.Context) ([]dto.ThreadRef, error) {
	return nil, nil
}

func (s *stubSession) ForkThread(_ context.Context, req dto.ForkRequest) (dto.ForkResult, error) {
	s.forkRequest = req
	return s.forkResult, nil
}

func (stubSessionUnusedMethods) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	return nil, nil
}

func (s *stubSession) ReadConfig(context.Context, string) (dto.ThreadConfig, error) {
	return s.readConfigResult, nil
}

func (s *stubSession) RuntimeConfigSnapshot() map[string]any {
	return shared.CloneRuntimeConfigMap(s.runtimeConfig)
}

func (s *stubSession) Configure(_ context.Context, patch dto.ThreadConfigPatch) error {
	s.configureCalls++
	s.configurePatch = patch
	return s.configureErr
}

func (s *stubSession) AllowedModels(context.Context) ([]string, error) { return s.allowedModels, nil }

func (stubSessionUnusedMethods) Close(context.Context) error { return nil }

func (stubSessionUnusedMethods) ForceStop() error { return nil }

func (s *stubSession) SetThreadName(_ context.Context, threadID, name string) error {
	s.setThreadNameCalls = append(s.setThreadNameCalls, threadID+":"+name)
	return s.setThreadNameErr
}

type stubThreadStore struct {
	stubThreadStoreLifecycleNoop
	stubThreadStoreListNoop

	thread                  *ThreadRecord
	threads                 []ThreadRecord
	threadByID              map[string]*ThreadRecord
	getErr                  error
	upsert                  ThreadUpsert
	upsertCount             int
	upsertErr               error
	existsErr               error
	countChildrenErr        error
	status                  ThreadStatusUpdate
	promptSnapshot          *PromptSnapshotRecord
	promptSnapshotError     error
	savePromptSnapshotError error
	loadPromptSnapshotError error
	savePromptSnapshotIDs   []string
	promptSnapshotID        string
}

func (s *stubThreadStore) GetByThreadID(_ context.Context, threadID string) (*ThreadRecord, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.threadByID != nil {
		if t, ok := s.threadByID[threadID]; ok && t != nil {
			thread := *t
			return &thread, nil
		}
		return nil, platformdb.ErrNotFound
	}
	if s.thread == nil || (s.thread.ThreadID != "" && s.thread.ThreadID != threadID) {
		return nil, platformdb.ErrNotFound
	}
	thread := *s.thread
	return &thread, nil
}

type stubThreadStoreLifecycleNoop struct{}

func (stubThreadStoreLifecycleNoop) GetByPort(context.Context, int32) (*ThreadRecord, error) {
	return nil, errors.New("not implemented")
}

func (s *stubThreadStore) ListAll(context.Context) ([]ThreadRecord, error) {
	if s.threads != nil {
		return s.threads, nil
	}
	if s.thread != nil {
		return []ThreadRecord{*s.thread}, nil
	}
	return nil, nil
}

func (s *stubThreadStore) ListConfigsByIDs(ctx context.Context, threadIDs []string) ([]ThreadRecord, error) {
	idMap := make(map[string]bool)
	for _, id := range threadIDs {
		idMap[id] = true
	}
	var result []ThreadRecord
	all, _ := s.ListAll(ctx)
	for _, t := range all {
		if idMap[t.ThreadID] {
			result = append(result, t)
		}
	}
	return result, nil
}

func (stubThreadStoreLifecycleNoop) ListRunning(context.Context) ([]ThreadRecord, error) {
	return nil, nil
}

func (stubThreadStoreLifecycleNoop) ListRecoverable(context.Context) ([]ThreadRecord, error) {
	return nil, nil
}

func (stubThreadStoreLifecycleNoop) ListRunningAgents(context.Context) ([]threadstore.RunningAgent, error) {
	return nil, nil
}

func (s *stubThreadStore) SavePromptSnapshot(_ context.Context, threadID string, snapshot PromptSnapshotRecord) error {
	s.savePromptSnapshotIDs = append(s.savePromptSnapshotIDs, threadID)
	if s.savePromptSnapshotError != nil {
		return s.savePromptSnapshotError
	}
	if s.promptSnapshotError != nil {
		return s.promptSnapshotError
	}
	s.promptSnapshotID = threadID
	snapshotCopy := snapshot
	snapshotCopy.SectionSnapshot = clonePromptSectionMap(snapshot.SectionSnapshot)
	s.promptSnapshot = &snapshotCopy
	return nil
}

func (s *stubThreadStore) LoadPromptSnapshot(context.Context, string) (*PromptSnapshotRecord, error) {
	if s.loadPromptSnapshotError != nil {
		return nil, s.loadPromptSnapshotError
	}
	if s.promptSnapshotError != nil {
		return nil, s.promptSnapshotError
	}
	if s.promptSnapshot == nil {
		return nil, nil
	}
	snapshotCopy := *s.promptSnapshot
	snapshotCopy.SectionSnapshot = clonePromptSectionMap(snapshotCopy.SectionSnapshot)
	return &snapshotCopy, nil
}

func (s *stubThreadStore) Upsert(_ context.Context, params ThreadUpsert) error {
	if s.upsertErr != nil {
		return s.upsertErr
	}
	s.upsertCount++
	s.upsert = params
	s.thread = &ThreadRecord{
		ThreadID:       params.ThreadID,
		Prompt:         params.Prompt,
		Model:          params.Model,
		Cwd:            params.Cwd,
		Status:         params.Status,
		CreatedAt:      params.CreatedAt,
		UpdatedAt:      params.UpdatedAt,
		OwnerThreadID:  params.OwnerThreadID,
		ConfigOverride: params.ConfigOverride,
	}
	return nil
}

func (s *stubThreadStore) UpdateStatus(_ context.Context, params ThreadStatusUpdate) error {
	s.status = params
	return nil
}

func (stubThreadStoreLifecycleNoop) UpdateLaunchResult(context.Context, threadstore.UpdateLaunchResultParams) error {
	return nil
}

func (stubThreadStoreLifecycleNoop) DeleteByThreadID(context.Context, string) error { return nil }

func (stubThreadStoreLifecycleNoop) ResetRunning(context.Context) error { return nil }

func (stubThreadStoreLifecycleNoop) ExpireStale(context.Context, threadstore.ExpireStaleParams) (int64, error) {
	return 0, nil
}

func (stubThreadStoreLifecycleNoop) RunningExists(context.Context, string) (bool, error) {
	return false, nil
}

type stubThreadStoreListNoop struct{}

func (stubThreadStoreListNoop) ListCwds(context.Context) ([]threadstore.ThreadCwd, error) {
	return nil, nil
}

func (stubThreadStoreListNoop) ListCwdsByPrefix(context.Context, string) ([]threadstore.ThreadCwd, error) {
	return nil, nil
}

func (s *stubThreadStore) CountChildren(context.Context, string) (int64, error) {
	if s.countChildrenErr != nil {
		return 0, s.countChildrenErr
	}
	return 0, nil
}

func (s *stubThreadStore) Exists(_ context.Context, threadID string) (bool, error) {
	if s.existsErr != nil {
		return false, s.existsErr
	}
	if s.thread != nil && s.thread.ThreadID == threadID {
		return true, nil
	}
	return false, nil
}

func (stubThreadStoreListNoop) CountAll(context.Context) (int64, error) { return 0, nil }

type stubBindingStore struct {
	stubBindingStoreNoopMethods

	binding                *BindingRecord
	bindings               []BindingRecord
	upsert                 BindingUpsert
	upserts                []BindingUpsert
	deleteAgentIDs         []string
	deleteErr              error
	sessionUpdates         []BindingSessionUUIDUpdate
	updateProviderThreadID BindingProviderThreadIDUpdate
}

func (s *stubBindingStore) GetByProviderThread(_ context.Context, provider, providerThreadID string) (*BindingRecord, error) {
	if s.binding == nil || s.binding.Provider != provider || s.binding.ProviderThreadID != providerThreadID {
		return nil, platformdb.ErrNotFound
	}
	binding := *s.binding
	return &binding, nil
}

func (s *stubBindingStore) Upsert(_ context.Context, params BindingUpsert) error {
	s.upsert = params
	s.upserts = append(s.upserts, params)
	// fixture 与 production sqlc Upsert 15 字段对齐，防止 verifyThreadBinding
	// 下游读到空字段产生 mismatch。B-4.7 仅修 prompt 模块 fixture，
	// reviewer 反审指出本 fixture 同样漂移，本 commit 补全。
	s.binding = &BindingRecord{
		AgentID:            params.AgentID,
		Provider:           params.Provider,
		ProviderThreadID:   params.ProviderThreadID,
		CodexThreadID:      params.CodexThreadID,
		RolloutPath:        params.RolloutPath,
		Cwd:                params.Cwd,
		ParentAgentID:      params.ParentAgentID,
		AgentType:          params.AgentType,
		AgentMemoryScope:   params.AgentMemoryScope,
		SessionUUID:        params.SessionUUID,
		CodexHome:          params.CodexHome,
		CodexInstanceKey:   params.CodexInstanceKey,
		CodexModelProvider: params.CodexModelProvider,
		CreatedAt:          params.CreatedAt,
		UpdatedAt:          params.UpdatedAt,
	}
	return nil
}
