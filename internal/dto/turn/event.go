package turn

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"

// TurnStarted 报告一次 turn 执行开始。
type TurnStarted struct {
	shared.TurnHeader
}

// TurnCompleted 报告 turn 已到达终态，Success/Status/Reason 描述最终结果。
type TurnCompleted struct {
	shared.TurnHeader
	Success              bool     `json:"success"`
	Error                string   `json:"error,omitempty"`
	Status               string   `json:"status,omitempty"`
	Reason               string   `json:"reason,omitempty"`
	Result               string   `json:"result,omitempty"`
	Summary              string   `json:"summary,omitempty"`
	Message              string   `json:"message,omitempty"`
	StopReason           string   `json:"stop_reason,omitempty"`
	TerminationRequestID string   `json:"termination_request_id,omitempty"`
	PartialItemIDs       []string `json:"partial_item_ids,omitempty"`
	canonicalTerminal    *TurnTerminalV2
}

// TurnInterrupted 报告运行中的 turn 已收到中断请求。
type TurnInterrupted struct {
	shared.TurnHeader
	Reason string `json:"reason,omitempty"`
}

// TurnStalled 报告 turn 停止推进，Reason/StalledMS 用于观测卡顿原因和时长。
type TurnStalled struct {
	shared.TurnHeader
	Reason    string `json:"reason,omitempty"`
	StalledMS int64  `json:"stalled_ms,omitempty"`
}

// TurnResumed 报告停滞或暂停的 turn 恢复执行。
type TurnResumed struct {
	shared.TurnHeader
	Reason string `json:"reason,omitempty"`
}

// TurnInputReceived 报告已有 turn 接收了新的输入。
type TurnInputReceived struct {
	shared.TurnHeader
	InputType string `json:"input_type"`
	RequestID int64  `json:"request_id,omitempty"`
	Source    string `json:"source,omitempty"`
	Text      string `json:"text,omitempty"`
}

// TurnOutputDelta 报告 turn 的流式输出增量。
type TurnOutputDelta struct {
	shared.TurnHeader
	Stream string `json:"stream"`
	Delta  string `json:"delta"`
}

// Type 返回事件总线使用的稳定类型编号，保持 turn started 事件可路由。
func (TurnStarted) Type() uint32 { return shared.EventTypeTurnStarted }

// Type 返回事件总线使用的稳定类型编号，保持 turn completed 事件可路由。
func (TurnCompleted) Type() uint32 { return shared.EventTypeTurnCompleted }

// Type 返回事件总线使用的稳定类型编号，保持 turn interrupted 事件可路由。
func (TurnInterrupted) Type() uint32 { return shared.EventTypeTurnInterrupted }

// Type 返回事件总线使用的稳定类型编号，保持 turn stalled 事件可路由。
func (TurnStalled) Type() uint32 { return shared.EventTypeTurnStalled }

// Type 返回事件总线使用的稳定类型编号，保持 turn resumed 事件可路由。
func (TurnResumed) Type() uint32 { return shared.EventTypeTurnResumed }

// Type 返回事件总线使用的稳定类型编号，保持 turn 输入事件可路由。
func (TurnInputReceived) Type() uint32 { return shared.EventTypeTurnInputReceived }

// Type 返回事件总线使用的稳定类型编号，保持 turn 输出增量事件可路由。
func (TurnOutputDelta) Type() uint32 { return shared.EventTypeTurnOutputDelta }
