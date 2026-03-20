package ui

import "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"

// UIProjectionUpdated reports a projection snapshot revision change.
type UIProjectionUpdated struct {
	shared.UIProjectionHeader
	Revision int64 `json:"revision"`
}

// UITimelineAppended reports a new timeline item appended to a projection.
type UITimelineAppended struct {
	shared.UITurnHeader
	ItemID    string `json:"itemId"`
	ItemKind  string `json:"itemKind"`
	RequestID int64  `json:"requestId,omitempty"`
	CallID    string `json:"callId,omitempty"`
}

// UITokensUpdated reports token usage changes for a thread projection.
type UITokensUpdated struct {
	shared.UITurnHeader
	InputTokens         int `json:"inputTokens,omitempty"`
	OutputTokens        int `json:"outputTokens,omitempty"`
	TotalTokens         int `json:"totalTokens,omitempty"`
	ContextWindowTokens int `json:"contextWindowTokens,omitempty"`
}

func (UIProjectionUpdated) Type() uint32 { return shared.EventTypeUIProjectionUpdated }
func (UITimelineAppended) Type() uint32  { return shared.EventTypeUITimelineAppended }
func (UITokensUpdated) Type() uint32     { return shared.EventTypeUITokensUpdated }
