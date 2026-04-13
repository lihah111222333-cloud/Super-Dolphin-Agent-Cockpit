package cachekeepalive

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

const keepaliveInterval = 55 * time.Minute

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
}

type keepaliveIn struct {
	fx.In

	Dispatcher *event.Dispatcher
	Manager    *Manager
	Logger     *pkglogger.Logger `optional:"true"`
}

func NewManager(
	resolver contract.SessionResolver,
	bindingStore bindingstore.Store,
	threadStore threadstore.Store,
	logger *pkglogger.Logger,
) *Manager {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &Manager{
		resolver:     resolver,
		bindingStore: bindingStore,
		threadStore:  threadStore,
		logger:       logger,
		timers:       make(map[string]*agentTimer),
	}
}

func (m *Manager) HandleAgentLaunched(ev agentdto.AgentLaunched) {
	if m == nil {
		return
	}

	sessionUUID := strings.TrimSpace(ev.SessionID)
	threadID := strings.TrimSpace(ev.ThreadID)
	if sessionUUID == "" || threadID == "" {
		return
	}

	agentID := m.resolveLaunchAgentID(context.Background(), strings.TrimSpace(ev.AgentID), threadID)
	if agentID == "" {
		return
	}

	m.register(sessionUUID, agentID, threadID)
}

func (m *Manager) resolveLaunchAgentID(ctx context.Context, agentID, threadID string) string {
	if agentID != "" && m.hasBinding(ctx, agentID) {
		return agentID
	}
	if m.threadStore == nil || threadID == "" {
		return ""
	}
	threadRef, err := m.threadStore.GetByThreadID(ctx, threadID)
	if err != nil || threadRef == nil {
		return ""
	}
	return strings.TrimSpace(threadRef.AgentID)
}

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

func (m *Manager) Shutdown() {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for sessionUUID := range m.timers {
		m.stopLocked(sessionUUID)
	}
}

func (m *Manager) executePing(sessionUUID string, fired *time.Timer) {
	timerRef := m.snapshotTimer(sessionUUID, fired)
	if timerRef == nil {
		return
	}

	ctx := context.Background()
	if !m.canPing(ctx, timerRef) {
		m.removeTimer(sessionUUID, fired)
		return
	}

	kc := m.keepaliveSession(ctx, timerRef.threadID)
	if kc == nil {
		m.removeTimer(sessionUUID, fired)
		return
	}
	if err := kc.SendKeepalive(ctx); err != nil {
		if m.logger != nil {
			m.logger.Warn("cachekeepalive: keepalive ping failed", "session_uuid", sessionUUID, "agent_id", timerRef.agentID, "thread_id", timerRef.threadID, "error", err)
		}
		return
	}

	m.ResetTimerByAgent(timerRef.agentID)
}

func (m *Manager) canPing(ctx context.Context, t *agentTimer) bool {
	if !m.hasLiveBinding(ctx, t) {
		return false
	}
	return m.keepaliveSession(ctx, t.threadID) != nil
}

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
		return
	}
	m.timers[sessionUUID] = &agentTimer{
		sessionUUID: sessionUUID,
		agentID:     agentID,
		threadID:    threadID,
	}
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

	sessionUUID := timerRef.sessionUUID
	var fired *time.Timer
	fired = time.AfterFunc(keepaliveInterval, func() {
		m.executePing(sessionUUID, fired)
	})
	timerRef.timer = fired
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
