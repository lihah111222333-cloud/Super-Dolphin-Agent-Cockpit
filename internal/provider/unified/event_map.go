package unified

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kelindar/event"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	taskdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/task"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// typedEventPublisher 是反射分发表中的发布函数，负责把 any 还原成强类型事件。
type typedEventPublisher func(*event.Dispatcher, any) bool

// EventTranslator 将 provider 原始事件翻译为内部强类型事件。
// 翻译器通过 publish 回调输出事件，便于一个 raw event 派生多个投影。
type EventTranslator func(raw dto.RawProviderEvent, publish func(ev any))

// typedEventPublishers 保存内部事件类型到发布函数的映射，未知类型必须显式拒绝。
var typedEventPublishers = map[reflect.Type]typedEventPublisher{
	typedEventType[agentdto.StateChanged]():         publishEvent[agentdto.StateChanged],
	typedEventType[agentdto.AgentLaunched]():        publishEvent[agentdto.AgentLaunched],
	typedEventType[agentdto.AgentStopped]():         publishEvent[agentdto.AgentStopped],
	typedEventType[agentdto.AgentRecovering]():      publishEvent[agentdto.AgentRecovering],
	typedEventType[agentdto.AgentFailed]():          publishEvent[agentdto.AgentFailed],
	typedEventType[agentdto.AgentRuntimeReported](): publishEvent[agentdto.AgentRuntimeReported],
	typedEventType[agentdto.AgentWarning]():         publishEvent[agentdto.AgentWarning],
	typedEventType[agentdto.AgentError]():           publishEvent[agentdto.AgentError],
	typedEventType[dto.RawProviderEvent]():          publishEvent[dto.RawProviderEvent],
	typedEventType[dto.BusRawProviderEvent]():       publishEvent[dto.BusRawProviderEvent],
	typedEventType[taskdto.TaskNodeStatusChanged](): publishEvent[taskdto.TaskNodeStatusChanged],
	typedEventType[threaddto.Started]():             publishEvent[threaddto.Started],
	typedEventType[threaddto.Stopped]():             publishEvent[threaddto.Stopped],
	typedEventType[threaddto.MessagesPage]():        publishEvent[threaddto.MessagesPage],
	typedEventType[threaddto.Compacted]():           publishEvent[threaddto.Compacted],
	typedEventType[tooldto.ToolCallBegin]():         publishEvent[tooldto.ToolCallBegin],
	typedEventType[tooldto.ToolCallEnd]():           publishEvent[tooldto.ToolCallEnd],
	typedEventType[tooldto.ToolApprovalRequested](): publishEvent[tooldto.ToolApprovalRequested],
	typedEventType[tooldto.ToolApprovalResolved]():  publishEvent[tooldto.ToolApprovalResolved],
	typedEventType[tooldto.ToolDiffUpdated]():       publishEvent[tooldto.ToolDiffUpdated],
	typedEventType[turndto.TurnStarted]():           publishEvent[turndto.TurnStarted],
	typedEventType[turndto.TurnCompleted]():         publishEvent[turndto.TurnCompleted],
	typedEventType[turndto.TurnInterrupted]():       publishEvent[turndto.TurnInterrupted],
	typedEventType[turndto.TurnStalled]():           publishEvent[turndto.TurnStalled],
	typedEventType[turndto.TurnResumed]():           publishEvent[turndto.TurnResumed],
	typedEventType[turndto.TurnInputReceived]():     publishEvent[turndto.TurnInputReceived],
	typedEventType[turndto.TurnOutputDelta]():       publishEvent[turndto.TurnOutputDelta],
	typedEventType[turndto.PlanDelta]():             publishEvent[turndto.PlanDelta],
	typedEventType[turndto.PlanUpdated]():           publishEvent[turndto.PlanUpdated],
	typedEventType[turndto.ItemStarted]():           publishEvent[turndto.ItemStarted],
	typedEventType[turndto.ItemCompleted]():         publishEvent[turndto.ItemCompleted],
	typedEventType[uidto.UIProjectionUpdated]():     publishEvent[uidto.UIProjectionUpdated],
	typedEventType[uidto.UITimelineAppended]():      publishEvent[uidto.UITimelineAppended],
	typedEventType[uidto.UITokensUpdated]():         publishEvent[uidto.UITokensUpdated],
	typedEventType[uidto.SkillsChanged]():           publishEvent[uidto.SkillsChanged],
	typedEventType[uidto.UIThreadPatch]():           publishEvent[uidto.UIThreadPatch],
	typedEventType[uidto.UIPreferencesChanged]():    publishEvent[uidto.UIPreferencesChanged],
	typedEventType[uidto.UISharedFilesChanged]():    publishEvent[uidto.UISharedFilesChanged],
	typedEventType[uidto.UIMemoryChanged]():         publishEvent[uidto.UIMemoryChanged],
}

// EventDispatcher 接收 provider 原始事件，并按注册的 translator 重新发布为内部强类型事件。
type EventDispatcher struct {
	mu          sync.RWMutex
	translators []EventTranslator
	bus         *event.Dispatcher
	logger      *slog.Logger
}

// NewEventDispatcher 创建事件调度器，并默认注册通用 provider 事件翻译器。
func NewEventDispatcher(bus *event.Dispatcher, logger *slog.Logger) *EventDispatcher {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &EventDispatcher{
		translators: []EventTranslator{translateCommonRawEvent},
		bus:         bus,
		logger:      logger,
	}
}

// Register 注册一个 provider 事件翻译器，nil 翻译器会被忽略以保护分发链。
func (d *EventDispatcher) Register(t EventTranslator) {
	if t == nil {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.translators = append(d.translators, t)
}

// Publish 发布已经标准化的内部事件；未知事件类型会记录告警而不是静默丢弃。
func (d *EventDispatcher) Publish(ev any) {
	if d == nil {
		return
	}
	d.publishTranslatedEvent("typed", ev)
}

func (d *EventDispatcher) publishTranslatedEvent(rawType string, ev any) {
	canonicalEvent, err := attachCanonicalTurnTerminal(ev)
	if err != nil {
		d.logger.Warn("event dispatcher rejected turn terminal without canonical projection", "raw_type", rawType)
		return
	}
	if !publishTypedEvent(d.bus, canonicalEvent) {
		d.logger.Warn("event dispatcher received unsupported typed event", "event", ev)
	}
}

// Dispatch 先广播原始 provider 事件，再把快照化的 translator 列表逐个执行。
// translator 列表复制后释放读锁，避免翻译器发布事件时阻塞后续注册。
func (d *EventDispatcher) Dispatch(raw dto.RawProviderEvent) {
	d.mu.RLock()
	translators := make([]EventTranslator, len(d.translators))
	copy(translators, d.translators)
	d.mu.RUnlock()

	if d.bus != nil {
		event.Publish(d.bus, dto.BusRawProviderEvent{Event: raw.SanitizedCopy()})
	}

	for _, translator := range translators {
		translator(raw, func(ev any) {
			d.publishTranslatedEvent(raw.EventType, ev)
		})
	}
}

// attachCanonicalTurnTerminal 让统一 provider dispatcher 在 typed subscriber 之前产出唯一终态真值。
func attachCanonicalTurnTerminal(ev any) (any, error) {
	completed, ok := ev.(turndto.TurnCompleted)
	if !ok {
		return ev, nil
	}
	if _, canonical, err := turndto.CanonicalTurnTerminal(completed); err != nil {
		return nil, errors.New("invalid canonical turn terminal")
	} else if canonical {
		return completed, nil
	}
	eventID := fmt.Sprintf("terminal:%s:%s:%s", completed.ThreadID, completed.TurnID, completed.Timestamp.UTC().Format(time.RFC3339Nano))
	terminal, err := turndto.NewTurnTerminalV2(completed, eventID)
	if err != nil {
		return nil, errors.New("turn terminal canonicalization failed")
	}
	attached, err := turndto.AttachCanonicalTurnTerminal(completed, terminal)
	if err != nil {
		return nil, errors.New("turn terminal attachment failed")
	}
	return attached, nil
}

// publishTypedEvent 根据事件实际类型查找发布器，bus 缺失时视为已处理以支持无事件总线测试。
func publishTypedEvent(bus *event.Dispatcher, ev any) bool {
	if bus == nil {
		return true
	}
	switch typed := ev.(type) {
	case dto.RawProviderEvent:
		ev = typed.SanitizedCopy()
	case dto.BusRawProviderEvent:
		ev = dto.BusRawProviderEvent{Event: typed.Event.SanitizedCopy()}
	}
	publisher, ok := typedEventPublishers[reflect.TypeOf(ev)]
	if !ok {
		return false
	}
	return publisher(bus, ev)
}

// typedEventType 返回泛型事件的反射类型，保证分发表 key 与 publishEvent 的类型一致。
func typedEventType[T event.Event]() reflect.Type {
	var zero T
	return reflect.TypeOf(zero)
}

// publishEvent 将 any 事件还原为指定强类型，类型不匹配时返回 false 给调用方告警。
func publishEvent[T event.Event](bus *event.Dispatcher, ev any) bool {
	typed, ok := ev.(T)
	if !ok {
		return false
	}
	event.Publish(bus, typed)
	return true
}

// translateCommonRawEvent 翻译通用 provider raw event，覆盖告警、错误、plan 和 item 生命周期。
// 无 payload 或重试进度错误会被跳过，避免污染用户可见时间线。
func translateCommonRawEvent(raw dto.RawProviderEvent, publish func(ev any)) {
	if publish == nil {
		return
	}
	payload := payloadMap(raw.Data)
	if len(payload) == 0 {
		return
	}
	rawType := strings.TrimSpace(raw.EventType)
	if isRetryProgressRawError(rawType, payload) {
		return
	}
	switch {
	case isWarningRawType(rawType):
		publish(agentdto.AgentWarning{
			AgentSessionHeader: commonAgentSessionHeader(payload),
			RawType:            rawType,
			Message:            shared.FirstNonEmpty(stringValue(payload, "message", "warning", "reason"), stringValue(nestedMap(payload, "error"), "message")),
			Code:               stringValue(payload, "code"),
			Payload:            raw.SafePayload(),
		})
	case isErrorRawType(rawType):
		publish(agentdto.AgentError{
			AgentSessionHeader: commonAgentSessionHeader(payload),
			RawType:            rawType,
			Message:            shared.FirstNonEmpty(stringValue(payload, "message", "error", "reason"), stringValue(nestedMap(payload, "error"), "message")),
			Code:               stringValue(payload, "code"),
			Recoverable:        boolValue(payload, "recoverable", "willRetry", "will_retry"),
			Payload:            raw.SafePayload(),
		})
	case isPlanDeltaRawType(rawType):
		publish(turndto.PlanDelta{
			TurnHeader: commonTurnHeader(payload),
			RawType:    rawType,
			Delta:      shared.FirstNonEmpty(stringValue(payload, "delta", "content", "text"), marshalPreview(payload["delta"], payload["plan"], payload["steps"], payload["items"], payload)),
			Payload:    raw.SafePayload(),
		})
	case isPlanUpdatedRawType(rawType):
		publish(turndto.PlanUpdated{
			TurnHeader: commonTurnHeader(payload),
			RawType:    rawType,
			Payload:    raw.SafePayload(),
		})
	case isItemStartedRawType(rawType):
		publish(turndto.ItemStarted{
			TurnHeader: commonTurnHeader(payload),
			RawType:    rawType,
			ItemType:   shared.FirstNonEmpty(stringValue(payload, "type"), stringValue(nestedMap(payload, "item"), "type")),
			Command:    shared.FirstNonEmpty(stringValue(payload, "command"), stringValue(nestedMap(payload, "item"), "command")),
			File:       shared.FirstNonEmpty(stringValue(payload, "file", "path"), stringValue(nestedMap(payload, "item"), "file", "path")),
			ToolName:   shared.FirstNonEmpty(stringValue(payload, "toolName", "tool_name", "tool"), stringValue(nestedMap(payload, "item"), "toolName", "tool_name", "tool")),
			CallID:     shared.FirstNonEmpty(stringValue(payload, "callId", "call_id"), stringValue(nestedMap(payload, "item"), "callId", "call_id")),
			Payload:    raw.SafePayload(),
		})
	case isItemCompletedRawType(rawType):
		publish(turndto.ItemCompleted{
			TurnHeader: commonTurnHeader(payload),
			RawType:    rawType,
			ItemType:   shared.FirstNonEmpty(stringValue(payload, "type"), stringValue(nestedMap(payload, "item"), "type")),
			Command:    shared.FirstNonEmpty(stringValue(payload, "command"), stringValue(nestedMap(payload, "item"), "command")),
			File:       shared.FirstNonEmpty(stringValue(payload, "file", "path"), stringValue(nestedMap(payload, "item"), "file", "path")),
			ToolName:   shared.FirstNonEmpty(stringValue(payload, "toolName", "tool_name", "tool"), stringValue(nestedMap(payload, "item"), "toolName", "tool_name", "tool")),
			CallID:     shared.FirstNonEmpty(stringValue(payload, "callId", "call_id"), stringValue(nestedMap(payload, "item"), "callId", "call_id")),
			ExitCode:   firstIntValue(payload, "exitCode", "exit_code"),
			Success:    !hasErrorPayload(payload),
			Error:      shared.FirstNonEmpty(stringValue(payload, "error", "message", "reason"), stringValue(nestedMap(payload, "error"), "message")),
			Payload:    raw.SafePayload(),
		})
	}
}

// commonAgentSessionHeader 从 provider payload 中提取 agent/session 头，兼容 camelCase 和 snake_case。
func commonAgentSessionHeader(payload map[string]any) shareddto.AgentSessionHeader {
	threadID := shared.FirstNonEmpty(
		stringValue(payload, "threadId", "thread_id"),
		stringValue(nestedMap(payload, "thread"), "id"),
	)
	return shareddto.AgentSessionHeader{
		AgentHeader: shareddto.AgentHeader{
			ThreadHeader: shareddto.ThreadHeader{
				EventHeader: shareddto.EventHeader{Timestamp: shared.FirstEventTime(shared.EventTimeFromPayload(payload))},
				ThreadID:    threadID,
			},
			AgentID: shared.FirstNonEmpty(
				stringValue(payload, "agentId", "agent_id"),
				stringValue(nestedMap(payload, "agent"), "id"),
			),
		},
		SessionID: shared.FirstNonEmpty(stringValue(payload, "sessionId", "session_id"), threadID),
	}
}

// commonTurnHeader 从 provider payload 中提取 turn 头，并复用 agent/session 头解析规则。
func commonTurnHeader(payload map[string]any) shareddto.TurnHeader {
	return shareddto.TurnHeader{
		AgentHeader: commonAgentSessionHeader(payload).AgentHeader,
		TurnIDHeader: shareddto.TurnIDHeader{
			TurnID: shared.FirstNonEmpty(
				stringValue(payload, "turnId", "turn_id"),
				stringValue(nestedMap(payload, "turn"), "id"),
			),
		},
	}
}

// marshalPreview 把候选字段序列化成可展示摘要，无法编码的值会继续尝试下一个候选。
func marshalPreview(values ...any) string {
	for _, value := range values {
		if value == nil {
			continue
		}
		raw, err := json.Marshal(value)
		if err == nil && len(raw) > 0 {
			return string(raw)
		}
	}
	return ""
}

// boolValue 从 payload 多个候选键读取布尔值，字符串 true 也按 true 处理。
func boolValue(payload map[string]any, keys ...string) bool {
	for _, key := range keys {
		switch typed := payload[key].(type) {
		case bool:
			return typed
		case string:
			return strings.EqualFold(strings.TrimSpace(typed), "true")
		}
	}
	return false
}

// firstIntValue 从 payload 中读取第一个整数候选，缺失时返回 0。
func firstIntValue(payload map[string]any, keys ...string) int {
	value, _ := intFromMap(payload, keys...)
	return value
}

// hasErrorPayload 判断 provider payload 是否表示失败，success=false 优先于错误文本。
func hasErrorPayload(payload map[string]any) bool {
	if value, ok := payload["success"].(bool); ok {
		return !value
	}
	return shared.FirstNonEmpty(
		stringValue(payload, "error", "message", "reason"),
		stringValue(nestedMap(payload, "error"), "message"),
	) != ""
}

// isWarningRawType 识别应进入 AgentWarning 的 provider raw type。
func isWarningRawType(rawType string) bool {
	switch strings.TrimSpace(rawType) {
	case "warning", "configWarning", "windows/worldWritableWarning", "deprecationNotice":
		return true
	default:
		return false
	}
}

// isErrorRawType 识别应进入 AgentError 的 provider raw type。
func isErrorRawType(rawType string) bool {
	switch strings.TrimSpace(rawType) {
	case "error", "stream_error":
		return true
	default:
		return false
	}
}

// isRetryProgressRawError 识别 provider 自己的重试进度事件，避免把
// "Reconnecting... 2/5" 这类状态当成用户可见错误写入时间线。
func isRetryProgressRawError(rawType string, payload map[string]any) bool {
	if !isErrorRawType(rawType) || !boolValue(payload, "willRetry", "will_retry") {
		return false
	}
	message := shared.FirstNonEmpty(
		stringValue(payload, "message", "error", "reason"),
		stringValue(nestedMap(payload, "error"), "message"),
	)
	return isRetryProgressMessage(message)
}

// isRetryProgressMessage 判断重试进度文案是否只表示 provider 正在重连。
// 只接受带当前次数和总次数的格式，避免把真实错误误判成可忽略状态。
func isRetryProgressMessage(message string) bool {
	text := strings.ToLower(strings.TrimSpace(message))
	if !strings.HasPrefix(text, "reconnecting") && !strings.HasPrefix(text, "retrying") {
		return false
	}
	slash := strings.LastIndex(text, "/")
	if slash <= 0 || slash >= len(text)-1 {
		return false
	}
	if _, err := strconv.Atoi(strings.TrimSpace(text[slash+1:])); err != nil {
		return false
	}
	fields := strings.Fields(strings.TrimSpace(text[:slash]))
	if len(fields) == 0 {
		return false
	}
	_, err := strconv.Atoi(strings.Trim(fields[len(fields)-1], "."))
	return err == nil
}

// isPlanDeltaRawType 识别 provider 输出的 plan delta 事件类型。
func isPlanDeltaRawType(rawType string) bool {
	switch strings.TrimSpace(rawType) {
	case "item/plan/delta", "plan_delta", "agent/event/plan_delta":
		return true
	default:
		return false
	}
}

// isPlanUpdatedRawType 识别 provider 输出的 plan 完整更新事件类型。
func isPlanUpdatedRawType(rawType string) bool {
	switch strings.TrimSpace(rawType) {
	case "turn/plan/updated", "plan_update", "turn_plan":
		return true
	default:
		return false
	}
}

// isItemStartedRawType 识别 provider 输出的工作项开始事件类型。
func isItemStartedRawType(rawType string) bool {
	switch strings.TrimSpace(rawType) {
	case "item/started", "item_started", "agent/event/item_started":
		return true
	default:
		return false
	}
}

// isItemCompletedRawType 识别 provider 输出的工作项完成事件类型。
func isItemCompletedRawType(rawType string) bool {
	switch strings.TrimSpace(rawType) {
	case "item/completed", "item_completed", "agent/event/item_completed", "rawResponseItem/completed":
		return true
	default:
		return false
	}
}
