package rpc

import (
	"context"
	"encoding/json"
	"strings"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	"github.com/creachadair/jrpc2"
	"github.com/kelindar/event"

	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/eventsurface"
)

// PushBridge bridges internal events into jrpc2 server push APIs.
type PushBridge struct {
	dispatcher *event.Dispatcher
	logger     *pkglogger.Logger
}

// NewPushBridge 创建push桥接。
func NewPushBridge(dispatcher *event.Dispatcher, logger *pkglogger.Logger) *PushBridge {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &PushBridge{dispatcher: dispatcher, logger: logger}
}

// NotifyClient 处理notify客户端。
func (b *PushBridge) NotifyClient(ctx context.Context, server *jrpc2.Server, method string, params any) error {
	if server == nil {
		return ErrInvalidState("rpc push server is nil")
	}
	return server.Notify(ctx, method, params)
}

// CallbackClient 处理callback客户端。
func (b *PushBridge) CallbackClient(ctx context.Context, server *jrpc2.Server, method string, params any) (json.RawMessage, error) {
	if server == nil {
		return nil, ErrInvalidState("rpc push server is nil")
	}
	resp, err := server.Callback(ctx, method, params)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, ErrInvalidState("rpc callback response is nil")
	}
	var raw json.RawMessage
	if err := resp.UnmarshalResult(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// subscribeCoreEventPushes is the P22 P2 boundary for
// `internal/platform/rpc/push.go`: the bus callback body contains no
// NotifyAll call and no `context.Background()` — only a worker Enqueue.
// Legacy expansion (`eventsurface.ExpandNotifications`) stays on the
// callback side because it's a deterministic, O(1), ctx-free pure
// function; moving it to the worker wouldn't change behavior but would
// make `TestExpandNotificationsAddsLegacyThreadRefresh` harder to keep
// independent of worker timing.
func subscribeCoreEventPushes(worker *pushNotificationWorker, dispatcher *event.Dispatcher, logger *pkglogger.Logger) []context.CancelFunc {
	if worker == nil || dispatcher == nil {
		return nil
	}
	cancels := eventsurface.Bind(dispatcher, logger, func(method string, payload any) {
		worker.Enqueue(eventsurface.ExpandNotifications(method, payload))
	})
	cancels = append(cancels, subscribeRawProviderEventPushes(worker, dispatcher, logger))
	return cancels
}

var typedPushMethods = map[string]struct{}{
	strings.ToLower(eventsurface.MethodUIStateChanged):       {},
	strings.ToLower(eventsurface.MethodTurnStarted):          {},
	strings.ToLower(eventsurface.MethodTurnCompleted):        {},
	strings.ToLower(eventsurface.MethodAgentMessageDelta):    {},
	strings.ToLower(eventsurface.MethodReasoningTextDelta):   {},
	strings.ToLower(eventsurface.MethodCommandOutputDelta):   {},
	strings.ToLower(eventsurface.MethodToolCall):             {},
	strings.ToLower(eventsurface.MethodThreadStarted):        {},
	strings.ToLower(eventsurface.MethodThreadStopped):        {},
	strings.ToLower(eventsurface.MethodThreadMessages):       {},
	strings.ToLower(eventsurface.MethodThreadCompacted):      {},
	strings.ToLower(eventsurface.MethodSkillsChanged):        {},
	strings.ToLower(eventsurface.MethodUIThreadPatch):        {},
	strings.ToLower(eventsurface.MethodUISharedFilesChanged): {},
	strings.ToLower(eventsurface.MethodUIMemoryChanged):      {},
	strings.ToLower(eventsurface.MethodUIPromptsChanged):     {},
	strings.ToLower(eventsurface.MethodAgentLaunched):        {},
	strings.ToLower(eventsurface.MethodAgentStopped):         {},
}

func subscribeRawProviderEventPushes(worker *pushNotificationWorker, dispatcher *event.Dispatcher, logger *pkglogger.Logger) context.CancelFunc {
	if worker == nil || dispatcher == nil {
		return func() {}
	}
	return platformbus.ResilientSubscribe(dispatcher, func(raw providerdto.BusRawProviderEvent) {
		worker.Enqueue(providerPushNotifications(raw.Event))
	}, logger)
}

func providerPushNotifications(raw providerdto.RawProviderEvent) []eventsurface.Notification {
	method := normalizeRawProviderPushMethod(raw.EventType)
	if shouldSuppressTypedRawProviderPush(method, raw.Data) {
		return nil
	}
	if !shouldPushRawProviderMethod(method) {
		return nil
	}
	return eventsurface.ExpandNotifications(method, raw.Data)
}

func shouldSuppressTypedRawProviderPush(method string, data any) bool {
	key := normalizedRawProviderMethodKey(method)
	if _, ok := typedPushMethods[key]; ok {
		return true
	}
	if _, ok := typedRawProviderAliasMethods[key]; ok {
		return true
	}
	return isTypedToolLifecycleRawProviderEvent(method, data)
}

var typedRawProviderAliasMethods = map[string]struct{}{
	normalizedRawProviderMethodKey("item/reasoning/summaryTextDelta"): {},
	normalizedRawProviderMethodKey("message.delta"):                   {},
	normalizedRawProviderMethodKey("agent_message_delta"):             {},
	normalizedRawProviderMethodKey("reasoning.delta"):                 {},
	normalizedRawProviderMethodKey("exec_output_delta"):               {},
}

func isTypedToolLifecycleRawProviderEvent(method string, data any) bool {
	payload, ok := clonePayloadMap(data)
	if !ok {
		return false
	}
	return isTypedToolStartRawProviderEvent(method, payload) ||
		isTypedToolCompletionRawProviderEvent(method, payload)
}

func isTypedToolStartRawProviderEvent(method string, payload map[string]any) bool {
	if _, ok := toolStartRawProviderMethods[normalizedRawProviderMethodKey(method)]; !ok {
		return false
	}
	item := toolLifecycleItemPayload(payload)
	switch payloadString(item, "type") {
	case "function_call", "tool_call":
		return toolLifecycleCallID(payload, item) != "" && toolLifecycleToolName(payload, item) != ""
	default:
		return false
	}
}

func isTypedToolCompletionRawProviderEvent(method string, payload map[string]any) bool {
	if _, ok := toolCompletionRawProviderMethods[normalizedRawProviderMethodKey(method)]; !ok {
		return false
	}
	item := toolLifecycleItemPayload(payload)
	switch payloadString(item, "type") {
	case "mcp_tool_call_end", "tool_result", "function_call_output":
		return toolLifecycleCallID(payload, item) != "" && toolLifecycleToolName(payload, item) != ""
	default:
		return toolLifecycleCallID(payload, item) != "" && toolLifecycleToolName(payload, item) != ""
	}
}

var toolStartRawProviderMethods = map[string]struct{}{
	normalizedRawProviderMethodKey("item/started"):             {},
	normalizedRawProviderMethodKey("item_started"):             {},
	normalizedRawProviderMethodKey("agent/event/item_started"): {},
	normalizedRawProviderMethodKey("response_item"):            {},
}

var toolCompletionRawProviderMethods = map[string]struct{}{
	normalizedRawProviderMethodKey("item/completed"):             {},
	normalizedRawProviderMethodKey("item_completed"):             {},
	normalizedRawProviderMethodKey("agent/event/item_completed"): {},
	normalizedRawProviderMethodKey("rawResponseItem/completed"):  {},
	normalizedRawProviderMethodKey("event_msg"):                  {},
	normalizedRawProviderMethodKey("tool.call.end"):              {},
	normalizedRawProviderMethodKey("response_item"):              {},
}

func normalizedRawProviderMethodKey(method string) string {
	return strings.ToLower(strings.TrimSpace(method))
}

func toolLifecycleItemPayload(payload map[string]any) map[string]any {
	if item := nestedPayloadMap(payload, "item"); len(item) > 0 {
		return item
	}
	if nested := nestedPayloadMap(payload, "payload"); len(nested) > 0 {
		return nested
	}
	return payload
}

func toolLifecycleCallID(payload, item map[string]any) string {
	if callID := firstPayloadString(payload, "callId", "call_id"); callID != "" {
		return callID
	}
	return firstPayloadString(item, "callId", "call_id")
}

func toolLifecycleToolName(payload, item map[string]any) string {
	if toolName := firstPayloadString(payload, "name", "toolName", "tool_name", "tool"); toolName != "" {
		return toolName
	}
	if toolName := firstPayloadString(item, "name", "toolName", "tool_name", "tool"); toolName != "" {
		return toolName
	}
	invocation := nestedPayloadMap(item, "invocation")
	return firstPayloadString(invocation, "tool", "name", "toolName", "tool_name")
}

func nestedPayloadMap(payload map[string]any, key string) map[string]any {
	if payload == nil {
		return nil
	}
	child, _ := payload[key].(map[string]any)
	return child
}

func firstPayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if text := payloadString(payload, key); text != "" {
			return text
		}
	}
	return ""
}

func normalizeRawProviderPushMethod(method string) string {
	return approvalMethodCatalog.normalize(method)
}

// shouldPushRawProviderMethod 判断push原始providermethod是否可用。
func shouldPushRawProviderMethod(method string) bool {
	method = strings.TrimSpace(method)
	if method == "" {
		return false
	}
	if _, ok := typedPushMethods[strings.ToLower(method)]; ok {
		return false
	}
	if approvalMethodCatalog.isPushMethod(method) {
		return true
	}
	switch {
	case strings.HasPrefix(method, "item/"),
		strings.HasPrefix(method, "turn/plan/"),
		strings.HasPrefix(method, "turn/diff/"),
		strings.HasPrefix(method, "agent/event/"),
		strings.HasPrefix(method, "account/"),
		strings.HasPrefix(method, "app/list/"),
		strings.HasPrefix(method, "fuzzyFileSearch/"),
		strings.HasSuffix(method, "/requestApproval"):
		return true
	}
	switch method {
	case "error",
		"configWarning",
		"deprecationNotice",
		"thread/name/updated",
		"thread/tokenUsage/updated",
		"thread/tokenusage/updated":
		return true
	default:
		return false
	}
}
