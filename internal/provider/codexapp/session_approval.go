package codexapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type processedApprovalEntry struct {
	decision contract.ApprovalDecision
	err      error
	ready    chan struct{}
	done     bool
}

const processedApprovalLimit = 1000

func (s *session) handleApprovalRequest(method string, params json.RawMessage) {
	if s.approvals == nil {
		return
	}
	payload := append(json.RawMessage(nil), params...)
	runtimesafe.SafeGo(s.ctx, s.logger, "codexapp.session.toolApprovalRequest", func(ctx context.Context) {
		if err := s.requestToolApprovalWithContext(ctx, strings.TrimSpace(method), payload); err != nil && s.logger != nil {
			s.logger.Warn("codexapp: approval request failed", "method", method, "error", err)
		}
	})
}

func (s *session) requestToolApproval(method string, params json.RawMessage) error {
	return s.requestToolApprovalWithContext(s.ctx, method, params)
}

func (s *session) requestToolApprovalWithContext(ctx context.Context, method string, params json.RawMessage) error {
	req, requestID, ok := s.buildApprovalRequest(method, decodeEventPayload(params))
	if !ok {
		return nil
	}
	key := processedApprovalRequestKey(req, requestID)
	entry, owner := s.beginProcessedApproval(key)
	if !owner {
		decision, err := s.waitProcessedApproval(ctx, entry)
		if err != nil {
			return err
		}
		return s.sendApprovalDecision(requestID, decision)
	}
	decision, err := s.requestApprovalDecision(req)
	s.finishProcessedApproval(key, entry, decision, err)
	if err != nil {
		return err
	}
	return s.sendApprovalDecision(requestID, decision)
}

func (s *session) requestApprovalDecision(req rpc.ApprovalRequest) (contract.ApprovalDecision, error) {
	if s == nil {
		return contract.ApprovalDecision{}, errors.New("session is nil")
	}
	ctx, cancel := approvalDecisionContext(s.ctx)
	defer cancel()
	if s.approvalDecisionHook != nil {
		return s.approvalDecisionHook(ctx, req)
	}
	if isRequestUserInputMethod(req.SourceMethod) {
		req.Kind = "request_user_input"
		return s.approvals.RequestUserInput(ctx, nil, nil, req)
	}
	return s.approvals.RequestApproval(ctx, nil, nil, req)
}

func approvalDecisionContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return rpc.WithApprovalDeadline(rpc.WithApprovalAutoDeclineOnCancel(ctx))
}

func (s *session) setApprovalPolicy(policy string) {
	if s == nil {
		return
	}
	s.approvalPolicy.Store(strings.TrimSpace(policy))
}

func (s *session) approvalPolicyValue() string {
	if s == nil {
		return ""
	}
	value, _ := s.approvalPolicy.Load().(string)
	return strings.TrimSpace(value)
}

// beginProcessedApproval 为 approval 请求建立去重槽。
// 返回 owner=false 表示已有 goroutine 正在处理同一请求，当前调用方只需要等待结果。
func (s *session) beginProcessedApproval(key string) (*processedApprovalEntry, bool) {
	if s == nil || key == "" {
		return nil, true
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	if s.processedApprovals == nil {
		s.processedApprovals = map[string]*processedApprovalEntry{}
	}
	if entry := s.processedApprovals[key]; entry != nil {
		return entry, false
	}
	if len(s.processedApprovals) >= processedApprovalLimit {
		s.purgeCompletedProcessedApprovalsLocked()
	}
	entry := &processedApprovalEntry{ready: make(chan struct{})}
	s.processedApprovals[key] = entry
	return entry, true
}

func (s *session) purgeCompletedProcessedApprovalsLocked() {
	for key, entry := range s.processedApprovals {
		if entry != nil && entry.done {
			delete(s.processedApprovals, key)
		}
	}
}

// finishProcessedApproval 写入 approval 决策并唤醒等待方。
// 失败结果不会留在去重表中，避免后续相同请求复用一次失败的临时状态。
func (s *session) finishProcessedApproval(key string, entry *processedApprovalEntry, decision contract.ApprovalDecision, err error) {
	if s == nil || entry == nil {
		return
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	if entry.done {
		return
	}
	entry.decision = cloneApprovalDecision(decision)
	entry.err = err
	entry.done = true
	if err != nil {
		if current := s.processedApprovals[key]; current == entry {
			delete(s.processedApprovals, key)
		}
	}
	close(entry.ready)
}

func (s *session) clearProcessedApprovals() {
	if s == nil {
		return
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	clear(s.processedApprovals)
}

func (s *session) buildApprovalRequest(method string, payload map[string]any) (rpc.ApprovalRequest, int64, bool) {
	if len(payload) == 0 {
		return rpc.ApprovalRequest{}, 0, false
	}
	requestID := int64Value(payload, "requestId", "request_id")
	if requestID <= 0 {
		return rpc.ApprovalRequest{}, 0, false
	}
	callID := payloadCallID(payload, stringValue(payload, "approvalId", "approval_id"), strconv.FormatInt(requestID, 10))
	requestRef := requestID
	return rpc.ApprovalRequest{
		CallID:         callID,
		ApprovalID:     stringValue(payload, "approvalId", "approval_id"),
		ToolName:       payloadToolName(payload),
		AgentID:        s.agentID,
		ThreadID:       payloadThreadID(payload, s.ThreadID()),
		TurnID:         payloadTurnID(payload),
		Reason:         stringValue(payload, "reason", "message"),
		SourceMethod:   method,
		RequestID:      &requestRef,
		ApprovalPolicy: s.approvalPolicyValue(),
		Payload:        payload,
	}, requestID, callID != ""
}

// sendApprovalDecision 把宿主 approval 决策回写给 Codex app-server。
// requestID 和 transport 都必须存在，否则 fail-fast 防止 provider 侧请求悬空。
func (s *session) sendApprovalDecision(requestID int64, decision contract.ApprovalDecision) error {
	if requestID <= 0 {
		return errors.New("codexapp: approval request id is required")
	}
	if s == nil || s.transport == nil {
		return errors.New("codexapp: approval transport is not initialized")
	}
	params := map[string]any{"requestId": requestID}
	if decision.Approved != nil {
		params["approved"] = *decision.Approved
	}
	if len(decision.Detail) > 0 {
		params["decision"] = append(json.RawMessage(nil), decision.Detail...)
	} else if reason := strings.TrimSpace(decision.Reason); reason != "" {
		params["decision"] = reason
	}
	_, err := callWithTimeout(s.ctx, callTargetFunc(s.callTransport), 10*time.Second, "approval/respond", params)
	return err
}

func cloneApprovalDecision(decision contract.ApprovalDecision) contract.ApprovalDecision {
	decision.Detail = append(json.RawMessage(nil), decision.Detail...)
	return decision
}

func processedApprovalKey(callID string, requestID int64) string {
	if requestID <= 0 {
		return ""
	}
	id := strconv.FormatInt(requestID, 10)
	if callID = strings.TrimSpace(callID); callID != "" {
		return callID + ":" + id
	}
	return id
}

func processedApprovalRequestKey(req rpc.ApprovalRequest, requestID int64) string {
	if requestID <= 0 {
		return ""
	}
	fingerprint := approvalRequestFingerprint(req, requestID)
	if fingerprint == "" {
		return processedApprovalKey(req.CallID, requestID)
	}
	return strconv.FormatInt(requestID, 10) + ":" + fingerprint
}

func approvalRequestFingerprint(req rpc.ApprovalRequest, requestID int64) string {
	payload := normalizeApprovalFingerprintPayload(req.Payload)
	envelope := map[string]any{
		"requestId": requestID,
		"method":    strings.TrimSpace(req.SourceMethod),
		"payload":   payload,
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func normalizeApprovalFingerprintPayload(payload map[string]any) any {
	out := make(map[string]any, len(payload))
	for key, child := range payload {
		switch strings.TrimSpace(key) {
		case "callId", "call_id", "approvalId", "approval_id":
			continue
		default:
			out[key] = normalizeApprovalFingerprintValue(child)
		}
	}
	return out
}

func normalizeApprovalFingerprintValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = normalizeApprovalFingerprintValue(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = normalizeApprovalFingerprintValue(child)
		}
		return out
	default:
		return value
	}
}

// waitProcessedApproval 等待同一 approval 请求的 owner 写入结果。
// 任一外部 ctx 或 session ctx 取消都会立即返回，避免重复请求在关闭时卡住。
func (s *session) waitProcessedApproval(ctx context.Context, entry *processedApprovalEntry) (contract.ApprovalDecision, error) {
	if entry == nil {
		return contract.ApprovalDecision{}, nil
	}
	ctx = shared.NonNilContext(ctx)
	sessionCtx := context.Background()
	if s != nil {
		sessionCtx = shared.NonNilContext(s.ctx)
	}
	select {
	case <-entry.ready:
		return cloneApprovalDecision(entry.decision), entry.err
	case <-ctx.Done():
		return contract.ApprovalDecision{}, ctx.Err()
	case <-sessionCtx.Done():
		return contract.ApprovalDecision{}, sessionCtx.Err()
	}
}

func isApprovalBridgeMethod(method string) bool {
	return hasMethod(method, approvalBridgeMethods)
}

func isRequestUserInputMethod(method string) bool {
	return hasMethod(method, requestUserInputMethods)
}

// onNotification 处理 Codex app-server 推送的通知事件。
// 外来线程事件会被丢弃；turn 输出先补齐 result 再进入抑制和派发路径。
func (s *session) onNotification(method string, params json.RawMessage) {
	s.noteReadActivity()
	if eventThread, ok := s.alienThreadEventThread(params); ok {
		pkglogger.Warn("codexapp: dropped alien thread event",
			"agent_id", s.agentID,
			"method", method,
			"own_thread", s.ThreadID(),
			"event_thread", eventThread,
		)
		return
	}
	// 先合并流式输出，再判断是否抑制终态事件，确保 forceComplete 路径仍能释放累积结果。
	params = s.sniffTurnOutput(method, params)
	if s.shouldSuppressTurnEvent(method, params) {
		pkglogger.Warn("codexapp: suppressed duplicate turn terminal event",
			"agent_id", s.agentID, "method", method)
		return
	}
	if s.shouldSuppressToolEndEvent(method, params) {
		pkglogger.Warn("codexapp: suppressed duplicate tool terminal event",
			"agent_id", s.agentID, "method", method)
		return
	}
	method = strings.TrimSpace(method)
	raw := dto.RawProviderEvent{EventType: method, Data: params}
	if !isApprovalBridgeMethod(method) || s.approvals == nil {
		s.dispatch(raw)
	}
	s.handleNotificationAction(method, params)
}

// sniffTurnOutput 把流式 message delta 累积到 turn buffer，并在终态事件中补回 result。
// 这样下游仍只消费一个 TurnCompleted payload，同时保留 1 MiB 截断标记。
//
// 非 turn 输出事件会原样返回，保持普通通知的派发行为不变。
func (s *session) sniffTurnOutput(method string, params json.RawMessage) json.RawMessage {
	switch {
	case isMessageStreamDeltaEvent(method):
		s.absorbMessageDelta(params)
		return params
	case isTurnTerminalEvent(method):
		return s.injectAccumulatedResult(params)
	default:
		return params
	}
}

// absorbMessageDelta 将 stream=message 的 TurnOutputDelta 写入对应 turn 累积器。
// 缺 turnID 或 delta 为空时直接忽略，避免污染未知 turn 的结果。
func (s *session) absorbMessageDelta(params json.RawMessage) {
	payload := decodeEventPayload(params)
	if normalizedTurnOutputStream(payload, "message") != "message" {
		return
	}
	turnID := payloadTurnID(payload)
	delta := stringValue(payload, "delta", "content")
	if turnID == "" || delta == "" {
		return
	}
	s.appendTurnOutputDelta(turnID, delta)
}

// injectAccumulatedResult 把累积的 message stream 合并进 TurnCompleted payload。
// provider 已提供 result 时不会覆盖；编码失败或无累积内容时保持原 params。
func (s *session) injectAccumulatedResult(params json.RawMessage) json.RawMessage {
	payload := decodeEventPayload(params)
	turnID := payloadTurnID(payload)
	if turnID == "" {
		return params
	}
	merged, truncated := s.consumeTurnOutputAccumulator(turnID)
	if merged == "" && !truncated {
		return params
	}
	if payload == nil {
		payload = map[string]any{}
	}
	// provider 将来若原生提供 result，本地累积只作为补充，不能覆盖远端事实。
	if _, ok := payload["result"]; !ok && merged != "" {
		payload["result"] = merged
	}
	if truncated {
		payload["truncated"] = true
	}
	return encodeEventPayload(payload, params)
}

func (s *session) handleNotificationAction(method string, params json.RawMessage) {
	switch {
	case isApprovalBridgeMethod(method):
		s.handleApprovalRequest(method, params)
	case isTurnTerminalEvent(method):
		s.finishTurn(params, turnTerminalSuccess(method, decodeEventPayload(params)))
	case s.completeRolloutAssistantMessage(method, params):
	case method == "connection.dead":
		s.handleConnectionDead(params)
	}
}

// completeRolloutAssistantMessage 将 rollout 中的 assistant response_item 合成为本地 turn 完成事件。
// 只有消息属于当前 active turn 时才会补完成，避免其他线程或历史回放误关闭正在运行的 turn。
func (s *session) completeRolloutAssistantMessage(method string, params json.RawMessage) bool {
	if strings.TrimSpace(method) != "response_item" {
		return false
	}
	msg, ok := parseRolloutLine(mustJSON(map[string]any{"type": "response_item", "payload": params}))
	if !ok || msg.Role != "assistant" {
		return false
	}
	payload := decodeEventPayload(params)
	turnID := shared.FirstNonEmpty(payloadTurnID(payload), payloadTurnID(codexToolItemPayload(payload)))
	s.mu.Lock()
	if turnID == "" {
		turnID = s.activeTurnID
	}
	ok = turnID != "" && turnID == s.activeTurnID && s.turns[turnID] != nil
	s.mu.Unlock()
	if !ok {
		return false
	}
	s.completeSyntheticTurn(turnID, "rollout_assistant_message", msg.Content)
	return true
}

func (s *session) alienThreadEventThread(params json.RawMessage) (string, bool) {
	own := s.ThreadID()
	if own == "" {
		return "", false
	}
	var envelope struct {
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(params, &envelope); err != nil {
		return "", false
	}
	eventThread := strings.TrimSpace(envelope.ThreadID)
	if eventThread == "" {
		return "", false
	}
	if eventThread == own {
		return "", false
	}
	return eventThread, true
}
