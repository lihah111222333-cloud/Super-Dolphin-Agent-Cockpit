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
	if s.shouldSuppressTurnEvent(method, params) {
		return
	}
	raw := dto.RawProviderEvent{EventType: method, Data: params}
	method = strings.TrimSpace(method)
	if w := s.getMCPWatcher(); w != nil && isMCPStartupStatus(method) {
		name, status := extractStartupStatus(params)
		w.OnStartupStatus(name, status)
	}
	if !isApprovalBridgeMethod(method) || s.approvals == nil {
		s.dispatch(raw)
	}
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
