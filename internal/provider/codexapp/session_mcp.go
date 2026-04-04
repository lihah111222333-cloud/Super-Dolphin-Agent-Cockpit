package codexapp

import (
	"encoding/json"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func (s *session) setMCPWatcher(w *mcpReadyWatcher) {
	s.mcpWatcherMu.Lock()
	s.mcpWatcher = w
	s.mcpWatcherMu.Unlock()
}

func (s *session) getMCPWatcher() *mcpReadyWatcher {
	s.mcpWatcherMu.Lock()
	w := s.mcpWatcher
	s.mcpWatcherMu.Unlock()
	return w
}

func (s *session) onNotification(method string, params json.RawMessage) {
	s.noteReadActivity()
	// Each session has its own WS connection, so it may receive events
	// for threads it doesn't own. Filter them out.
	if s.isAlienThreadEvent(params) {
		return
	}
	if s.shouldSuppressTurnEvent(method, params) {
		return
	}
	raw := dto.RawProviderEvent{EventType: method, Data: params}
	method = strings.TrimSpace(method)
	s.forwardMCPStatus(method, params)
	if !isApprovalBridgeMethod(method) || s.approvals == nil {
		s.dispatch(raw)
	}
	s.handleNotificationAction(method, params)
}

func (s *session) forwardMCPStatus(method string, params json.RawMessage) {
	if w := s.getMCPWatcher(); w != nil && isMCPStartupStatus(method) {
		name, status := extractStartupStatus(params)
		w.OnStartupStatus(name, status)
	}
}

func (s *session) handleNotificationAction(method string, params json.RawMessage) {
	switch {
	case isApprovalBridgeMethod(method):
		s.handleApprovalRequest(method, params)
	case isTurnTerminalEvent(method):
		s.finishTurn(params, turnTerminalSuccess(method, decodeEventPayload(params)))
	case method == "connection.dead":
		s.handleConnectionDead(params)
	}
}

func isMCPStartupStatus(method string) bool {
	return hasMethod(method, mcpStartupStatusMethods)
}

func extractStartupStatus(params json.RawMessage) (name, status string) {
	var payload struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal(params, &payload)
	return strings.TrimSpace(payload.Name), strings.TrimSpace(payload.Status)
}

// isAlienThreadEvent returns true when the event payload carries a threadId
// that does not match this session's thread. Events without a threadId (e.g.
// MCP startup status, account/rateLimits) are never considered alien.
func (s *session) isAlienThreadEvent(params json.RawMessage) bool {
	own := s.ThreadID()
	if own == "" {
		return false // thread not assigned yet, accept everything
	}
	var envelope struct {
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(params, &envelope); err != nil {
		return false
	}
	eventThread := strings.TrimSpace(envelope.ThreadID)
	if eventThread == "" {
		return false // no threadId in payload, accept (global event)
	}
	return eventThread != own
}
