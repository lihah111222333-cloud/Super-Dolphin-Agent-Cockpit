package cachekeepalive

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const keepaliveInterval = 55 * time.Minute

// cachekeepaliveShutdownGrace 限制 Shutdown 等待进行中 keepalive ping 响应 ctx 取消的时间。
// 该上限避免模块停止时被会话传输层卡住，让 fx OnStop 成本保持可预测。
const cachekeepaliveShutdownGrace = 10 * time.Second

// KeepaliveCapable 标记支持静默 keepalive turn 的会话实现。
// Manager 只通过该窄接口发 ping，避免把 provider 具体类型暴露给平台层。
type KeepaliveCapable interface {
	SendKeepalive(ctx context.Context) error
}

type agentTimer struct {
	sessionUUID string
	agentID     string
	threadID    string
	timer       *time.Timer
}

type Manager struct {
	resolver     contract.SessionResolver
	bindingStore bindingstore.Store
	threadStore  threadstore.Store
	logger       *pkglogger.Logger

	mu     sync.Mutex
	timers map[string]*agentTimer

	// pingCtx 会传入每一次 SendKeepalive；Shutdown 先取消它，再等待已进入传输层的 ping 退出。
	// pingInflight 只统计真正开始执行的 AfterFunc，避免关闭门闩后新 timer 逃出 drain 统计。
	pingCtx      context.Context
	pingCancel   context.CancelFunc
	pingInflight sync.WaitGroup
	drainClosed  chan struct{}
}

// NewManager 创建 cache keepalive 管理器，并为后续 ping 提前建立可取消的共享上下文。
func NewManager(
	resolver contract.SessionResolver,
	bindingStore bindingstore.Store,
	threadStore threadstore.Store,
	logger *pkglogger.Logger,
) *Manager {
	if logger == nil {
		logger = pkglogger.Get()
	}
	pingCtx, pingCancel := context.WithCancel(context.Background())
	return &Manager{
		resolver:     resolver,
		bindingStore: bindingStore,
		threadStore:  threadStore,
		logger:       logger,
		timers:       make(map[string]*agentTimer),
		pingCtx:      pingCtx,
		pingCancel:   pingCancel,
		drainClosed:  make(chan struct{}),
	}
}

// HandleAgentLaunched 在代理会话建立后注册 keepalive timer。
// 事件缺少 session/thread/agent 绑定时直接跳过，避免为无法解析的会话保留后台 timer。
func (m *Manager) HandleAgentLaunched(ev agentdto.AgentLaunched) {
	if m == nil {
		return
	}

	sessionUUID := strings.TrimSpace(ev.SessionID)
	threadID := strings.TrimSpace(ev.ThreadID)
	if sessionUUID == "" || threadID == "" {
		return
	}

	agentID, fallbackUsed := m.resolveLaunchAgentID(context.Background(), strings.TrimSpace(ev.AgentID), threadID)
	if agentID == "" {
		return
	}
	if fallbackUsed && m.logger != nil {
		m.logger.Debug("cachekeepalive: agentID fallback via threadStore", "thread_id", threadID, "resolved_agent_id", agentID)
	}

	m.register(sessionUUID, agentID, threadID)
	if m.logger != nil {
		m.logger.Info("cachekeepalive: session registered", "session_uuid", sessionUUID, "agent_id", agentID, "thread_id", threadID)
	}
}

// resolveLaunchAgentID 解析启动事件里的 agentID，必要时通过 threadStore 回查绑定。
// 返回的 bool 标记是否使用回查路径，供调用方记录可观测日志。
func (m *Manager) resolveLaunchAgentID(ctx context.Context, agentID, threadID string) (string, bool) {
	if agentID != "" && m.hasBinding(ctx, agentID) {
		return agentID, false
	}
	if m.threadStore == nil || threadID == "" {
		return "", false
	}
	threadRef, err := m.threadStore.GetByThreadID(ctx, threadID)
	if err != nil || threadRef == nil {
		return "", false
	}
	resolvedAgentID := strings.TrimSpace(threadRef.AgentID)
	if resolvedAgentID == "" {
		return "", false
	}
	return resolvedAgentID, true
}

// ResetTimerByAgent 在代理有新活动后重置对应 keepalive timer。
// 调用方只需要提供 agentID；内部在锁内找到当前 session，避免暴露 timer 句柄。
func (m *Manager) ResetTimerByAgent(agentID string) {
	if m == nil {
		return
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	timerRef := m.timerByAgentLocked(agentID)
	if timerRef == nil {
		return
	}
	m.scheduleLocked(timerRef)
}

// StopTimerByAgent 停止指定代理的所有 keepalive timer。
// 代理结束或绑定失效时调用，确保旧 session 不再发起静默 ping。
func (m *Manager) StopTimerByAgent(agentID string) {
	if m == nil {
		return
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for sessionUUID, timerRef := range m.timers {
		if strings.TrimSpace(timerRef.agentID) != agentID {
			continue
		}
		m.stopLocked(sessionUUID)
	}
}

// Shutdown 关闭 drain 门闩、取消 pingCtx、停止所有 timer，并按 ctx 有界等待进行中的 ping 退出。
// 该方法可重复调用；第一次之后 drainClosed 已关闭，后续调用直接返回。
func (m *Manager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if !m.closeDrainGate() {
		return nil
	}
	m.pingCancel()

	m.mu.Lock()
	for sessionUUID := range m.timers {
		m.stopLocked(sessionUUID)
	}
	m.mu.Unlock()

	waitCtx := ctx
	if waitCtx == nil {
		waitCtx = context.Background()
	}
	if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > cachekeepaliveShutdownGrace {
		var cancel context.CancelFunc
		waitCtx, cancel = platformconfig.WithTimeout(waitCtx, cachekeepaliveShutdownGrace)
		defer cancel()
		_ = deadline
	}
	done := make(chan struct{})
	go func() {
		defer func() { _ = recover() }()
		m.pingInflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-waitCtx.Done():
		return waitCtx.Err()
	}
}

// closeDrainGate 只允许第一次 Shutdown 关闭 drain gate。
// drainClosed 是一次性门闩；后续调用返回 false，避免重复 cancel 或等待同一批 ping。
func (m *Manager) closeDrainGate() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	select {
	case <-m.drainClosed:
		return false
	default:
		close(m.drainClosed)
		return true
	}
}

// drainActive 判断 Shutdown 是否已关闭门闩。
// timer 回调在锁内检查它，避免 Shutdown 开始等待后又启动新的 ping。
func (m *Manager) drainActive() bool {
	if m == nil || m.drainClosed == nil {
		return false
	}
	select {
	case <-m.drainClosed:
		return true
	default:
		return false
	}
}

func (m *Manager) executePing(sessionUUID string, fired *time.Timer) {
	timerRef, ctx := m.preparePing(sessionUUID, fired)
	if timerRef == nil {
		return
	}
	kc := m.resolvePingPeer(ctx, sessionUUID, fired, timerRef)
	if kc == nil {
		return
	}
	m.deliverPing(ctx, sessionUUID, timerRef, kc)
}

// preparePing 为触发的 keepalive timer 取快照并选择共享上下文。
// timer 已失效或 pingCtx 已取消时返回 nil，调用方必须退出且不能触碰会话运行时。
func (m *Manager) preparePing(sessionUUID string, fired *time.Timer) (*agentTimer, context.Context) {
	timerRef := m.snapshotTimer(sessionUUID, fired)
	if timerRef == nil {
		return nil, nil
	}
	if m.logger != nil {
		m.logger.Info("cachekeepalive: timer fired, executing ping", "session_uuid", sessionUUID, "agent_id", timerRef.agentID)
	}
	ctx := m.pingCtx
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return nil, nil
	}
	return timerRef, ctx
}

// resolvePingPeer 只在 agent binding 仍 live 且当前会话实现 KeepaliveCapable 时返回 peer。
// 任一边界不满足都会移除对应 timer，防止失效代理或不支持 keepalive 的会话继续自我续约。
func (m *Manager) resolvePingPeer(ctx context.Context, sessionUUID string, fired *time.Timer, timerRef *agentTimer) KeepaliveCapable {
	if !m.canPing(ctx, timerRef) {
		if m.logger != nil {
			m.logger.Debug("cachekeepalive: ping skipped, condition not met", "session_uuid", sessionUUID, "agent_id", timerRef.agentID)
		}
		m.removeTimer(sessionUUID, fired)
		return nil
	}
	kc := m.keepaliveSession(ctx, timerRef.threadID)
	if kc == nil {
		m.removeTimer(sessionUUID, fired)
		return nil
	}
	return kc
}

// deliverPing 调用 SendKeepalive，并在上下文仍有效时重新布置下一次 timer。
// keepalive 是静默 turn，失败时不会依赖 TurnCompleted 重新触发，因此这里必须自维持循环；
// 下一次触发会重新检查 live binding，失效代理会被移除而不是无限重试。
func (m *Manager) deliverPing(ctx context.Context, sessionUUID string, timerRef *agentTimer, kc KeepaliveCapable) {
	err := kc.SendKeepalive(ctx)
	if err != nil && m.logger != nil {
		m.logger.Warn("cachekeepalive: keepalive ping failed", "session_uuid", sessionUUID, "agent_id", timerRef.agentID, "thread_id", timerRef.threadID, "error", err)
	}
	if ctx.Err() != nil {
		return
	}
	m.ResetTimerByAgent(timerRef.agentID)
	if err == nil && m.logger != nil {
		m.logger.Info("cachekeepalive: keepalive success, timer reset", "session_uuid", sessionUUID, "agent_id", timerRef.agentID)
	}
}

func (m *Manager) canPing(ctx context.Context, t *agentTimer) bool {
	if !m.hasLiveBinding(ctx, t) {
		return false
	}
	return m.keepaliveSession(ctx, t.threadID) != nil
}

// hasLiveBinding 检查 agent 绑定是否仍存在且未归档，用于阻止旧 timer ping 已结束的会话。
func (m *Manager) hasLiveBinding(ctx context.Context, t *agentTimer) bool {
	if m == nil || t == nil || m.bindingStore == nil {
		return false
	}
	bindingRef, err := m.bindingStore.GetByAgentID(ctx, t.agentID)
	if err != nil || bindingRef == nil {
		return false
	}
	return !bindingRef.Archived
}

// keepaliveSession 通过 threadID 解析当前会话，并只接受实现 KeepaliveCapable 的 provider。
func (m *Manager) keepaliveSession(ctx context.Context, threadID string) KeepaliveCapable {
	if m == nil || m.resolver == nil {
		return nil
	}
	sess, err := m.resolver.ResolveSession(ctx, threadID)
	if err != nil || sess == nil {
		return nil
	}
	kc, ok := sess.(KeepaliveCapable)
	if !ok {
		return nil
	}
	return kc
}

func (m *Manager) hasBinding(ctx context.Context, agentID string) bool {
	if m == nil || m.bindingStore == nil || strings.TrimSpace(agentID) == "" {
		return false
	}
	bindingRef, err := m.bindingStore.GetByAgentID(ctx, agentID)
	return err == nil && bindingRef != nil
}

func (m *Manager) register(sessionUUID, agentID, threadID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for key, timerRef := range m.timers {
		if key == sessionUUID || strings.TrimSpace(timerRef.agentID) != agentID {
			continue
		}
		m.stopLocked(key)
	}

	if existing := m.timers[sessionUUID]; existing != nil {
		existing.agentID = agentID
		existing.threadID = threadID
		m.scheduleLocked(existing)
		return
	}
	newTimer := &agentTimer{
		sessionUUID: sessionUUID,
		agentID:     agentID,
		threadID:    threadID,
	}
	m.timers[sessionUUID] = newTimer
	m.scheduleLocked(newTimer)
}

func (m *Manager) timerByAgentLocked(agentID string) *agentTimer {
	for _, timerRef := range m.timers {
		if strings.TrimSpace(timerRef.agentID) == agentID {
			return timerRef
		}
	}
	return nil
}

func (m *Manager) scheduleLocked(timerRef *agentTimer) {
	if timerRef == nil {
		return
	}
	if timerRef.timer != nil {
		timerRef.timer.Stop()
	}
	// drain 启动后禁止重新布置 timer，否则新的 AfterFunc 可能在 Shutdown 完成等待后才进入 pingInflight。
	if m.drainActive() {
		timerRef.timer = nil
		return
	}

	sessionUUID := timerRef.sessionUUID
	var fired *time.Timer
	fired = time.AfterFunc(keepaliveInterval, func() {
		if !m.enterPing() {
			return
		}
		defer m.pingInflight.Done()
		m.executePing(sessionUUID, fired)
	})
	timerRef.timer = fired
}

// enterPing 控制 timer 触发到实际执行 ping 的并发入口。
// Shutdown 已关闭门闩时返回 false，确保 pingInflight 只统计真正进入执行的回调。
func (m *Manager) enterPing() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	select {
	case <-m.drainClosed:
		return false
	default:
	}
	m.pingInflight.Add(1)
	return true
}

func (m *Manager) snapshotTimer(sessionUUID string, fired *time.Timer) *agentTimer {
	m.mu.Lock()
	defer m.mu.Unlock()

	timerRef := m.timers[sessionUUID]
	if timerRef == nil {
		return nil
	}
	if fired != nil && timerRef.timer != fired {
		return nil
	}
	return &agentTimer{
		sessionUUID: timerRef.sessionUUID,
		agentID:     timerRef.agentID,
		threadID:    timerRef.threadID,
		timer:       timerRef.timer,
	}
}

func (m *Manager) removeTimer(sessionUUID string, fired *time.Timer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	timerRef := m.timers[sessionUUID]
	if timerRef == nil {
		return
	}
	if fired != nil && timerRef.timer != fired {
		return
	}
	m.stopLocked(sessionUUID)
}

func (m *Manager) stopLocked(sessionUUID string) {
	timerRef := m.timers[sessionUUID]
	if timerRef == nil {
		return
	}
	if timerRef.timer != nil {
		timerRef.timer.Stop()
		timerRef.timer = nil
	}
	delete(m.timers, sessionUUID)
}
