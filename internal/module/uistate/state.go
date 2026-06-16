package uistate

import (
	"sort"
	"strings"
	"time"

	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate/timeline"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

type UIState struct {
	Threads                  []ThreadSummary                         `json:"threads"`
	Agents                   []AgentSummary                          `json:"agents"`
	ActiveTurn               *TurnSummary                            `json:"active_turn,omitempty"`
	RecentTurns              []TurnSummary                           `json:"recent_turns,omitempty"`
	TokenUsage               TokenUsage                              `json:"token_usage"`
	Statuses                 map[string]string                       `json:"statuses,omitempty"`
	InterruptibleByThread    map[string]bool                         `json:"interruptibleByThread,omitempty"`
	StatusHeadersByThread    map[string]string                       `json:"statusHeadersByThread,omitempty"`
	StatusDetailsByThread    map[string]string                       `json:"statusDetailsByThread,omitempty"`
	TokenUsageByThread       map[string]*uidto.ThreadPatchTokenUsage `json:"tokenUsageByThread,omitempty"`
	TokenUsages              map[string]TokenUsage                   `json:"-"`
	AgentMetaByID            map[string]map[string]any               `json:"agentMetaById,omitempty"`
	AgentRuntimeByID         map[string]map[string]any               `json:"agentRuntimeById,omitempty"`
	DiffTextByAgent          map[string]string                       `json:"diffTextByThread,omitempty"`
	DiffRevisionByAgent      map[string]int64                        `json:"diffRevisionByThread,omitempty"`
	TimelineByThread         map[string][]timeline.Item              `json:"timelinesByThread,omitempty"`
	ActivityStatsByThread    map[string]*ActivityStats               `json:"activityStatsByThread,omitempty"`
	AlertsByThread           map[string][]uidto.PatchAlert           `json:"alertsByThread,omitempty"`
	Unchanged                bool                                    `json:"unchanged,omitempty"`
	ActiveThreadID           string                                  `json:"activeThreadId,omitempty"`
	ActiveCmdThreadID        string                                  `json:"activeCmdThreadId,omitempty"`
	MainAgentID              string                                  `json:"mainAgentId,omitempty"`
	MainAgentState           string                                  `json:"mainAgentState,omitempty"`
	StallThresholdSec        int                                     `json:"-"`
	ShowInjectedPromptInChat *bool                                   `json:"settings.showInjectedPromptInChat,omitempty"`
	ViewPrefsChat            map[string]any                          `json:"viewPrefs.chat,omitempty"`
	ViewPrefsCmd             map[string]any                          `json:"viewPrefs.cmd,omitempty"`
	ThreadPinsChat           map[string]int64                        `json:"threadPins.chat,omitempty"`
	ThreadArchivesChat       map[string]int64                        `json:"threadArchives.chat,omitempty"`
	Groups                   []ThreadGroup                           `json:"groups,omitempty"`
}
type ThreadSummary struct {
	ID        string     `json:"id"`
	Name      string     `json:"name,omitempty"`
	AgentID   string     `json:"agent_id,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
	// LifecycleStatus is the DB lifecycle truth (created/stopped/archived).
	// State remains the UI/runtime union field and may be overwritten by
	// deriveThreadStatuses, so archive projection must not rely on State alone.
	LifecycleStatus string `json:"lifecycleStatus,omitempty"`
	State           string `json:"state,omitempty"`
	ThreadStatus    string `json:"threadStatus,omitempty"`
	AgentState      string `json:"agentState,omitempty"`
	LastMessage     string `json:"lastMessage,omitempty"`
	OverlayText     string `json:"overlayText,omitempty"`
	OverlayType     string `json:"overlayType,omitempty"`
	OverlayPriority int    `json:"overlayPriority,omitempty"`
}

type AgentSummary struct {
	ID               string     `json:"id"`
	Name             string     `json:"name,omitempty"`
	ThreadID         string     `json:"thread_id,omitempty"`
	ProviderThreadID string     `json:"provider_thread_id,omitempty"`
	ParentID         string     `json:"parent_id,omitempty"`
	State            string     `json:"state,omitempty"`
	Provider         string     `json:"provider,omitempty"`
	Model            string     `json:"model,omitempty"`
	CWD              string     `json:"cwd,omitempty"`
	Port             int        `json:"port,omitempty"`
	LogPath          string     `json:"logPath,omitempty"`
	CreatedAt        *time.Time `json:"createdAt,omitempty"`
	UpdatedAt        *time.Time `json:"updatedAt,omitempty"`
	LastReport       string     `json:"last_report,omitempty"`
	AgentState       string     `json:"agentState,omitempty"`
	ThreadStatus     string     `json:"threadStatus,omitempty"`
	LastMessage      string     `json:"lastMessage,omitempty"`
}

type TurnSummary struct {
	ID          string     `json:"id"`
	AgentID     string     `json:"agent_id"`
	ThreadID    string     `json:"thread_id,omitempty"`
	Status      string     `json:"status"`
	Success     *bool      `json:"success,omitempty"`
	Error       string     `json:"error,omitempty"`
	Reason      string     `json:"reason,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type TokenUsage struct {
	InputTokens         int     `json:"inputTokens"`
	OutputTokens        int     `json:"outputTokens"`
	TotalTokens         int     `json:"totalTokens"`
	UsedTokens          int     `json:"usedTokens"`
	ContextWindowTokens int     `json:"contextWindowTokens,omitempty"`
	UsedPercent         float64 `json:"usedPercent,omitempty"`
}

type ActivityStats struct {
	LSPCalls  int64            `json:"lspCalls"`
	Commands  int64            `json:"commands"`
	FileEdits int64            `json:"fileEdits"`
	ToolCalls map[string]int64 `json:"toolCalls,omitempty"`
}

type Sidebar struct {
	Threads               []ThreadSummary           `json:"threads"`
	Agents                []AgentSummary            `json:"agents"`
	ActiveTurn            *TurnSummary              `json:"active_turn,omitempty"`
	RecentTurns           []TurnSummary             `json:"recent_turns,omitempty"`
	Workspace             WorkspacePanel            `json:"workspace"`
	TokenUsage            TokenUsage                `json:"token_usage"`
	Statuses              map[string]string         `json:"statuses,omitempty"`
	InterruptibleByThread map[string]bool           `json:"interruptibleByThread,omitempty"`
	StatusHeadersByThread map[string]string         `json:"statusHeadersByThread,omitempty"`
	StatusDetailsByThread map[string]string         `json:"statusDetailsByThread,omitempty"`
	AgentRuntimeByID      map[string]map[string]any `json:"agentRuntimeById,omitempty"`
	ActiveThreadID        string                    `json:"activeThreadId,omitempty"`
	ActiveCmdThreadID     string                    `json:"activeCmdThreadId,omitempty"`
	MainAgentID           string                    `json:"mainAgentId,omitempty"`
	ViewPrefsChat         map[string]any            `json:"viewPrefs.chat,omitempty"`
	ViewPrefsCmd          map[string]any            `json:"viewPrefs.cmd,omitempty"`
	ThreadPinsChat        map[string]int64          `json:"threadPins.chat,omitempty"`
	ThreadArchivesChat    map[string]int64          `json:"threadArchives.chat,omitempty"`
	Groups                []ThreadGroup             `json:"groups,omitempty"`
}

type WorkspacePanel struct {
	Runs []WorkspaceRunSummary `json:"runs"`
}

type WorkspaceRunSummary struct {
	RunKey          string     `json:"run_key"`
	DagKey          string     `json:"dag_key,omitempty"`
	Status          string     `json:"status,omitempty"`
	SourceRoot      string     `json:"source_root,omitempty"`
	WorkspacePath   string     `json:"workspace_path,omitempty"`
	CreatedBy       string     `json:"created_by,omitempty"`
	UpdatedBy       string     `json:"updated_by,omitempty"`
	MergedFileCount int        `json:"merged_file_count,omitempty"`
	Conflicts       int        `json:"conflicts,omitempty"`
	Errors          int        `json:"errors,omitempty"`
	Message         string     `json:"message,omitempty"`
	UpdatedAt       *time.Time `json:"updated_at,omitempty"`
}

type Preferences struct {
	CWD                      string            `json:"cwd,omitempty"`
	Values                   map[string]any    `json:"values"`
	ActiveThreadID           string            `json:"active_thread_id,omitempty"`
	ActiveCmdThreadID        string            `json:"active_cmd_thread_id,omitempty"`
	MainAgentID              string            `json:"main_agent_id,omitempty"`
	StallThresholdSec        int               `json:"stall_threshold_sec,omitempty"`
	ShowInjectedPromptInChat *bool             `json:"show_injected_prompt_in_chat,omitempty"`
	ViewPrefs                ViewPrefs         `json:"view_prefs"`
	ThreadPins               ThreadCollections `json:"thread_pins"`
	ThreadArchives           ThreadCollections `json:"thread_archives"`
}

type ThreadGroup struct {
	Key     string          `json:"key"`
	Title   string          `json:"title"`
	Threads []ThreadSummary `json:"threads"`
}

type ViewPrefs struct {
	Chat map[string]any `json:"chat,omitempty"`
	Cmd  map[string]any `json:"cmd,omitempty"`
}

type ThreadCollections struct {
	Chat map[string]int64 `json:"chat,omitempty"`
	Cmd  map[string]int64 `json:"cmd,omitempty"`
}

func cloneState(value UIState) *UIState {
	return &UIState{
		Threads:                  cloneThreads(value.Threads),
		Agents:                   cloneAgents(value.Agents),
		ActiveTurn:               cloneTurn(value.ActiveTurn),
		RecentTurns:              cloneTurns(value.RecentTurns),
		TokenUsage:               value.TokenUsage,
		TokenUsages:              cloneTokenUsages(value.TokenUsages),
		DiffTextByAgent:          nil,
		DiffRevisionByAgent:      nil,
		ActivityStatsByThread:    cloneActivityStatsByThread(value.ActivityStatsByThread),
		Unchanged:                value.Unchanged,
		ActiveThreadID:           value.ActiveThreadID,
		ActiveCmdThreadID:        value.ActiveCmdThreadID,
		MainAgentID:              value.MainAgentID,
		StallThresholdSec:        value.StallThresholdSec,
		ShowInjectedPromptInChat: cloneBoolPtr(value.ShowInjectedPromptInChat),
		ViewPrefsChat:            kernel.CloneJSONMap(value.ViewPrefsChat),
		ViewPrefsCmd:             kernel.CloneJSONMap(value.ViewPrefsCmd),
		ThreadPinsChat:           cloneTimestampMap(value.ThreadPinsChat),
		ThreadArchivesChat:       cloneTimestampMap(value.ThreadArchivesChat),
		Groups:                   copyThreadGroups(value.Groups),
	}
}

func cloneSidebar(value Sidebar) *Sidebar {
	return &Sidebar{
		Threads:               cloneThreads(value.Threads),
		Agents:                cloneAgents(value.Agents),
		ActiveTurn:            cloneTurn(value.ActiveTurn),
		RecentTurns:           cloneTurns(value.RecentTurns),
		Workspace:             cloneWorkspacePanel(value.Workspace),
		TokenUsage:            value.TokenUsage,
		Statuses:              cloneStringMap(value.Statuses),
		InterruptibleByThread: cloneBoolMap(value.InterruptibleByThread),
		StatusHeadersByThread: cloneStringMap(value.StatusHeadersByThread),
		StatusDetailsByThread: cloneStringMap(value.StatusDetailsByThread),
		AgentRuntimeByID:      cloneRuntimeMap(value.AgentRuntimeByID),
		ActiveThreadID:        value.ActiveThreadID,
		ActiveCmdThreadID:     value.ActiveCmdThreadID,
		MainAgentID:           value.MainAgentID,
		ViewPrefsChat:         kernel.CloneJSONMap(value.ViewPrefsChat),
		ViewPrefsCmd:          kernel.CloneJSONMap(value.ViewPrefsCmd),
		ThreadPinsChat:        cloneTimestampMap(value.ThreadPinsChat),
		ThreadArchivesChat:    cloneTimestampMap(value.ThreadArchivesChat),
		Groups:                copyThreadGroups(value.Groups),
	}
}

func clonePreferences(value Preferences) *Preferences {
	return &Preferences{
		CWD:                      value.CWD,
		Values:                   kernel.CloneJSONMap(value.Values),
		ActiveThreadID:           value.ActiveThreadID,
		ActiveCmdThreadID:        value.ActiveCmdThreadID,
		MainAgentID:              value.MainAgentID,
		StallThresholdSec:        value.StallThresholdSec,
		ShowInjectedPromptInChat: cloneBoolPtr(value.ShowInjectedPromptInChat),
		ViewPrefs:                copyViewPrefs(value.ViewPrefs),
		ThreadPins:               copyThreadCollections(value.ThreadPins),
		ThreadArchives:           copyThreadCollections(value.ThreadArchives),
	}
}

func cloneThreads(items []ThreadSummary) []ThreadSummary {
	out := append([]ThreadSummary(nil), items...)
	for i := range out {
		out[i].CreatedAt = kernel.CloneTime(items[i].CreatedAt)
		out[i].UpdatedAt = kernel.CloneTime(items[i].UpdatedAt)
	}
	return out
}

func cloneAgents(items []AgentSummary) []AgentSummary {
	out := append([]AgentSummary(nil), items...)
	for i := range out {
		out[i].CreatedAt = kernel.CloneTime(items[i].CreatedAt)
		out[i].UpdatedAt = kernel.CloneTime(items[i].UpdatedAt)
	}
	return out
}

func cloneTokenUsages(m map[string]TokenUsage) map[string]TokenUsage {
	if m == nil {
		return nil
	}
	out := make(map[string]TokenUsage, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneTurns(items []TurnSummary) []TurnSummary {
	out := make([]TurnSummary, len(items))
	for i := range items {
		out[i] = items[i]
		out[i].StartedAt = kernel.CloneTime(items[i].StartedAt)
		out[i].CompletedAt = kernel.CloneTime(items[i].CompletedAt)
		if items[i].Success != nil {
			success := *items[i].Success
			out[i].Success = &success
		}
	}
	return out
}

func cloneTurn(value *TurnSummary) *TurnSummary {
	if value == nil {
		return nil
	}
	copied := *value
	copied.StartedAt = kernel.CloneTime(value.StartedAt)
	copied.CompletedAt = kernel.CloneTime(value.CompletedAt)
	if value.Success != nil {
		success := *value.Success
		copied.Success = &success
	}
	return &copied
}

func cloneWorkspacePanel(value WorkspacePanel) WorkspacePanel {
	return WorkspacePanel{Runs: cloneWorkspaceRuns(value.Runs)}
}

func cloneWorkspaceRuns(items []WorkspaceRunSummary) []WorkspaceRunSummary {
	out := make([]WorkspaceRunSummary, len(items))
	for i := range items {
		out[i] = items[i]
		out[i].UpdatedAt = kernel.CloneTime(items[i].UpdatedAt)
	}
	return out
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func upsertThreadSummary(items []ThreadSummary, next ThreadSummary) []ThreadSummary {
	next.ID = strings.TrimSpace(next.ID)
	if next.ID == "" {
		return items
	}
	for i := range items {
		if items[i].ID == next.ID {
			mergeThreadSummary(&items[i], next)
			return items
		}
	}
	return append(items, next)
}

func mergeThreadSummary(dst *ThreadSummary, src ThreadSummary) {
	dst.Name = chooseString(src.Name, dst.Name)
	dst.AgentID = chooseString(src.AgentID, dst.AgentID)
	dst.CreatedAt = chooseTime(src.CreatedAt, dst.CreatedAt)
	dst.UpdatedAt = chooseTime(src.UpdatedAt, dst.UpdatedAt)
	dst.LifecycleStatus = chooseString(src.LifecycleStatus, dst.LifecycleStatus)
	dst.State = chooseString(src.State, dst.State)
	dst.ThreadStatus = chooseString(src.ThreadStatus, dst.ThreadStatus)
	dst.AgentState = chooseString(src.AgentState, dst.AgentState)
	dst.LastMessage = chooseString(src.LastMessage, dst.LastMessage)
	if src.OverlayType != "" || src.OverlayText != "" || src.OverlayPriority > 0 {
		dst.OverlayText = src.OverlayText
		dst.OverlayType = src.OverlayType
		dst.OverlayPriority = src.OverlayPriority
	}
}

func upsertAgentSummary(items []AgentSummary, next AgentSummary) []AgentSummary {
	next.ID = strings.TrimSpace(next.ID)
	if next.ID == "" {
		return items
	}
	for i := range items {
		if items[i].ID != next.ID {
			continue
		}
		mergeAgentSummary(&items[i], next)
		return items
	}
	return append(items, next)
}

func mergeAgentSummary(dst *AgentSummary, src AgentSummary) {
	mergeAgentIdentity(dst, src)
	mergeAgentRuntime(dst, src)
	mergeAgentTurnInfo(dst, src)
}

func mergeAgentIdentity(dst *AgentSummary, src AgentSummary) {
	dst.Name = chooseString(src.Name, dst.Name)
	dst.ThreadID = chooseString(src.ThreadID, dst.ThreadID)
	dst.ParentID = chooseString(src.ParentID, dst.ParentID)
	dst.CreatedAt = chooseTime(src.CreatedAt, dst.CreatedAt)
	dst.UpdatedAt = chooseTime(src.UpdatedAt, dst.UpdatedAt)
}

func mergeAgentRuntime(dst *AgentSummary, src AgentSummary) {
	dst.State = chooseString(src.State, dst.State)
	dst.Provider = chooseString(src.Provider, dst.Provider)
	dst.ProviderThreadID = chooseString(src.ProviderThreadID, dst.ProviderThreadID)
	dst.Model = chooseString(src.Model, dst.Model)
	dst.CWD = chooseString(src.CWD, dst.CWD)
	dst.Port = choosePositiveInt(src.Port, dst.Port)
	dst.LogPath = chooseString(src.LogPath, dst.LogPath)
}

func mergeAgentTurnInfo(dst *AgentSummary, src AgentSummary) {
	dst.LastReport = chooseString(src.LastReport, dst.LastReport)
	dst.AgentState = chooseString(src.AgentState, dst.AgentState)
	dst.ThreadStatus = chooseString(src.ThreadStatus, dst.ThreadStatus)
	dst.LastMessage = chooseString(src.LastMessage, dst.LastMessage)
}

func sortThreads(items []ThreadSummary) {
	sort.SliceStable(items, func(i, j int) bool {
		leftCreatedAt, rightCreatedAt := summarySortTime(items[i].CreatedAt, items[i].UpdatedAt), summarySortTime(items[j].CreatedAt, items[j].UpdatedAt)
		if !leftCreatedAt.Equal(rightCreatedAt) {
			return leftCreatedAt.After(rightCreatedAt)
		}
		left, right := strings.TrimSpace(items[i].Name), strings.TrimSpace(items[j].Name)
		if left == right {
			return items[i].ID < items[j].ID
		}
		if left == "" || right == "" {
			return items[i].ID < items[j].ID
		}
		return left < right
	})
}

func sortAgents(items []AgentSummary) {
	sort.SliceStable(items, func(i, j int) bool {
		leftCreatedAt, rightCreatedAt := summarySortTime(items[i].CreatedAt, items[i].UpdatedAt), summarySortTime(items[j].CreatedAt, items[j].UpdatedAt)
		if !leftCreatedAt.Equal(rightCreatedAt) {
			return leftCreatedAt.After(rightCreatedAt)
		}
		if items[i].Name != items[j].Name && items[i].Name != "" && items[j].Name != "" {
			return items[i].Name < items[j].Name
		}
		return items[i].ID < items[j].ID
	})
}

func sortWorkspaceRuns(items []WorkspaceRunSummary) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := zeroTime(items[i].UpdatedAt), zeroTime(items[j].UpdatedAt)
		if !left.Equal(right) {
			return left.After(right)
		}
		return items[i].RunKey < items[j].RunKey
	})
}
func zeroTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func summarySortTime(createdAt, updatedAt *time.Time) time.Time {
	if t := zeroTime(createdAt); !t.IsZero() {
		return t
	}
	return zeroTime(updatedAt)
}

func nonZeroTimePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return kernel.CloneTime(&value)
}
