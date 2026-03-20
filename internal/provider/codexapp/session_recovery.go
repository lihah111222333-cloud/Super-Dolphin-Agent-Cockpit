package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func (s *session) callTransport(ctx context.Context, method string, params any) (json.RawMessage, error) {
	raw, err := s.transport.Call(ctx, method, params)
	if err == nil || !shouldReconnect(err) {
		return raw, err
	}
	if recoverErr := s.attemptRecovery(err.Error()); recoverErr != nil {
		return nil, errors.Join(err, recoverErr)
	}
	return s.transport.Call(ctx, method, params)
}

func (s *session) handleConnectionDead(params json.RawMessage) {
	reason := firstNonEmpty(stringValue(decodeEventPayload(params), "error", "message"), "connection lost")
	s.failTurns(errors.New("codexapp: " + reason))
	if s.ctx.Err() != nil {
		return
	}
	go func() { _ = s.attemptRecovery(reason) }()
}

func (s *session) attemptRecovery(reason string) error {
	if s.recovery == nil {
		return errors.New("codexapp: recovery unavailable")
	}
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	s.dispatch(dto.RawProviderEvent{
		Type: "recovery.attempt",
		Data: map[string]any{
			"agentId":  strings.TrimSpace(s.agentID),
			"threadId": strings.TrimSpace(s.threadID),
			"reason":   strings.TrimSpace(reason),
			"attempt":  1,
		},
	})
	if err := s.recovery.Reconnect(s.ctx); err != nil {
		return err
	}
	go s.transport.ReadLoop(s.ctx, s.onNotification)
	return nil
}

func shouldReconnect(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(message, "transport closed") || strings.HasPrefix(message, "rpc error ") {
		return false
	}
	return true
}
