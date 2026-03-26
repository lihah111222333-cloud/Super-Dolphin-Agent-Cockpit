package thread

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
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
			if req.ThreadID != "provider-thread-1" {
				t.Fatalf("ThreadID = %q, want provider-thread-1", req.ThreadID)
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
	if threads.upsert.ThreadID != "thread-1" {
		t.Fatalf("persisted thread id = %q, want thread-1", threads.upsert.ThreadID)
	}
	if bindings.upsert.AgentID != "" {
		t.Fatalf("binding upsert = %#v, want idempotent no-op", bindings.upsert)
	}
	if orch.launchReq.Cwd != "/repo" {
		t.Fatalf("launch cwd = %q, want /repo", orch.launchReq.Cwd)
	}
	if !reflect.DeepEqual(orch.launchReq.Env, []string{"AGENT_PROVIDER=codex", "AGENT_MODEL=override-model"}) {
		t.Fatalf("launch env = %#v", orch.launchReq.Env)
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

func (p *stubSessionProvider) GetSession(string) (contract.Session, error) {
	if p.session == nil {
		return nil, errors.New("session not found")
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

func (s *stubSession) ThreadID() string { return s.threadID }

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
	return cloneRuntimeConfigMap(s.runtimeConfig)
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
	thread    *threadstore.Thread
	upsert    threadstore.UpsertParams
	upsertErr error
	status    threadstore.UpdateStatusParams
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
	return firstNonEmpty(s.binding.CodexThreadID, s.binding.ProviderThreadID), nil
}

func (s *stubBindingStore) UpdateAgentCwd(context.Context, bindingstore.UpdateAgentCwdParams) error {
	return nil
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
