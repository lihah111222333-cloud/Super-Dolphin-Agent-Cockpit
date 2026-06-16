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

// cachekeepaliveShutdownGrace bounds the wait for in-flight keepalive pings
// to observe ctx cancellation. Matches the other P22 P2 drain budgets
// (auto-dream scheduler / nested ingest worker / hook dispatch worker) so
// the global OnStop cost stays predictable.
const cachekeepaliveShutdownGrace = 10 * time.Second

// KeepaliveCapable marks sessions that support silent keepalive turns.
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

	// P22 P2 (cachekeepalive): pingCtx is the ambient ctx passed to every
	// in-flight SendKeepalive invocation. Shutdown cancels it so a ping
	// that has already reached the session transport sees ctx.Err() and
	// bails out cleanly. pingInflight tracks those in-flight goroutines so
	// Shutdown can wait for them to finish bounded by its own ctx — the
	// P2 §验收 bullet "cachekeepalive 不再由 bus callback 直接持有 timer /
	// session runtime" pins this drain-on-stop contract.
	pingCtx      context.Context
	pingCancel   context.CancelFunc
	pingInflight sync.WaitGroup
	drainClosed  chan struct{}
}

// NewManager 创建manager。
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

// HandleAgentLaunched 处理代理launched。
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

// resolveLaunchAgentID 解析启动代理ID。
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

// ResetTimerByAgent 按代理重置timer。
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

// StopTimerByAgent 按代理停止timer。
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

// Shutdown closes the drain gate, cancels pingCtx (so any in-flight
// SendKeepalive sees ctx.Err()), stops every scheduled timer, and waits
// bounded by ctx for in-flight ping goroutines to unwind. Idempotent.
// Shutdown 发送 LSP 关闭请求。
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

// closeDrainGate returns true iff this call is the first to close the gate.
// Subsequent Shutdown invocations short-circuit so the cancel + wait only
// happens once.
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

// drainActive reports whether Shutdown has closed the gate. Timer
// callbacks consult this under mu to avoid starting a new ping after
// Shutdown has started waiting on pingInflight.
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

// preparePing resolves the snapshot + ambient ctx for a firing keepalive
// timer. Returns (nil, nil) when the timer was already invalidated (e.g.
// Shutdown cleared it) or when pingCtx is already cancelled — in either
// case the caller must bail out without touching the session runtime.
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

// resolvePingPeer checks gate conditions (live binding + keepalive-capable
// session) and returns the session to ping, or nil after removing the
// timer when no eligible peer exists.
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

// deliverPing invokes SendKeepalive and reschedules the keepalive timer
// afterwards — on success and on failure alike — unless Shutdown cancelled
// pingCtx, in which case a fresh 55-minute timer would outlive the module
// Lifecycle so the reset is skipped.
//
// Rescheduling on failure matters because the keepalive runs as a silent
// turn whose events are not dispatched; the cachekeepalive relay therefore
// no longer receives a TurnCompleted to re-arm the timer when a ping fails,
// so the loop must be self-sustaining here. A dead agent does not loop
// forever: the next fire's resolvePingPeer re-checks the binding and drops
// the timer once no live peer remains.
// deliverPing 处理deliverping。
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

// hasLiveBinding 判断livebinding是否可用。
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

// keepaliveSession 处理keepalive会话。
func (m *Manager) keepaliveSession(ctx context.Context, threadID string) KeepaliveCapable {
	if m == nil || m.resolver == nil {
		return nil
	}
	sess, err := m.resolver.ResolveSession(ctx, threadID)
	if err != nil {
		m.logger.Warn("cachekeepalive: resolve session failed",
			"thread_id", threadID,
			"error", err,
		)
		return missingKeepaliveSession()
	}
	if sess == nil {
		return nil
	}
	kc, ok := sess.(KeepaliveCapable)
	if !ok {
		return nil
	}
	return kc
}

func missingKeepaliveSession() KeepaliveCapable {
	return nil
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
	// P22 P2: don't re-arm after drain has started; the timer would
	// register a new pingInflight.Add that Shutdown already finished
	// waiting for, leaving a goroutine unaccounted for.
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

// enterPing gates the transition from "timer fired" to "actively running
// a ping". It returns false if Shutdown has already closed drainClosed;
// in that case the AfterFunc closure exits without calling executePing so
// the manager's pingInflight counter stays accurate.
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
