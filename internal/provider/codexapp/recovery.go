package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type recoveryManager struct {
	transport *transport
	logger    *slog.Logger
	maxRetry  int
}

const (
	maxRecoveryAttempts      = 3
	healthCheckInterval      = 15 * time.Second
	healthCheckIdleThreshold = 30 * time.Second
)

type turnReplayState struct {
	localID    string
	providerID string
	params     turnStartParams
	handle     *turnHandle
}

func (r *recoveryManager) CheckHealth(ctx context.Context) error {
	if r.transport == nil || !r.transport.Running() {
		return errors.New("codexapp: transport not running")
	}
	_, err := callWithTimeout(ctx, r.transport, 3*time.Second, "app/list", map[string]any{})
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
		r.logger.Debug("codexapp reconnect", "attempts", attempts)
	}
	return shared.Retry(ctx, attempts, 200*time.Millisecond, func() error {
		callCtx, cancel := withTimeout(ctx, 5*time.Second)
		defer cancel()
		return r.transport.reconnect(callCtx)
	})
}

func cloneTurnStartParams(params turnStartParams) turnStartParams {
	cloned := params
	if len(params.Input) > 0 {
		cloned.Input = append([]turnInputItem(nil), params.Input...)
	}
	if len(params.SelectedSkills) > 0 {
		cloned.SelectedSkills = append([]string(nil), params.SelectedSkills...)
	}
	if len(params.OutputSchema) > 0 {
		cloned.OutputSchema = append(json.RawMessage(nil), params.OutputSchema...)
	}
	return cloned
}

func (s *session) rememberPendingTurn(handle *turnHandle, params turnStartParams) {
	if s == nil || handle == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingTurn = &turnReplayState{
		localID:    strings.TrimSpace(handle.LocalID()),
		providerID: strings.TrimSpace(handle.ProviderID()),
		params:     cloneTurnStartParams(params),
		handle:     handle,
	}
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
	reason := shared.FirstNonEmpty(stringValue(decodeEventPayload(params), "error", "message"), "connection lost")
	if err := shared.CheckCtx(s.ctx); err != nil {
		return
	}
	shared.SafeGo(s.logger, func() {
		shared.LogIgnoredError(s.logger, "background recovery failed", s.attemptRecovery(reason))
	})
}

func (s *session) attemptRecovery(reason string) error {
	count := s.recoveryCount.Add(1)
	if count > maxRecoveryAttempts {
		s.failTurns(errors.New("codexapp: max recovery attempts exceeded"))
		return fmt.Errorf("codexapp: max recovery attempts (%d) exceeded", maxRecoveryAttempts)
	}
	if s.recovery == nil {
		return errors.New("codexapp: recovery unavailable")
	}
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	s.dispatch(dto.RawProviderEvent{
		EventType: "recovery.attempt",
		Data: map[string]any{
			"agentId":  strings.TrimSpace(s.agentID),
			"threadId": s.ThreadID(),
			"reason":   strings.TrimSpace(reason),
			"attempt":  count,
		},
	})
	if err := s.recovery.Reconnect(s.ctx); err != nil {
		return s.failRecovery(reason, err)
	}
	waitCtx, cancel := withTimeout(s.ctx, 2*time.Second)
	defer cancel()
	if err := s.waitReadLoopStopped(waitCtx); err != nil {
		return s.failRecovery(reason, err)
	}
	s.startReadLoop()
	s.mu.Lock()
	s.suppressed = make(map[string]struct{})
	s.mu.Unlock()
	if err := s.resumeThreadAfterRecovery(s.ctx); err != nil {
		return s.failRecovery(reason, err)
	}
	if err := s.replayPendingTurn(s.ctx); err != nil {
		return s.failRecovery(reason, err)
	}
	s.recoveryCount.Store(0)
	s.noteReadActivity()
	return nil
}

func (s *session) resumeThreadAfterRecovery(ctx context.Context) error {
	threadID := s.ThreadID()
	if threadID == "" {
		return nil
	}
	if s.logger != nil {
		s.logger.Info("codexapp: resuming thread after recovery", "thread_id", threadID)
	}
	raw, err := callWithTimeout(ctx, s.transport, 30*time.Second, "thread/resume", threadResumeParams{
		ThreadID: threadID,
	})
	if err != nil {
		return fmt.Errorf("codexapp: thread/resume after recovery failed: %w", err)
	}
	if newID, decodeErr := decodeThreadID(raw, threadID); decodeErr == nil && newID != "" {
		s.setThreadID(newID)
	}
	return nil
}

func (s *session) replayPendingTurn(ctx context.Context) error {
	if err := shared.CheckCtx(ctx); err != nil {
		return err
	}
	ctx = shared.NonNilContext(ctx)
	snapshot := s.pendingTurnSnapshot()
	if snapshot == nil || replayTurnDone(snapshot.handle) {
		return nil
	}
	if err := validatePendingTurnSnapshot(snapshot); err != nil {
		return err
	}
	s.logReplayPendingTurn(snapshot)
	newProviderID, err := s.replayTurnStart(ctx, snapshot.params)
	if err != nil {
		return err
	}
	s.applyReplayedTurn(snapshot, newProviderID)
	s.logReplayedTurn(snapshot, newProviderID)
	return nil
}

func (s *session) pendingTurnSnapshot() *turnReplayState {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingTurn == nil || s.pendingTurn.handle == nil {
		return nil
	}
	snapshot := *s.pendingTurn
	snapshot.params = cloneTurnStartParams(snapshot.params)
	return &snapshot
}

func replayTurnDone(handle *turnHandle) bool {
	if handle == nil {
		return true
	}
	select {
	case <-handle.Done():
		return true
	default:
		return false
	}
}

func validatePendingTurnSnapshot(snapshot *turnReplayState) error {
	if snapshot == nil || strings.TrimSpace(snapshot.params.ThreadID) == "" {
		return errors.New("codexapp: replay thread id is required")
	}
	return nil
}

func (s *session) replayTurnStart(ctx context.Context, params turnStartParams) (string, error) {
	raw, err := callWithTimeout(ctx, s.transport, 30*time.Second, "turn/start", params)
	if err != nil {
		return "", err
	}
	resp, err := decodeTurnStartResult(raw)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Turn.ID), nil
}

func (s *session) applyReplayedTurn(snapshot *turnReplayState, newProviderID string) {
	snapshot.handle.setProviderID(newProviderID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if snapshot.providerID != "" {
		delete(s.turns, snapshot.providerID)
	}
	s.turns[newProviderID] = snapshot.handle
	s.activeTurnID = newProviderID
	if s.pendingTurn != nil && s.pendingTurn.handle == snapshot.handle {
		s.pendingTurn.providerID = newProviderID
		s.pendingTurn.params = cloneTurnStartParams(snapshot.params)
	}
}

func (s *session) logReplayPendingTurn(snapshot *turnReplayState) {
	if s.logger == nil || snapshot == nil {
		return
	}
	s.logger.Info("codexapp: replaying unfinished turn after recovery",
		"thread_id", snapshot.params.ThreadID,
		"local_turn_id", snapshot.localID,
		"provider_turn_id", snapshot.providerID,
	)
}

func (s *session) logReplayedTurn(snapshot *turnReplayState, newProviderID string) {
	if s.logger == nil || snapshot == nil {
		return
	}
	s.logger.Info("codexapp: unfinished turn replayed after recovery",
		"thread_id", snapshot.params.ThreadID,
		"local_turn_id", snapshot.localID,
		"old_provider_turn_id", snapshot.providerID,
		"new_provider_turn_id", newProviderID,
		"replayed_at", time.Now().UTC().Format(time.RFC3339Nano),
	)
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
	shared.SafeGo(s.logger, func() {
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
	})
}

func (s *session) checkIdleHealth() {
	if s.recovery == nil || time.Since(s.lastReadTime()) < healthCheckIdleThreshold {
		return
	}
	err := s.recovery.CheckHealth(s.ctx)
	if err == nil {
		s.noteReadActivity()
		return
	}
	if s.logger != nil {
		s.logger.Warn("codexapp: health check failed", "error", err)
	}
	// RPC protocol errors (-32600, -32601, etc.) indicate the server is alive but
	// returned an error response. Do not trigger recovery for these — only recover
	// when the transport is truly broken (connection lost, timeout, etc.).
	errMsg := strings.ToLower(err.Error())
	if strings.Contains(errMsg, "rpc error") || strings.Contains(errMsg, "invalid request") || strings.Contains(errMsg, "method not found") {
		s.noteReadActivity()
		return
	}
	shared.LogIgnoredError(s.logger, "health check recovery failed", s.attemptRecovery("health check failed: "+err.Error()))
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
	s.transport.ReadLoop(s.ctx, s.onInboundMessage)
}

func (s *session) finishReadLoop(done chan struct{}) {
	pkglogger.Warn("codexapp: read loop exited",
		"agent_id", s.agentID, "thread_id", s.ThreadID())
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
