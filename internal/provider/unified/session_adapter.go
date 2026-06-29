package unified

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// sessionProviderAdapter 把 SessionManager 收窄为 thread 和 turn 模块使用的 session 查询接口。
type sessionProviderAdapter struct {
	manager *SessionManager
}

// sessionCleanerAdapter 把 SessionManager 收窄为 orchestration 清理路径使用的移除接口。
type sessionCleanerAdapter struct {
	manager *SessionManager
}

// NewSessionProvider 创建 thread 模块使用的 session 查询适配器。
func NewSessionProvider(manager *SessionManager) *sessionProviderAdapter {
	return &sessionProviderAdapter{manager: manager}
}

// NewTurnSessionProvider 创建 turn 模块使用的 session 查询适配器。
// 返回值保持窄接口形态，避免 turn 模块依赖完整 SessionManager。
func NewTurnSessionProvider(manager *SessionManager) *sessionProviderAdapter {
	return &sessionProviderAdapter{manager: manager}
}

// NewSessionCleaner 创建 orchestration 会话清理适配器，清理路径只暴露移除能力。
func NewSessionCleaner(manager *SessionManager) contract.OrchestrationSessionCleaner {
	return &sessionCleanerAdapter{manager: manager}
}

// GetSession 按 agent ID 读取内存 session，错误语义保持与 SessionManager.Get 一致。
func (a *sessionProviderAdapter) GetSession(agentID string) (contract.Session, error) {
	return a.manager.Get(agentID)
}

// RemoveSession 移除当前 agent session，nil adapter 在清理路径中安全忽略。
func (a *sessionProviderAdapter) RemoveSession(agentID string) {
	if a == nil || a.manager == nil {
		return
	}
	a.manager.RemoveCurrent(agentID)
}

// SessionGeneration 返回当前 session 代际，nil adapter 按未注册处理。
func (a *sessionProviderAdapter) SessionGeneration(agentID string) uint64 {
	if a == nil || a.manager == nil {
		return 0
	}
	return a.manager.SessionGeneration(agentID)
}

// ActivateSession 公开 pending resume session，必须在 thread 状态持久化成功后调用。
func (a *sessionProviderAdapter) ActivateSession(agentID string) bool {
	if a == nil || a.manager == nil {
		return false
	}
	return a.manager.ActivateSession(agentID)
}

// RemoveSessionGeneration 只移除匹配 generation 的 session，防止异步清理删掉新会话。
func (a *sessionProviderAdapter) RemoveSessionGeneration(agentID string, generation uint64) {
	if a == nil || a.manager == nil {
		return
	}
	a.manager.Remove(agentID, generation)
}

// RemoveSession 通过 cleaner 适配器移除当前 session，供 orchestration 停止路径调用。
func (a *sessionCleanerAdapter) RemoveSession(agentID string) {
	if a == nil || a.manager == nil {
		return
	}
	a.manager.RemoveCurrent(agentID)
}

// RemoveSessionGeneration 通过 cleaner 适配器执行代际保护的 session 移除。
func (a *sessionCleanerAdapter) RemoveSessionGeneration(agentID string, generation uint64) {
	if a == nil || a.manager == nil {
		return
	}
	a.manager.Remove(agentID, generation)
}
