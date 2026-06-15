package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
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
	// maxRecoveryAttempts caps transport-level WS reconnect before escalating
	// to session-level recovery (evict zombie + re-launch CLI). Set to 2 so
	// a dead app-server port does not burn 3 pointless reconnect cycles.
	maxRecoveryAttempts      = 2
	healthCheckInterval      = 15 * time.Second
	healthCheckIdleThreshold = 30 * time.Second
)

type turnReplayState struct {
	localID    string
	providerID string
	params     turnStartParams
	handle     *turnHandle
}

// CheckHealth 检查底层服务健康状态。
func (r *recoveryManager) CheckHealth(ctx context.Context) error {
	if r.transport == nil {
		return errors.New("codexapp: transport not running")
	}
	// Keep liveness local to the app-server transport. Capability calls such
	// as app/list depend on ChatGPT connectors/catalog availability and must
	// not decide whether this session is alive.
	return r.transport.CheckHealth(ctx)
}

// Reconnect 处理reconnect。
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
	// Sync path: the caller goroutine owns this attempt, so recovery runs
	// inline rather than via the async signal worker. NotifyRecovery + async
	// wait would force every caller to coordinate completion, which P1c
	// §实施方式 explicitly keeps out of scope.
	if recoverErr := s.attemptRecovery("transport-call: " + err.Error()); recoverErr != nil {
		return nil, errors.Join(err, recoverErr)
	}
	return s.transport.Call(ctx, method, params)
}

// handleConnectionDead 处理connectiondead。
func (s *session) handleConnectionDead(params json.RawMessage) {
	reason := shared.FirstNonEmpty(stringValue(decodeEventPayload(params), "error", "message"), "connection lost")
	pkglogger.Warn("codexapp: CONNECTION DEAD (passive)",
		"agent_id", s.agentID,
		"thread_id", s.ThreadID(),
		"reason", reason,
	)
	if isNonRecoverableAuthErrorText(reason) {
		s.failNonRecoverableConnection(reason)
		return
	}
	if strings.TrimSpace(s.ThreadID()) == "" {
		err := errors.New("codexapp: startup failed: " + strings.TrimSpace(reason))
		if s.transport != nil {
			s.transport.failPending(err)
		}
		return
	}
	if err := shared.CheckCtx(s.ctx); err != nil {
		pkglogger.Warn("codexapp: handleConnectionDead skipped (ctx done)",
			"agent_id", s.agentID,
			"thread_id", s.ThreadID(),
			"ctx_err", err,
		)
		return
	}
	// P1c: replace the fire-and-forget SafeGo + attemptRecovery chain with a
	// coalesced signal into SessionRuntime. The runtime's recovery worker
	// serialises the attempt and honours the stop gate.
	if s.runtime == nil {
		pkglogger.Warn("codexapp: handleConnectionDead dropped — runtime missing",
			"agent_id", s.agentID,
			"thread_id", s.ThreadID())
		return
	}
	s.runtime.NotifyRecovery("connection-dead", reason)
}

// isNonRecoverableAuthErrorText 判断nonrecoverable认证错误文本是否可用。
func isNonRecoverableAuthErrorText(reason string) bool {
	text := strings.ToLower(strings.TrimSpace(reason))
	if text == "" {
		return false
	}
	if strings.Contains(text, "invalid_api_key") || strings.Contains(text, "incorrect api key") {
		return true
	}
	if strings.Contains(text, "api key") && (strings.Contains(text, "invalid") || strings.Contains(text, "incorrect") || strings.Contains(text, "unauthorized")) {
		return true
	}
	return strings.Contains(text, "401") && strings.Contains(text, "unauthorized")
}

func (s *session) failNonRecoverableConnection(reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "connection lost"
	}
	pkglogger.Warn("codexapp: non-recoverable connection failure",
		"agent_id", s.agentID,
		"thread_id", s.ThreadID(),
		"reason", reason,
	)
	err := errors.New("codexapp: " + reason)
	if strings.TrimSpace(s.ThreadID()) == "" {
		if s.transport != nil {
			s.transport.failPending(err)
		}
		return
	}
	s.failTurns(err)
	s.dispatch(dto.RawProviderEvent{
		EventType: "connection.dead",
		Data: map[string]any{
			"agentId":     strings.TrimSpace(s.agentID),
			"threadId":    s.ThreadID(),
			"error":       reason,
			"recoverable": false,
		},
	})
}

// attemptRecovery 处理attemptrecovery。
func (s *session) attemptRecovery(reason string) error {
	if err := s.recoveryShutdownErr(); err != nil {
		return err
	}
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
	// P1c stop gate: if the runtime has already begun shutdown, do not
	// attempt a recovery that would race with Close's drain.
	if err := s.recoveryShutdownErr(); err != nil {
		return err
	}
	s.dispatchRecoveryAttempt(reason, count)
	if s.runtime != nil {
		s.runtime.cancelReader()
		if s.transport != nil {
			s.transport.closeSocket()
		}
		waitCtx, cancel := withTimeout(s.ctx, 2*time.Second)
		defer cancel()
		if err := s.runtime.waitReader(waitCtx); err != nil {
			return s.failRecovery(reason, err)
		}
	}
	if err := s.recovery.Reconnect(s.ctx); err != nil {
		return s.failRecovery(reason, err)
	}
	if err := s.completeRecoveryReplay(reason); err != nil {
		// Replay failure means the transport connected but the session is
		// unrecoverable at this level. Route through failRecovery so
		// connection.dead is dispatched → thread layer can escalate to a
		// full CLI re-launch (evict zombie + backgroundResume).
		return s.failRecovery(reason, err)
	}
	s.recoveryCount.Store(0)
	s.noteReadActivity()
	return nil
}

// dispatchRecoveryAttempt publishes the `recovery.attempt` provider event.
// Split out from attemptRecovery so both that function and any future
// signal-level observers share a single payload shape.
func (s *session) dispatchRecoveryAttempt(reason string, attempt int32) {
	s.dispatch(dto.RawProviderEvent{
		EventType: "recovery.attempt",
		Data: map[string]any{
			"agentId":  strings.TrimSpace(s.agentID),
			"threadId": s.ThreadID(),
			"reason":   strings.TrimSpace(reason),
			"attempt":  attempt,
		},
	})
}

// completeRecoveryReplay performs the P1c-frozen replay sequence (see README
// §recovery replay 顺序): spawn a new reader after pre-reconnect drain,
// reset the suppressed map, resume the thread, then replay the pending turn.
// Returns the first error it encounters, already wrapped via failRecovery.
func (s *session) completeRecoveryReplay(reason string) error {
	if s.runtime != nil {
		if !s.runtime.restartReader() {
			return s.failRecovery(reason, errRuntimeStopped)
		}
	}
	s.mu.Lock()
	s.suppressed = make(map[string]struct{})
	s.mu.Unlock()
	// The app-server/proxy may reset approval request IDs across reconnects.
	// Drop completed/in-flight approval de-dupe state so a post-recovery request
	// cannot inherit a stale decision from the previous transport generation.
	s.clearProcessedApprovals()
	if err := s.resumeThreadAfterRecovery(s.ctx); err != nil {
		return s.failRecovery(reason, err)
	}
	if err := s.replayPendingTurn(s.ctx); err != nil {
		return s.failRecovery(reason, err)
	}
	return nil
}

// resumeThreadAfterRecovery 处理恢复线程后置recovery。
func (s *session) resumeThreadAfterRecovery(ctx context.Context) error {
	threadID := s.ThreadID()
	if threadID == "" {
		return nil
	}
	cwd, err := s.recoveryResumeCWD()
	if err != nil {
		return err
	}
	if s.logger != nil {
		s.logger.Info("codexapp: resuming thread after recovery", "thread_id", threadID, "cwd", cwd)
	}
	raw, err := callWithTimeout(ctx, s.transport, 30*time.Second, "thread/resume", threadResumeParams{
		ThreadID: threadID,
		Cwd:      cwd,
	})
	if err != nil {
		return fmt.Errorf("codexapp: thread/resume after recovery failed: %w", err)
	}
	if newID, decodeErr := decodeThreadID(raw, threadID); decodeErr == nil && newID != "" {
		s.setThreadID(newID)
	}
	return nil
}

// recoveryResumeCWD 处理recovery恢复工作目录。
func (s *session) recoveryResumeCWD() (string, error) {
	if s == nil {
		return "", errors.New("codexapp: recovery cwd is required")
	}
	cwd := strings.TrimSpace(s.runtimeConfigString("cwd"))
	if cwd == "" || cwd == "." {
		return "", fmt.Errorf("codexapp: recovery cwd is required for thread %q", s.ThreadID())
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return "", fmt.Errorf("codexapp: recovery cwd stat %q: %w", cwd, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("codexapp: recovery cwd is not a directory: %q", cwd)
	}
	return cwd, nil
}

// replayPendingTurn 处理replay待处理turn。
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
	var staleProviderID string
	if snapshot.providerID != "" {
		delete(s.turns, snapshot.providerID)
		staleProviderID = snapshot.providerID
	}
	s.turns[newProviderID] = snapshot.handle
	s.activeTurnID = newProviderID
	if s.pendingTurn != nil && s.pendingTurn.handle == snapshot.handle {
		s.pendingTurn.providerID = newProviderID
		s.pendingTurn.params = cloneTurnStartParams(snapshot.params)
	}
	s.mu.Unlock()
	// ADR-015 v4.1 §2.1 cleanup hook: recovery allocates a fresh provider
	// turn-id (turn/start above), so the buffer under the previous id can
	// never be flushed by a future TurnCompleted. Drop it explicitly.
	if staleProviderID != "" {
		s.dropTurnOutputAccumulator(staleProviderID)
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

// shouldReconnect 判断reconnect是否可用。
func shouldReconnect(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	// "transport unavailable" means session was explicitly destroyed (nil transport).
	// RPC protocol errors (-32600, -32601, etc.) mean the server is alive.
	// In both cases recovery would be pointless or harmful.
	if strings.Contains(message, "transport unavailable") ||
		strings.Contains(message, "transport closing") ||
		strings.HasPrefix(message, "rpc error ") {
		return false
	}
	// "transport closed" now triggers recovery — the WebSocket may have
	// disconnected due to idle timeout or network issues, and reconnecting
	// via attemptRecovery + thread/resume will restore the session.
	return true
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
