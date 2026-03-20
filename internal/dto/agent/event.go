package agent

import "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"

// StateChanged reports an agent lifecycle state transition.
type StateChanged struct {
	shared.AgentSessionHeader
	OldState string `json:"old_state"`
	NewState string `json:"new_state"`
	Trigger  string `json:"trigger"`
}

// AgentLaunched reports a new agent process or session becoming active.
type AgentLaunched struct {
	shared.AgentSessionHeader
	Model string `json:"model,omitempty"`
	CWD   string `json:"cwd,omitempty"`
}

// AgentStopped reports a graceful or forced agent stop.
type AgentStopped struct {
	shared.AgentSessionHeader
	Reason string `json:"reason,omitempty"`
}

// AgentRecovering reports a recovery attempt for an existing agent session.
type AgentRecovering struct {
	shared.AgentSessionHeader
	Reason  string `json:"reason"`
	Attempt int    `json:"attempt,omitempty"`
}

// AgentFailed reports a terminal agent failure.
type AgentFailed struct {
	shared.AgentSessionHeader
	Error       string `json:"error"`
	Recoverable bool   `json:"recoverable,omitempty"`
}

func (StateChanged) Type() uint32    { return shared.EventTypeAgentStateChanged }
func (AgentLaunched) Type() uint32   { return shared.EventTypeAgentLaunched }
func (AgentStopped) Type() uint32    { return shared.EventTypeAgentStopped }
func (AgentRecovering) Type() uint32 { return shared.EventTypeAgentRecovering }
func (AgentFailed) Type() uint32     { return shared.EventTypeAgentFailed }
