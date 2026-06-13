package ui

import (
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

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
	ToolName  string `json:"tool_name,omitempty"`
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
	SkillsDir       string   `json:"skillsDir,omitempty"`
	Name            string   `json:"name,omitempty"`
	Action          string   `json:"action,omitempty"`
	Actions         []string `json:"actions,omitempty"`
	Count           int      `json:"count,omitempty"`
	Scope           string   `json:"scope,omitempty"`            // "project" | "system"
	PersonalType    string   `json:"personal_type,omitempty"`    // personal scope bucket, e.g. "user" | "agent"
	RepoFingerprint string   `json:"repo_fingerprint,omitempty"` // project scope only
	RelativePath    string   `json:"relative_path,omitempty"`    // project scope only, relative to repo root
	Cwd             string   `json:"cwd,omitempty"`              // deprecated: intentionally left empty to avoid leaking host paths
}

// UIPreferencesChanged reports a persisted preference mutation.
type UIPreferencesChanged struct {
	shared.EventHeader
	Cwd   string `json:"cwd,omitempty"`
	Key   string `json:"key"`
	Value any    `json:"value,omitempty"`
}

// UISharedFilesChanged reports shared file inventory mutations.
type UISharedFilesChanged struct {
	shared.EventHeader
	Path   string `json:"path,omitempty"`
	Action string `json:"action,omitempty"`
}

// UIMemoryChanged reports durable memory inventory or status mutations.
type UIMemoryChanged struct {
	shared.EventHeader
	Action string `json:"action,omitempty"`
}

// UIPromptsChanged reports prompt asset inventory mutations.
type UIPromptsChanged struct {
	shared.EventHeader
	Cwd       string `json:"cwd,omitempty"`
	PromptKey string `json:"promptKey,omitempty"`
	DraftKey  string `json:"draftKey,omitempty"`
	Action    string `json:"action,omitempty"`
}

type ThreadPatchThread struct {
	ID        string     `json:"id"`
	Name      string     `json:"name,omitempty"`
	State     string     `json:"state,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}

type ThreadPatchTokenUsage struct {
	UsedTokens          int     `json:"usedTokens,omitempty"`
	ContextWindowTokens int     `json:"contextWindowTokens,omitempty"`
	UsedPercent         float64 `json:"usedPercent,omitempty"`
}

type ThreadPatchActiveTurn struct {
	ID          string     `json:"id"`
	ThreadID    string     `json:"threadId"`
	AgentID     string     `json:"agentId,omitempty"`
	Status      string     `json:"status,omitempty"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
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
	ActiveTurn        *ThreadPatchActiveTurn `json:"activeTurn,omitempty"`
	AgentMeta         map[string]any         `json:"agentMeta,omitempty"`
	AgentRuntime      map[string]any         `json:"agentRuntime,omitempty"`
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

// Type 返回事件分发用的类型编号。
func (UIProjectionUpdated) Type() uint32 { return shared.EventTypeUIProjectionUpdated }

// Type 返回事件分发用的类型编号。
func (UITimelineAppended) Type() uint32 { return shared.EventTypeUITimelineAppended }

// Type 返回事件分发用的类型编号。
func (UITokensUpdated) Type() uint32 { return shared.EventTypeUITokensUpdated }

// Type 返回事件分发用的类型编号。
func (SkillsChanged) Type() uint32 { return shared.EventTypeUISkillsChanged }

// Type 返回事件分发用的类型编号。
func (UIThreadPatch) Type() uint32 { return shared.EventTypeUIThreadPatch }

// Type 返回事件分发用的类型编号。
func (UIPreferencesChanged) Type() uint32 { return shared.EventTypeUIPreferencesChanged }

// Type 返回事件分发用的类型编号。
func (UISharedFilesChanged) Type() uint32 { return shared.EventTypeUISharedFilesChanged }

// Type 返回事件分发用的类型编号。
func (UIMemoryChanged) Type() uint32 { return shared.EventTypeUIMemoryChanged }

// Type 返回事件分发用的类型编号。
func (UIPromptsChanged) Type() uint32 { return shared.EventTypeUIPromptsChanged }
