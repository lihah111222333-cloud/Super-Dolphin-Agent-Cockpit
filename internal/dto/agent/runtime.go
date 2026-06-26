package agent

import "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"

// RuntimeReport 是 ctl/report runtime 变体投影到 agent 事件流后的载荷。
type RuntimeReport struct {
	AgentID  string `json:"agent_id"`
	Port     int    `json:"port,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// AgentRuntimeReported 表示 agent 运行时端口和 provider 信息已上报。
type AgentRuntimeReported struct {
	shared.AgentSessionHeader
	Port     int    `json:"port,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// Type 返回 agent runtime reported 事件分发用的类型编号。
func (AgentRuntimeReported) Type() uint32 { return shared.EventTypeAgentRuntimeReported }
