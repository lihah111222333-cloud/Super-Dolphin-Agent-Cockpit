package thread

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	bindingstore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/binding"
)

type stubThreadBindingStore struct {
	stubThreadBindingStoreNoopMethods

	binding         *BindingRecord
	sessionUpdates  []BindingSessionUUIDUpdate
	archived        []BindingArchiveUpdate
	deletedAgentIDs []string
	calls           *[]string
	getByAgentIDErr error
}

type stubThreadBindingStoreNoopMethods struct{}

func (stubThreadBindingStoreNoopMethods) GetByProviderThread(context.Context, string, string) (*BindingRecord, error) {
	return nil, platformdb.ErrNotFound
}
func (stubThreadBindingStoreNoopMethods) Upsert(context.Context, BindingUpsert) error {
	return nil
}
func (s *stubThreadBindingStore) DeleteByAgentID(_ context.Context, agentID string) error {
	s.deletedAgentIDs = append(s.deletedAgentIDs, agentID)
	recordCall(s.calls, "binding_delete:"+agentID)
	return nil
}
func (s *stubThreadBindingStore) UpdateSessionUUID(_ context.Context, params BindingSessionUUIDUpdate) error {
	s.sessionUpdates = append(s.sessionUpdates, params)
	return nil
}
func (stubThreadBindingStoreNoopMethods) UpdateProviderThreadID(context.Context, BindingProviderThreadIDUpdate) error {
	return nil
}
func (s *stubThreadBindingStore) SetArchived(_ context.Context, params BindingArchiveUpdate) error {
	s.archived = append(s.archived, params)
	archived := "false"
	if params.Archived {
		archived = "true"
	}
	recordCall(s.calls, "binding_archive:"+params.AgentID+":"+archived)
	return nil
}
func (s *stubThreadBindingStore) GetByAgentID(_ context.Context, agentID string) (*BindingRecord, error) {
	if s.binding != nil && s.binding.AgentID == agentID {
		return s.binding, nil
	}
	if s.getByAgentIDErr != nil {
		return nil, s.getByAgentIDErr
	}
	return nil, platformdb.ErrNotFound
}
func (stubThreadBindingStoreNoopMethods) BindAgentThread(context.Context, bindingstore.BindAgentThreadParams) error {
	return nil
}
func (stubThreadBindingStoreNoopMethods) UnbindAgentThread(context.Context, string) error { return nil }
func (s *stubThreadBindingStore) ListAgentThreadBindings(context.Context) ([]BindingRecord, error) {
	if s.binding == nil {
		return nil, nil
	}
	return []BindingRecord{*s.binding}, nil
}
func (s *stubThreadBindingStore) GetThreadByAgent(context.Context, string) (string, error) {
	if s.binding == nil {
		return "", platformdb.ErrNotFound
	}
	return s.binding.ProviderThreadID, nil
}
func (stubThreadBindingStoreNoopMethods) UpdateAgentCwd(context.Context, BindingCWDUpdate) error {
	return nil
}

func (stubThreadBindingStoreNoopMethods) Rebind(context.Context, bindingstore.RebindParams) error {
	return nil
}

func (stubThreadBindingStoreNoopMethods) ListProviderMap(context.Context) (map[string]string, error) {
	return nil, nil
}

func (stubThreadBindingStoreNoopMethods) ListCwdMap(context.Context) (map[string]string, error) {
	return nil, nil
}

type stubThreadSessions struct {
	agentID            string
	session            contract.Session
	getErr             error
	generation         uint64
	removed            []string
	removedGenerations []uint64
	calls              *[]string
}

func (s *stubThreadSessions) GetSession(agentID string) (contract.Session, error) {
	if s.session != nil && agentID == s.agentID {
		return s.session, nil
	}
	if s.getErr != nil {
		return nil, s.getErr
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
	stubThreadSessionUnusedMethods

	threadID   string
	closeCalls int
	calls      *[]string
}

func (s *stubThreadSession) ThreadID() string { return s.threadID }

type stubThreadSessionUnusedMethods struct{}

func (stubThreadSessionUnusedMethods) RolloutPath() string { return "" }
func (stubThreadSessionUnusedMethods) Capabilities() dto.CapabilitySet {
	return nil
}
func (stubThreadSessionUnusedMethods) StartTurn(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
	return nil, nil
}
func (stubThreadSessionUnusedMethods) Interrupt(context.Context, dto.InterruptRequest) error {
	return nil
}
func (stubThreadSessionUnusedMethods) ForceComplete(context.Context, dto.ForceCompleteRequest) error {
	return nil
}
func (stubThreadSessionUnusedMethods) ListThreads(context.Context) ([]dto.ThreadRef, error) {
	return nil, nil
}
func (stubThreadSessionUnusedMethods) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, nil
}
func (stubThreadSessionUnusedMethods) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	return nil, nil
}
func (stubThreadSessionUnusedMethods) Configure(context.Context, dto.ThreadConfigPatch) error {
	return nil
}
func (s *stubThreadSession) Close(context.Context) error {
	s.closeCalls++
	recordCall(s.calls, "session_close:"+s.threadID)
	return nil
}
func (stubThreadSessionUnusedMethods) ForceStop() error { return nil }

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

func (s *recordingThreadStore) UpdateStatus(ctx context.Context, params ThreadStatusUpdate) error {
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
