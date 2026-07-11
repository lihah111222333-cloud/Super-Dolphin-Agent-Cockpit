package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

type callTarget interface {
	Call(context.Context, string, any) (json.RawMessage, error)
}

type callTargetFunc func(context.Context, string, any) (json.RawMessage, error)

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

func decodeJSONMap(raw []byte) map[string]any {
	var payload map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &payload) != nil || len(payload) == 0 {
		return nil
	}
	return payload
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

// turnTerminalSuccess 从 terminal method 和 payload 中判断本轮是否成功结束。
// method 中的 aborted/failed/error 优先于 payload success，缺省状态按成功兼容旧 Codex 事件。
func turnTerminalSuccess(method string, payload map[string]any) bool {
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
