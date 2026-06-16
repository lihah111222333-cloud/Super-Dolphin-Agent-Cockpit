package unified

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// SessionManager manages active sessions by agent ID.
type SessionManager struct {
	mu             sync.RWMutex
	sessions       map[string]sessionEntry
	nextGeneration uint64
	logger         *pkglogger.Logger
}

type sessionEntry struct {
	generation uint64
	session    contract.Session
}

// NewSessionManager 创建会话manager。
func NewSessionManager(logger *pkglogger.Logger) *SessionManager {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &SessionManager{
		sessions: make(map[string]sessionEntry),
		logger:   logger,
	}
}

// Register 注册unified provider。
func (m *SessionManager) Register(agentID string, session contract.Session) uint64 {
	id := normalizeAgentID(agentID)
	if id == "" || session == nil {
		return 0
	}

	m.mu.Lock()
	previous := m.sessions[id]
	generation := m.nextGenerationLocked()
	m.sessions[id] = sessionEntry{generation: generation, session: session}
	m.mu.Unlock()

	if previous.session != nil && previous.session != session {
		if err := previous.session.ForceStop(); err != nil {
			m.logger.Warn("failed to stop replaced session", "agent_id", id, "error", err)
		}
	}
	return generation
}

// Get 读取unified provider。
func (m *SessionManager) Get(agentID string) (contract.Session, error) {
	id := normalizeAgentID(agentID)
	m.mu.RLock()
	entry, ok := m.sessions[id]
	m.mu.RUnlock()
	if ok && entry.session != nil {
		return entry.session, nil
	}
	return nil, fmt.Errorf("%w for agent %q", contract.ErrSessionNotFound, agentID)
}

// SessionGeneration 处理会话代际。
func (m *SessionManager) SessionGeneration(agentID string) uint64 {
	id := normalizeAgentID(agentID)
	if id == "" {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[id].generation
}

// Remove 移除unified provider。
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

// RemoveCurrent 移除当前。
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

// CloseAll 关闭all。
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

func (m *SessionManager) nextGenerationLocked() uint64 {
	m.nextGeneration++
	if m.nextGeneration == 0 {
		m.nextGeneration++
	}
	return m.nextGeneration
}

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

func normalizeAgentID(agentID string) string {
	return strings.TrimSpace(agentID)
}
