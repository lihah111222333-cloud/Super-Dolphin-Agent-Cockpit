package agent

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"

// StateChanged 表示 agent 生命周期状态发生迁移。
type StateChanged struct {
	shared.AgentSessionHeader
	OldState string `json:"old_state"`
	NewState string `json:"new_state"`
	Trigger  string `json:"trigger"`
}

// AgentLaunched 表示新的 agent 进程或会话已进入可用状态。
type AgentLaunched struct {
	shared.AgentSessionHeader
	Model    string `json:"model,omitempty"`
	CWD      string `json:"cwd,omitempty"`
	Name     string `json:"name,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// AgentStopped 表示 agent 已按请求停止或被强制停止。
type AgentStopped struct {
	shared.AgentSessionHeader
	Reason string `json:"reason,omitempty"`
}

// AgentRecovering 表示现有 agent session 正在执行恢复尝试。
type AgentRecovering struct {
	shared.AgentSessionHeader
	Reason  string `json:"reason"`
	Attempt int    `json:"attempt,omitempty"`
}

// AgentFailed 表示 agent 进入终止失败状态。
type AgentFailed struct {
	shared.AgentSessionHeader
	Error       string `json:"error"`
	Recoverable bool   `json:"recoverable,omitempty"`
}

// Type 返回状态迁移事件分发用的类型编号。
func (StateChanged) Type() uint32 { return shared.EventTypeAgentStateChanged }

// Type 返回 agent 启动事件分发用的类型编号。
func (AgentLaunched) Type() uint32 { return shared.EventTypeAgentLaunched }

// Type 返回 agent 停止事件分发用的类型编号。
func (AgentStopped) Type() uint32 { return shared.EventTypeAgentStopped }

// Type 返回 agent 恢复事件分发用的类型编号。
func (AgentRecovering) Type() uint32 { return shared.EventTypeAgentRecovering }

// Type 返回 agent 失败事件分发用的类型编号。
func (AgentFailed) Type() uint32 { return shared.EventTypeAgentFailed }
