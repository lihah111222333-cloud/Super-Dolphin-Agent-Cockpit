// Package bus provides the V3 type-safe event bus built on kelindar/event.
//
// V2 comparison:
//   - V2: json.RawMessage payloads, 50+ string constants, manual marshal/unmarshal
//   - V3: Go generics, compile-time type safety, zero allocations
package bus

import "github.com/kelindar/event"

// ── Core event types (replace V2 string constants) ──

// Event type IDs — each event type gets a unique uint32.
const (
	typeTurnStarted      uint32 = iota + 1
	typeTurnCompleted
	typeCommandStarted
	typeCommandCompleted
	typeAgentStateChanged
)

// TurnStarted is emitted when a new turn begins.
type TurnStarted struct {
	ThreadID string
	TurnID   string
}

func (TurnStarted) Type() uint32 { return typeTurnStarted }

// TurnCompleted is emitted when a turn finishes.
type TurnCompleted struct {
	ThreadID string
	TurnID   string
	Status   string
}

func (TurnCompleted) Type() uint32 { return typeTurnCompleted }

// CommandStarted is emitted when a tool command begins execution.
type CommandStarted struct {
	ThreadID  string
	CommandID string
	ToolName  string
}

func (CommandStarted) Type() uint32 { return typeCommandStarted }

// CommandCompleted is emitted when a tool command finishes.
type CommandCompleted struct {
	ThreadID  string
	CommandID string
	ExitCode  int
}

func (CommandCompleted) Type() uint32 { return typeCommandCompleted }

// AgentStateChanged is emitted when agent state transitions.
type AgentStateChanged struct {
	AgentID  string
	OldState string
	NewState string
	Trigger  string
}

func (AgentStateChanged) Type() uint32 { return typeAgentStateChanged }

// ── Subscription helpers ──

// OnTurnStarted subscribes to TurnStarted events with compile-time type safety.
func OnTurnStarted(fn func(TurnStarted)) func() {
	return event.On(fn)
}

// OnTurnCompleted subscribes to TurnCompleted events.
func OnTurnCompleted(fn func(TurnCompleted)) func() {
	return event.On(fn)
}

// OnAgentStateChanged subscribes to state change events.
func OnAgentStateChanged(fn func(AgentStateChanged)) func() {
	return event.On(fn)
}

// ── Publishing helpers ──

// EmitTurnStarted publishes a TurnStarted event.
func EmitTurnStarted(threadID, turnID string) {
	event.Emit(TurnStarted{ThreadID: threadID, TurnID: turnID})
}

// EmitTurnCompleted publishes a TurnCompleted event.
func EmitTurnCompleted(threadID, turnID, status string) {
	event.Emit(TurnCompleted{ThreadID: threadID, TurnID: turnID, Status: status})
}

// EmitAgentStateChanged publishes an AgentStateChanged event.
func EmitAgentStateChanged(agentID, oldState, newState, trigger string) {
	event.Emit(AgentStateChanged{
		AgentID:  agentID,
		OldState: oldState,
		NewState: newState,
		Trigger:  trigger,
	})
}
