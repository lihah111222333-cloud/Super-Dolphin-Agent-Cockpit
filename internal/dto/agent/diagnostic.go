package agent

import (
	"encoding/json"

	shared "github.com/anthropic-ai/super-agent-v3/internal/dto/eventcore"
)

// AgentWarning reports a non-terminal provider/runtime warning.
type AgentWarning struct {
	shared.AgentSessionHeader
	RawType string          `json:"raw_type,omitempty"`
	Message string          `json:"message,omitempty"`
	Code    string          `json:"code,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// AgentError reports a provider/runtime error richer than terminal lifecycle failure.
type AgentError struct {
	shared.AgentSessionHeader
	RawType     string          `json:"raw_type,omitempty"`
	Message     string          `json:"message,omitempty"`
	Code        string          `json:"code,omitempty"`
	Recoverable bool            `json:"recoverable,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
}

// Type 返回事件分发用的类型编号。
func (AgentWarning) Type() uint32 { return shared.EventTypeAgentWarning }

// Type 返回事件分发用的类型编号。
func (AgentError) Type() uint32 { return shared.EventTypeAgentError }
