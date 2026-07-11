package agent

import (
	"encoding/json"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
)

// AgentWarning 表示 provider/runtime 上报的非终止警告事件。
type AgentWarning struct {
	shared.AgentSessionHeader
	RawType string          `json:"raw_type,omitempty"`
	Message string          `json:"message,omitempty"`
	Code    string          `json:"code,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// AgentError 表示 provider/runtime 上报的结构化错误事件。
// Recoverable 用于区分可恢复错误和需要生命周期进入 failed 的终止错误。
type AgentError struct {
	shared.AgentSessionHeader
	RawType     string          `json:"raw_type,omitempty"`
	Message     string          `json:"message,omitempty"`
	Code        string          `json:"code,omitempty"`
	Recoverable bool            `json:"recoverable,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
}

// Type 返回 agent warning 事件分发用的类型编号。
func (AgentWarning) Type() uint32 { return shared.EventTypeAgentWarning }

// Type 返回 agent error 事件分发用的类型编号。
func (AgentError) Type() uint32 { return shared.EventTypeAgentError }
