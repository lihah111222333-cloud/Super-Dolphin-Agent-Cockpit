package turn

import "encoding/json"

type turnStartParams struct {
	ThreadID string   `json:"threadId"`
	Prompt   string   `json:"prompt,omitempty"`
	Images   []string `json:"images,omitempty"`
	Files    []string `json:"files,omitempty"`
	Model    string   `json:"model,omitempty"`
	Effort   string   `json:"effort,omitempty"`
}

type turnSteerParams struct {
	ThreadID string `json:"threadId"`
	Prompt   string `json:"prompt"`
}

type turnInterruptParams struct {
	ThreadID string `json:"threadId"`
	Source   string `json:"source,omitempty"`
}

type threadIDOnlyParams struct {
	ThreadID string `json:"threadId"`
}

type approvalRespondParams struct {
	CallID    string          `json:"callId,omitempty"`
	RequestID *int64          `json:"requestId,omitempty"`
	Approved  *bool           `json:"approved,omitempty"`
	Decision  json.RawMessage `json:"decision,omitempty"`
}

type turnInterruptResult struct {
	OK bool `json:"ok"`
}

type turnStartResult struct {
	TurnID string `json:"turnId"`
}
