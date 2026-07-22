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

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

type recoveryManager struct {
	transport *transport
	logger    *slog.Logger
	maxRetry  int
}

const (
	// maxRecoveryAttempts 限制单个会话内的 transport 恢复次数。
	// 连续失败后交给线程层重建 provider，避免死端口反复占用调用方等待时间。
	maxRecoveryAttempts      = 2
	healthCheckInterval      = 15 * time.Second
	healthCheckIdleThreshold = 30 * time.Second
)

type transportRetryPolicy struct {
	retryAfterWrite bool
}

type recoverableTransportWriteError struct {
	method string
	err    error
}

func retryPolicyForTransportMethod(method string) transportRetryPolicy {
	if strings.TrimSpace(method) == "turn/start" {
		return transportRetryPolicy{retryAfterWrite: false}
	}
	return transportRetryPolicy{retryAfterWrite: true}
}

func newRecoverableTransportWriteError(method string, err error) error {
	return &recoverableTransportWriteError{method: strings.TrimSpace(method), err: err}
}

// Error 描述写入后结果未知的 provider 调用，供上层提示用户可恢复重试。
func (e *recoverableTransportWriteError) Error() string {
	if e == nil {
		return "codexapp: transport write outcome unknown"
	}
	method := strings.TrimSpace(e.method)
	if method == "" {
		method = "transport"
	}
	if e.err == nil {
		return fmt.Sprintf("codexapp: %s write outcome unknown", method)
	}
	return fmt.Sprintf("codexapp: %s write outcome unknown: %v", method, e.err)
}

// Unwrap 保留底层 transport 失败，避免丢失 websocket 或 context 诊断。
func (e *recoverableTransportWriteError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// Recoverable 标记该错误可由用户或上层恢复流程重新发起，但本次不自动重放。
func (e *recoverableTransportWriteError) Recoverable() bool { return true }

type turnReplayState struct {
	localID, providerID string
	params              turnStartParams
	handle              *turnHandle
}

type replayTurnStatus struct {
	Active *bool
	Status string
	Turn   struct {
		Active *bool
		Status string
	}
}

type pendingTurnRecoveryState uint8

const (
	pendingTurnStillActive pendingTurnRecoveryState = iota
	pendingTurnLost
	pendingTurnCompleted
	pendingTurnFailed
	pendingTurnCancelled
)

// CheckHealth 检查 transport 是否仍能响应本地健康探测。
// 这里只看 app-server 连接本身，避免把 connector/catalog 等远端能力失败误判为会话不可用。
func (r *recoveryManager) CheckHealth(ctx context.Context) error {
	if r.transport == nil {
		return errors.New("codexapp: transport not running")
	}
	return r.transport.CheckHealth(ctx)
}

// Reconnect 在有限次数内重建 Codex app WebSocket。
// 每次尝试都有短超时，避免恢复路径因为单次 connect 卡住而阻塞 turn 调用。
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
	s.rememberPendingTurnLocked(handle, params)
}

// rememberPendingTurnLocked 由 StartTurn 的绑定临界区复用，避免 terminal 在登记前穿透 owner 生命周期。
func (s *session) rememberPendingTurnLocked(handle *turnHandle, params turnStartParams) {
	if handle == nil {
		return
	}
	s.pendingTurn = &turnReplayState{
		localID:    strings.TrimSpace(handle.LocalID()),
		providerID: strings.TrimSpace(handle.ProviderID()),
		params:     cloneTurnStartParams(params),
		handle:     handle,
	}
}

// callTransport 为同步 RPC 提供一次恢复重试，但尊重每个 method 的写后重试策略。
// turn/start 没有 idempotency key，写入已尝试后必须返回 recoverable error，不能自动再发一次。
func (s *session) callTransport(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return s.callTransportWithGuard(ctx, method, params, nil)
}

// callTransportWithGuard 在每次真实 wire write 前重验调用方 claim，恢复重试也使用同一 guard。
func (s *session) callTransportWithGuard(ctx context.Context, method string, params any, guard func() error) (json.RawMessage, error) {
	raw, err := s.transport.callGuarded(ctx, method, params, guard)
	if err == nil {
		return raw, err
	}
	if errors.Is(err, contract.ErrInterruptTargetChanged) {
		return nil, err
	}
	policy := retryPolicyForTransportMethod(method)
	if transportWriteAttempted(err) && !policy.retryAfterWrite {
		s.notifyWriteOutcomeUnknown(method, err)
		return nil, newRecoverableTransportWriteError(method, err)
	}
	if !shouldReconnect(err) {
		return raw, err
	}
	// 同步调用方正在等待 RPC 结果，因此由当前 goroutine 直接恢复并重试一次。
	// 异步信号只适合后台连接事件，否则调用方还要额外协调恢复完成时机。
	if recoverErr := s.attemptRecovery("transport-call: " + err.Error()); recoverErr != nil {
		return nil, errors.Join(err, recoverErr)
	}
	return s.transport.callGuarded(ctx, method, params, guard)
}

// notifyWriteOutcomeUnknown 在非幂等写入结果未知时只触发恢复，不自动重放请求。
func (s *session) notifyWriteOutcomeUnknown(method string, err error) {
	if s == nil || s.runtime == nil || strings.TrimSpace(s.ThreadID()) == "" {
		return
	}
	if shutdownErr := s.recoveryShutdownErr(); shutdownErr != nil {
		pkglogger.Warn("codexapp: write-outcome recovery suppressed during shutdown",
			"agent_id", s.agentID,
			"thread_id", s.ThreadID(),
			"method", strings.TrimSpace(method),
			"error", err,
			"shutdown_error", shutdownErr,
		)
		return
	}
	reason := strings.TrimSpace(method) + " write outcome unknown"
	if err != nil {
		if safeReason := observability.SafeProviderErrorReason(err.Error()); strings.TrimSpace(safeReason.Message) != "" {
			reason += ": " + safeReason.Message
		}
	}
	s.runtime.NotifyRecovery("write-outcome-unknown", reason)
}

// handleConnectionDead 处理 provider 主动上报的连接断开事件。
// 启动阶段没有 threadID 时会失败所有 pending RPC；运行中事件交给 runtime 的恢复队列串行处理。
func (s *session) handleConnectionDead(params json.RawMessage) {
	reason := shared.FirstNonEmpty(stringValue(decodeEventPayload(params), "error", "message"), "connection lost")
	safeReason := observability.SafeProviderErrorReason(reason)
	pkglogger.Warn("codexapp: CONNECTION DEAD (passive)",
		"agent_id", s.agentID,
		"thread_id", s.ThreadID(),
		"reason", safeReason.Message,
		"error_code", safeReason.Code,
	)
	if isNonRecoverableAuthErrorText(reason) {
		s.failNonRecoverableConnection(reason)
		return
	}
	if strings.TrimSpace(s.ThreadID()) == "" {
		err := errors.New("codexapp: startup failed: " + safeReason.Message)
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
	// 连接断开可能连续到达，runtime 会合并信号并受 stop gate 保护，避免 Close 时重启 reader。
	if s.runtime == nil {
		pkglogger.Warn("codexapp: handleConnectionDead dropped — runtime missing",
			"agent_id", s.agentID,
			"thread_id", s.ThreadID())
		return
	}
	s.runtime.NotifyRecovery("connection-dead", safeReason.Message)
}

// isNonRecoverableAuthErrorText 识别无需恢复的认证失败文本。
// 这些错误代表配置或凭据无效，继续重连只会重复失败并延迟上层失败处理。
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

// failNonRecoverableConnection 处理不可恢复的认证或配置失败，避免继续重连。
// 原始 provider reason 可能包含密钥，只能把安全 code/message 写入 turn error 和 AgentFailed payload。
func (s *session) failNonRecoverableConnection(reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "connection lost"
	}
	safeReason := observability.SafeProviderErrorReason(reason)
	pkglogger.Warn("codexapp: non-recoverable connection failure",
		"agent_id", s.agentID,
		"thread_id", s.ThreadID(),
		"reason", safeReason.Message,
		"error_code", safeReason.Code,
	)
	err := errors.New("codexapp: " + safeReason.Message)
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
			"timestamp":   time.Now().UTC().Format(time.RFC3339Nano),
			"error":       safeReason.Message,
			"errorCode":   safeReason.Code,
			"recoverable": false,
		},
	})
}

// attemptRecovery 串行执行 transport 重连、线程恢复和未完成 turn 重放。
// stop gate 已关闭或超过重试上限时立即失败，避免关闭流程和恢复流程互相抢 reader。
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
	// runtime 已进入关闭时不再恢复，否则会和 Close 的 reader drain 互相竞争。
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
		// replay 失败说明 transport 已恢复但 session 层状态无法继续复用。
		// 通过 failRecovery 分发 connection.dead，让 thread 层升级为完整重启。
		return s.failRecovery(reason, err)
	}
	s.recoveryCount.Store(0)
	s.noteReadActivity()
	return nil
}

// dispatchRecoveryAttempt 发布恢复尝试事件。
// 所有恢复入口共用这一 payload，便于 UI 和日志按同一字段追踪失败原因与第几次尝试。
func (s *session) dispatchRecoveryAttempt(reason string, attempt int32) {
	s.dispatch(dto.RawProviderEvent{
		EventType: "recovery.attempt",
		Data: map[string]any{
			"agentId":   strings.TrimSpace(s.agentID),
			"threadId":  s.ThreadID(),
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"reason":    strings.TrimSpace(reason),
			"attempt":   attempt,
		},
	})
}

// completeRecoveryReplay 按固定顺序重建 reader、恢复线程并重放未完成 turn。
// 任一步失败都会进入 failRecovery，交给线程层决定是否重启 provider。
func (s *session) completeRecoveryReplay(reason string) error {
	if s.runtime != nil {
		if !s.runtime.restartReader() {
			return s.failRecovery(reason, errRuntimeStopped)
		}
	}
	s.mu.Lock()
	s.suppressed = make(map[string]struct{})
	s.mu.Unlock()
	// app-server 重连后 approval request ID 可能重置，旧去重状态不能跨 transport generation 复用。
	s.clearProcessedApprovals()
	if err := s.resumeThreadAfterRecovery(s.ctx); err != nil {
		return s.failRecovery(reason, err)
	}
	if err := s.replayPendingTurn(s.ctx); err != nil {
		return s.failRecovery(reason, err)
	}
	return nil
}

// resumeThreadAfterRecovery 在新连接上恢复当前 provider thread。
// 恢复需要可信 cwd；缺失或不可访问会 fail-fast，避免 provider 在错误目录继续执行。
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
		fields := []any{"thread_id", threadID}
		fields = append(fields, shared.SafePathLogFields("cwd", cwd)...)
		s.logger.Info("codexapp: resuming thread after recovery", fields...)
	}
	raw, err := callWithTimeout(ctx, s.transport, 30*time.Second, "thread/resume", threadResumeParams{
		ThreadID: threadID,
		Cwd:      cwd,
	})
	if err != nil {
		return fmt.Errorf("codexapp: thread/resume after recovery failed: %w", err)
	}
	newID, err := decodeThreadID(raw)
	if err != nil {
		return fmt.Errorf("codexapp: decode thread/resume recovery result: %w", err)
	}
	s.setThreadID(newID)
	return nil
}

// recoveryResumeCWD 返回恢复线程时必须使用的工作目录。
// 空 cwd、相对占位或不存在目录都会报错，防止恢复后工具调用落到未知路径。
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

// replayPendingTurn 只在 provider 明确确认丢失且没有本地 Stop owner 时重放。
// 已确认终态必须先同步收敛本地 handle，避免恢复成功后留下永远 active 的 turn。
func (s *session) replayPendingTurn(ctx context.Context) error {
	if err := shared.CheckCtx(ctx); err != nil {
		return err
	}
	ctx = shared.NonNilContext(ctx)
	snapshot, state, err := s.pendingTurnRecoveryState(ctx)
	if err != nil {
		return err
	}
	if snapshot == nil {
		return nil
	}
	settled, err := s.settlePendingTurnForRecovery(snapshot, state)
	if err != nil {
		return err
	}
	if settled {
		return nil
	}
	return s.replayLostPendingTurn(ctx, snapshot)
}

// pendingTurnRecoveryState 获取可恢复 turn 的稳定快照和 provider 确认状态。
func (s *session) pendingTurnRecoveryState(ctx context.Context) (*turnReplayState, pendingTurnRecoveryState, error) {
	snapshot := s.pendingTurnSnapshot()
	if snapshot == nil || replayTurnDone(snapshot.handle) {
		return nil, 0, nil
	}
	if err := validatePendingTurnSnapshot(snapshot); err != nil {
		return nil, 0, err
	}
	state, err := s.confirmPendingTurnState(ctx, snapshot)
	if err != nil {
		return nil, 0, err
	}
	return snapshot, state, nil
}

// settlePendingTurnForRecovery 优先收敛明确终态或可信 Stop owner，只有 lost 且无 Stop owner 才允许重放。
func (s *session) settlePendingTurnForRecovery(snapshot *turnReplayState, state pendingTurnRecoveryState) (bool, error) {
	switch state {
	case pendingTurnStillActive:
		return true, nil
	case pendingTurnLost:
		return s.settleLostPendingInterrupt(snapshot)
	case pendingTurnCompleted, pendingTurnFailed, pendingTurnCancelled:
		return true, s.settleRecoveredPendingTurn(snapshot, state)
	default:
		return false, errors.New("codexapp: recovery turn state is required")
	}
}

// replayLostPendingTurn 只在远端确认 lost 且本地没有可信 Stop owner 后创建新的 provider turn。
func (s *session) replayLostPendingTurn(ctx context.Context, snapshot *turnReplayState) error {
	s.logReplayPendingTurn(snapshot)
	newProviderID, err := s.replayTurnStart(ctx, snapshot.params)
	if err != nil {
		return err
	}
	s.applyReplayedTurn(snapshot, newProviderID)
	s.logReplayedTurn(snapshot, newProviderID)
	return nil
}

// confirmPendingTurnState 查询 provider 侧 turn 状态；仍 active 时禁止重放，避免同一输入执行两次。
func (s *session) confirmPendingTurnState(ctx context.Context, snapshot *turnReplayState) (pendingTurnRecoveryState, error) {
	providerID := strings.TrimSpace(snapshot.providerID)
	if providerID == "" {
		return 0, errors.New("codexapp: replay provider turn id is required")
	}
	raw, err := callWithTimeout(ctx, s.transport, 10*time.Second, "turn/status", map[string]any{"threadId": snapshot.params.ThreadID, "turnId": providerID})
	if err != nil {
		return 0, fmt.Errorf("codexapp: turn/status before replay failed: %w", err)
	}
	var payload replayTurnStatus
	if err := json.Unmarshal(raw, &payload); err != nil {
		return 0, fmt.Errorf("codexapp: turn/status before replay decode failed: %w", err)
	}
	active := payload.Active
	if payload.Turn.Active != nil {
		active = payload.Turn.Active
	}
	if active == nil {
		return 0, errors.New("codexapp: turn/status before replay missing active state")
	}
	if *active {
		return pendingTurnStillActive, nil
	}
	return pendingTurnRecoveryStateForStatus(payload)
}

// pendingTurnRecoveryStateForStatus 将 provider 的非 active 状态规范为 recovery 所需的唯一状态集合。
func pendingTurnRecoveryStateForStatus(payload replayTurnStatus) (pendingTurnRecoveryState, error) {
	status := strings.ToLower(strings.TrimSpace(shared.FirstNonEmpty(payload.Turn.Status, payload.Status)))
	switch status {
	case "lost", "not_found", "not-found", "missing":
		return pendingTurnLost, nil
	case "completed", "complete":
		return pendingTurnCompleted, nil
	case "failed":
		return pendingTurnFailed, nil
	case "canceled", "cancelled", "aborted", "interrupted":
		return pendingTurnCancelled, nil
	case "":
		return 0, errors.New("codexapp: turn/status before replay missing lost or terminal state")
	default:
		return 0, fmt.Errorf("codexapp: turn/status before replay unknown state %q", status)
	}
}

// settleLostPendingInterrupt 在重放前消费可信的本地 Stop owner。
// 空 requestID 仍会阻止重放，但绝不伪造 user_request 归因。
func (s *session) settleLostPendingInterrupt(snapshot *turnReplayState) (bool, error) {
	h, claim, settled, terminalOwned := s.claimAndTakeRecoveryTurn(snapshot, true)
	if !settled {
		if terminalOwned || replayTurnDone(snapshot.handle) {
			return true, nil
		}
		// 没有与当前 turn 代际匹配的 Stop owner 时，保留恢复重放；pendingTurn
		// 本身是 replay snapshot，不应被误判为一条已接受的 Stop 请求。
		return false, nil
	}
	turnID := strings.TrimSpace(snapshot.providerID)
	payload := map[string]any{
		"agentId":   s.agentID,
		"threadId":  s.agentID,
		"turnId":    turnID,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"success":   false,
		"status":    "cancelled",
		"error":     "turn cancelled after interrupt recovery",
	}
	outcome := &dto.TerminalOutcome{Status: "cancelled", Cause: "system"}
	payload["termination_cause"] = outcome.Cause
	if requestID := strings.TrimSpace(claim.requestID); requestID != "" {
		outcome.Cause = "user_request"
		outcome.RequestID = requestID
		payload["termination_cause"] = outcome.Cause
		payload["termination_request_id"] = outcome.RequestID
	}
	s.dispatch(dto.RawProviderEvent{EventType: "turn/completed", Data: payload, Terminal: outcome})
	h.complete(errors.New("turn cancelled after interrupt recovery"))
	return true, nil
}

// settleRecoveredPendingTurn 将明确的远端终态收敛为唯一的本地 canonical terminal。
func (s *session) settleRecoveredPendingTurn(snapshot *turnReplayState, state pendingTurnRecoveryState) error {
	h, claim, settled, terminalOwned := s.claimAndTakeRecoveryTurn(snapshot, false)
	if !settled {
		if terminalOwned || replayTurnDone(snapshot.handle) {
			return nil
		}
		return errors.New("codexapp: confirmed remote terminal could not settle live turn")
	}
	turnID := strings.TrimSpace(snapshot.providerID)
	payload := map[string]any{
		"agentId":   s.agentID,
		"threadId":  s.agentID,
		"turnId":    turnID,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	}
	outcome := &dto.TerminalOutcome{}
	var completeErr error
	switch state {
	case pendingTurnCompleted:
		outcome.Success = true
		outcome.Status = "completed"
		payload["success"] = true
		payload["status"] = outcome.Status
	case pendingTurnFailed:
		outcome.Status = "failed"
		payload["success"] = false
		payload["status"] = outcome.Status
		payload["error"] = "turn failed during recovery"
		completeErr = errors.New("turn failed during recovery")
	case pendingTurnCancelled:
		outcome.Status = "cancelled"
		outcome.Cause = "provider"
		payload["success"] = false
		payload["status"] = outcome.Status
		payload["error"] = "turn cancelled during recovery"
		payload["termination_cause"] = outcome.Cause
		completeErr = errors.New("turn cancelled during recovery")
		if claim != nil {
			if requestID := strings.TrimSpace(claim.requestID); requestID != "" {
				outcome.Cause = "user_request"
				outcome.RequestID = requestID
				payload["termination_cause"] = outcome.Cause
				payload["termination_request_id"] = outcome.RequestID
			}
		}
	default:
		return errors.New("codexapp: recovered terminal state is required")
	}
	s.dispatch(dto.RawProviderEvent{EventType: "turn/completed", Data: payload, Terminal: outcome})
	h.complete(completeErr)
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
		delete(s.interruptRequests, snapshot.providerID)
		staleProviderID = snapshot.providerID
	}
	s.turns[newProviderID] = snapshot.handle
	s.setActiveTurnLocked(newProviderID)
	if s.pendingTurn != nil && s.pendingTurn.handle == snapshot.handle {
		s.pendingTurn.providerID = newProviderID
		s.pendingTurn.params = cloneTurnStartParams(snapshot.params)
	}
	s.mu.Unlock()
	// 重放会产生新的 provider turnID，旧 ID 下累积的输出不会再收到 TurnCompleted，必须显式丢弃。
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

// shouldReconnect 判断 transport 错误是否值得进入恢复流程。
// 上下文取消、显式关闭和 RPC 协议错误都不是连接失活，继续恢复会掩盖真实原因。
func shouldReconnect(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	// 明确销毁或 RPC 协议错误说明服务仍有确定响应，此时恢复只会制造额外副作用。
	if strings.Contains(message, "transport unavailable") ||
		strings.Contains(message, "transport closing") ||
		strings.HasPrefix(message, "rpc error ") {
		return false
	}
	// transport closed 可能来自空闲断开或网络抖动，需要走恢复和 thread/resume。
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
