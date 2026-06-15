package unified

import (
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"sync"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	taskdto "github.com/anthropic-ai/super-agent-v3/internal/dto/task"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/kelindar/event"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type typedEventPublisher func(*event.Dispatcher, any) bool

type EventTranslator func(raw dto.RawProviderEvent, publish func(ev any))

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
	typedEventType[taskdto.TaskDagCreated]():        publishEvent[taskdto.TaskDagCreated],
	typedEventType[taskdto.TaskNodeStatusChanged](): publishEvent[taskdto.TaskNodeStatusChanged],
	typedEventType[taskdto.TaskWakeupDispatched]():  publishEvent[taskdto.TaskWakeupDispatched],
	typedEventType[taskdto.TaskWakeupCompleted]():   publishEvent[taskdto.TaskWakeupCompleted],
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

// EventDispatcher manages raw driver events and republishes translated typed events.
type EventDispatcher struct {
	mu          sync.RWMutex
	translators []EventTranslator
	bus         *event.Dispatcher
	logger      *slog.Logger
}

// NewEventDispatcher 创建事件调度器。
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

// Register registers one event translator from a driver.
// Register 注册unified provider。
func (d *EventDispatcher) Register(t EventTranslator) {
	if t == nil {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.translators = append(d.translators, t)
}

// Publish sends an already-normalized typed event to the shared event bus.
// Publish 发布unified provider。
func (d *EventDispatcher) Publish(ev any) {
	if d == nil {
		return
	}
	if !publishTypedEvent(d.bus, ev) {
		d.logger.Warn("event dispatcher received unsupported typed event", "event", ev)
	}
}

// Dispatch sends one raw driver event through all registered translators.
// Dispatch 派发unified provider。
func (d *EventDispatcher) Dispatch(raw dto.RawProviderEvent) {
	d.mu.RLock()
	translators := make([]EventTranslator, len(d.translators))
	copy(translators, d.translators)
	d.mu.RUnlock()

	if d.bus != nil {
		event.Publish(d.bus, dto.BusRawProviderEvent{Event: raw})
	}

	for _, translator := range translators {
		translator(raw, func(ev any) {
			if !publishTypedEvent(d.bus, ev) {
				d.logger.Warn(
					"event translator produced unsupported event",
					"raw_type", raw.EventType,
					"event", ev,
				)
			}
		})
	}
}

func publishTypedEvent(bus *event.Dispatcher, ev any) bool {
	if bus == nil {
		return true
	}
	publisher, ok := typedEventPublishers[reflect.TypeOf(ev)]
	if !ok {
		return false
	}
	return publisher(bus, ev)
}

func typedEventType[T event.Event]() reflect.Type {
	var zero T
	return reflect.TypeOf(zero)
}

func publishEvent[T event.Event](bus *event.Dispatcher, ev any) bool {
	typed, ok := ev.(T)
	if !ok {
		return false
	}
	event.Publish(bus, typed)
	return true
}

// translateCommonRawEvent 处理translatecommon原始事件。
func translateCommonRawEvent(raw dto.RawProviderEvent, publish func(ev any)) {
	if publish == nil {
		return
	}
	payload := payloadMap(raw.Data)
	if len(payload) == 0 {
		return
	}
	rawType := strings.TrimSpace(raw.EventType)
	switch {
	case isWarningRawType(rawType):
		publish(agentdto.AgentWarning{
			AgentSessionHeader: commonAgentSessionHeader(payload),
			RawType:            rawType,
			Message:            shared.FirstNonEmpty(stringValue(payload, "message", "warning", "reason"), stringValue(nestedMap(payload, "error"), "message")),
			Code:               stringValue(payload, "code"),
			Payload:            rawEventPayload(raw.Data),
		})
	case isErrorRawType(rawType):
		publish(agentdto.AgentError{
			AgentSessionHeader: commonAgentSessionHeader(payload),
			RawType:            rawType,
			Message:            shared.FirstNonEmpty(stringValue(payload, "message", "error", "reason"), stringValue(nestedMap(payload, "error"), "message")),
			Code:               stringValue(payload, "code"),
			Recoverable:        boolValue(payload, "recoverable", "willRetry", "will_retry"),
			Payload:            rawEventPayload(raw.Data),
		})
	case isPlanDeltaRawType(rawType):
		publish(turndto.PlanDelta{
			TurnHeader: commonTurnHeader(payload),
			RawType:    rawType,
			Delta:      shared.FirstNonEmpty(stringValue(payload, "delta", "content", "text"), marshalPreview(payload["delta"], payload["plan"], payload["steps"], payload["items"], payload)),
			Payload:    rawEventPayload(raw.Data),
		})
	case isPlanUpdatedRawType(rawType):
		publish(turndto.PlanUpdated{
			TurnHeader: commonTurnHeader(payload),
			RawType:    rawType,
			Payload:    rawEventPayload(raw.Data),
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
			Payload:    rawEventPayload(raw.Data),
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
			Payload:    rawEventPayload(raw.Data),
		})
	}
}

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

func rawEventPayload(data any) json.RawMessage {
	switch typed := data.(type) {
	case json.RawMessage:
		return append(json.RawMessage(nil), typed...)
	case []byte:
		return append(json.RawMessage(nil), typed...)
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return nil
		}
		return raw
	}
}

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

func firstIntValue(payload map[string]any, keys ...string) int {
	value, _ := intFromMap(payload, keys...)
	return value
}

func hasErrorPayload(payload map[string]any) bool {
	if value, ok := payload["success"].(bool); ok {
		return !value
	}
	return shared.FirstNonEmpty(
		stringValue(payload, "error", "message", "reason"),
		stringValue(nestedMap(payload, "error"), "message"),
	) != ""
}

func isWarningRawType(rawType string) bool {
	switch strings.TrimSpace(rawType) {
	case "warning", "configWarning", "windows/worldWritableWarning", "deprecationNotice":
		return true
	default:
		return false
	}
}

func isErrorRawType(rawType string) bool {
	switch strings.TrimSpace(rawType) {
	case "error", "stream_error":
		return true
	default:
		return false
	}
}

func isPlanDeltaRawType(rawType string) bool {
	switch strings.TrimSpace(rawType) {
	case "item/plan/delta", "plan_delta", "agent/event/plan_delta":
		return true
	default:
		return false
	}
}

func isPlanUpdatedRawType(rawType string) bool {
	switch strings.TrimSpace(rawType) {
	case "turn/plan/updated", "plan_update", "turn_plan":
		return true
	default:
		return false
	}
}

func isItemStartedRawType(rawType string) bool {
	switch strings.TrimSpace(rawType) {
	case "item/started", "item_started", "agent/event/item_started":
		return true
	default:
		return false
	}
}

func isItemCompletedRawType(rawType string) bool {
	switch strings.TrimSpace(rawType) {
	case "item/completed", "item_completed", "agent/event/item_completed", "rawResponseItem/completed":
		return true
	default:
		return false
	}
}
