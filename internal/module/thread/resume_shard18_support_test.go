package thread

import (
	"context"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	bindingstore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/binding"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

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

func (s *stubBindingStore) UpdateSessionUUID(_ context.Context, params BindingSessionUUIDUpdate) error {
	s.sessionUpdates = append(s.sessionUpdates, params)
	if s.binding != nil && s.binding.AgentID == params.AgentID {
		s.binding.SessionUUID = params.SessionUUID
		s.binding.UpdatedAt = params.UpdatedAt
	}
	return nil
}

func (s *stubBindingStore) UpdateProviderThreadID(_ context.Context, params BindingProviderThreadIDUpdate) error {
	s.updateProviderThreadID = params
	if s.binding != nil && s.binding.AgentID == params.AgentID {
		s.binding.ProviderThreadID = params.ProviderThreadID
		s.binding.UpdatedAt = params.UpdatedAt
	}
	return nil
}

type stubBindingStoreNoopMethods struct{}

func (stubBindingStoreNoopMethods) SetArchived(context.Context, BindingArchiveUpdate) error {
	return nil
}

func (s *stubBindingStore) GetByAgentID(_ context.Context, agentID string) (*BindingRecord, error) {
	return s.bindingForAgent(agentID)
}

func (s *stubBindingStore) bindingForAgent(agentID string) (*BindingRecord, error) {
	if s.binding == nil || (agentID != "" && s.binding.AgentID != agentID) {
		return nil, db.ErrNotFound
	}
	binding := *s.binding
	return &binding, nil
}

func (stubBindingStoreNoopMethods) BindAgentThread(context.Context, bindingstore.BindAgentThreadParams) error {
	return nil
}

func (stubBindingStoreNoopMethods) UnbindAgentThread(context.Context, string) error { return nil }

func (s *stubBindingStore) ListAgentThreadBindings(context.Context) ([]BindingRecord, error) {
	if s.bindings != nil {
		return s.bindings, nil
	}
	if s.binding == nil {
		return nil, nil
	}
	return []BindingRecord{*s.binding}, nil
}

func (s *stubBindingStore) GetThreadByAgent(context.Context, string) (string, error) {
	if s.binding == nil {
		return "", db.ErrNotFound
	}
	return shared.FirstNonEmpty(s.binding.CodexThreadID, s.binding.ProviderThreadID), nil
}

func (stubBindingStoreNoopMethods) UpdateAgentCwd(context.Context, BindingCWDUpdate) error {
	return nil
}

func (stubBindingStoreNoopMethods) Rebind(context.Context, bindingstore.RebindParams) error {
	return nil
}

func (stubBindingStoreNoopMethods) ListProviderMap(context.Context) (map[string]string, error) {
	return nil, nil
}

func (stubBindingStoreNoopMethods) ListCwdMap(context.Context) (map[string]string, error) {
	return nil, nil
}

func silentLogger() *pkglogger.Logger {
	return pkglogger.Get()
}
