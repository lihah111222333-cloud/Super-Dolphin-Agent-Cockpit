package unified

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// SessionManager 按 agent ID 管理活跃 provider session，并用 generation 防止旧清理误删新会话。
type SessionManager struct {
	mu             sync.RWMutex
	sessions       map[string]sessionEntry
	nextGeneration uint64
	logger         *slog.Logger
}

// sessionEntry 记录单个内存 session 及其代际，代际用于异步清理时的 CAS 保护。
type sessionEntry struct {
	generation uint64
	session    contract.Session
	pending    bool
}

// NewSessionManager 创建空的内存 session 管理器，logger 缺失时使用包级默认 logger。
func NewSessionManager(logger *slog.Logger) *SessionManager {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &SessionManager{
		sessions: make(map[string]sessionEntry),
		logger:   logger,
	}
}

// Register 绑定 agent ID 到新 session，并关闭被替换的旧 session。
// 返回的 generation 供调用方后续按代际移除，避免覆盖新注册会话。
func (m *SessionManager) Register(agentID string, session contract.Session) uint64 {
	return m.register(agentID, session, false)
}

// RegisterPending 先登记恢复出的 session 但不让 Get 返回，等待上层持久化提交后再激活。
func (m *SessionManager) RegisterPending(agentID string, session contract.Session) uint64 {
	return m.register(agentID, session, true)
}

func (m *SessionManager) register(agentID string, session contract.Session, pending bool) uint64 {
	id := normalizeAgentID(agentID)
	if id == "" || session == nil {
		return 0
	}

	m.mu.Lock()
	previous := m.sessions[id]
	generation := m.nextGenerationLocked()
	m.sessions[id] = sessionEntry{generation: generation, session: session, pending: pending}
	m.mu.Unlock()

	if previous.session != nil && previous.session != session {
		if err := previous.session.ForceStop(); err != nil {
			m.logger.Warn("failed to stop replaced session", "agent_id", id, "error", err)
		}
	}
	return generation
}

// Get 按 agent ID 读取当前内存 session，缺失时返回 contract.ErrSessionNotFound 包装错误。
func (m *SessionManager) Get(agentID string) (contract.Session, error) {
	id := normalizeAgentID(agentID)
	m.mu.RLock()
	entry, ok := m.sessions[id]
	m.mu.RUnlock()
	if ok && entry.session != nil && !entry.pending {
		return entry.session, nil
	}
	return nil, fmt.Errorf("%w for agent %q", contract.ErrSessionNotFound, agentID)
}

// Activate 将 pending resume session 标记为可见；没有匹配 session 时返回 false。
func (m *SessionManager) Activate(agentID string) bool {
	return m.ActivateSession(agentID)
}

// ActivateSession 在 resume 持久化成功后公开 session，供 thread service 通过窄接口调用。
func (m *SessionManager) ActivateSession(agentID string) bool {
	id := normalizeAgentID(agentID)
	if id == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.sessions[id]
	if !ok || entry.session == nil {
		return false
	}
	entry.pending = false
	m.sessions[id] = entry
	return true
}

// SessionGeneration 返回当前 session 代际，空 agent 或未注册时返回 0。
func (m *SessionManager) SessionGeneration(agentID string) uint64 {
	id := normalizeAgentID(agentID)
	if id == "" {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[id].generation
}

// Remove 按 agent ID 和 generation 移除 session，generation 不匹配说明已有新会话接管。
func (m *SessionManager) Remove(agentID string, generation uint64) {
	if m == nil {
		return
	}
	id := normalizeAgentID(agentID)
	if id == "" || generation == 0 {
		return
	}
	session, ok := m.removeEntry(id, generation, true)
	if !ok {
		return
	}
	m.closeRemovedSession(id, session)
}

// RemoveCurrent 移除 agent 当前 session，不检查 generation，供显式停止路径使用。
func (m *SessionManager) RemoveCurrent(agentID string) {
	if m == nil {
		return
	}
	id := normalizeAgentID(agentID)
	if id == "" {
		return
	}
	session, ok := m.removeEntry(id, 0, false)
	if !ok {
		return
	}
	m.closeRemovedSession(id, session)
}

// CloseAll 排空当前 session 表并逐个关闭；正常 Close 超时或失败时再执行 ForceStop。
func (m *SessionManager) CloseAll(ctx context.Context) {
	if m == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for agentID, entry := range m.drain() {
		err := closeSession(ctx, entry.session)
		if err == nil {
			continue
		}
		m.logger.Warn("failed to close session", "agent_id", agentID, "error", err)
		if stopErr := entry.session.ForceStop(); stopErr != nil {
			m.logger.Warn("failed to force stop session", "agent_id", agentID, "error", stopErr)
		}
	}
}

// drain 在锁内复制并清空 session 表，保证 CloseAll 之后不会重复关闭同一批 session。
func (m *SessionManager) drain() map[string]sessionEntry {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make(map[string]sessionEntry, len(m.sessions))
	for agentID, entry := range m.sessions {
		out[agentID] = entry
	}
	m.sessions = make(map[string]sessionEntry)
	return out
}

// removeEntry 在锁内执行删除，requireMatch 为 true 时只删除指定 generation。
func (m *SessionManager) removeEntry(agentID string, generation uint64, requireMatch bool) (contract.Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.sessions[agentID]
	if !ok || entry.session == nil {
		return nil, false
	}
	if requireMatch && entry.generation != generation {
		return nil, false
	}
	delete(m.sessions, agentID)
	return entry.session, true
}

// closeRemovedSession 用统一超时关闭被移除 session，失败时降级到 ForceStop 并记录日志。
func (m *SessionManager) closeRemovedSession(agentID string, session contract.Session) {
	closeCtx, cancel := platformconfig.WithSessionCloseTimeout(context.Background())
	defer cancel()
	if err := closeSession(closeCtx, session); err != nil {
		m.logger.Warn("failed to close removed session", "agent_id", agentID, "error", err)
		if stopErr := session.ForceStop(); stopErr != nil {
			m.logger.Warn("failed to force stop removed session", "agent_id", agentID, "error", stopErr)
		}
	}
}

// nextGenerationLocked 在持锁状态下生成非零 generation，溢出到 0 时跳过零值。
func (m *SessionManager) nextGenerationLocked() uint64 {
	m.nextGeneration++
	if m.nextGeneration == 0 {
		m.nextGeneration++
	}
	return m.nextGeneration
}

// closeSession 在受保护 goroutine 中调用 provider Close，调用方通过 ctx 控制最长等待时间。
func closeSession(ctx context.Context, session contract.Session) error {
	if session == nil {
		return nil
	}
	done := make(chan error, 1)
	runtimesafe.SafeGo(ctx, pkglogger.Get(), "provider.unified.sessionClose", func(context.Context) {
		done <- session.Close(ctx)
	})
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// normalizeAgentID 标准化 agent ID，避免空白字符造成重复 session 键。
func normalizeAgentID(agentID string) string {
	return strings.TrimSpace(agentID)
}
