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

// SkillsChanged reports local skill inventory mutations.
type SkillsChanged struct {
	shared.EventHeader
	SkillsDir string   `json:"skillsDir,omitempty"`
	Name      string   `json:"name,omitempty"`
	Action    string   `json:"action,omitempty"`
	Actions   []string `json:"actions,omitempty"`
	Count     int      `json:"count,omitempty"`
}

// UIPreferencesChanged reports a persisted preference mutation.
type UIPreferencesChanged struct {
	shared.EventHeader
	Cwd   string `json:"cwd,omitempty"`
	Key   string `json:"key"`
	Value any    `json:"value,omitempty"`
}

type ThreadPatchThread struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	State string `json:"state,omitempty"`
}

type ThreadPatchTokenUsage struct {
	UsedTokens          int     `json:"usedTokens,omitempty"`
	ContextWindowTokens int     `json:"contextWindowTokens,omitempty"`
	UsedPercent         float64 `json:"usedPercent,omitempty"`
}

// UIThreadPatch reports a targeted incremental runtime patch for a thread.
type UIThreadPatch struct {
	ThreadID          string                 `json:"threadId"`
	Source            string                 `json:"source,omitempty"`
	Sequence          int64                  `json:"sequence,omitempty"`
	Thread            *ThreadPatchThread     `json:"thread,omitempty"`
	Status            string                 `json:"status,omitempty"`
	StatusHeader      string                 `json:"statusHeader,omitempty"`
	StatusDetails     string                 `json:"statusDetails,omitempty"`
	OverlayText       string                 `json:"overlayText,omitempty"`
	OverlayType       string                 `json:"overlayType,omitempty"`
	OverlayPriority   int                    `json:"overlayPriority,omitempty"`
	TokenUsage        *ThreadPatchTokenUsage `json:"tokenUsage,omitempty"`
	DiffText          string                 `json:"diffText,omitempty"`
	DiffRevision      int64                  `json:"diffRevision,omitempty"`
	Interruptible     *bool                  `json:"interruptible,omitempty"`
	AgentMeta         map[string]any         `json:"agentMeta,omitempty"`
	ActivityStats     *PatchActivityStats    `json:"activityStats,omitempty"`
	Alerts            []PatchAlert           `json:"alerts,omitempty"`
	TimelineItems     []PatchTimelineItem    `json:"timelineItems,omitempty"`
	RemovedItemIds    []string               `json:"removedItemIds,omitempty"`
	TimelineOrder     []string               `json:"timelineOrder,omitempty"`
	Recover           bool                   `json:"recover,omitempty"`
	RefreshRequired   bool                   `json:"refreshRequired,omitempty"`
	FallbackReason    string                 `json:"fallbackReason,omitempty"`
	ActiveThreadID    string                 `json:"activeThreadId,omitempty"`
	ActiveCmdThreadID string                 `json:"activeCmdThreadId,omitempty"`
	MainAgentID       string                 `json:"mainAgentId,omitempty"`
	MainAgentState    string                 `json:"mainAgentState,omitempty"`
	Partial           bool                   `json:"partial,omitempty"`
}

func (UIProjectionUpdated) Type() uint32  { return shared.EventTypeUIProjectionUpdated }
func (UITimelineAppended) Type() uint32   { return shared.EventTypeUITimelineAppended }
func (UITokensUpdated) Type() uint32      { return shared.EventTypeUITokensUpdated }
func (SkillsChanged) Type() uint32        { return shared.EventTypeUISkillsChanged }
func (UIThreadPatch) Type() uint32        { return shared.EventTypeUIThreadPatch }
func (UIPreferencesChanged) Type() uint32 { return shared.EventTypeUIPreferencesChanged }
