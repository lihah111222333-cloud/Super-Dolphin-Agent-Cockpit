package rpc

import (
	"context"
	"encoding/json"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
	"regexp"
	"strings"
	"sync"

	"github.com/creachadair/jrpc2"
	"github.com/kelindar/event"

	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	platformbus "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/eventsurface"
)

// retryProgressPushPattern 匹配 provider 内部重连重试进度文本。
var retryProgressPushPattern = regexp.MustCompile(`(?i)^\s*(reconnecting|retrying)(\.\.\.)?\s+\d+\s*/\s*\d+\s*$`)

// PushBridge 把内部事件转换为 jrpc2 server 的 notify/callback 调用。
type PushBridge struct {
	dispatcher *event.Dispatcher
	logger     *pkglogger.Logger
}

// NewPushBridge 创建 RPC push bridge，并补齐默认 logger。
func NewPushBridge(dispatcher *event.Dispatcher, logger *pkglogger.Logger) *PushBridge {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &PushBridge{dispatcher: dispatcher, logger: logger}
}

// NotifyClient 向单个 RPC 客户端发送通知，server 缺失时 fail-fast。
func (b *PushBridge) NotifyClient(ctx context.Context, server *jrpc2.Server, method string, params any) error {
	if server == nil {
		return ErrInvalidState("rpc push server is nil")
	}
	return server.Notify(ctx, method, params)
}

// CallbackClient 向客户端发起 callback 并返回原始 JSON 结果。
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

// subscribeCoreEventPushes 订阅核心事件并只把展开后的通知入队。
// bus callback 不直接调用 NotifyAll，也不创建 context.Background，慢路径由 worker 托管。
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

// typedPushMethods 是已经有强类型事件面负责推送的方法集合。
var typedPushMethods = sync.OnceValue(newTypedPushMethods)

func newTypedPushMethods() map[string]struct{} {
	out := make(map[string]struct{}, len(eventsurface.AllTypedWireMethods()))
	for _, method := range eventsurface.AllTypedWireMethods() {
		out[strings.ToLower(method)] = struct{}{}
	}
	return out
}

// subscribeRawProviderEventPushes 订阅 provider raw 事件，并过滤掉已有强类型覆盖的事件。
func subscribeRawProviderEventPushes(worker *pushNotificationWorker, dispatcher *event.Dispatcher, logger *pkglogger.Logger) context.CancelFunc {
	if worker == nil || dispatcher == nil {
		return func() {}
	}
	return platformbus.ResilientSubscribe(dispatcher, func(raw providerdto.BusRawProviderEvent) {
		worker.Enqueue(providerPushNotifications(raw.Event))
	}, logger)
}

// providerPushNotifications 把 raw provider event 转成前端通知列表。
func providerPushNotifications(raw providerdto.RawProviderEvent) []eventsurface.Notification {
	method := normalizeRawProviderPushMethod(raw.EventType)
	if shouldSuppressRetryProgressRawProviderPush(method, raw.Data) {
		return nil
	}
	if shouldSuppressTypedRawProviderPush(method, raw.Data) {
		return nil
	}
	if !shouldPushRawProviderMethod(method) {
		return nil
	}
	return eventsurface.ExpandNotifications(method, raw.Data)
}

// shouldSuppressRetryProgressRawProviderPush 拦截 provider 内部重试进度，
// 避免 Reconnecting... n/m 通过 raw push 触发前端错误展示或刷新。
func shouldSuppressRetryProgressRawProviderPush(method string, data any) bool {
	if normalizedRawProviderMethodKey(method) != "error" {
		return false
	}
	payload, ok := clonePayloadMap(data)
	if !ok || !payloadBool(payload, "willRetry", "will_retry") {
		return false
	}
	message := firstPayloadString(payload, "message", "error", "reason")
	if message == "" {
		message = payloadString(nestedPayloadMap(payload, "error"), "message")
	}
	return retryProgressPushPattern.MatchString(message)
}

// shouldSuppressTypedRawProviderPush 判断 raw 事件是否已被强类型 push 覆盖。
func shouldSuppressTypedRawProviderPush(method string, data any) bool {
	key := normalizedRawProviderMethodKey(method)
	if _, ok := typedPushMethods()[key]; ok {
		return true
	}
	if _, ok := typedRawProviderAliasMethods[key]; ok {
		return true
	}
	return isTypedToolLifecycleRawProviderEvent(method, data)
}

// typedRawProviderAliasMethods 是强类型事件的 raw provider 兼容别名集合。
var typedRawProviderAliasMethods = map[string]struct{}{
	normalizedRawProviderMethodKey("item/reasoning/summaryTextDelta"): {},
	normalizedRawProviderMethodKey("message.delta"):                   {},
	normalizedRawProviderMethodKey("agent_message_delta"):             {},
	normalizedRawProviderMethodKey("reasoning.delta"):                 {},
	normalizedRawProviderMethodKey("exec_output_delta"):               {},
}

// isTypedToolLifecycleRawProviderEvent 判断 raw 事件是否等价于强类型工具生命周期事件。
func isTypedToolLifecycleRawProviderEvent(method string, data any) bool {
	payload, ok := clonePayloadMap(data)
	if !ok {
		return false
	}
	return isTypedToolStartRawProviderEvent(method, payload) ||
		isTypedToolCompletionRawProviderEvent(method, payload)
}

// isTypedToolStartRawProviderEvent 判断 raw 事件是否表示工具调用开始。
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

// isTypedToolCompletionRawProviderEvent 判断 raw 事件是否表示工具调用完成。
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

// toolStartRawProviderMethods 是可能表示工具开始的 raw 方法集合。
var toolStartRawProviderMethods = map[string]struct{}{
	normalizedRawProviderMethodKey("item/started"):             {},
	normalizedRawProviderMethodKey("item_started"):             {},
	normalizedRawProviderMethodKey("agent/event/item_started"): {},
	normalizedRawProviderMethodKey("response_item"):            {},
}

// toolCompletionRawProviderMethods 是可能表示工具完成的 raw 方法集合。
var toolCompletionRawProviderMethods = map[string]struct{}{
	normalizedRawProviderMethodKey("item/completed"):             {},
	normalizedRawProviderMethodKey("item_completed"):             {},
	normalizedRawProviderMethodKey("agent/event/item_completed"): {},
	normalizedRawProviderMethodKey("rawResponseItem/completed"):  {},
	normalizedRawProviderMethodKey("event_msg"):                  {},
	normalizedRawProviderMethodKey("tool.call.end"):              {},
	normalizedRawProviderMethodKey("response_item"):              {},
}

// normalizedRawProviderMethodKey 标准化 raw provider 方法名用于集合查找。
func normalizedRawProviderMethodKey(method string) string {
	return strings.ToLower(strings.TrimSpace(method))
}

// toolLifecycleItemPayload 从 raw payload 中提取工具生命周期 item。
func toolLifecycleItemPayload(payload map[string]any) map[string]any {
	if item := nestedPayloadMap(payload, "item"); len(item) > 0 {
		return item
	}
	if nested := nestedPayloadMap(payload, "payload"); len(nested) > 0 {
		return nested
	}
	return payload
}

// toolLifecycleCallID 从 payload 或 item 中读取工具调用 ID。
func toolLifecycleCallID(payload, item map[string]any) string {
	if callID := firstPayloadString(payload, "callId", "call_id"); callID != "" {
		return callID
	}
	return firstPayloadString(item, "callId", "call_id")
}

// toolLifecycleToolName 从 payload、item 或 invocation 中读取工具名。
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

// nestedPayloadMap 读取嵌套 map payload，类型不匹配时返回 nil。
func nestedPayloadMap(payload map[string]any, key string) map[string]any {
	if payload == nil {
		return nil
	}
	child, _ := payload[key].(map[string]any)
	return child
}

// firstPayloadString 返回候选 key 中第一个非空字符串。
func firstPayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if text := payloadString(payload, key); text != "" {
			return text
		}
	}
	return ""
}

// payloadBool 兼容 bool 和字符串 true 的 payload 布尔字段。
func payloadBool(payload map[string]any, keys ...string) bool {
	for _, key := range keys {
		switch value := payload[key].(type) {
		case bool:
			return value
		case string:
			return strings.EqualFold(strings.TrimSpace(value), "true")
		}
	}
	return false
}

// normalizeRawProviderPushMethod 复用审批方法目录的别名标准化逻辑。
func normalizeRawProviderPushMethod(method string) string {
	return approvalMethodCatalog.normalize(method)
}

// shouldPushRawProviderMethod 判断 raw provider 方法是否允许直接 push 给前端。
func shouldPushRawProviderMethod(method string) bool {
	method = strings.TrimSpace(approvalMethodCatalog.normalize(method))
	if method == "" {
		return false
	}
	if _, ok := typedPushMethods()[strings.ToLower(method)]; ok {
		return false
	}
	return eventsurface.RawWireAllowed(eventsurface.RawWireAllowlistSpec(), method)
}
