package unified

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// sessionProviderAdapter adapts SessionManager to the thread module's narrow lookup contract.
type sessionProviderAdapter struct {
	manager *SessionManager
}

func NewSessionProvider(manager *SessionManager) *sessionProviderAdapter {
	return &sessionProviderAdapter{manager: manager}
}

func (a *sessionProviderAdapter) GetSession(agentID string) (contract.Session, error) {
	return a.manager.Get(agentID)
}
