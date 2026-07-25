package codexapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	contract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

type callTarget interface {
	Call(context.Context, string, any) (json.RawMessage, error)
}

type callTargetFunc func(context.Context, string, any) (json.RawMessage, error)

type approvalDecisionSender func(requestID int64, decision contract.ApprovalDecision) error

// Call 将函数适配为 callTarget。
// 测试和轻量调用点可注入闭包，而生产 transport 仍实现同一接口。
func (fn callTargetFunc) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return fn(ctx, method, params)
}

var requestUserInputMethods = map[string]struct{}{
	"request_user_input":                       {},
	"codex/event/request_user_input":           {},
	"item/commandExecution/requestUserInput":   {},
	"item/commandExecution/request_user_input": {},
	"item/tool/requestUserInput":               {},
	"item/tool/request_user_input":             {},
}

var approvalBridgeMethods = map[string]struct{}{
	rpc.DefaultApprovalCallbackMethod:          {},
	"tool/approval/request":                    {},
	"item/commandExecution/requestApproval":    {},
	"item/fileChange/requestApproval":          {},
	"skill/requestApproval":                    {},
	"tool.approval.requested":                  {},
	"request_user_input":                       {},
	"codex/event/request_user_input":           {},
	"item/commandExecution/requestUserInput":   {},
	"item/commandExecution/request_user_input": {},
	"item/tool/requestUserInput":               {},
	"item/tool/request_user_input":             {},
	"mcpServer/elicitation/request":            {},
}

func callWithTimeout(ctx context.Context, t callTarget, d time.Duration, method string, params any) (json.RawMessage, error) {
	callCtx, cancel := withTimeout(ctx, d)
	defer cancel()
	return t.Call(callCtx, method, params)
}

// prepareApprovalRequest 校验审批载荷并构造宿主审批请求；可定位的坏请求会立即回写拒绝。
func (s *session) prepareApprovalRequest(
	method string,
	payload map[string]any,
	send approvalDecisionSender,
) (rpc.ApprovalRequest, int64, bool, error) {
	requestID, hasRequestID := strictApprovalRequestID(payload, "requestId", "request_id")
	if hasRequestID {
		s.logApprovalRequestReceived(requestID, method)
	}
	if err := validateApprovalPayload(payload); err != nil {
		if hasRequestID {
			s.logApprovalRequestFailed(requestID, method, "parse", err)
			return rpc.ApprovalRequest{}, requestID, false, send(requestID, approvalParseFailedDecision(err))
		}
		s.failTurns(err)
		return rpc.ApprovalRequest{}, 0, false, err
	}
	req, requestID, ok := s.buildApprovalRequest(method, payload)
	if !ok {
		err := errors.New("codexapp: approval_parse_failed: approval request identity is required")
		s.logApprovalRequestFailed(requestID, method, "parse", err)
		s.failTurns(err)
		return rpc.ApprovalRequest{}, requestID, false, err
	}
	return req, requestID, true, nil
}

// handleInboundApprovalRequest 处理当前 app-server 的 JSON-RPC 审批请求。
// message.id 是 provider 的响应身份；params 不再携带旧 requestId，因此先生成宿主侧正整数代理 ID。
func (s *session) handleInboundApprovalRequest(resp Responder, msg RawMessage) {
	method := strings.TrimSpace(msg.Method)
	pkglogger.Info("codexapp: approval server request received",
		"agent_id", s.agentID, "method", method)
	params, err := approvalParamsWithJSONRPCID(msg.ID, msg.Params)
	if err != nil {
		pkglogger.Warn("codexapp: approval server request identity failed",
			"agent_id", s.agentID, "method", method, "error", err)
		s.failTurns(err)
		if respErr := resp.RespondWithID(msg.ID, nil, err); respErr != nil && s.logger != nil {
			s.logger.Warn("codexapp: invalid approval request respond failed",
				"agent_id", s.agentID, "method", msg.Method, "error", respErr)
		}
		return
	}
	runtimesafe.SafeGo(s.ctx, s.logger, "codexapp.session.jsonrpcApprovalRequest", func(runCtx context.Context) {
		responded := false
		send := func(_ int64, decision contract.ApprovalDecision) error {
			result, resultErr := approvalJSONRPCResponse(method, decision)
			if resultErr != nil {
				return resultErr
			}
			responded = true
			return resp.RespondWithID(msg.ID, result, nil)
		}
		requestErr := s.requestToolApprovalWithSender(runCtx, method, params, send, true)
		if requestErr == nil || responded {
			return
		}
		if respErr := resp.RespondWithID(msg.ID, nil, requestErr); respErr != nil && s.logger != nil {
			s.logger.Warn("codexapp: approval request error respond failed",
				"agent_id", s.agentID, "method", method, "error", respErr)
		}
	})
}

// approvalParamsWithJSONRPCID 将 JSON-RPC 响应身份映射为宿主审批使用的正整数 requestId。
func approvalParamsWithJSONRPCID(id json.RawMessage, params json.RawMessage) (json.RawMessage, error) {
	requestID, err := approvalRequestIDFromJSONRPC(id)
	if err != nil {
		return nil, err
	}
	payload := decodeEventPayload(params)
	if len(payload) == 0 {
		return nil, errors.New("codexapp: approval_parse_failed: payload is required")
	}
	if existing, ok := strictApprovalRequestID(payload, "requestId", "request_id"); ok && existing != requestID {
		return nil, errors.New("codexapp: approval_parse_failed: JSON-RPC id conflicts with requestId")
	}
	payload["requestId"] = requestID
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("codexapp: encode approval request params: %w", err)
	}
	return encoded, nil
}

// approvalRequestIDFromJSONRPC 保留正整数 ID，并把零、负整数或不透明字符串确定性映射为正整数代理身份。
// JSON-RPC 允许数值 ID 为零或负数，而宿主审批身份要求 requestId 大于零；映射仅用于宿主关联，响应仍使用原始 JSON-RPC ID。
func approvalRequestIDFromJSONRPC(id json.RawMessage) (int64, error) {
	var number json.Number
	if err := json.Unmarshal(id, &number); err == nil {
		if parsed, parseErr := number.Int64(); parseErr == nil {
			if parsed > 0 {
				return parsed, nil
			}
			return approvalRequestIDSurrogate("jsonrpc-number:" + number.String())
		}
	}
	var text string
	if err := json.Unmarshal(id, &text); err != nil || strings.TrimSpace(text) == "" {
		return 0, errors.New("codexapp: approval JSON-RPC id must be a non-empty string or integer")
	}
	text = strings.TrimSpace(text)
	if parsed, err := strconv.ParseInt(text, 10, 64); err == nil && parsed > 0 {
		return parsed, nil
	}
	return approvalRequestIDSurrogate("jsonrpc-string:" + text)
}

// approvalRequestIDSurrogate 把不能直接作为宿主审批身份的 JSON-RPC ID 映射到稳定正整数。
func approvalRequestIDSurrogate(identity string) (int64, error) {
	digest := sha256.Sum256([]byte(identity))
	surrogate, err := strconv.ParseInt(hex.EncodeToString(digest[:8])[:15], 16, 64)
	if err != nil || surrogate <= 0 {
		return 0, errors.New("codexapp: approval JSON-RPC id surrogate is invalid")
	}
	return surrogate, nil
}

// approvalJSONRPCResponse 将宿主布尔决策映射为当前 app-server 请求响应契约。
func approvalJSONRPCResponse(method string, decision contract.ApprovalDecision) (map[string]any, error) {
	switch strings.TrimSpace(method) {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
	default:
		return nil, fmt.Errorf("codexapp: unsupported JSON-RPC approval response method %q", method)
	}
	if decision.Approved == nil {
		return nil, errors.New("codexapp: approval decision must explicitly accept or decline")
	}
	value := "decline"
	if *decision.Approved {
		value = "accept"
	}
	return map[string]any{"decision": value}, nil
}

func decodeTurnStartResult(raw json.RawMessage) (*turnStartResult, error) {
	var resp turnStartResult
	if err := json.Unmarshal(raw, &resp); err != nil || strings.TrimSpace(resp.Turn.ID) == "" {
		return nil, errors.New("codexapp: invalid turn/start response")
	}
	return &resp, nil
}

func parsePortFromURL(rawURL string) int {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(strings.TrimSpace(parsed.Port()))
	if err != nil || port <= 0 {
		return 0
	}
	return port
}

func hasMethod(method string, methods map[string]struct{}) bool {
	_, ok := methods[strings.TrimSpace(method)]
	return ok
}

func hasAnyKey(payload map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, ok := payload[strings.TrimSpace(key)]; ok {
			return true
		}
	}
	return false
}

func requireThreadID(s *session, explicit ...string) (string, error) {
	values := make([]string, 0, len(explicit)+1)
	values = append(values, explicit...)
	if s != nil {
		values = append(values, s.ThreadID())
	}
	threadID := shared.FirstNonEmpty(values...)
	if threadID == "" {
		return "", errors.New("codexapp: thread id is required")
	}
	return threadID, nil
}

func payloadAgentID(payload map[string]any) string {
	return stringValue(payload, "agentId", "agent_id")
}

func payloadThreadID(payload map[string]any, fallbacks ...string) string {
	values := append([]string{
		stringValue(payload, "threadId", "thread_id"),
		stringValue(nestedValue(payload, "thread"), "id"),
	}, fallbacks...)
	return shared.FirstNonEmpty(values...)
}

func payloadTurnID(payload map[string]any, fallbacks ...string) string {
	values := append([]string{
		stringValue(payload, "turnId", "turn_id"),
		stringValue(nestedValue(payload, "turn"), "id"),
	}, fallbacks...)
	return shared.FirstNonEmpty(values...)
}

func payloadCallID(payload map[string]any, fallbacks ...string) string {
	item := nestedValue(payload, "item")
	values := append([]string{
		stringValue(payload, "callId", "call_id"),
		stringValue(item, "callId", "call_id"),
	}, fallbacks...)
	return shared.FirstNonEmpty(values...)
}

func payloadToolName(payload map[string]any, fallbacks ...string) string {
	item := nestedValue(payload, "item")
	values := append([]string{
		stringValue(payload, "name", "toolName", "tool_name", "tool"),
		stringValue(item, "name", "toolName", "tool"),
	}, fallbacks...)
	return shared.FirstNonEmpty(values...)
}

// normalizedTurnOutputStream 把 provider 兼容事件里的 stream/kind/type 归一成 UI 认识的输出流，避免 message.delta 携带的 reasoning 进入最终回答。
func normalizedTurnOutputStream(payload map[string]any, fallback string) string {
	item := nestedValue(payload, "item")
	raw := strings.ToLower(strings.TrimSpace(shared.FirstNonEmpty(stringValue(payload, "stream"), stringValue(item, "stream"), stringValue(payload, "kind", "type"), stringValue(item, "kind", "type"))))
	if raw == "" {
		return strings.TrimSpace(fallback)
	}
	exact := "|" + raw + "|"
	switch {
	case strings.Contains("|message|assistant|agentmessage|agent_message|", exact):
		return "message"
	case strings.Contains("|reasoning|thinking|reasoning_summary|summary|", exact) || strings.Contains(raw, "reasoning") || strings.Contains(raw, "thinking"):
		return "reasoning"
	case strings.Contains("|stdout|stderr|command|command_output|exec_output|", exact) || strings.Contains(raw, "command") || strings.Contains(raw, "stdout") || strings.Contains(raw, "stderr"):
		return "stdout"
	default:
		return strings.TrimSpace(fallback)
	}
}

func isTurnTerminalEvent(method string) bool {
	switch strings.TrimSpace(method) {
	case "turn/completed", "turn.completed",
		"turn/interrupted", "turn.interrupted",
		"turn/aborted", "turn.aborted",
		"turn/failed", "turn.failed",
		"turn/error", "turn.error":
		return true
	default:
		return false
	}
}

// isMessageStreamDeltaEvent 判断 raw method 是否是 assistant message 的流式增量。
// reasoning 和命令输出不会写入 TurnCompleted.Result，避免最终回答混入非 message stream。
func isMessageStreamDeltaEvent(method string) bool {
	switch strings.TrimSpace(method) {
	case "item/agentMessage/delta", "message.delta", "agent_message_delta":
		return true
	default:
		return false
	}
}

type turnTerminalOutcome struct {
	success       bool
	status        string
	reason        string
	requestID     string
	contractError string
}

// resolveTurnTerminalOutcome 在 adapter 边界唯一映射 Codex 原始终态，并对缺失或矛盾字段 fail-fast。
func resolveTurnTerminalOutcome(method string, payload map[string]any) turnTerminalOutcome {
	normalizedMethod := strings.ToLower(strings.TrimSpace(method))
	switch {
	case strings.Contains(normalizedMethod, "interrupted"):
		return resolveExplicitTerminationOutcome("interrupted", payload)
	case strings.Contains(normalizedMethod, "aborted"):
		return resolveExplicitTerminationOutcome("cancelled", payload)
	case strings.Contains(normalizedMethod, "failed"), strings.Contains(normalizedMethod, "error"):
		return turnTerminalOutcome{status: "failed", reason: stringValue(payload, "reason")}
	}
	return resolveCompletedTerminalOutcome(payload)
}

func canonicalTurnTerminalOutcome(method string, payload map[string]any) dto.TerminalOutcome {
	outcome := resolveTurnTerminalOutcome(method, payload)
	return dto.TerminalOutcome{
		Success:       outcome.success,
		Status:        outcome.status,
		Cause:         outcome.reason,
		RequestID:     outcome.requestID,
		ContractError: outcome.contractError,
	}
}

func resolveExplicitTerminationOutcome(status string, payload map[string]any) turnTerminalOutcome {
	sanitized, acceptedRequestID, contractError := codexTerminationPayload(payload)
	if contractError != "" {
		return turnTerminalOutcome{status: "failed", contractError: contractError}
	}
	termination := providershared.ResolveRawTermination(sanitized, "provider")
	if termination.ContractError != "" {
		return turnTerminalOutcome{status: "failed", contractError: termination.ContractError}
	}
	if acceptedRequestID != "" && termination.Cause != "system" {
		termination.Cause = "user_request"
		termination.RequestID = acceptedRequestID
	}
	return turnTerminalOutcome{status: status, reason: termination.Cause, requestID: termination.RequestID}
}

// resolveCompletedTerminalOutcome 校验 completed 事件的 success/status 配对，避免失败默认为成功。
func resolveCompletedTerminalOutcome(payload map[string]any) turnTerminalOutcome {
	sanitized, acceptedRequestID, contractError := codexTerminationPayload(payload)
	if contractError != "" {
		return turnTerminalOutcome{status: "failed", contractError: contractError}
	}
	resolved := providershared.ResolveRawTerminalOutcome(sanitized)
	if resolved.ContractError == "" && acceptedRequestID != "" &&
		(resolved.Status == "interrupted" || resolved.Status == "cancelled") && resolved.Cause != "system" {
		resolved.Cause = "user_request"
		resolved.RequestID = acceptedRequestID
	}
	reason := resolved.Cause
	if reason == "" {
		reason = stringValue(payload, "reason")
	}
	return turnTerminalOutcome{
		success:       resolved.Success,
		status:        resolved.Status,
		reason:        reason,
		requestID:     resolved.RequestID,
		contractError: resolved.ContractError,
	}
}

// codexTerminationPayload 移除 provider 无权生产的用户 Stop 归因，仅保留 session 注入的私有 claim。
func codexTerminationPayload(payload map[string]any) (map[string]any, string, string) {
	sanitized := make(map[string]any, len(payload))
	maps.Copy(sanitized, payload)
	acceptedRequestID := ""
	if value, exists := sanitized[acceptedInterruptRequestIDKey]; exists {
		attribution, ok := value.(acceptedInterruptAttribution)
		if !ok || strings.TrimSpace(attribution.requestID) == "" ||
			strings.TrimSpace(attribution.turnID) != strings.TrimSpace(payloadTurnID(payload)) {
			return nil, "", "accepted interrupt request id is missing or non-string"
		}
		acceptedRequestID = strings.TrimSpace(attribution.requestID)
	}
	delete(sanitized, acceptedInterruptRequestIDKey)
	clearCodexProviderUserAttribution(sanitized)
	return sanitized, acceptedRequestID, ""
}

// toolEventRawSuccess 保留工具事件兼容判定；turn terminal 禁止调用。
func toolEventRawSuccess(method string, payload map[string]any) bool {
	normalizedMethod := strings.ToLower(strings.TrimSpace(method))
	if strings.Contains(normalizedMethod, "aborted") ||
		strings.Contains(normalizedMethod, "failed") ||
		strings.Contains(normalizedMethod, "error") {
		return false
	}
	if value, ok := payload["success"].(bool); ok {
		return value
	}
	status := strings.ToLower(stringValue(payload, "status"))
	return status == "" || (status != "failed" && status != "error" && status != "aborted")
}

func (t *transport) ensureOpen() error {
	if t == nil {
		return errors.New("codexapp: transport unavailable")
	}
	if t.closed.Load() {
		return errors.New("codexapp: transport closed")
	}
	if t.closing.Load() {
		return errors.New("codexapp: transport closing")
	}
	return nil
}

func (t *transport) currentWSOrErr() (*websocket.Conn, error) {
	if err := t.ensureOpen(); err != nil {
		return nil, err
	}
	ws := t.currentWS()
	if ws == nil {
		return nil, errors.New("codexapp: websocket not connected")
	}
	return ws, nil
}

// shutdownTransport 关闭 transport 拥有的 websocket 和本地进程。
// 只有 local transport 会发送 shutdown 并停止进程，共享 app-server 连接不能被会话关闭误杀。
func (t *transport) shutdownTransport(graceful bool) error {
	if t == nil {
		return nil
	}
	if graceful && t.closed.Load() {
		return nil
	}
	// 只有本地启动的 transport 才拥有进程；共享 app-server 连接不能在会话关闭时被杀掉。
	if graceful && t.local && !t.closing.Load() {
		_ = t.notifyDirect("shutdown", nil)
	}
	t.closed.Store(true)
	err := t.stopProcess(graceful)
	t.closeSocket()
	return err
}

// shutdownSession 停止会话运行时、释放工具面并关闭 transport。
// 读循环阻塞在 WebSocket 上，必须先关 socket 再等待 runtime drain，否则关闭会卡住。
func (s *session) shutdownSession(graceful bool) error {
	if s == nil {
		return nil
	}
	pkglogger.Warn("codexapp: shutdownSession ENTERED",
		"agent_id", s.agentID,
		"thread_id", s.ThreadID(),
		"graceful", graceful,
		"caller", codexCallerStack(),
	)
	if graceful {
		s.failTurns(errors.New("codexapp: session closed"))
	} else {
		s.failTurns(errors.New("codexapp: session stopped"))
	}
	s.clearProcessedApprovals()
	// transport.ReadLoop 的 ReadMessage 不感知 ctx，先关 socket 才能让 runtime.Stop 汇合 reader。
	// closing=true 会把随后的 EOF 归类为预期关闭，而不是被动连接死亡。
	if s.transport != nil {
		s.transport.closing.Store(true)
		if graceful && s.transport.local {
			_ = s.transport.notifyDirect("shutdown", nil)
		}
		s.transport.closeSocket()
	}
	if s.runtime != nil {
		s.runtime.Stop()
	} else {
		s.cancel()
	}
	cleanupErr := s.shutdownSessionCleanup() // release tool surface and idle tracking
	return errors.Join(cleanupErr, s.transport.shutdownTransport(graceful))
}

// failRecovery 在被动连接死亡无法本地恢复时通知上游做 session 级恢复。
// 如果正在关闭或 runtime 已停，则只记录 suppression，避免把正常关闭误报成 recoverable death。
func (s *session) failRecovery(reason string, err error) error {
	if s == nil {
		return err
	}
	if suppressedErr := s.recoveryShutdownErr(); suppressedErr != nil || errors.Is(err, errRuntimeStopped) {
		pkglogger.Warn("codexapp: recovery suppressed during shutdown",
			"agent_id", s.agentID,
			"thread_id", s.ThreadID(),
			"reason", reason,
			"error", err,
			"shutdown_error", suppressedErr,
		)
		return err
	}
	pkglogger.Warn("codexapp: RECOVERY FAILED (passive death)",
		"agent_id", s.agentID,
		"thread_id", s.ThreadID(),
		"reason", reason,
		"error", err,
	)
	s.failTurns(errors.New("codexapp: " + strings.TrimSpace(reason)))
	// 通知上游执行完整 session 级恢复，而不是继续复用已经坏掉的 transport。
	s.dispatch(dto.RawProviderEvent{
		EventType: "connection.dead",
		Data: map[string]any{
			"agentId":     strings.TrimSpace(s.agentID),
			"threadId":    s.ThreadID(),
			"timestamp":   time.Now().UTC().Format(time.RFC3339Nano),
			"error":       strings.TrimSpace(reason),
			"recoverable": true,
		},
	})
	return err
}

// recoveryShutdownErr 判断当前错误是否发生在预期关闭路径中。
// transport closing、runtime stopped 或 ctx canceled 都会抑制被动恢复告警。
func (s *session) recoveryShutdownErr() error {
	if s == nil {
		return nil
	}
	if s.transport != nil && s.transport.closing.Load() {
		return errSessionClosing
	}
	if s.runtime != nil && s.runtime.Stopped() {
		return errRuntimeStopped
	}
	if s.ctx != nil {
		if err := shared.CheckCtx(s.ctx); err != nil {
			return err
		}
	}
	return nil
}

// codexCallerStack 返回简短调用栈用于 shutdown 调试日志。
// 只保留少量 frame，避免高频关闭路径写出过大的日志字段。
func codexCallerStack() string {
	var pcs [8]uintptr
	n := runtime.Callers(3, pcs[:])
	if n == 0 {
		return "<unknown>"
	}
	frames := runtime.CallersFrames(pcs[:n])
	var parts []string
	for {
		frame, more := frames.Next()
		short := frame.Function
		if idx := strings.LastIndex(short, "/"); idx >= 0 {
			short = short[idx+1:]
		}
		parts = append(parts, short)
		if !more || len(parts) >= 6 {
			break
		}
	}
	return strings.Join(parts, " <- ")
}

func cleanupFailedSession(s *session, msg string) {
	if s == nil {
		return
	}
	shared.LogIgnoredError(s.logger, msg, s.ForceStop())
}

func decodeThreadRPCResult(raw json.RawMessage) (*threadRPCResult, error) {
	var resp threadRPCResult
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("codexapp: decode thread rpc result: %w", err)
	}
	return &resp, nil
}

func turnOutputDelta(payload map[string]any, stream string) turndto.TurnOutputDelta {
	return turndto.TurnOutputDelta{
		TurnHeader: buildTurnHeader(payload),
		Stream:     normalizedTurnOutputStream(payload, stream),
		Delta:      stringValue(payload, "delta", "content"),
	}
}

func newTextTurnInput(kind, text string) turnInputItem {
	content := strings.TrimSpace(text)
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "text"
	}
	return turnInputItem{Type: kind, Text: content, Content: content}
}
