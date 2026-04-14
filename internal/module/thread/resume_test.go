package thread

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func TestServiceResumeInfersProviderAndRebuildsSession(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:      "thread-1",
		AgentID:       "agent-1",
		Prompt:        "resume",
		Model:         "stored-model",
		Cwd:           "/repo",
		CreatedAt:     123,
		Status:        statusCreated,
		LastEventType: "",
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-1",
		CodexThreadID:    "thread-1",
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			if req.Provider != "codex" {
				t.Fatalf("Provider = %q, want codex", req.Provider)
			}
			if req.AgentID != "agent-1" {
				t.Fatalf("AgentID = %q, want agent-1", req.AgentID)
			}
			if req.ThreadID != "thread-1" {
				t.Fatalf("ThreadID = %q, want thread-1", req.ThreadID)
			}
			if req.ProviderThreadID != "provider-thread-1" {
				t.Fatalf("ProviderThreadID = %q, want provider-thread-1", req.ProviderThreadID)
			}
			if req.Model != "override-model" {
				t.Fatalf("Model = %q, want override-model", req.Model)
			}
			session := &stubSession{threadID: "provider-thread-1"}
			sessions.session = session
			return session, nil
		},
	}

	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)
	result, err := svc.Resume(context.Background(), ResumeRequest{
		ThreadID: "thread-1",
		Model:    "override-model",
	})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if len(sessions.removed) != 1 || sessions.removed[0] != "agent-1" {
		t.Fatalf("removed sessions = %#v, want [agent-1]", sessions.removed)
	}
	if result.ThreadID != "thread-1" {
		t.Fatalf("ThreadID = %q, want thread-1", result.ThreadID)
	}
	if result.SessionID != "provider-thread-1" {
		t.Fatalf("SessionID = %q, want provider-thread-1", result.SessionID)
	}
	if result.Status != "resumed" {
		t.Fatalf("Status = %q, want resumed", result.Status)
	}
	if result.Model != "override-model" {
		t.Fatalf("Model = %q, want override-model", result.Model)
	}
	if result.CWD != "/repo" {
		t.Fatalf("CWD = %q, want /repo", result.CWD)
	}
	if threads.upsert.ThreadID != "thread-1" {
		t.Fatalf("persisted thread id = %q, want thread-1", threads.upsert.ThreadID)
	}
	if bindings.upsert.AgentID != "" {
		t.Fatalf("binding upsert = %#v, want idempotent no-op", bindings.upsert)
	}
	if orch.launchReq.Cwd != "/repo" {
		t.Fatalf("launch cwd = %q, want /repo", orch.launchReq.Cwd)
	}
	if orch.launchReq.Name != "resume" {
		t.Fatalf("launch name = %q, want resume", orch.launchReq.Name)
	}
	if threads.upsert.Prompt != "resume" {
		t.Fatalf("persisted prompt = %q, want resume", threads.upsert.Prompt)
	}
	if !reflect.DeepEqual(orch.launchReq.Env, []string{"AGENT_PROVIDER=codex", "AGENT_MODEL=override-model"}) {
		t.Fatalf("launch env = %#v", orch.launchReq.Env)
	}
}

func TestServiceResumePrefersStoredPromptSnapshot(t *testing.T) {
	t.Parallel()

	stored := threadstore.PromptSnapshot{
		DisplayName:           "resume",
		BaseInstructions:      "stored base",
		DeveloperInstructions: "stored dev",
		Provider:              "codex",
		Version:               contract.PromptAssemblySnapshotVersion,
		Hash:                  promptSnapshotHash("resume", "stored base", "stored dev", "codex"),
	}
	threads := &stubThreadStore{
		thread: &threadstore.Thread{
			ThreadID:      "thread-1",
			AgentID:       "agent-1",
			Prompt:        "resume",
			Model:         "stored-model",
			Cwd:           "/repo",
			CreatedAt:     123,
			Status:        statusCreated,
			LastEventType: "",
		},
		promptSnapshot: &stored,
	}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-1",
		CodexThreadID:    "thread-1",
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			if req.PromptSnapshot.BaseInstructions != stored.BaseInstructions || req.PromptSnapshot.DeveloperInstructions != stored.DeveloperInstructions {
				t.Fatalf("PromptSnapshot = %#v, want stored snapshot", req.PromptSnapshot)
			}
			session := &stubSession{threadID: "provider-thread-1"}
			sessions.session = session
			return session, nil
		},
	}

	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, nil, nil).(*service)
	_, err := svc.Resume(context.Background(), ResumeRequest{
		ThreadID: "thread-1",
		PromptSnapshot: contract.PromptAssemblySnapshot{
			DisplayName:           "caller",
			BaseInstructions:      "caller base",
			DeveloperInstructions: "caller dev",
			Provider:              "codex",
		},
	})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
}

func TestServiceResumeRehydratesClaudeOverrideConfig(t *testing.T) {
	t.Parallel()

	model := "claude-sonnet-4-20250514[1m]"
	effort := "max"
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:       "thread-1",
		AgentID:        "agent-1",
		Prompt:         "resume",
		Model:          "sonnet",
		Cwd:            "/repo",
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Model: model, Effort: effort}),
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "claude",
		ProviderThreadID: "provider-thread-1",
		CodexThreadID:    "thread-1",
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			if req.Provider != "claude" {
				t.Fatalf("Provider = %q, want claude", req.Provider)
			}
			if req.Model != model {
				t.Fatalf("Model = %q, want %q", req.Model, model)
			}
			if req.Effort != effort {
				t.Fatalf("Effort = %q, want %q", req.Effort, effort)
			}
			if req.ConfigOverride.Model == nil || *req.ConfigOverride.Model != model {
				t.Fatalf("ConfigOverride.Model = %#v, want %q", req.ConfigOverride.Model, model)
			}
			if req.ConfigOverride.Effort == nil || *req.ConfigOverride.Effort != effort {
				t.Fatalf("ConfigOverride.Effort = %#v, want %q", req.ConfigOverride.Effort, effort)
			}
			session := &stubSession{threadID: "provider-thread-1"}
			sessions.session = session
			return session, nil
		},
	}

	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)
	result, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-1"})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if result.Model != model {
		t.Fatalf("result.Model = %q, want %q", result.Model, model)
	}
	if !reflect.DeepEqual(orch.launchReq.Env, []string{"AGENT_PROVIDER=claude", "AGENT_MODEL=" + model}) {
		t.Fatalf("launch env = %#v", orch.launchReq.Env)
	}
}

func TestServiceResumeClaudeWithoutStoredOverrideDoesNotInventConfigOverride(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:      "thread-1",
		AgentID:       "agent-1",
		Prompt:        "resume",
		Model:         "sonnet",
		Cwd:           "/repo",
		CreatedAt:     123,
		Status:        statusCreated,
		LastEventType: "",
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "claude",
		ProviderThreadID: "provider-thread-1",
		CodexThreadID:    "thread-1",
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			if req.Model != "sonnet" {
				t.Fatalf("Model = %q, want sonnet", req.Model)
			}
			if req.Effort != "" {
				t.Fatalf("Effort = %q, want empty", req.Effort)
			}
			if req.ConfigOverride.Model != nil || req.ConfigOverride.Effort != nil {
				t.Fatalf("ConfigOverride = %#v, want empty", req.ConfigOverride)
			}
			session := &stubSession{threadID: "provider-thread-1"}
			sessions.session = session
			return session, nil
		},
	}

	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)
	if _, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-1"}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if !reflect.DeepEqual(orch.launchReq.Env, []string{"AGENT_PROVIDER=claude", "AGENT_MODEL=sonnet"}) {
		t.Fatalf("launch env = %#v", orch.launchReq.Env)
	}
}

func TestBackgroundResumeIfNeededRehydratesClaudeOverrideConfig(t *testing.T) {
	t.Parallel()

	model := "claude-sonnet-4-20250514[1m]"
	effort := "max"
	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:       "thread-1",
		AgentID:        "agent-1",
		Prompt:         "resume",
		Model:          "sonnet",
		Cwd:            "/repo",
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{Model: model, Effort: effort}),
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "claude",
		ProviderThreadID: "provider-thread-1",
		CodexThreadID:    "thread-1",
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	resumeReqCh := make(chan dto.ResumeSessionRequest, 1)
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			select {
			case resumeReqCh <- req:
			default:
			}
			session := &stubSession{threadID: "provider-thread-1"}
			sessions.session = session
			return session, nil
		},
	}

	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, nil, nil).(*service)
	svc.backgroundResumeIfNeeded(context.Background(), "thread-1")

	select {
	case req := <-resumeReqCh:
		if req.Model != model || req.Effort != effort {
			t.Fatalf("ResumeSession request = %#v, want model/effort restored", req)
		}
		if req.ConfigOverride.Model == nil || *req.ConfigOverride.Model != model {
			t.Fatalf("ConfigOverride.Model = %#v, want %q", req.ConfigOverride.Model, model)
		}
		if req.ConfigOverride.Effort == nil || *req.ConfigOverride.Effort != effort {
			t.Fatalf("ConfigOverride.Effort = %#v, want %q", req.ConfigOverride.Effort, effort)
		}
	case <-time.After(time.Second):
		t.Fatal("backgroundResumeIfNeeded() did not trigger resume")
	}
}

func TestSetNameSyncsProviderWhenSupported(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-1",
		AgentID:   "agent-1",
		Prompt:    "before",
		CreatedAt: 123,
		Status:    statusCreated,
	}}
	session := &stubSession{threadID: "thread-1"}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "thread-1",
		CodexThreadID:    "thread-1",
	}}
	sessions := &stubSessionProvider{session: session}
	svc := NewService(silentLogger(), threads, bindings, sessions, nil, nil, nil, nil)

	if err := svc.SetName(context.Background(), "thread-1", "after"); err != nil {
		t.Fatalf("SetName() error = %v", err)
	}
	if threads.upsert.Prompt != "after" {
		t.Fatalf("persisted prompt = %q, want after", threads.upsert.Prompt)
	}
	if !reflect.DeepEqual(session.setThreadNameCalls, []string{"thread-1:after"}) {
		t.Fatalf("provider rename calls = %#v", session.setThreadNameCalls)
	}
}

func TestSetModelReturnsFriendlyCapabilityError(t *testing.T) {
	t.Parallel()

	sessions := &stubSessionProvider{session: &stubSession{
		threadID:      "thread-1",
		allowedModels: []string{"sonnet"},
		configureErr:  dto.NewCapabilityError(dto.CapModelSwitch, "claude"),
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
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
	var capErr *dto.CapabilityError
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
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
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
	var capErr *dto.CapabilityError
	if !errors.As(err, &capErr) {
		t.Fatalf("error = %v, want CapabilityError", err)
	}
	if capErr.Capability != dto.CapContextCompact || capErr.Driver != "claude" {
		t.Fatalf("capability error = %#v, want context_compact/claude", capErr)
	}
}

type stubSessionStarter struct {
	onStart  func(context.Context, dto.StartSessionRequest) (contract.Session, error)
	onResume func(context.Context, dto.ResumeSessionRequest) (contract.Session, error)
}

func (s *stubSessionStarter) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	if s.onStart != nil {
		return s.onStart(ctx, req)
	}
	return nil, errors.New("unexpected start session")
}

func (s *stubSessionStarter) ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	return s.onResume(ctx, req)
}

type stubSessionProvider struct {
	session contract.Session
	removed []string
}

func (p *stubSessionProvider) GetSession(agentID string) (contract.Session, error) {
	if p.session == nil {
		return nil, fmt.Errorf("%w for agent %q", contract.ErrSessionNotFound, agentID)
	}
	return p.session, nil
}

func (p *stubSessionProvider) RemoveSession(agentID string) {
	p.removed = append(p.removed, agentID)
	p.session = nil
}

type stubSession struct {
	threadID           string
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
}

func (s *stubSession) ThreadID() string    { return s.threadID }
func (s *stubSession) RolloutPath() string { return "" }

func (s *stubSession) Capabilities() dto.CapabilitySet { return s.caps }

func (s *stubSession) StartTurn(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
	return nil, errors.New("not implemented")
}

func (s *stubSession) Steer(context.Context, dto.SteerRequest) error { return nil }

func (s *stubSession) Interrupt(context.Context, dto.InterruptRequest) error { return nil }

func (s *stubSession) ForceComplete(context.Context, dto.ForceCompleteRequest) error { return nil }

func (s *stubSession) ListThreads(context.Context) ([]dto.ThreadRef, error) { return nil, nil }

func (s *stubSession) ForkThread(_ context.Context, req dto.ForkRequest) (dto.ForkResult, error) {
	s.forkRequest = req
	return s.forkResult, nil
}

func (s *stubSession) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
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

func (s *stubSession) Close(context.Context) error { return nil }

func (s *stubSession) ForceStop() error { return nil }

func (s *stubSession) SetThreadName(_ context.Context, threadID, name string) error {
	s.setThreadNameCalls = append(s.setThreadNameCalls, threadID+":"+name)
	return nil
}

type stubThreadStore struct {
	thread              *threadstore.Thread
	upsert              threadstore.UpsertParams
	upsertErr           error
	status              threadstore.UpdateStatusParams
	promptSnapshot      *threadstore.PromptSnapshot
	promptSnapshotError error
	promptSnapshotID    string
}

func (s *stubThreadStore) GetByThreadID(_ context.Context, threadID string) (*threadstore.Thread, error) {
	if s.thread == nil || (s.thread.ThreadID != "" && s.thread.ThreadID != threadID) {
		return nil, platformdb.ErrNotFound
	}
	thread := *s.thread
	return &thread, nil
}

func (s *stubThreadStore) GetByPort(context.Context, int32) (*threadstore.Thread, error) {
	return nil, errors.New("not implemented")
}

func (s *stubThreadStore) ListAll(context.Context) ([]threadstore.Thread, error) { return nil, nil }

func (s *stubThreadStore) ListRunning(context.Context) ([]threadstore.Thread, error) { return nil, nil }

func (s *stubThreadStore) ListRecoverable(context.Context) ([]threadstore.Thread, error) {
	return nil, nil
}

func (s *stubThreadStore) ListRunningAgents(context.Context) ([]threadstore.RunningAgent, error) {
	return nil, nil
}

func (s *stubThreadStore) SavePromptSnapshot(_ context.Context, threadID string, snapshot threadstore.PromptSnapshot) error {
	if s.promptSnapshotError != nil {
		return s.promptSnapshotError
	}
	s.promptSnapshotID = threadID
	snapshotCopy := snapshot
	snapshotCopy.SectionSnapshot = clonePromptSectionMap(snapshot.SectionSnapshot)
	s.promptSnapshot = &snapshotCopy
	return nil
}

func (s *stubThreadStore) LoadPromptSnapshot(context.Context, string) (*threadstore.PromptSnapshot, error) {
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

func (s *stubThreadStore) Upsert(_ context.Context, params threadstore.UpsertParams) error {
	if s.upsertErr != nil {
		return s.upsertErr
	}
	s.upsert = params
	s.thread = &threadstore.Thread{
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

func (s *stubThreadStore) UpdateStatus(_ context.Context, params threadstore.UpdateStatusParams) error {
	s.status = params
	return nil
}

func (s *stubThreadStore) DeleteByThreadID(context.Context, string) error { return nil }

func (s *stubThreadStore) ResetRunning(context.Context) error { return nil }

func (s *stubThreadStore) ExpireStale(context.Context, threadstore.ExpireStaleParams) (int64, error) {
	return 0, nil
}

func (s *stubThreadStore) RunningExists(context.Context, string) (bool, error) { return false, nil }

func (s *stubThreadStore) ListCwds(context.Context) ([]threadstore.ThreadCwd, error) { return nil, nil }

func (s *stubThreadStore) ListCwdsByPrefix(context.Context, string) ([]threadstore.ThreadCwd, error) {
	return nil, nil
}

type stubBindingStore struct {
	binding        *bindingstore.Binding
	upsert         bindingstore.UpsertParams
	upserts        []bindingstore.UpsertParams
	deleteAgentIDs []string
	deleteErr      error
}

func (s *stubBindingStore) GetByProviderThread(_ context.Context, provider, providerThreadID string) (*bindingstore.Binding, error) {
	if s.binding == nil || s.binding.Provider != provider || s.binding.ProviderThreadID != providerThreadID {
		return nil, platformdb.ErrNotFound
	}
	binding := *s.binding
	return &binding, nil
}

func (s *stubBindingStore) Upsert(_ context.Context, params bindingstore.UpsertParams) error {
	s.upsert = params
	s.upserts = append(s.upserts, params)
	s.binding = &bindingstore.Binding{
		AgentID:          params.AgentID,
		Provider:         params.Provider,
		ProviderThreadID: params.ProviderThreadID,
		CodexThreadID:    params.CodexThreadID,
		Cwd:              params.Cwd,
		CreatedAt:        params.CreatedAt,
		UpdatedAt:        params.UpdatedAt,
	}
	return nil
}

func (s *stubBindingStore) DeleteByAgentID(_ context.Context, agentID string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deleteAgentIDs = append(s.deleteAgentIDs, agentID)
	if s.binding != nil && s.binding.AgentID == agentID {
		s.binding = nil
	}
	return nil
}

func (s *stubBindingStore) UpdateSessionUUID(context.Context, bindingstore.UpdateSessionUUIDParams) error {
	return nil
}

func (s *stubBindingStore) SetArchived(context.Context, bindingstore.SetArchivedParams) error {
	return nil
}

func (s *stubBindingStore) GetByAgentID(_ context.Context, agentID string) (*bindingstore.Binding, error) {
	return s.bindingForAgent(agentID)
}

func (s *stubBindingStore) bindingForAgent(agentID string) (*bindingstore.Binding, error) {
	if s.binding == nil || (agentID != "" && s.binding.AgentID != agentID) {
		return nil, platformdb.ErrNotFound
	}
	binding := *s.binding
	return &binding, nil
}

func (s *stubBindingStore) BindAgentThread(context.Context, bindingstore.BindAgentThreadParams) error {
	return nil
}

func (s *stubBindingStore) UnbindAgentThread(context.Context, string) error { return nil }

func (s *stubBindingStore) ListAgentThreadBindings(context.Context) ([]bindingstore.Binding, error) {
	if s.binding == nil {
		return nil, nil
	}
	return []bindingstore.Binding{*s.binding}, nil
}

func (s *stubBindingStore) GetThreadByAgent(context.Context, string) (string, error) {
	if s.binding == nil {
		return "", platformdb.ErrNotFound
	}
	return shared.FirstNonEmpty(s.binding.CodexThreadID, s.binding.ProviderThreadID), nil
}

func (s *stubBindingStore) UpdateAgentCwd(context.Context, bindingstore.UpdateAgentCwdParams) error {
	return nil
}

func silentLogger() *pkglogger.Logger {
	return pkglogger.Get()
}
