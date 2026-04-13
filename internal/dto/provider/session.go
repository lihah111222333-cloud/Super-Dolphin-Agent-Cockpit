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
	Provider         string            `json:"provider"`
	AgentID          string            `json:"agentId"`
	ThreadID         string            `json:"threadId"`
	ProviderThreadID string            `json:"providerThreadId,omitempty"`
	Path             string            `json:"path,omitempty"`
	CWD              string            `json:"cwd,omitempty"`
	Model            string            `json:"model,omitempty"`
	Effort           string            `json:"effort,omitempty"`
	ConfigOverride   ThreadConfigPatch `json:"configOverride,omitempty"`
}

var _ json.Marshaler = json.RawMessage(nil)
