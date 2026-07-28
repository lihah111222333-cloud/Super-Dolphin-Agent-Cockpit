package ui

import (
	"time"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
)

// UIProjectionUpdated 报告 UI projection 快照版本变化。
type UIProjectionUpdated struct {
	shared.UIProjectionHeader
	Revision int64 `json:"revision"`
}

// UITimelineAppended 报告 projection 追加了新的 timeline item。
type UITimelineAppended struct {
	shared.UITurnHeader
	ItemID    string `json:"item_id"`
	ItemKind  string `json:"item_kind"`
	RequestID int64  `json:"request_id,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
}

// UITokensUpdated 报告 thread projection 的 token 用量变化。
type UITokensUpdated struct {
	shared.UITurnHeader
	InputTokens         int `json:"input_tokens,omitempty"`
	OutputTokens        int `json:"output_tokens,omitempty"`
	TotalTokens         int `json:"total_tokens,omitempty"`
	ContextWindowTokens int `json:"context_window_tokens,omitempty"`
}

// SkillsChanged 报告本地 skill 清单变化，字段只暴露可展示元数据，避免泄漏完整宿主路径。
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

// UIPreferencesChanged 报告持久化偏好发生变化。
type UIPreferencesChanged struct {
	shared.EventHeader
	Cwd   string `json:"cwd,omitempty"`
	Key   string `json:"key"`
	Value any    `json:"value,omitempty"`
}

// UISharedFilesChanged 报告共享文件清单发生变化。
type UISharedFilesChanged struct {
	shared.EventHeader
	Path   string `json:"path,omitempty"`
	Action string `json:"action,omitempty"`
}

// UIMemoryChanged 报告持久化 memory 清单或状态变化。
type UIMemoryChanged struct {
	shared.EventHeader
	Action string `json:"action,omitempty"`
}

// UIPromptsChanged 报告 prompt 资产清单变化。
type UIPromptsChanged struct {
	shared.EventHeader
	Cwd       string `json:"cwd,omitempty"`
	PromptKey string `json:"promptKey,omitempty"`
	DraftKey  string `json:"draftKey,omitempty"`
	Action    string `json:"action,omitempty"`
}

// ThreadPatchThread 是 UIThreadPatch 内的 thread 基础信息片段。
type ThreadPatchThread struct {
	ID        string     `json:"id"`
	Name      string     `json:"name,omitempty"`
	State     string     `json:"state,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}

// ThreadPatchTokenUsage 是 UIThreadPatch 内的 token 用量片段。
type ThreadPatchTokenUsage struct {
	UsedTokens          int     `json:"usedTokens,omitempty"`
	ContextWindowTokens int     `json:"contextWindowTokens,omitempty"`
	UsedPercent         float64 `json:"usedPercent,omitempty"`
}

// ThreadPatchActiveTurn 是 UIThreadPatch 内的活动 turn 片段。
type ThreadPatchActiveTurn struct {
	ID          string     `json:"id"`
	ThreadID    string     `json:"threadId"`
	AgentID     string     `json:"agentId,omitempty"`
	Status      string     `json:"status,omitempty"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

// UIThreadPatch 报告 thread 运行态的定向增量 patch，允许 UI 只刷新变更片段。
type UIThreadPatch struct {
	ThreadID          string                 `json:"threadId"`
	Source            string                 `json:"source,omitempty"`
	Sequence          int64                  `json:"sequence,omitempty"`
	Generation        int64                  `json:"generation,omitempty"`
	Thread            *ThreadPatchThread     `json:"thread,omitempty"`
	Agent             *agentdto.BoardView    `json:"agent,omitempty"`
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

// Type 返回事件总线使用的稳定类型编号，保持 projection 更新事件可路由。
func (UIProjectionUpdated) Type() uint32 { return shared.EventTypeUIProjectionUpdated }

// Type 返回事件总线使用的稳定类型编号，保持 timeline 追加事件可路由。
func (UITimelineAppended) Type() uint32 { return shared.EventTypeUITimelineAppended }

// Type 返回事件总线使用的稳定类型编号，保持 token 用量更新事件可路由。
func (UITokensUpdated) Type() uint32 { return shared.EventTypeUITokensUpdated }

// Type 返回事件总线使用的稳定类型编号，保持 skill 清单更新事件可路由。
func (SkillsChanged) Type() uint32 { return shared.EventTypeUISkillsChanged }

// Type 返回事件总线使用的稳定类型编号，保持 thread patch 事件可路由。
func (UIThreadPatch) Type() uint32 { return shared.EventTypeUIThreadPatch }

// Type 返回事件总线使用的稳定类型编号，保持偏好更新事件可路由。
func (UIPreferencesChanged) Type() uint32 { return shared.EventTypeUIPreferencesChanged }

// Type 返回事件总线使用的稳定类型编号，保持共享文件更新事件可路由。
func (UISharedFilesChanged) Type() uint32 { return shared.EventTypeUISharedFilesChanged }

// Type 返回事件总线使用的稳定类型编号，保持 memory 更新事件可路由。
func (UIMemoryChanged) Type() uint32 { return shared.EventTypeUIMemoryChanged }

// Type 返回事件总线使用的稳定类型编号，保持 prompt 更新事件可路由。
func (UIPromptsChanged) Type() uint32 { return shared.EventTypeUIPromptsChanged }
