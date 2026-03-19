package runner

import "log/slog"

// AgentProcess holds the state for a single agent instance.
// Exported for use in state machine construction.
type AgentProcess struct {
	ID       string
	ThreadID string
	state    string
	logger   *slog.Logger
}

// State returns the current state.
func (p *AgentProcess) State() string { return p.state }

// SetState sets the state (used for initialization and testing).
func (p *AgentProcess) SetState(s string) { p.state = s }
