package observation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kelindar/event"
	buscontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"

	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
)

// Subscribe 把事件总线上的 turn/tool/UI 事件写入 canonical observation Contract。
// 返回的 cancel 会关闭全部订阅；本层只写不读，读取方通过 Contract 与 turn/tracker 解耦。
// 事件乱序和重复由各 handler 的去重键、终态优先级和归因表处理。
func Subscribe(dispatcher *event.Dispatcher, contract Contract, logger *pkglogger.Logger) context.CancelFunc {
	if dispatcher == nil || contract == nil {
		return func() {}
	}
	cancels := []context.CancelFunc{
		buscontract.ResilientSubscribe(dispatcher, onTurnStarted(contract), logger),
		buscontract.ResilientSubscribe(dispatcher, onTurnCompleted(contract), logger),
		buscontract.ResilientSubscribe(dispatcher, onTurnInterrupted(contract), logger),
		buscontract.ResilientSubscribe(dispatcher, onTurnStalled(contract), logger),
		buscontract.ResilientSubscribe(dispatcher, onToolCallBegin(contract), logger),
		buscontract.ResilientSubscribe(dispatcher, onToolCallEnd(contract), logger),
		buscontract.ResilientSubscribe(dispatcher, onToolApprovalRequested(contract), logger),
		buscontract.ResilientSubscribe(dispatcher, onUITokensUpdated(contract), logger),
		buscontract.ResilientSubscribe(dispatcher, onRawProviderEvent(contract), logger),
	}
	return func() {
		for _, c := range cancels {
			c()
		}
	}
}

// mapTerminalFromCompleted 把 TurnCompleted DTO 转为 observation Terminal。
// Success 是非指针 bool，只有明确 completed 时才提升为指针，避免失败状态被默认 true 污染。
func mapTerminalFromCompleted(ev turndto.TurnCompleted) Terminal {
	status := strings.ToLower(strings.TrimSpace(ev.Status))
	reason := platformPickReason(ev.Reason, ev.StopReason, ev.Error)
	switch status {
	case "interrupted":
		return Terminal{Kind: TerminalInterrupted, Reason: reason}
	case "aborted":
		return Terminal{Kind: TerminalAborted, Reason: reason}
	case "failed", "error":
		return Terminal{Kind: TerminalFailed, Reason: reason}
	case "stalled":
		return Terminal{Kind: TerminalStalled, Reason: reason}
	}
	if !ev.Success {
		// success=false 但缺少显式状态时也不能记为 completed，保留可用原因并按 failed 处理。
		return Terminal{Kind: TerminalFailed, Reason: reason}
	}
	success := true
	return Terminal{Kind: TerminalCompleted, Success: &success, Reason: reason}
}

// platformPickReason 返回第一个非空原因字段，用于兼容不同 provider 的终止原因命名。
func platformPickReason(parts ...string) string {
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			return s
		}
	}
	return ""
}

// onTurnStarted 记录 turn 的首次开始时间，空 turnID 事件不会污染共享状态。
func onTurnStarted(c Contract) func(turndto.TurnStarted) {
	return func(ev turndto.TurnStarted) {
		turnID := strings.TrimSpace(ev.TurnID)
		if turnID == "" {
			return
		}
		c.RecordStartedAt(turnID, ev.Timestamp)
	}
}

// onTurnCompleted 写入终止状态和完成时间，由 Contract 负责终止优先级合并。
func onTurnCompleted(c Contract) func(turndto.TurnCompleted) {
	return func(ev turndto.TurnCompleted) {
		turnID := strings.TrimSpace(ev.TurnID)
		if turnID == "" {
			return
		}
		c.RecordTerminal(turnID, mapTerminalFromCompleted(ev))
		c.RecordCompletedAt(turnID, ev.Timestamp)
	}
}

// onTurnInterrupted 将显式中断事件写成 sticky terminal，避免后续 completed 覆盖。
func onTurnInterrupted(c Contract) func(turndto.TurnInterrupted) {
	return func(ev turndto.TurnInterrupted) {
		turnID := strings.TrimSpace(ev.TurnID)
		if turnID == "" {
			return
		}
		c.RecordTerminal(turnID, Terminal{
			Kind:   TerminalInterrupted,
			Reason: strings.TrimSpace(ev.Reason),
		})
		c.RecordCompletedAt(turnID, ev.Timestamp)
	}
}

// onTurnStalled 将 stalled 事件记录为终止状态，并保留 provider 给出的原因。
func onTurnStalled(c Contract) func(turndto.TurnStalled) {
	return func(ev turndto.TurnStalled) {
		turnID := strings.TrimSpace(ev.TurnID)
		if turnID == "" {
			return
		}
		c.RecordTerminal(turnID, Terminal{
			Kind:   TerminalStalled,
			Reason: strings.TrimSpace(ev.Reason),
		})
		c.RecordCompletedAt(turnID, ev.Timestamp)
	}
}

// onToolCallBegin 建立 callID 到 turnID 的归因，并按 callID 去重累加工具调用数。
func onToolCallBegin(c Contract) func(tooldto.ToolCallBegin) {
	return func(ev tooldto.ToolCallBegin) {
		callID := strings.TrimSpace(ev.CallID)
		turnID := strings.TrimSpace(ev.TurnID)
		// ToolCallBegin 同时携带 callID 和 turnID，可立即建立归因。
		// 后续 ToolDiffUpdated 不带 turnID，消费者只能查这张表，不能猜测归属。
		c.AttributeCall(callID, turnID)
		// 按 callID 去重计数，避免同一次工具开始事件重放时重复累加 tool_calls。
		if callID != "" && c.Dedupe(DedupeKey{CallID: callID}) {
			c.IncrementToolCalls(turnID)
		}
	}
}

// onToolCallEnd 补齐工具调用归因，并只对失败结束事件去重计数。
func onToolCallEnd(c Contract) func(tooldto.ToolCallEnd) {
	return func(ev tooldto.ToolCallEnd) {
		callID := strings.TrimSpace(ev.CallID)
		turnID := strings.TrimSpace(ev.TurnID)
		// Begin 已归因时再次写入同值是幂等的，Memory.AttributeCall 会容忍重复归因。
		c.AttributeCall(callID, turnID)
		// End 事件用独立 key 去重，避免重放重复累加失败数，同时不影响 Begin 的调用计数。
		if !ev.Success && callID != "" &&
			c.Dedupe(DedupeKey{CallID: callID, Key: "end"}) {
			c.IncrementToolFailures(turnID)
		}
	}
}

// onToolApprovalRequested 统计可归因到 turn 的审批请求，无法归因的事件直接丢弃。
func onToolApprovalRequested(c Contract) func(tooldto.ToolApprovalRequested) {
	return func(ev tooldto.ToolApprovalRequested) {
		callID := strings.TrimSpace(ev.CallID)
		turnID := strings.TrimSpace(ev.TurnID)
		if turnID == "" {
			// 审批事件缺 turnID 时无法归因，直接丢弃，不能污染任意 turn 的计数桶。
			return
		}
		// 优先按 ApprovalID 去重；缺失时退回 CallID+request_id。
		// Codex 路径会填 ApprovalID，Claude 路径不触发该事件，因此 observed 标志能区分未观测。
		key := DedupeKey{
			CallID: callID,
			Key:    "approval:" + strings.TrimSpace(ev.ApprovalID),
		}
		if c.Dedupe(key) {
			c.IncrementApprovalRequests(turnID)
		}
	}
}

// onUITokensUpdated 只记录带 turnID 的 token 快照，线程级 token 另由专门路径处理。
func onUITokensUpdated(c Contract) func(uidto.UITokensUpdated) {
	return func(ev uidto.UITokensUpdated) {
		turnID := strings.TrimSpace(ev.TurnID)
		if turnID == "" {
			// 线程级 token 事件可能没有 turn_id；这里直接丢弃，避免误归因到任意 turn。
			return
		}
		snap := TokenSnapshot{
			Input:               int64(ev.InputTokens),
			Output:              int64(ev.OutputTokens),
			Total:               int64(ev.TotalTokens),
			ContextWindowTokens: int64(ev.ContextWindowTokens),
			Projection:          ev.Projection,
			Observed:            ev.InputTokens != 0 || ev.OutputTokens != 0 || ev.TotalTokens != 0,
		}
		c.RecordTokens(turnID, snap)
	}
}

// onRawProviderEvent 只把 raw event 写入去重集合，为后续消费者提供 best-effort 幂等线索。
func onRawProviderEvent(c Contract) func(providerdto.BusRawProviderEvent) {
	return func(ev providerdto.BusRawProviderEvent) {
		if key := rawProviderDedupeKey(ev.Event); key != (DedupeKey{}) {
			c.Dedupe(key)
		}
	}
}

// rawProviderDedupeKey 从 raw provider payload 中提取稳定事件 ID，缺失时退化到事件类型+payload。
func rawProviderDedupeKey(ev providerdto.RawProviderEvent) DedupeKey {
	payload := rawPayloadMap(ev.Data)
	if id := firstPayloadString(payload, "eventId", "event_id", "id"); id != "" {
		return DedupeKey{RawEventID: strings.TrimSpace(ev.EventType) + ":" + id}
	}
	if callID := firstPayloadString(payload, "callId", "call_id"); callID != "" {
		return DedupeKey{RawEventID: strings.TrimSpace(ev.EventType) + ":call:" + callID}
	}
	if eventType := strings.TrimSpace(ev.EventType); eventType != "" {
		return DedupeKey{RawEventID: eventType + ":" + fmt.Sprint(ev.Data)}
	}
	return DedupeKey{}
}

// rawPayloadMap 把 provider raw data 容忍地转成 map，失败返回 nil 表示没有可用去重字段。
func rawPayloadMap(data any) map[string]any {
	switch v := data.(type) {
	case map[string]any:
		return v
	case json.RawMessage:
		return decodeRawPayload(v)
	case []byte:
		return decodeRawPayload(v)
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		return decodeRawPayload(raw)
	}
}

// decodeRawPayload 解码 JSON object payload，非 object 或非法 JSON 视为不可提取。
func decodeRawPayload(raw []byte) map[string]any {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	return payload
}

// firstPayloadString 按优先级从 payload 中取第一个非空字符串字段。
func firstPayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if s := payloadString(payload[key]); s != "" {
			return s
		}
	}
	return ""
}

// payloadString 提取字符串或 Stringer 值，其他类型不参与 ID 推导。
func payloadString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	}
	return ""
}
