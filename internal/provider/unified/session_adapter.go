package unified

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/orchestration"
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

func NewSessionCleaner(manager *SessionManager) orchestration.SessionCleaner {
	return &sessionCleanerAdapter{manager: manager}
}

func (a *sessionProviderAdapter) GetSession(agentID string) (contract.Session, error) {
	return a.manager.Get(agentID)
}

func (a *sessionCleanerAdapter) RemoveSession(agentID string) {
	if a == nil || a.manager == nil {
		return
	}
	a.manager.Remove(agentID)
}
