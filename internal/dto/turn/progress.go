package turn

import (
	"encoding/json"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
)

// PlanDelta 报告 plan 的增量更新载荷，RawType 保留 provider 原始事件类型。
type PlanDelta struct {
	shared.TurnHeader
	RawType string          `json:"raw_type,omitempty"`
	Delta   string          `json:"delta,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// PlanUpdated 报告 plan 的完整快照更新。
type PlanUpdated struct {
	shared.TurnHeader
	RawType string          `json:"raw_type,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// ItemStarted 报告通用 item 生命周期开始事件，例如命令、文件或工具调用开始。
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

// ItemCompleted 报告通用 item 生命周期结束事件，并携带退出码、成功状态或错误。
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

// Type 返回事件总线使用的稳定类型编号，保持 plan delta 事件可路由。
func (PlanDelta) Type() uint32 { return shared.EventTypeTurnPlanDelta }

// Type 返回事件总线使用的稳定类型编号，保持 plan snapshot 事件可路由。
func (PlanUpdated) Type() uint32 { return shared.EventTypeTurnPlanUpdated }

// Type 返回事件总线使用的稳定类型编号，保持通用 item started 事件可路由。
func (ItemStarted) Type() uint32 { return shared.EventTypeTurnItemStarted }

// Type 返回事件总线使用的稳定类型编号，保持通用 item completed 事件可路由。
func (ItemCompleted) Type() uint32 { return shared.EventTypeTurnItemCompleted }
