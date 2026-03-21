package agent

import "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"

type RuntimeReport struct {
	AgentID  string `json:"agent_id"`
	Port     int    `json:"port,omitempty"`
	Provider string `json:"provider,omitempty"`
}

type AgentRuntimeReported struct {
	shared.AgentSessionHeader
	Port     int    `json:"port,omitempty"`
	Provider string `json:"provider,omitempty"`
}

func (AgentRuntimeReported) Type() uint32 { return shared.EventTypeAgentRuntimeReported }
