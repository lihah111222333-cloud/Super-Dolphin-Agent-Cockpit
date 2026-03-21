package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type recoveryManager struct {
	transport *transport
	logger    *slog.Logger
	maxRetry  int
}

const (
	healthCheckInterval      = 15 * time.Second
	healthCheckIdleThreshold = 30 * time.Second
)

func (r *recoveryManager) CheckHealth(ctx context.Context) error {
	if r.transport == nil || !r.transport.Running() {
		return errors.New("codexapp: transport not running")
	}
	callCtx, cancel := withTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err := r.transport.Call(callCtx, "app/list", nil)
	return err
}

func (r *recoveryManager) Reconnect(ctx context.Context) error {
	if r.transport == nil {
		return errors.New("codexapp: transport not configured")
	}
	attempts := r.maxRetry
	if attempts <= 0 {
		attempts = 1
	}
	if r.logger != nil {
		r.logger.Warn("codexapp reconnect", "attempts", attempts)
	}
	return shared.Retry(ctx, attempts, 200*time.Millisecond, func() error {
		callCtx, cancel := withTimeout(ctx, 5*time.Second)
		defer cancel()
		return r.transport.reconnect(callCtx)
	})
}

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
			"threadId": s.ThreadID(),
			"reason":   strings.TrimSpace(reason),
			"attempt":  1,
		},
	})
	if err := s.recovery.Reconnect(s.ctx); err != nil {
		s.failTurns(errors.New("codexapp: " + reason))
		return err
	}
	waitCtx, cancel := withTimeout(s.ctx, 2*time.Second)
	defer cancel()
	if err := s.waitReadLoopStopped(waitCtx); err != nil {
		s.failTurns(errors.New("codexapp: " + reason))
		return err
	}
	s.startReadLoop()
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

func (s *session) startHealthLoop() {
	go func() {
		ticker := time.NewTicker(healthCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.checkIdleHealth()
			}
		}
	}()
}

func (s *session) checkIdleHealth() {
	if s.recovery == nil || time.Since(s.lastReadTime()) < healthCheckIdleThreshold {
		return
	}
	if err := s.recovery.CheckHealth(s.ctx); err == nil {
		s.noteReadActivity()
		return
	} else if s.logger != nil {
		s.logger.Warn("codexapp: health check failed", "error", err)
		_ = s.attemptRecovery("health check failed: " + err.Error())
		return
	}
	_ = s.attemptRecovery("health check failed")
}

func (s *session) noteReadActivity() {
	s.lastReadAt.Store(time.Now().UnixNano())
}

func (s *session) lastReadTime() time.Time {
	stamp := s.lastReadAt.Load()
	if stamp <= 0 {
		return time.Time{}
	}
	return time.Unix(0, stamp)
}

func (s *session) startReadLoop() {
	done, ok := s.prepareReadLoop()
	if !ok {
		return
	}
	go s.runReadLoop(done)
}

func (s *session) prepareReadLoop() (chan struct{}, bool) {
	s.readLoopMu.Lock()
	defer s.readLoopMu.Unlock()
	if s.readLoopDone != nil {
		select {
		case <-s.readLoopDone:
			s.readLoopDone = nil
		default:
			return nil, false
		}
	}
	done := make(chan struct{})
	s.readLoopDone = done
	return done, true
}

func (s *session) runReadLoop(done chan struct{}) {
	defer s.finishReadLoop(done)
	s.transport.ReadLoop(s.ctx, s.onNotification)
}

func (s *session) finishReadLoop(done chan struct{}) {
	close(done)
	s.readLoopMu.Lock()
	defer s.readLoopMu.Unlock()
	if s.readLoopDone == done {
		s.readLoopDone = nil
	}
}

func (s *session) waitReadLoopStopped(ctx context.Context) error {
	s.readLoopMu.Lock()
	done := s.readLoopDone
	s.readLoopMu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
