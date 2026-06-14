package unified

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// sessionProviderAdapter adapts SessionManager to the thread module's narrow lookup contract.
type sessionProviderAdapter struct {
	manager *SessionManager
}

type sessionCleanerAdapter struct {
	manager *SessionManager
}

// NewSessionProvider 创建会话provider。
func NewSessionProvider(manager *SessionManager) *sessionProviderAdapter {
	return &sessionProviderAdapter{manager: manager}
}

// NewTurnSessionProvider returns a narrow session lookup for the turn module.
// The returned adapter satisfies any interface that requires GetSession(agentID) (Session, error).
// NewTurnSessionProvider 创建turn会话provider。
func NewTurnSessionProvider(manager *SessionManager) *sessionProviderAdapter {
	return &sessionProviderAdapter{manager: manager}
}

// NewSessionCleaner 创建会话cleaner。
func NewSessionCleaner(manager *SessionManager) contract.OrchestrationSessionCleaner {
	return &sessionCleanerAdapter{manager: manager}
}

// GetSession 读取会话。
func (a *sessionProviderAdapter) GetSession(agentID string) (contract.Session, error) {
	return a.manager.Get(agentID)
}

// RemoveSession 移除会话。
func (a *sessionProviderAdapter) RemoveSession(agentID string) {
	if a == nil || a.manager == nil {
		return
	}
	a.manager.RemoveCurrent(agentID)
}

// SessionGeneration 处理会话代际。
func (a *sessionProviderAdapter) SessionGeneration(agentID string) uint64 {
	if a == nil || a.manager == nil {
		return 0
	}
	return a.manager.SessionGeneration(agentID)
}

// RemoveSessionGeneration 移除会话代际。
func (a *sessionProviderAdapter) RemoveSessionGeneration(agentID string, generation uint64) {
	if a == nil || a.manager == nil {
		return
	}
	a.manager.Remove(agentID, generation)
}

// RemoveSession 移除会话。
func (a *sessionCleanerAdapter) RemoveSession(agentID string) {
	if a == nil || a.manager == nil {
		return
	}
	a.manager.RemoveCurrent(agentID)
}

// RemoveSessionGeneration 移除会话代际。
func (a *sessionCleanerAdapter) RemoveSessionGeneration(agentID string, generation uint64) {
	if a == nil || a.manager == nil {
		return
	}
	a.manager.Remove(agentID, generation)
}
