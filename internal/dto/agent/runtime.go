package agent

import "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"

// RuntimeReport is the sidecar-to-orchestration runtime endpoint report.
type RuntimeReport struct {
	AgentID  string `json:"agent_id"`
	Port     int    `json:"port,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// AgentRuntimeReported publishes runtime endpoint metadata to the event bus.
type AgentRuntimeReported struct {
	shared.AgentSessionHeader
	Port     int    `json:"port,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// Type 返回事件分发用的类型编号。
func (AgentRuntimeReported) Type() uint32 { return shared.EventTypeAgentRuntimeReported }
