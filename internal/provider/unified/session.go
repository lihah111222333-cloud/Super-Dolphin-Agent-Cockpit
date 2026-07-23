package unified

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
	"golang.org/x/sync/singleflight"
)

// SessionManager 按 agent ID 管理活跃 provider session，并用 generation 防止旧清理误删新会话。
type SessionManager struct {
	mu             sync.RWMutex
	sessions       map[string]sessionEntry
	nextGeneration uint64
	logger         *slog.Logger
	resumeFlight   singleflight.Group
}

// sessionEntry 记录单个内存 session 及其代际，代际用于异步清理时的 CAS 保护。
type sessionEntry struct {
	generation     uint64
	session        contract.Session
	pending        bool
	pendingDone    chan struct{}
	resumeIdentity string
}

// coordinatedResumeResult 保存一次共享恢复的 session 及 pending 激活信号。
type coordinatedResumeResult struct {
	session        contract.Session
	pending        bool
	pendingDone    <-chan struct{}
	resumeIdentity string
}

// resumeCoordinationIdentity 生成同一 agent 恢复时必须一致的 provider thread 身份。
func resumeCoordinationIdentity(provider, providerThreadID string) string {
	return normalizeProviderName(provider) + "\x00" + strings.TrimSpace(providerThreadID)
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

// register 写入 session manager，并返回新的 generation。
// pending session 不会被 Get 暴露，替换旧 session 时负责停止旧进程。
func (m *SessionManager) register(agentID string, session contract.Session, pending bool) uint64 {
	return m.registerWithIdentity(agentID, session, pending, "")
}

// registerWithIdentity 写入 session，并保留共享恢复的 provider identity 供冲突检查。
func (m *SessionManager) registerWithIdentity(
	agentID string,
	session contract.Session,
	pending bool,
	resumeIdentity string,
) uint64 {
	id := normalizeAgentID(agentID)
	if id == "" || session == nil {
		return 0
	}

	m.mu.Lock()
	previous := m.sessions[id]
	generation := m.nextGenerationLocked()
	entry := sessionEntry{
		generation:     generation,
		session:        session,
		pending:        pending,
		resumeIdentity: resumeIdentity,
	}
	if pending {
		entry.pendingDone = make(chan struct{})
	}
	m.sessions[id] = entry
	closePendingSignal(previous)
	m.mu.Unlock()

	if previous.session != nil && previous.session != session {
		if err := previous.session.ForceStop(); err != nil {
			m.logger.Warn("failed to stop replaced session", "agent_id", id, "error", err)
		}
	}
	return generation
}

// resumeSession 按 agent 合并 Client resume 与 resolver auto-resume。
// pending owner 可以先返回给 thread 持久化；活跃 resolver 会等待 Activate 或 Remove 后再继续。
func (m *SessionManager) resumeSession(
	ctx context.Context,
	agentID string,
	resumeIdentity string,
	pending bool,
	run func() (contract.Session, error),
) (contract.Session, error) {
	if m == nil {
		return nil, fmt.Errorf("coordinate resume: session manager is not configured")
	}
	id := normalizeAgentID(agentID)
	if id == "" {
		return nil, fmt.Errorf("coordinate resume: agent id is required")
	}
	if run == nil {
		return nil, fmt.Errorf("coordinate resume: resume function is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	value, err, _ := m.resumeFlight.Do(id, func() (any, error) {
		return m.runCoordinatedResume(id, resumeIdentity, pending, run)
	})
	return m.finishCoordinatedResume(ctx, id, resumeIdentity, pending, value, err)
}

// runCoordinatedResume 由 singleflight owner 执行 provider 恢复和唯一一次注册。
func (m *SessionManager) runCoordinatedResume(
	agentID string,
	resumeIdentity string,
	pending bool,
	run func() (contract.Session, error),
) (coordinatedResumeResult, error) {
	if current, ok := m.currentResume(agentID); ok {
		return current, nil
	}
	session, err := run()
	if err != nil {
		return coordinatedResumeResult{}, err
	}
	m.registerWithIdentity(agentID, session, pending, resumeIdentity)
	current, ok := m.currentResume(agentID)
	if !ok {
		return coordinatedResumeResult{}, fmt.Errorf("coordinate resume: registered session for agent %q is missing", agentID)
	}
	return current, nil
}

// finishCoordinatedResume 校验共享结果，并按调用方角色决定立即返回或等待 pending 激活。
func (m *SessionManager) finishCoordinatedResume(
	ctx context.Context,
	agentID string,
	resumeIdentity string,
	pending bool,
	value any,
	resumeErr error,
) (contract.Session, error) {
	if resumeErr != nil {
		return nil, resumeErr
	}
	result, ok := value.(coordinatedResumeResult)
	if !ok || result.session == nil {
		return nil, fmt.Errorf("coordinate resume: invalid result for agent %q", agentID)
	}
	if result.resumeIdentity != "" && result.resumeIdentity != resumeIdentity {
		return nil, fmt.Errorf("coordinate resume: conflicting provider session identity for agent %q", agentID)
	}
	if !result.pending || pending {
		return result.session, nil
	}
	return m.waitPendingResume(ctx, agentID, result.pendingDone)
}

// currentResume 读取 active 或 pending session，供共享恢复 owner 使用。
func (m *SessionManager) currentResume(agentID string) (coordinatedResumeResult, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.sessions[agentID]
	if !ok || entry.session == nil {
		return coordinatedResumeResult{}, false
	}
	return coordinatedResumeResult{
		session:        entry.session,
		pending:        entry.pending,
		pendingDone:    entry.pendingDone,
		resumeIdentity: entry.resumeIdentity,
	}, true
}

// waitPendingResume 等待 thread 持久化激活共享 session；移除或取消都返回明确错误。
func (m *SessionManager) waitPendingResume(
	ctx context.Context,
	agentID string,
	pendingDone <-chan struct{},
) (contract.Session, error) {
	if pendingDone == nil {
		return nil, fmt.Errorf("coordinate resume: pending signal for agent %q is missing", agentID)
	}
	select {
	case <-pendingDone:
		session, err := m.Get(agentID)
		if err != nil {
			return nil, fmt.Errorf("coordinate resume: pending session for agent %q was not activated: %w", agentID, err)
		}
		return session, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("coordinate resume: wait for agent %q activation: %w", agentID, ctx.Err())
	}
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
	closePendingSignal(entry)
	entry.pendingDone = nil
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
		closePendingSignal(entry)
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
	closePendingSignal(entry)
	return entry.session, true
}

// closePendingSignal 唤醒等待 pending 激活的 resolver；仅在 channel owner 持锁时调用。
func closePendingSignal(entry sessionEntry) {
	if entry.pendingDone != nil {
		close(entry.pendingDone)
	}
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
