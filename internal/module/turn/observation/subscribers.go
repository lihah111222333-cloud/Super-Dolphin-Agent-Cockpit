package observation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	buscontract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"

	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
)

// Subscribe wires the six canonical observation facts onto an event
// dispatcher. It returns a single cancel that tears down every
// subscription. The caller owns the cancel and must invoke it on shutdown.
//
// The subscriber layer only pushes facts into the Contract; it does not
// read from it. This is the one-way direction required by the P0b plan:
// observation fans in from bus subscribers and fans out via Contract reads
// to consumers (P3 collector, P0b extractor). turn/tracker must not import
// this package.
// Subscribe 注册事件订阅。
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

// mapTerminalFromCompleted decodes a TurnCompleted DTO into an observation
// Terminal. TurnCompleted.Success is a non-pointer bool, so we only promote
// it into a Success pointer when Status is empty or completed — for every
// other explicit Status we encode the failure/interrupt/abort/stall kind
// and leave Success nil to avoid the "default true" trap the P0b plan
// explicitly flagged.
// mapTerminalFromCompleted 从completed映射terminal。
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
		// Non-success without an explicit status still must not be recorded
		// as "completed" — keep it as failed with available reason context.
		return Terminal{Kind: TerminalFailed, Reason: reason}
	}
	success := true
	return Terminal{Kind: TerminalCompleted, Success: &success, Reason: reason}
}

func platformPickReason(parts ...string) string {
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			return s
		}
	}
	return ""
}

func onTurnStarted(c Contract) func(turndto.TurnStarted) {
	return func(ev turndto.TurnStarted) {
		turnID := strings.TrimSpace(ev.TurnID)
		if turnID == "" {
			return
		}
		c.RecordStartedAt(turnID, ev.Timestamp)
	}
}

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

func onToolCallBegin(c Contract) func(tooldto.ToolCallBegin) {
	return func(ev tooldto.ToolCallBegin) {
		callID := strings.TrimSpace(ev.CallID)
		turnID := strings.TrimSpace(ev.TurnID)
		// ToolCallHeader embeds CallID + TurnHeader.TurnID, so we can bind
		// the attribution as soon as the tool begins. ToolDiffUpdated later
		// in the stream has no TurnID, and consumers must consult this
		// mapping instead of guessing.
		c.AttributeCall(callID, turnID)
		// Dedupe-gated count so a retried ToolCallBegin for the same call
		// does not double-bump tool_calls.
		if callID != "" && c.Dedupe(DedupeKey{CallID: callID}) {
			c.IncrementToolCalls(turnID)
		}
	}
}

func onToolCallEnd(c Contract) func(tooldto.ToolCallEnd) {
	return func(ev tooldto.ToolCallEnd) {
		callID := strings.TrimSpace(ev.CallID)
		turnID := strings.TrimSpace(ev.TurnID)
		// Idempotent: if Begin already attributed the call we just overwrite
		// with the same value. Memory.AttributeCall already tolerates this.
		c.AttributeCall(callID, turnID)
		// Dedupe by (callID, "end") so a retransmitted End does not double-
		// bump tool_failures; the Begin dedupe key is different so both can
		// fire independently.
		if !ev.Success && callID != "" &&
			c.Dedupe(DedupeKey{CallID: callID, Key: "end"}) {
			c.IncrementToolFailures(turnID)
		}
	}
}

func onToolApprovalRequested(c Contract) func(tooldto.ToolApprovalRequested) {
	return func(ev tooldto.ToolApprovalRequested) {
		callID := strings.TrimSpace(ev.CallID)
		turnID := strings.TrimSpace(ev.TurnID)
		if turnID == "" {
			// Approval events without a turn id cannot be attributed; drop
			// rather than pollute an arbitrary per-turn bucket.
			return
		}
		// ApprovalID-scoped dedupe; falls back to CallID+request_id
		// otherwise. Codex path fills ApprovalID; Claude path never fires
		// here at all (the whole reason approval_requests_observed exists).
		key := DedupeKey{
			CallID: callID,
			Key:    "approval:" + strings.TrimSpace(ev.ApprovalID),
		}
		if c.Dedupe(key) {
			c.IncrementApprovalRequests(turnID)
		}
	}
}

func onUITokensUpdated(c Contract) func(uidto.UITokensUpdated) {
	return func(ev uidto.UITokensUpdated) {
		turnID := strings.TrimSpace(ev.TurnID)
		if turnID == "" {
			// Claude path's UITokensUpdated is fixed to Projection="thread"
			// and may carry no turn_id. We drop such events here rather
			// than misattributing them to an arbitrary per-turn bucket;
			// P3 collector is expected to read thread-level snapshots
			// through a separate path if it cares.
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

func onRawProviderEvent(c Contract) func(providerdto.BusRawProviderEvent) {
	return func(ev providerdto.BusRawProviderEvent) {
		if key := rawProviderDedupeKey(ev.Event); key != (DedupeKey{}) {
			c.Dedupe(key)
		}
	}
}

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
			return map[string]any{}
		}
		return decodeRawPayload(raw)
	}
}

func decodeRawPayload(raw []byte) map[string]any {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	return payload
}

func firstPayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if s := payloadString(payload[key]); s != "" {
			return s
		}
	}
	return ""
}

func payloadString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	}
	return ""
}
