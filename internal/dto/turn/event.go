package turn

import "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"

// TurnStarted reports the beginning of a turn execution.
type TurnStarted struct {
	shared.TurnHeader
}

// TurnCompleted reports a turn reaching a terminal result.
type TurnCompleted struct {
	shared.TurnHeader
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
	Status     string `json:"status,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Result     string `json:"result,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Message    string `json:"message,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
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
	Text      string `json:"text,omitempty"`
}

// TurnOutputDelta reports streamed output for a turn.
type TurnOutputDelta struct {
	shared.TurnHeader
	Stream string `json:"stream"`
	Delta  string `json:"delta"`
}

// Type 返回事件分发用的类型编号。
func (TurnStarted) Type() uint32 { return shared.EventTypeTurnStarted }

// Type 返回事件分发用的类型编号。
func (TurnCompleted) Type() uint32 { return shared.EventTypeTurnCompleted }

// Type 返回事件分发用的类型编号。
func (TurnInterrupted) Type() uint32 { return shared.EventTypeTurnInterrupted }

// Type 返回事件分发用的类型编号。
func (TurnStalled) Type() uint32 { return shared.EventTypeTurnStalled }

// Type 返回事件分发用的类型编号。
func (TurnResumed) Type() uint32 { return shared.EventTypeTurnResumed }

// Type 返回事件分发用的类型编号。
func (TurnInputReceived) Type() uint32 { return shared.EventTypeTurnInputReceived }

// Type 返回事件分发用的类型编号。
func (TurnOutputDelta) Type() uint32 { return shared.EventTypeTurnOutputDelta }
