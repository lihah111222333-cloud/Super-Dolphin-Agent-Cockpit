package thread

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestServiceResumeInfersProviderAndRebuildsSession(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:   "thread-1",
		AgentID:    "agent-1",
		Prompt:     "resume",
		Model:      "stored-model",
		Cwd:        "/repo",
		CreatedAt:  123,
		Status:     statusCreated,
		LastEventType: "",
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "thread-1",
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
			if req.Model != "override-model" {
				t.Fatalf("Model = %q, want override-model", req.Model)
			}
			session := &stubSession{threadID: "thread-1-resumed"}
			sessions.session = session
			return session, nil
		},
	}

	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, nil, nil).(*service)
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
	if result.ThreadID != "thread-1-resumed" {
		t.Fatalf("ThreadID = %q, want thread-1-resumed", result.ThreadID)
	}
	if result.Status != "resumed" {
		t.Fatalf("Status = %q, want resumed", result.Status)
	}
	if result.Model != "override-model" {
		t.Fatalf("Model = %q, want override-model", result.Model)
	}
	if threads.upsert.ThreadID != "thread-1-resumed" {
		t.Fatalf("persisted thread id = %q, want thread-1-resumed", threads.upsert.ThreadID)
	}
	if bindings.upsert.Provider != "codex" {
		t.Fatalf("persisted provider = %q, want codex", bindings.upsert.Provider)
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
	onResume func(context.Context, dto.ResumeSessionRequest) (contract.Session, error)
}

func (s *stubSessionStarter) StartSession(context.Context, dto.StartSessionRequest) (contract.Session, error) {
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
	threadID      string
	allowedModels []string
	configureErr  error
	caps          dto.CapabilitySet
}

func (s *stubSession) ThreadID() string { return s.threadID }

func (s *stubSession) Capabilities() dto.CapabilitySet { return s.caps }

func (s *stubSession) StartTurn(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
	return nil, errors.New("not implemented")
}

func (s *stubSession) Interrupt(context.Context, dto.InterruptRequest) error { return nil }

func (s *stubSession) ForceComplete(context.Context, dto.ForceCompleteRequest) error { return nil }

func (s *stubSession) ListThreads(context.Context) ([]dto.ThreadRef, error) { return nil, nil }

func (s *stubSession) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, nil
}

func (s *stubSession) ReadHistory(context.Context, string, int) ([]dto.Message, error) { return nil, nil }

func (s *stubSession) Configure(context.Context, dto.ThreadConfigPatch) error { return s.configureErr }

func (s *stubSession) AllowedModels(context.Context) ([]string, error) { return s.allowedModels, nil }

func (s *stubSession) Close(context.Context) error { return nil }

func (s *stubSession) ForceStop() error { return nil }

type stubThreadStore struct {
	thread  *threadstore.Thread
	upsert  threadstore.UpsertParams
	status  threadstore.UpdateStatusParams
}

func (s *stubThreadStore) GetByThreadID(context.Context, string) (*threadstore.Thread, error) {
	if s.thread == nil {
		return nil, errors.New("thread not found")
	}
	thread := *s.thread
	return &thread, nil
}

func (s *stubThreadStore) GetByPort(context.Context, int32) (*threadstore.Thread, error) {
	return nil, errors.New("not implemented")
}

func (s *stubThreadStore) ListAll(context.Context) ([]threadstore.Thread, error) { return nil, nil }

func (s *stubThreadStore) ListRunning(context.Context) ([]threadstore.Thread, error) { return nil, nil }

func (s *stubThreadStore) ListRecoverable(context.Context) ([]threadstore.Thread, error) { return nil, nil }

func (s *stubThreadStore) ListRunningAgents(context.Context) ([]threadstore.RunningAgent, error) {
	return nil, nil
}

func (s *stubThreadStore) Upsert(_ context.Context, params threadstore.UpsertParams) error {
	s.upsert = params
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
	binding *bindingstore.Binding
	upsert  bindingstore.UpsertParams
}

func (s *stubBindingStore) GetByProviderThread(_ context.Context, provider, providerThreadID string) (*bindingstore.Binding, error) {
	if s.binding == nil || s.binding.Provider != provider || s.binding.ProviderThreadID != providerThreadID {
		return nil, errors.New("binding not found")
	}
	binding := *s.binding
	return &binding, nil
}

func (s *stubBindingStore) Upsert(_ context.Context, params bindingstore.UpsertParams) error {
	s.upsert = params
	return nil
}

func (s *stubBindingStore) DeleteByAgentID(context.Context, string) error { return nil }

func (s *stubBindingStore) UpdateSessionUUID(context.Context, bindingstore.UpdateSessionUUIDParams) error {
	return nil
}

func (s *stubBindingStore) SetArchived(context.Context, bindingstore.SetArchivedParams) error { return nil }

func (s *stubBindingStore) GetByAgentID(context.Context, string) (*bindingstore.Binding, error) {
	if s.binding == nil {
		return nil, errors.New("binding not found")
	}
	binding := *s.binding
	return &binding, nil
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
