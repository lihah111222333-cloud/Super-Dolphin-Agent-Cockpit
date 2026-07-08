package prompt_test

import (
	"context"
	"errors"

	contractpkg "github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	thread "github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	turnpkg "github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

type capturingSessionBridge struct {
	session  contractpkg.Session
	startReq dto.StartSessionRequest
}

type e2ePromptStore struct{ promptstore.Store }

func newE2EPromptStore() promptstore.Store { return e2ePromptStore{} }

func (e2ePromptStore) List(context.Context, promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
	return nil, nil
}

func (e2ePromptStore) ListRecallSections(context.Context, string) ([]promptstore.PromptTemplateSection, error) {
	return nil, nil
}

func (e2ePromptStore) ListDefaultRuleSections(context.Context, string) ([]promptstore.PromptTemplateSection, error) {
	return nil, nil
}

func newMockSession(threadID string) contractpkg.Session { return &mockSession{threadID: threadID} }

func newMockSessionWithRolloutPath(threadID, rolloutPath string) contractpkg.Session {
	return &mockSession{threadID: threadID, rolloutPath: rolloutPath}
}

func (b *capturingSessionBridge) StartSession(_ context.Context, req dto.StartSessionRequest) (contractpkg.Session, error) {
	b.startReq = req
	if b.session == nil {
		b.session = newMockSession("019e0bcb-0cf7-7982-964f-c2654783ba17")
	}
	return b.session, nil
}

func (*capturingSessionBridge) ResumeSession(context.Context, dto.ResumeSessionRequest) (contractpkg.Session, error) {
	return nil, errors.New("unexpected resume session")
}

func (b *capturingSessionBridge) GetSession(string) (contractpkg.Session, error) {
	if b.session == nil {
		return nil, contractpkg.ErrSessionNotFound
	}
	return b.session, nil
}

func (b *capturingSessionBridge) RemoveSession(string) { b.session = nil }

type mockSession struct {
	contractpkg.Session
	threadID    string
	rolloutPath string
}

func (s *mockSession) ThreadID() string              { return s.threadID }
func (s *mockSession) RolloutPath() string           { return s.rolloutPath }
func (*mockSession) Capabilities() dto.CapabilitySet { return nil }
func (*mockSession) Close(context.Context) error     { return nil }

type capturingThreadStore struct {
	threadstore.Store
	thread *threadstore.Thread
	upsert threadstore.UpsertParams
	status threadstore.UpdateStatusParams
}

func (s *capturingThreadStore) GetByThreadID(_ context.Context, threadID string) (*threadstore.Thread, error) {
	if s.thread == nil || (s.thread.ThreadID != "" && s.thread.ThreadID != threadID) {
		return nil, platformdb.ErrNotFound
	}
	thread := *s.thread
	return &thread, nil
}

func (*capturingThreadStore) SavePromptSnapshot(context.Context, string, threadstore.PromptSnapshot) error {
	return nil
}
func (s *capturingThreadStore) Upsert(_ context.Context, params threadstore.UpsertParams) error {
	s.upsert = params
	s.thread = &threadstore.Thread{ThreadID: params.ThreadID, AgentID: params.ThreadID, Prompt: params.Prompt, Model: params.Model, Cwd: params.Cwd, Status: params.Status, CreatedAt: params.CreatedAt, UpdatedAt: params.UpdatedAt}
	return nil
}
func (s *capturingThreadStore) UpdateStatus(_ context.Context, params threadstore.UpdateStatusParams) error {
	s.status = params
	return nil
}
func (*capturingThreadStore) CountChildren(context.Context, string) (int64, error) { return 0, nil }
func (*capturingThreadStore) Exists(context.Context, string) (bool, error)         { return false, nil }

type capturingBindingStore struct {
	bindingstore.Store
	binding *bindingstore.Binding
	upsert  bindingstore.UpsertParams
}

func (s *capturingBindingStore) GetByProviderThread(_ context.Context, provider, providerThreadID string) (*bindingstore.Binding, error) {
	if s.binding == nil || s.binding.Provider != provider || s.binding.ProviderThreadID != providerThreadID {
		return nil, platformdb.ErrNotFound
	}
	binding := *s.binding
	return &binding, nil
}

func (s *capturingBindingStore) Upsert(_ context.Context, params bindingstore.UpsertParams) error {
	s.upsert = params
	// fixture 必须与 production sqlc Upsert 字段对齐，防止 verifyThreadBinding
	// 因漏写 codex_home / instance_key / agent_type / agent_memory_scope 等
	// optional 字段在未来 StartRequest 注入对应 Config 时炸（B-4.7 latent fix）
	s.binding = &bindingstore.Binding{
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

func (s *capturingBindingStore) UpdateSessionUUID(_ context.Context, params bindingstore.UpdateSessionUUIDParams) error {
	if s.binding != nil && s.binding.AgentID == params.AgentID {
		s.binding.SessionUUID = params.SessionUUID
		if s.binding.ProviderThreadID == "" || s.binding.ProviderThreadID == s.binding.AgentID {
			s.binding.ProviderThreadID = params.SessionUUID
		}
		s.binding.UpdatedAt = params.UpdatedAt
	}
	return nil
}
func (s *capturingBindingStore) UpdateProviderThreadID(_ context.Context, params bindingstore.UpdateProviderThreadIDParams) error {
	if s.binding != nil && s.binding.AgentID == params.AgentID {
		s.binding.ProviderThreadID = params.ProviderThreadID
		s.binding.UpdatedAt = params.UpdatedAt
	}
	return nil
}
func (s *capturingBindingStore) GetByAgentID(_ context.Context, agentID string) (*bindingstore.Binding, error) {
	if s.binding == nil || (agentID != "" && s.binding.AgentID != agentID) {
		return nil, platformdb.ErrNotFound
	}
	binding := *s.binding
	return &binding, nil
}
func (s *capturingBindingStore) ListAgentThreadBindings(context.Context) ([]bindingstore.Binding, error) {
	if s.binding == nil {
		return nil, nil
	}
	return []bindingstore.Binding{*s.binding}, nil
}
func (s *capturingBindingStore) GetThreadByAgent(context.Context, string) (string, error) {
	if s.binding == nil {
		return "", platformdb.ErrNotFound
	}
	return s.binding.CodexThreadID, nil
}

type capturingOrchestration struct{ launchReq thread.LaunchAgentRequest }

func (o *capturingOrchestration) LaunchAgent(_ context.Context, req thread.LaunchAgentRequest) error {
	o.launchReq = req
	return nil
}
func (*capturingOrchestration) StopAgent(context.Context, string) error { return nil }
func (*capturingOrchestration) Recover(context.Context, string) error   { return nil }
func (*capturingOrchestration) BindSessionGeneration(context.Context, string, uint64) error {
	return nil
}

type noopTurnService struct{}

func (*noopTurnService) PrepareTurn(context.Context, contractpkg.Session, turnpkg.PrepareInput) (dto.TurnRequest, error) {
	return dto.TurnRequest{}, nil
}
func (*noopTurnService) StartTurn(context.Context, contractpkg.Session, dto.TurnRequest) (contractpkg.TurnHandle, error) {
	return nil, nil
}
func (*noopTurnService) SteerTurn(context.Context, contractpkg.Session, string, turnpkg.PrepareInput) (contractpkg.TurnHandle, error) {
	return nil, nil
}
func (*noopTurnService) InterruptTurn(context.Context, contractpkg.Session, string) (turnpkg.TurnStatus, error) {
	return turnpkg.TurnStatus{}, nil
}
func (*noopTurnService) InterruptActiveTurn(context.Context, contractpkg.Session, string) error {
	return nil
}
func (*noopTurnService) ForceCompleteTurn(context.Context, contractpkg.Session) error { return nil }
func (*noopTurnService) CleanupThread(context.Context, string, string) error          { return nil }
func (*noopTurnService) TrackTurn(context.Context, string) (turnpkg.TurnStatus, error) {
	return turnpkg.TurnStatus{}, nil
}
func (*noopTurnService) LookupByDedupeKey(context.Context, string) (turnpkg.TurnStatus, bool, error) {
	return turnpkg.TurnStatus{}, false, nil
}
