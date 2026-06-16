package turn

import (
	"encoding/json"

	shared "github.com/anthropic-ai/super-agent-v3/internal/dto/eventcore"
)

// PlanDelta reports an incremental plan update payload.
type PlanDelta struct {
	shared.TurnHeader
	RawType string          `json:"raw_type,omitempty"`
	Delta   string          `json:"delta,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// PlanUpdated reports a full plan snapshot update.
type PlanUpdated struct {
	shared.TurnHeader
	RawType string          `json:"raw_type,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// ItemStarted reports a generic item lifecycle start event.
type ItemStarted struct {
	shared.TurnHeader
	RawType  string          `json:"raw_type,omitempty"`
	ItemType string          `json:"item_type,omitempty"`
	Command  string          `json:"command,omitempty"`
	File     string          `json:"file,omitempty"`
	ToolName string          `json:"tool_name,omitempty"`
	CallID   string          `json:"call_id,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

// ItemCompleted reports a generic item lifecycle completion event.
type ItemCompleted struct {
	shared.TurnHeader
	RawType  string          `json:"raw_type,omitempty"`
	ItemType string          `json:"item_type,omitempty"`
	Command  string          `json:"command,omitempty"`
	File     string          `json:"file,omitempty"`
	ToolName string          `json:"tool_name,omitempty"`
	CallID   string          `json:"call_id,omitempty"`
	ExitCode int             `json:"exit_code,omitempty"`
	Success  bool            `json:"success,omitempty"`
	Error    string          `json:"error,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

// Type 返回事件分发用的类型编号。
func (PlanDelta) Type() uint32 { return shared.EventTypeTurnPlanDelta }

// Type 返回事件分发用的类型编号。
func (PlanUpdated) Type() uint32 { return shared.EventTypeTurnPlanUpdated }

// Type 返回事件分发用的类型编号。
func (ItemStarted) Type() uint32 { return shared.EventTypeTurnItemStarted }

// Type 返回事件分发用的类型编号。
func (ItemCompleted) Type() uint32 { return shared.EventTypeTurnItemCompleted }
