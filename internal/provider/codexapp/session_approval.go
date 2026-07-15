package codexapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	contract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

type processedApprovalEntry struct {
	fingerprint string
	decision    contract.ApprovalDecision
	err         error
	ready       chan struct{}
	done        bool
}

const processedApprovalLimit = 1000

// handleApprovalRequest 接收运行时审批请求；依赖损坏时失败当前 turn 并取消会话。
func (s *session) handleApprovalRequest(method string, params json.RawMessage) {
	if s.approvals == nil {
		err := fmt.Errorf("%w: runtime approval request cannot be handled", errApprovalManagerRequired)
		if s.logger != nil {
			s.logger.Error("codexapp: approval request rejected because manager is unavailable", "method", method, "error", err)
		}
		s.failTurns(err)
		if s.cancel != nil {
			s.cancel()
		}
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

// requestToolApprovalWithContext 将 provider approval 通知转成宿主审批请求。
// payload 身份字段异常时必须 fail-fast；能定位 requestId 时回写拒绝决策，否则终止当前 turn。
func (s *session) requestToolApprovalWithContext(ctx context.Context, method string, params json.RawMessage) error {
	payload := decodeEventPayload(params)
	requestID, hasRequestID := strictApprovalRequestID(payload, "requestId", "request_id")
	if hasRequestID {
		s.logApprovalRequestReceived(requestID, method)
	}
	if err := validateApprovalPayload(payload); err != nil {
		if hasRequestID {
			s.logApprovalRequestFailed(requestID, method, "parse", err)
			return s.sendApprovalDecision(requestID, approvalParseFailedDecision(err))
		}
		s.failTurns(err)
		return err
	}
	req, requestID, ok := s.buildApprovalRequest(method, payload)
	if !ok {
		err := errors.New("codexapp: approval_parse_failed: approval request identity is required")
		s.logApprovalRequestFailed(requestID, method, "parse", err)
		s.failTurns(err)
		return err
	}
	key := processedApprovalRequestKey(req, requestID)
	fingerprint := approvalRequestFingerprint(req, requestID)
	entry, owner, err := s.beginProcessedApproval(key, fingerprint)
	if err != nil {
		return err
	}
	if !owner {
		decision, err := s.waitProcessedApproval(ctx, entry)
		if err != nil {
			return err
		}
		return s.sendApprovalDecision(requestID, decision)
	}
	decision, err := s.requestApprovalDecision(req)
	if err != nil {
		s.logApprovalRequestFailed(requestID, method, "decision", err)
	}
	decision, err = normalizeApprovalDecisionError(decision, err)
	if err != nil {
		s.finishProcessedApproval(key, entry, decision, err)
		return err
	}
	s.finishProcessedApproval(key, entry, decision, nil)
	return s.sendApprovalDecision(requestID, decision)
}

// normalizeApprovalDecisionError 将可恢复的审批失败归一化为拒绝决策。
// 调用方取消必须保留原始错误，避免把已终止请求误记为可复用的完成结果。
func normalizeApprovalDecisionError(decision contract.ApprovalDecision, err error) (contract.ApprovalDecision, error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return decision, err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return approvalDeadlineExceededDecision(err), nil
	}
	return approvalDecisionFailedDecision(err), nil
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

func validateApprovalPayload(payload map[string]any) error {
	if len(payload) == 0 {
		return errors.New("codexapp: approval_parse_failed: payload is required")
	}
	if err := validateApprovalStringFields(payload, "callId", "call_id", "approvalId", "approval_id", "toolName", "tool_name", "tool", "name", "threadId", "thread_id", "turnId", "turn_id", "reason", "message"); err != nil {
		return err
	}
	if err := validateApprovalObjectField(payload, "item"); err != nil {
		return err
	}
	return validateApprovalObjectField(payload, "turn")
}

func validateApprovalObjectField(payload map[string]any, key string) error {
	value, ok := payload[key]
	if !ok || value == nil {
		return nil
	}
	child, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("codexapp: approval_parse_failed: field %q must be object", key)
	}
	return validateApprovalStringFields(child, "callId", "call_id", "toolName", "tool_name", "tool", "name", "threadId", "thread_id", "turnId", "turn_id", "id")
}

func validateApprovalStringFields(payload map[string]any, keys ...string) error {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		switch value.(type) {
		case string, json.Number:
		default:
			return fmt.Errorf("codexapp: approval_parse_failed: field %q must be string", key)
		}
	}
	return nil
}

func approvalParseFailedDecision(err error) contract.ApprovalDecision {
	approved := false
	return contract.ApprovalDecision{
		Approved: &approved,
		Reason:   err.Error(),
	}
}

func approvalDeadlineExceededDecision(err error) contract.ApprovalDecision {
	approved := false
	return contract.ApprovalDecision{
		Approved: &approved,
		Reason:   fmt.Sprintf("approval deadline exceeded: %v", err),
	}
}

func approvalDecisionFailedDecision(err error) contract.ApprovalDecision {
	approved := false
	return contract.ApprovalDecision{
		Approved: &approved,
		Reason:   fmt.Sprintf("approval decision failed: %v", err),
	}
}

func (s *session) logApprovalRequestReceived(requestID int64, method string) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Info("codexapp: approval request received",
		"request_id", requestID,
		"method", strings.TrimSpace(method),
	)
}

func (s *session) logApprovalRequestFailed(requestID int64, method, stage string, err error) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Warn("codexapp: approval request failed",
		"request_id", requestID,
		"method", strings.TrimSpace(method),
		"stage", stage,
		"error", err,
	)
}

func (s *session) logApprovalRequestResponded(requestID int64, decision contract.ApprovalDecision) {
	if s == nil || s.logger == nil {
		return
	}
	outcome := "unspecified"
	if decision.Approved != nil {
		if *decision.Approved {
			outcome = "approved"
		} else {
			outcome = "declined"
		}
	}
	s.logger.Info("codexapp: approval request responded",
		"request_id", requestID,
		"outcome", outcome,
	)
}

func approvalDecisionContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return rpc.WithApprovalDeadline(rpc.WithApprovalAutoDeclineOnCancel(ctx))
}

func (s *session) setApprovalPolicy(policy string) {
	if s == nil {
		return
	}
	s.approvalPolicy.Store(strings.TrimSpace(policy))
	s.approvalPolicyVerified.Store(true)
}

func (s *session) approvalPolicyValue() string {
	if s == nil {
		return ""
	}
	value, _ := s.approvalPolicy.Load().(string)
	return strings.TrimSpace(value)
}

// beginProcessedApproval 为完整审批身份建立去重槽，并拒绝同身份不同 payload。
// 返回 owner=false 表示已有 goroutine 正在处理同一请求，当前调用方只需要等待结果。
func (s *session) beginProcessedApproval(key, fingerprint string) (*processedApprovalEntry, bool, error) {
	if s == nil {
		return nil, false, errors.New("codexapp: approval session is nil")
	}
	if key == "" || fingerprint == "" {
		return nil, false, errors.New("codexapp: approval identity fingerprint is required")
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	if s.processedApprovals == nil {
		s.processedApprovals = map[string]*processedApprovalEntry{}
	}
	if entry := s.processedApprovals[key]; entry != nil {
		if entry.fingerprint != fingerprint {
			return nil, false, errors.New("codexapp: approval payload conflicts with an existing identity")
		}
		return entry, false, nil
	}
	if len(s.processedApprovals) >= processedApprovalLimit {
		s.purgeCompletedProcessedApprovalsLocked()
	}
	entry := &processedApprovalEntry{fingerprint: fingerprint, ready: make(chan struct{})}
	s.processedApprovals[key] = entry
	return entry, true, nil
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

// buildApprovalRequest 从已验证 payload 构造带 sessionScope、callId、requestId 的权威审批身份。
// 任一身份或已验证审批策略缺失时必须 fail closed，禁止退回仅 requestId 的兼容路径。
func (s *session) buildApprovalRequest(method string, payload map[string]any) (rpc.ApprovalRequest, int64, bool) {
	if s == nil || !s.approvalPolicyVerified.Load() {
		return rpc.ApprovalRequest{}, 0, false
	}
	if len(payload) == 0 {
		return rpc.ApprovalRequest{}, 0, false
	}
	requestID, ok := strictApprovalRequestID(payload, "requestId", "request_id")
	if !ok {
		return rpc.ApprovalRequest{}, 0, false
	}
	callID, hasCallID := strictApprovalCallID(payload)
	sessionScope := strings.TrimSpace(s.approvalSessionScope)
	if !hasCallID || sessionScope == "" {
		return rpc.ApprovalRequest{}, 0, false
	}
	requestRef := requestID
	return rpc.ApprovalRequest{
		SessionScope:   sessionScope,
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
	}, requestID, true
}

// strictApprovalRequestID 只接受 approval 协议中的正整数 requestId。
// 审批决策会回写到 provider，不能把字符串或浮点数截断成另一个合法请求。
func strictApprovalRequestID(payload map[string]any, keys ...string) (int64, bool) {
	var requestID int64
	present := false
	for _, key := range keys {
		value, exists := payload[key]
		if !exists {
			continue
		}
		candidate, ok := strictApprovalRequestIDValue(value)
		if !ok || present && candidate != requestID {
			return 0, false
		}
		requestID = candidate
		present = true
	}
	return requestID, present
}

// strictApprovalCallID 要求顶层及 item 中所有兼容 call ID 字段非空且完全一致。
func strictApprovalCallID(payload map[string]any) (string, bool) {
	sources := []map[string]any{payload}
	if rawItem, exists := payload["item"]; exists && rawItem != nil {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return "", false
		}
		sources = append(sources, item)
	}
	var callID string
	present := false
	for _, source := range sources {
		for _, key := range []string{"callId", "call_id"} {
			value, exists := source[key]
			if !exists {
				continue
			}
			candidate, ok := strictApprovalCallIDValue(value)
			if !ok || present && candidate != callID {
				return "", false
			}
			callID = candidate
			present = true
		}
	}
	return callID, present
}

func strictApprovalCallIDValue(value any) (string, bool) {
	typed, ok := value.(string)
	if !ok {
		return "", false
	}
	normalized := strings.TrimSpace(typed)
	return normalized, normalized != ""
}

func strictApprovalRequestIDValue(value any) (int64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		return positiveApprovalRequestID(parsed, err == nil)
	case int64:
		return positiveApprovalRequestID(typed, true)
	case int:
		return positiveApprovalRequestID(int64(typed), true)
	default:
		return 0, false
	}
}

func positiveApprovalRequestID(value int64, ok bool) (int64, bool) {
	if !ok || value <= 0 {
		return 0, false
	}
	return value, true
}

// sendApprovalDecision 把宿主 approval 决策回写给 Codex app-server。
// requestID 和 transport 都必须存在，否则 fail-fast 防止 provider 侧请求悬空。
func (s *session) sendApprovalDecision(requestID int64, decision contract.ApprovalDecision) error {
	if requestID <= 0 {
		return errors.New("codexapp: approval request id is required")
	}
	if s == nil || s.transport == nil {
		err := errors.New("codexapp: approval transport is not initialized")
		if s != nil {
			s.logApprovalRequestFailed(requestID, "approval/respond", "transport", err)
		}
		return err
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
	if err != nil {
		s.logApprovalRequestFailed(requestID, "approval/respond", "respond", err)
		return err
	}
	s.logApprovalRequestResponded(requestID, decision)
	return nil
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
	return processedApprovalKey(req.CallID, requestID)
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
	terminalMethod := strings.TrimSpace(method)
	if eventThread, ok := s.alienThreadEventThread(params); ok {
		pkglogger.Warn("codexapp: dropped alien thread event",
			"agent_id", s.agentID,
			"method", method,
			"own_thread", s.ThreadID(),
			"event_thread", eventThread,
		)
		return
	}
	if malformedTerminalNotification(terminalMethod, params) {
		s.failMalformedTerminalNotification(terminalMethod)
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

func malformedTerminalNotification(method string, params json.RawMessage) bool {
	if !isTurnTerminalEvent(method) {
		return false
	}
	if !json.Valid(params) {
		return true
	}
	return payloadTurnID(decodeEventPayload(params)) == ""
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
	case s.completeAssistantMessageCompleted(method, params):
	case method == "connection.dead":
		s.handleConnectionDead(params)
	}
}

// completeAssistantMessageCompleted 将 Codex 的 assistant message 完成事件合成本地 turn 终态。
// 只有当前 active turn 的 assistant message 会触发，工具 item 和历史回放不会关闭正在运行的 turn。
func (s *session) completeAssistantMessageCompleted(method string, params json.RawMessage) bool {
	method = strings.TrimSpace(method)
	if !isAssistantMessageCompletedMethod(method) {
		return false
	}
	payload := decodeEventPayload(params)
	item, ok := assistantMessagePayload(payload)
	if !ok {
		return false
	}
	turnID := shared.FirstNonEmpty(payloadTurnID(payload), payloadTurnID(item))
	s.mu.Lock()
	if turnID == "" {
		turnID = s.activeTurnID
	}
	ok = turnID != "" && turnID == s.activeTurnID && s.turns[turnID] != nil
	s.mu.Unlock()
	if !ok {
		return false
	}
	s.completeSyntheticTurn(turnID, "assistant_message_completed", assistantMessageText(item))
	return true
}

func isAssistantMessageCompletedMethod(method string) bool {
	switch strings.TrimSpace(method) {
	case "response_item",
		"item/completed", "item_completed", "agent/event/item_completed", "rawResponseItem/completed":
		return true
	default:
		return false
	}
}

// assistantMessagePayload 从 root、item 或 payload 三种 Codex wire 形态里提取 assistant message。
// role/type 必须明确指向 assistant message，避免把工具结束事件误当成 turn 终态。
func assistantMessagePayload(payload map[string]any) (map[string]any, bool) {
	for _, candidate := range []map[string]any{
		payload,
		nestedValue(payload, "item"),
		nestedValue(payload, "payload"),
	} {
		if isAssistantMessageItem(candidate) {
			return candidate, true
		}
	}
	return nil, false
}

func isAssistantMessageItem(item map[string]any) bool {
	if len(item) == 0 {
		return false
	}
	role := strings.ToLower(strings.TrimSpace(stringValue(item, "role")))
	itemType := strings.ToLower(strings.TrimSpace(stringValue(item, "type", "kind")))
	switch itemType {
	case "message":
		return role == "assistant"
	case "assistant", "assistant_message", "agent_message", "agentmessage":
		return role == "" || role == "assistant"
	default:
		return false
	}
}

// assistantMessageText 提取 assistant message 的文本结果。
// 当前 Codex 会优先通过 delta 流传正文；这里仅消费 completed item 自带的显式文本字段。
func assistantMessageText(item map[string]any) string {
	if text := stringValue(item, "text", "message", "result"); text != "" {
		return text
	}
	return assistantMessageContentText(item["content"])
}

// assistantMessageContentText 解析 Codex message content 数组中的文本块。
// 只接受明确文本块类型，避免把图片、工具或结构化附件误拼进最终回答。
func assistantMessageContentText(raw any) string {
	switch typed := raw.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		var b strings.Builder
		for _, entry := range typed {
			obj, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			kind := strings.ToLower(strings.TrimSpace(stringValue(obj, "type")))
			if kind != "" && kind != "text" && kind != "output_text" {
				continue
			}
			b.WriteString(stringValue(obj, "text"))
		}
		return strings.TrimSpace(b.String())
	case map[string]any:
		return stringValue(typed, "text")
	default:
		return ""
	}
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
