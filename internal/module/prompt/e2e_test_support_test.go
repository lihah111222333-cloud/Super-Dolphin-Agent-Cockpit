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
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

type capturingSessionBridge struct {
	session  contractpkg.Session
	startReq dto.StartSessionRequest
}

func newMockSession(threadID string) contractpkg.Session { return &mockSession{threadID: threadID} }

func (b *capturingSessionBridge) StartSession(_ context.Context, req dto.StartSessionRequest) (contractpkg.Session, error) {
	b.startReq = req
	if b.session == nil {
		b.session = newMockSession("provider-thread-1")
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

type mockSession struct{ threadID string }

func (s *mockSession) ThreadID() string              { return s.threadID }
func (*mockSession) RolloutPath() string             { return "" }
func (*mockSession) Capabilities() dto.CapabilitySet { return nil }
func (*mockSession) StartTurn(context.Context, dto.TurnRequest) (contractpkg.TurnHandle, error) {
	return nil, errors.New("not implemented")
}
func (*mockSession) Interrupt(context.Context, dto.InterruptRequest) error         { return nil }
func (*mockSession) ForceComplete(context.Context, dto.ForceCompleteRequest) error { return nil }
func (*mockSession) ListThreads(context.Context) ([]dto.ThreadRef, error)          { return nil, nil }
func (*mockSession) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, nil
}
func (*mockSession) ReadHistory(context.Context, string, int) ([]dto.Message, error) { return nil, nil }
func (*mockSession) Configure(context.Context, dto.ThreadConfigPatch) error          { return nil }
func (*mockSession) Close(context.Context) error                                     { return nil }
func (*mockSession) ForceStop() error                                                { return nil }

type capturingThreadStore struct {
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

func (*capturingThreadStore) GetByPort(context.Context, int32) (*threadstore.Thread, error) {
	return nil, platformdb.ErrNotFound
}
func (*capturingThreadStore) ListAll(context.Context) ([]threadstore.Thread, error) { return nil, nil }
func (*capturingThreadStore) ListRunning(context.Context) ([]threadstore.Thread, error) {
	return nil, nil
}
func (*capturingThreadStore) ListRecoverable(context.Context) ([]threadstore.Thread, error) {
	return nil, nil
}
func (*capturingThreadStore) ListRunningAgents(context.Context) ([]threadstore.RunningAgent, error) {
	return nil, nil
}
func (*capturingThreadStore) SavePromptSnapshot(context.Context, string, threadstore.PromptSnapshot) error {
	return nil
}
func (*capturingThreadStore) LoadPromptSnapshot(context.Context, string) (*threadstore.PromptSnapshot, error) {
	return nil, nil
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
func (*capturingThreadStore) DeleteByThreadID(context.Context, string) error { return nil }
func (*capturingThreadStore) ResetRunning(context.Context) error             { return nil }
func (*capturingThreadStore) ExpireStale(context.Context, threadstore.ExpireStaleParams) (int64, error) {
	return 0, nil
}
func (*capturingThreadStore) RunningExists(context.Context, string) (bool, error) { return false, nil }
func (*capturingThreadStore) ListCwds(context.Context) ([]threadstore.ThreadCwd, error) {
	return nil, nil
}
func (*capturingThreadStore) ListCwdsByPrefix(context.Context, string) ([]threadstore.ThreadCwd, error) {
	return nil, nil
}

type capturingBindingStore struct {
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
	s.binding = &bindingstore.Binding{AgentID: params.AgentID, Provider: params.Provider, ProviderThreadID: params.ProviderThreadID, CodexThreadID: params.CodexThreadID, Cwd: params.Cwd, SessionUUID: params.SessionUUID, CreatedAt: params.CreatedAt, UpdatedAt: params.UpdatedAt}
	return nil
}

func (*capturingBindingStore) DeleteByAgentID(context.Context, string) error { return nil }
func (*capturingBindingStore) UpdateSessionUUID(context.Context, bindingstore.UpdateSessionUUIDParams) error {
	return nil
}
func (*capturingBindingStore) SetArchived(context.Context, bindingstore.SetArchivedParams) error {
	return nil
}
func (s *capturingBindingStore) GetByAgentID(_ context.Context, agentID string) (*bindingstore.Binding, error) {
	if s.binding == nil || (agentID != "" && s.binding.AgentID != agentID) {
		return nil, platformdb.ErrNotFound
	}
	binding := *s.binding
	return &binding, nil
}
func (*capturingBindingStore) BindAgentThread(context.Context, bindingstore.BindAgentThreadParams) error {
	return nil
}
func (*capturingBindingStore) UnbindAgentThread(context.Context, string) error { return nil }
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
func (*capturingBindingStore) UpdateAgentCwd(context.Context, bindingstore.UpdateAgentCwdParams) error {
	return nil
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
