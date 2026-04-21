package thread

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestReadMessagesSupportsTimestampCursorCompatibility(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 3, 21, 12, 0, 0, 0, time.UTC)
	session := &historyTestSession{
		threadID: "thread-1",
		messages: []dto.Message{
			{Role: "user", Content: "m1", Timestamp: base.Add(1 * time.Minute)},
			{Role: "assistant", Content: "m2", Timestamp: base.Add(2 * time.Minute)},
			{Role: "user", Content: "m3", Timestamp: base.Add(3 * time.Minute)},
			{Role: "assistant", Content: "m4", Timestamp: base.Add(4 * time.Minute)},
			{Role: "user", Content: "m5", Timestamp: base.Add(5 * time.Minute)},
			{Role: "assistant", Content: "m6", Timestamp: base.Add(6 * time.Minute)},
		},
	}
	svc := NewService(
		silentLogger(),
		nil,
		newHistoryTestBindingStore(&bindingstore.Binding{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "thread-1",
			CodexThreadID:    "thread-1",
		}),
		&historyTestSessionProvider{sessions: map[string]contract.Session{"agent-1": session}},
		nil,
		nil,
		nil,
		nil,
	)

	got, err := svc.ReadMessages(context.Background(), "thread-1", 2, base.Add(5*time.Minute).Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("ReadMessages() error = %v", err)
	}
	want := dto.ThreadMessagesResult{
		Messages: []dto.Message{
			{ID: 4, AgentID: "agent-1", Role: "assistant", EventType: "agent_message", Content: "m4", Timestamp: base.Add(4 * time.Minute)},
			{ID: 3, AgentID: "agent-1", Role: "user", EventType: "", Content: "m3", Timestamp: base.Add(3 * time.Minute)},
		},
		Total: 6,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadMessages() = %#v, want %#v", got, want)
	}
	if got := session.readCalls; len(got) != 1 {
		t.Fatalf("read calls = %#v, want 1 call", got)
	}
	if session.readCalls[0].Limit != 0 {
		t.Fatalf("read limits = %#v, want [0]", session.readCalls)
	}
	for _, call := range session.readCalls {
		if call.ThreadID != "thread-1" {
			t.Fatalf("read thread id = %q, want thread-1", call.ThreadID)
		}
	}
}

func TestForkedThreadHistoryUsesForkThreadID(t *testing.T) {
	t.Parallel()

	parentSession := &historyTestSession{
		threadID:   "thread-1",
		forkResult: dto.ForkResult{NewThreadID: "thread-2"},
	}
	forkedSession := &historyTestSession{threadID: "thread-2"}
	threadStore := &historyTestThreadStore{
		threads: map[string]threadstore.Thread{
			"thread-1": {
				ThreadID:  "thread-1",
				AgentID:   "agent-1",
				Prompt:    "demo",
				Model:     "gpt-5",
				Cwd:       "/tmp/demo",
				CreatedAt: 123,
			},
		},
	}
	bindings := newHistoryTestBindingStore(&bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "thread-1",
		CodexThreadID:    "thread-1",
		Cwd:              "/tmp/demo",
	})
	sessions := &historyTestSessionProvider{sessions: map[string]contract.Session{
		"agent-1": parentSession,
	}}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			if req.AgentID != "thread-2" || req.ThreadID != "thread-2" {
				t.Fatalf("resume request = %#v, want independent thread-2 agent/session", req)
			}
			sessions.sessions["thread-2"] = forkedSession
			return forkedSession, nil
		},
	}
	svc := NewService(
		silentLogger(),
		threadStore,
		bindings,
		sessions,
		starter,
		nil,
		&forkOrchestrationStub{},
		nil,
	)

	result, err := svc.Fork(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("Fork() error = %v", err)
	}
	if result.NewThreadID != "thread-2" {
		t.Fatalf("Fork() new thread id = %q, want thread-2", result.NewThreadID)
	}
	if len(threadStore.upserts) != 1 || threadStore.upserts[0].OwnerThreadID != "thread-1" {
		t.Fatalf("fork upserts = %#v, want owner_thread_id thread-1", threadStore.upserts)
	}

	if _, err := svc.ReadHistory(context.Background(), "thread-2", 5); err != nil {
		t.Fatalf("ReadHistory(fork) error = %v", err)
	}
	if len(forkedSession.readCalls) == 0 {
		t.Fatal("expected forked thread history read to hit session")
	}
	if got := forkedSession.readCalls[len(forkedSession.readCalls)-1].ThreadID; got != "thread-2" {
		t.Fatalf("forked thread history target = %q, want thread-2", got)
	}
	if len(parentSession.readCalls) != 0 {
		t.Fatalf("parent session read calls = %#v, want no reads after forked history lookup", parentSession.readCalls)
	}
}

type historyTestBindingStore struct {
	bindings map[string]bindingstore.Binding
}

func newHistoryTestBindingStore(binding *bindingstore.Binding) *historyTestBindingStore {
	store := &historyTestBindingStore{bindings: map[string]bindingstore.Binding{}}
	if binding != nil {
		store.bindings[strings.TrimSpace(binding.AgentID)] = *binding
	}
	return store
}

func (s *historyTestBindingStore) GetByProviderThread(_ context.Context, provider, providerThreadID string) (*bindingstore.Binding, error) {
	for _, binding := range s.bindings {
		if strings.TrimSpace(binding.Provider) != strings.TrimSpace(provider) || strings.TrimSpace(binding.ProviderThreadID) != strings.TrimSpace(providerThreadID) {
			continue
		}
		copy := binding
		return &copy, nil
	}
	return nil, platformdb.ErrNotFound
}

func (s *historyTestBindingStore) Upsert(_ context.Context, params bindingstore.UpsertParams) error {
	if s.bindings == nil {
		s.bindings = map[string]bindingstore.Binding{}
	}
	s.bindings[strings.TrimSpace(params.AgentID)] = bindingstore.Binding{
		AgentID:          params.AgentID,
		Provider:         params.Provider,
		ProviderThreadID: params.ProviderThreadID,
		CodexThreadID:    params.CodexThreadID,
		RolloutPath:      params.RolloutPath,
		Cwd:              params.Cwd,
		CreatedAt:        params.CreatedAt,
		UpdatedAt:        params.UpdatedAt,
	}
	return nil
}

func (s *historyTestBindingStore) DeleteByAgentID(context.Context, string) error { return nil }

func (s *historyTestBindingStore) UpdateSessionUUID(context.Context, bindingstore.UpdateSessionUUIDParams) error {
	return nil
}

func (s *historyTestBindingStore) SetArchived(context.Context, bindingstore.SetArchivedParams) error {
	return nil
}

func (s *historyTestBindingStore) GetByAgentID(_ context.Context, agentID string) (*bindingstore.Binding, error) {
	binding, ok := s.bindings[strings.TrimSpace(agentID)]
	if !ok {
		return nil, platformdb.ErrNotFound
	}
	copy := binding
	return &copy, nil
}

func (s *historyTestBindingStore) BindAgentThread(context.Context, bindingstore.BindAgentThreadParams) error {
	return nil
}

func (s *historyTestBindingStore) UnbindAgentThread(context.Context, string) error { return nil }

func (s *historyTestBindingStore) ListAgentThreadBindings(context.Context) ([]bindingstore.Binding, error) {
	if len(s.bindings) == 0 {
		return nil, nil
	}
	out := make([]bindingstore.Binding, 0, len(s.bindings))
	for _, binding := range s.bindings {
		out = append(out, binding)
	}
	return out, nil
}

func (s *historyTestBindingStore) GetThreadByAgent(_ context.Context, agentID string) (string, error) {
	binding, ok := s.bindings[strings.TrimSpace(agentID)]
	if !ok {
		return "", platformdb.ErrNotFound
	}
	return shared.FirstNonEmpty(binding.CodexThreadID, binding.ProviderThreadID), nil
}

func (s *historyTestBindingStore) UpdateAgentCwd(context.Context, bindingstore.UpdateAgentCwdParams) error {
	return nil
}

type historyTestSessionProvider struct {
	sessions map[string]contract.Session
}

func (p *historyTestSessionProvider) GetSession(agentID string) (contract.Session, error) {
	if p.sessions == nil {
		return nil, errors.New("session not found")
	}
	if session, ok := p.sessions[strings.TrimSpace(agentID)]; ok {
		return session, nil
	}
	return nil, errors.New("session not found")
}

func (p *historyTestSessionProvider) RemoveSession(string) {}

type historyTestThreadStore struct {
	threads  map[string]threadstore.Thread
	upserts  []threadstore.UpsertParams
	statuses []threadstore.UpdateStatusParams
}

func (s *historyTestThreadStore) GetByThreadID(_ context.Context, threadID string) (*threadstore.Thread, error) {
	thread, ok := s.threads[strings.TrimSpace(threadID)]
	if !ok {
		return nil, platformdb.ErrNotFound
	}
	copy := thread
	return &copy, nil
}

func (s *historyTestThreadStore) GetByPort(context.Context, int32) (*threadstore.Thread, error) {
	return nil, errors.New("not implemented")
}

func (s *historyTestThreadStore) ListAll(context.Context) ([]threadstore.Thread, error) {
	return nil, nil
}

func (s *historyTestThreadStore) ListRunning(context.Context) ([]threadstore.Thread, error) {
	return nil, nil
}

func (s *historyTestThreadStore) ListRecoverable(context.Context) ([]threadstore.Thread, error) {
	return nil, nil
}

func (s *historyTestThreadStore) ListRunningAgents(context.Context) ([]threadstore.RunningAgent, error) {
	return nil, nil
}

func (*historyTestThreadStore) SavePromptSnapshot(context.Context, string, threadstore.PromptSnapshot) error {
	return nil
}

func (*historyTestThreadStore) LoadPromptSnapshot(context.Context, string) (*threadstore.PromptSnapshot, error) {
	return nil, nil
}

func (s *historyTestThreadStore) Upsert(_ context.Context, params threadstore.UpsertParams) error {
	s.upserts = append(s.upserts, params)
	if s.threads == nil {
		s.threads = map[string]threadstore.Thread{}
	}
	s.threads[params.ThreadID] = threadstore.Thread{
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

func (s *historyTestThreadStore) UpdateStatus(_ context.Context, params threadstore.UpdateStatusParams) error {
	s.statuses = append(s.statuses, params)
	return nil
}

func (*historyTestThreadStore) UpdateLaunchResult(context.Context, threadstore.UpdateLaunchResultParams) error {
	return nil
}

func (s *historyTestThreadStore) DeleteByThreadID(context.Context, string) error { return nil }

func (s *historyTestThreadStore) ResetRunning(context.Context) error { return nil }

func (s *historyTestThreadStore) ExpireStale(context.Context, threadstore.ExpireStaleParams) (int64, error) {
	return 0, nil
}

func (s *historyTestThreadStore) RunningExists(context.Context, string) (bool, error) {
	return false, nil
}

func (s *historyTestThreadStore) ListCwds(context.Context) ([]threadstore.ThreadCwd, error) {
	return nil, nil
}

func (s *historyTestThreadStore) ListCwdsByPrefix(context.Context, string) ([]threadstore.ThreadCwd, error) {
	return nil, nil
}

type historyReadCall struct {
	ThreadID string
	Limit    int
}

type historyTestSession struct {
	threadID   string
	threads    []dto.ThreadRef
	messages   []dto.Message
	forkResult dto.ForkResult
	readCalls  []historyReadCall
}

func (s *historyTestSession) ThreadID() string    { return s.threadID }
func (s *historyTestSession) RolloutPath() string { return "" }

func (s *historyTestSession) Capabilities() dto.CapabilitySet { return nil }

func (s *historyTestSession) StartTurn(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
	return nil, nil
}

func (s *historyTestSession) Interrupt(context.Context, dto.InterruptRequest) error { return nil }

func (s *historyTestSession) ForceComplete(context.Context, dto.ForceCompleteRequest) error {
	return nil
}

func (s *historyTestSession) ListThreads(context.Context) ([]dto.ThreadRef, error) {
	if len(s.threads) == 0 {
		return nil, nil
	}
	return append([]dto.ThreadRef(nil), s.threads...), nil
}

func (s *historyTestSession) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return s.forkResult, nil
}

func (s *historyTestSession) ReadHistory(_ context.Context, threadID string, limit int) ([]dto.Message, error) {
	s.readCalls = append(s.readCalls, historyReadCall{
		ThreadID: strings.TrimSpace(threadID),
		Limit:    limit,
	})
	if limit <= 0 || limit >= len(s.messages) {
		return append([]dto.Message(nil), s.messages...), nil
	}
	start := len(s.messages) - limit
	return append([]dto.Message(nil), s.messages[start:]...), nil
}

func (s *historyTestSession) Configure(context.Context, dto.ThreadConfigPatch) error { return nil }

func (s *historyTestSession) Close(context.Context) error { return nil }

func (s *historyTestSession) ForceStop() error { return nil }
