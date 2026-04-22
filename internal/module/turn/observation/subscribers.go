package observation

import (
	"context"
	"strings"

	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"

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
func Subscribe(dispatcher *event.Dispatcher, contract Contract, logger *pkglogger.Logger) context.CancelFunc {
	if dispatcher == nil || contract == nil {
		return func() {}
	}
	cancels := []context.CancelFunc{
		platformbus.ResilientSubscribe(dispatcher, onTurnCompleted(contract), logger),
		platformbus.ResilientSubscribe(dispatcher, onTurnInterrupted(contract), logger),
		platformbus.ResilientSubscribe(dispatcher, onTurnStalled(contract), logger),
		platformbus.ResilientSubscribe(dispatcher, onToolCallBegin(contract), logger),
		platformbus.ResilientSubscribe(dispatcher, onToolCallEnd(contract), logger),
		platformbus.ResilientSubscribe(dispatcher, onUITokensUpdated(contract), logger),
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

func onTurnCompleted(c Contract) func(turndto.TurnCompleted) {
	return func(ev turndto.TurnCompleted) {
		if strings.TrimSpace(ev.TurnID) == "" {
			return
		}
		c.RecordTerminal(ev.TurnID, mapTerminalFromCompleted(ev))
	}
}

func onTurnInterrupted(c Contract) func(turndto.TurnInterrupted) {
	return func(ev turndto.TurnInterrupted) {
		if strings.TrimSpace(ev.TurnID) == "" {
			return
		}
		c.RecordTerminal(ev.TurnID, Terminal{
			Kind:   TerminalInterrupted,
			Reason: strings.TrimSpace(ev.Reason),
		})
	}
}

func onTurnStalled(c Contract) func(turndto.TurnStalled) {
	return func(ev turndto.TurnStalled) {
		if strings.TrimSpace(ev.TurnID) == "" {
			return
		}
		c.RecordTerminal(ev.TurnID, Terminal{
			Kind:   TerminalStalled,
			Reason: strings.TrimSpace(ev.Reason),
		})
	}
}

func onToolCallBegin(c Contract) func(tooldto.ToolCallBegin) {
	return func(ev tooldto.ToolCallBegin) {
		// ToolCallHeader embeds CallID + TurnHeader.TurnID, so we can bind
		// the attribution as soon as the tool begins. ToolDiffUpdated later
		// in the stream has no TurnID, and consumers must consult this
		// mapping instead of guessing.
		c.AttributeCall(strings.TrimSpace(ev.CallID), strings.TrimSpace(ev.TurnID))
	}
}

func onToolCallEnd(c Contract) func(tooldto.ToolCallEnd) {
	return func(ev tooldto.ToolCallEnd) {
		// Idempotent: if Begin already attributed the call we just overwrite
		// with the same value. Memory.AttributeCall already tolerates this.
		c.AttributeCall(strings.TrimSpace(ev.CallID), strings.TrimSpace(ev.TurnID))
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
