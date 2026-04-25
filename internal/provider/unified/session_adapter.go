package unified

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
)

// sessionProviderAdapter adapts SessionManager to the thread module's narrow lookup contract.
type sessionProviderAdapter struct {
	manager *SessionManager
}

type sessionCleanerAdapter struct {
	manager *SessionManager
}

func NewSessionProvider(manager *SessionManager) *sessionProviderAdapter {
	return &sessionProviderAdapter{manager: manager}
}

func NewTurnSessionProvider(manager *SessionManager) turn.SessionProvider {
	return &sessionProviderAdapter{manager: manager}
}

func NewSessionCleaner(manager *SessionManager) contract.OrchestrationSessionCleaner {
	return &sessionCleanerAdapter{manager: manager}
}

func (a *sessionProviderAdapter) GetSession(agentID string) (contract.Session, error) {
	return a.manager.Get(agentID)
}

func (a *sessionProviderAdapter) RemoveSession(agentID string) {
	if a == nil || a.manager == nil {
		return
	}
	a.manager.RemoveCurrent(agentID)
}

func (a *sessionProviderAdapter) SessionGeneration(agentID string) uint64 {
	if a == nil || a.manager == nil {
		return 0
	}
	return a.manager.SessionGeneration(agentID)
}

func (a *sessionProviderAdapter) RemoveSessionGeneration(agentID string, generation uint64) {
	if a == nil || a.manager == nil {
		return
	}
	a.manager.Remove(agentID, generation)
}

func (a *sessionCleanerAdapter) RemoveSession(agentID string) {
	if a == nil || a.manager == nil {
		return
	}
	a.manager.RemoveCurrent(agentID)
}

func (a *sessionCleanerAdapter) RemoveSessionGeneration(agentID string, generation uint64) {
	if a == nil || a.manager == nil {
		return
	}
	a.manager.Remove(agentID, generation)
}
