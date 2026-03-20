package provider

import "encoding/json"

type StartSessionRequest struct {
	Provider     string         `json:"provider"`
	AgentID      string         `json:"agentId"`
	CWD          string         `json:"cwd"`
	Model        string         `json:"model,omitempty"`
	Instructions string         `json:"instructions,omitempty"`
	Config       map[string]any `json:"config,omitempty"`
}

type ResumeSessionRequest struct {
	Provider string `json:"provider"`
	AgentID  string `json:"agentId"`
	ThreadID string `json:"threadId"`
	Model    string `json:"model,omitempty"`
}

var _ json.Marshaler = json.RawMessage(nil)
