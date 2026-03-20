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
	ItemID    string `json:"item_id"`
	ItemKind  string `json:"item_kind"`
	RequestID int64  `json:"request_id,omitempty"`
	CallID    string `json:"call_id,omitempty"`
}

// UITokensUpdated reports token usage changes for a thread projection.
type UITokensUpdated struct {
	shared.UITurnHeader
	InputTokens         int `json:"input_tokens,omitempty"`
	OutputTokens        int `json:"output_tokens,omitempty"`
	TotalTokens         int `json:"total_tokens,omitempty"`
	ContextWindowTokens int `json:"context_window_tokens,omitempty"`
}

func (UIProjectionUpdated) Type() uint32 { return shared.EventTypeUIProjectionUpdated }
func (UITimelineAppended) Type() uint32  { return shared.EventTypeUITimelineAppended }
func (UITokensUpdated) Type() uint32     { return shared.EventTypeUITokensUpdated }
