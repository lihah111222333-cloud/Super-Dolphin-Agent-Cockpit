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

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/gorilla/websocket"
)

type callTarget interface {
	Call(context.Context, string, any) (json.RawMessage, error)
}

type callTargetFunc func(context.Context, string, any) (json.RawMessage, error)

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
		stringValue(payload, "toolName", "tool_name", "tool"),
		stringValue(item, "toolName", "tool"),
	}, fallbacks...)
	return shared.FirstNonEmpty(values...)
}

func isTurnTerminalEvent(method string) bool {
	switch strings.TrimSpace(method) {
	case "turn/completed", "turn.completed", "turn/aborted", "turn.aborted":
		return true
	default:
		return false
	}
}

func turnTerminalSuccess(method string, payload map[string]any) bool {
	if strings.Contains(strings.ToLower(strings.TrimSpace(method)), "aborted") {
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

func (t *transport) shutdownTransport(graceful bool) error {
	if t == nil {
		return nil
	}
	if graceful && t.closed.Load() {
		return nil
	}
	// Only send shutdown notification and stop process when this transport
	// owns the process (local spawn). Non-local transports connect to a
	// shared app-server and must NOT kill it.
	if graceful && t.local {
		_ = t.Notify("shutdown", nil)
	}
	t.closed.Store(true)
	err := t.stopProcess(graceful)
	t.closeSocket()
	return err
}

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
	s.shutdownSessionCleanup() // unregister from idle tracking
	s.cancel()
	return s.transport.shutdownTransport(graceful)
}

func (s *session) failRecovery(reason string, err error) error {
	if s == nil {
		return err
	}
	pkglogger.Warn("codexapp: RECOVERY FAILED (passive death)",
		"agent_id", s.agentID,
		"thread_id", s.ThreadID(),
		"reason", reason,
		"error", err,
	)
	s.failTurns(errors.New("codexapp: " + strings.TrimSpace(reason)))
	// Notify upstream that the session is dead but recoverable — the thread
	// module can do a full session-level recovery (new WS connection + UUID
	// resume) instead of retrying the same broken transport.
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

// codexCallerStack returns a compact caller stack for debugging.
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
		Stream:     strings.TrimSpace(stream),
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
