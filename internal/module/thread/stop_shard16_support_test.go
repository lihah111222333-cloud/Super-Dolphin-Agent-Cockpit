package thread

import (
	"context"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

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
