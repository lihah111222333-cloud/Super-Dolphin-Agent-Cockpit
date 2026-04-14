package provider

import (
	"encoding/json"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

type TurnRequest struct {
	LocalID              string          `json:"localId,omitempty"`
	ThreadID             string          `json:"threadId"`
	Inputs               []InputItem     `json:"inputs"`
	Skills               []SkillRef      `json:"skills,omitempty"`
	TurnAssembly         TurnAssembly    `json:"turnAssembly"`
	ManualSkillSelection bool            `json:"manualSkillSelection,omitempty"`
	OutputSchema         json.RawMessage `json:"outputSchema,omitempty"`
	Overrides            TurnOverrides   `json:"overrides"`
	MCP                  MCPManifest     `json:"mcp"`
}

type TurnOverrides struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

type InputItem = shareddto.InputItem

type SkillRef struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt,omitempty"`
}

type TurnResult struct {
	LocalID    string `json:"localId"`
	ProviderID string `json:"providerId,omitempty"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
}

type InterruptRequest struct {
	ThreadID string `json:"threadId"`
	Source   string `json:"source,omitempty"`
}

type SteerRequest struct {
	ThreadID             string          `json:"threadId"`
	ExpectedTurnID       string          `json:"expectedTurnId,omitempty"`
	Inputs               []InputItem     `json:"inputs"`
	Skills               []SkillRef      `json:"skills,omitempty"`
	TurnAssembly         TurnAssembly    `json:"turnAssembly"`
	ManualSkillSelection bool            `json:"manualSkillSelection,omitempty"`
	OutputSchema         json.RawMessage `json:"outputSchema,omitempty"`
	Overrides            TurnOverrides   `json:"overrides"`
}

type ForceCompleteRequest struct {
	ThreadID   string `json:"threadId"`
	ProviderID string `json:"providerId,omitempty"`
}

type ForkRequest struct {
	ThreadID string `json:"threadId"`
}

type ForkResult struct {
	NewThreadID string `json:"newThreadId"`
}
