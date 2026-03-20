package unified

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// SessionManager manages active sessions by agent ID.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]contract.Session
	logger   *slog.Logger
}

func NewSessionManager(logger *slog.Logger) *SessionManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &SessionManager{
		sessions: make(map[string]contract.Session),
		logger:   logger,
	}
}

func (m *SessionManager) Register(agentID string, session contract.Session) {
	id := normalizeAgentID(agentID)
	if id == "" || session == nil {
		return
	}

	m.mu.Lock()
	previous := m.sessions[id]
	m.sessions[id] = session
	m.mu.Unlock()

	if previous != nil && previous != session {
		if err := previous.ForceStop(); err != nil {
			m.logger.Warn("failed to stop replaced session", "agent_id", id, "error", err)
		}
	}
}

func (m *SessionManager) Get(agentID string) (contract.Session, error) {
	id := normalizeAgentID(agentID)
	m.mu.RLock()
	session, ok := m.sessions[id]
	m.mu.RUnlock()
	if ok {
		return session, nil
	}
	return nil, fmt.Errorf("session not found for agent %q", agentID)
}

func (m *SessionManager) Remove(agentID string) {
	id := normalizeAgentID(agentID)
	if id == "" {
		return
	}
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

func (m *SessionManager) CloseAll(ctx context.Context) {
	if m == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for agentID, session := range m.drain() {
		err := session.Close(ctx)
		if err == nil {
			continue
		}
		m.logger.Warn("failed to close session", "agent_id", agentID, "error", err)
		if stopErr := session.ForceStop(); stopErr != nil {
			m.logger.Warn("failed to force stop session", "agent_id", agentID, "error", stopErr)
		}
	}
}

func (m *SessionManager) drain() map[string]contract.Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make(map[string]contract.Session, len(m.sessions))
	for agentID, session := range m.sessions {
		out[agentID] = session
	}
	m.sessions = make(map[string]contract.Session)
	return out
}

func normalizeAgentID(agentID string) string {
	return strings.TrimSpace(agentID)
}
