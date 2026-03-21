package turn

import "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"

// TurnStarted reports the beginning of a turn execution.
type TurnStarted struct {
	shared.TurnHeader
}

// TurnCompleted reports a turn reaching a terminal result.
type TurnCompleted struct {
	shared.TurnHeader
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Status  string `json:"status,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// TurnInterrupted reports an interrupt applied to a running turn.
type TurnInterrupted struct {
	shared.TurnHeader
	Reason string `json:"reason,omitempty"`
}

// TurnStalled reports a turn that stopped making progress.
type TurnStalled struct {
	shared.TurnHeader
	Reason    string `json:"reason,omitempty"`
	StalledMS int64  `json:"stalled_ms,omitempty"`
}

// TurnResumed reports a stalled or paused turn resuming execution.
type TurnResumed struct {
	shared.TurnHeader
	Reason string `json:"reason,omitempty"`
}

// TurnInputReceived reports new input accepted into an existing turn.
type TurnInputReceived struct {
	shared.TurnHeader
	InputType string `json:"input_type"`
	RequestID int64  `json:"request_id,omitempty"`
	Source    string `json:"source,omitempty"`
}

// TurnOutputDelta reports streamed output for a turn.
type TurnOutputDelta struct {
	shared.TurnHeader
	Stream string `json:"stream"`
	Delta  string `json:"delta"`
}

func (TurnStarted) Type() uint32       { return shared.EventTypeTurnStarted }
func (TurnCompleted) Type() uint32     { return shared.EventTypeTurnCompleted }
func (TurnInterrupted) Type() uint32   { return shared.EventTypeTurnInterrupted }
func (TurnStalled) Type() uint32       { return shared.EventTypeTurnStalled }
func (TurnResumed) Type() uint32       { return shared.EventTypeTurnResumed }
func (TurnInputReceived) Type() uint32 { return shared.EventTypeTurnInputReceived }
func (TurnOutputDelta) Type() uint32   { return shared.EventTypeTurnOutputDelta }
