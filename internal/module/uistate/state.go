package uistate

import (
	"sort"
	"strings"
	"time"
)

type UIState struct {
	Threads            []ThreadSummary   `json:"threads"`
	Agents             []AgentSummary    `json:"agents"`
	ActiveTurn         *TurnSummary      `json:"active_turn,omitempty"`
	RecentTurns        []TurnSummary     `json:"recent_turns,omitempty"`
	TokenUsage         TokenUsage        `json:"token_usage"`
	ActiveThreadID     string            `json:"activeThreadId,omitempty"`
	ActiveCmdThreadID  string            `json:"activeCmdThreadId,omitempty"`
	MainAgentID        string            `json:"mainAgentId,omitempty"`
	ViewPrefsChat      map[string]any    `json:"viewPrefs.chat,omitempty"`
	ViewPrefsCmd       map[string]any    `json:"viewPrefs.cmd,omitempty"`
	ThreadPinsChat     map[string]int64  `json:"threadPins.chat,omitempty"`
	ThreadArchivesChat map[string]int64  `json:"threadArchives.chat,omitempty"`
	Groups             []ThreadGroup     `json:"groups,omitempty"`
}

type ThreadSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	AgentID string `json:"agent_id,omitempty"`
	State   string `json:"state,omitempty"`
}

type AgentSummary struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	ThreadID   string `json:"thread_id,omitempty"`
	ParentID   string `json:"parent_id,omitempty"`
	State      string `json:"state,omitempty"`
	Provider   string `json:"provider,omitempty"`
	CWD        string `json:"cwd,omitempty"`
	Port       int    `json:"port,omitempty"`
	LastReport string `json:"last_report,omitempty"`
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
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	TotalTokens         int `json:"total_tokens"`
	ContextWindowTokens int `json:"context_window_tokens,omitempty"`
}

type Sidebar struct {
	Threads            []ThreadSummary   `json:"threads"`
	Agents             []AgentSummary    `json:"agents"`
	ActiveTurn         *TurnSummary      `json:"active_turn,omitempty"`
	RecentTurns        []TurnSummary     `json:"recent_turns,omitempty"`
	Workspace          WorkspacePanel    `json:"workspace"`
	TokenUsage         TokenUsage        `json:"token_usage"`
	ActiveThreadID     string            `json:"activeThreadId,omitempty"`
	ActiveCmdThreadID  string            `json:"activeCmdThreadId,omitempty"`
	MainAgentID        string            `json:"mainAgentId,omitempty"`
	ViewPrefsChat      map[string]any    `json:"viewPrefs.chat,omitempty"`
	ViewPrefsCmd       map[string]any    `json:"viewPrefs.cmd,omitempty"`
	ThreadPinsChat     map[string]int64  `json:"threadPins.chat,omitempty"`
	ThreadArchivesChat map[string]int64  `json:"threadArchives.chat,omitempty"`
	Groups             []ThreadGroup     `json:"groups,omitempty"`
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
	CWD               string            `json:"cwd,omitempty"`
	Values            map[string]any    `json:"values"`
	ActiveThreadID    string            `json:"active_thread_id,omitempty"`
	ActiveCmdThreadID string            `json:"active_cmd_thread_id,omitempty"`
	MainAgentID       string            `json:"main_agent_id,omitempty"`
	ViewPrefs         ViewPrefs         `json:"view_prefs"`
	ThreadPins        ThreadCollections `json:"thread_pins"`
	ThreadArchives    ThreadCollections `json:"thread_archives"`
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
		Threads:            cloneThreads(value.Threads),
		Agents:             cloneAgents(value.Agents),
		ActiveTurn:         cloneTurn(value.ActiveTurn),
		RecentTurns:        cloneTurns(value.RecentTurns),
		TokenUsage:         value.TokenUsage,
		ActiveThreadID:     value.ActiveThreadID,
		ActiveCmdThreadID:  value.ActiveCmdThreadID,
		MainAgentID:        value.MainAgentID,
		ViewPrefsChat:      cloneJSONMap(value.ViewPrefsChat),
		ViewPrefsCmd:       cloneJSONMap(value.ViewPrefsCmd),
		ThreadPinsChat:     cloneTimestampMap(value.ThreadPinsChat),
		ThreadArchivesChat: cloneTimestampMap(value.ThreadArchivesChat),
		Groups:             cloneThreadGroups(value.Groups),
	}
}

func cloneSidebar(value Sidebar) *Sidebar {
	return &Sidebar{
		Threads:            cloneThreads(value.Threads),
		Agents:             cloneAgents(value.Agents),
		ActiveTurn:         cloneTurn(value.ActiveTurn),
		RecentTurns:        cloneTurns(value.RecentTurns),
		Workspace:          cloneWorkspacePanel(value.Workspace),
		TokenUsage:         value.TokenUsage,
		ActiveThreadID:     value.ActiveThreadID,
		ActiveCmdThreadID:  value.ActiveCmdThreadID,
		MainAgentID:        value.MainAgentID,
		ViewPrefsChat:      cloneJSONMap(value.ViewPrefsChat),
		ViewPrefsCmd:       cloneJSONMap(value.ViewPrefsCmd),
		ThreadPinsChat:     cloneTimestampMap(value.ThreadPinsChat),
		ThreadArchivesChat: cloneTimestampMap(value.ThreadArchivesChat),
		Groups:             cloneThreadGroups(value.Groups),
	}
}

func clonePreferences(value Preferences) *Preferences {
	return &Preferences{
		CWD:               value.CWD,
		Values:            cloneJSONMap(value.Values),
		ActiveThreadID:    value.ActiveThreadID,
		ActiveCmdThreadID: value.ActiveCmdThreadID,
		MainAgentID:       value.MainAgentID,
		ViewPrefs:         cloneViewPrefs(value.ViewPrefs),
		ThreadPins:        cloneThreadCollections(value.ThreadPins),
		ThreadArchives:    cloneThreadCollections(value.ThreadArchives),
	}
}

func cloneThreads(items []ThreadSummary) []ThreadSummary {
	return append([]ThreadSummary(nil), items...)
}

func cloneAgents(items []AgentSummary) []AgentSummary {
	return append([]AgentSummary(nil), items...)
}

func cloneTurns(items []TurnSummary) []TurnSummary {
	out := make([]TurnSummary, len(items))
	for i := range items {
		out[i] = items[i]
		out[i].StartedAt = cloneTime(items[i].StartedAt)
		out[i].CompletedAt = cloneTime(items[i].CompletedAt)
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
	copied.StartedAt = cloneTime(value.StartedAt)
	copied.CompletedAt = cloneTime(value.CompletedAt)
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
		out[i].UpdatedAt = cloneTime(items[i].UpdatedAt)
	}
	return out
}

func cloneTime(value *time.Time) *time.Time {
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
		if items[i].ID != next.ID {
			continue
		}
		if next.Name != "" {
			items[i].Name = next.Name
		}
		if next.AgentID != "" {
			items[i].AgentID = next.AgentID
		}
		if next.State != "" {
			items[i].State = next.State
		}
		return items
	}
	return append(items, next)
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
	if src.Name != "" {
		dst.Name = src.Name
	}
	if src.ThreadID != "" {
		dst.ThreadID = src.ThreadID
	}
	if src.ParentID != "" {
		dst.ParentID = src.ParentID
	}
	if src.State != "" {
		dst.State = src.State
	}
	if src.Provider != "" {
		dst.Provider = src.Provider
	}
	if src.CWD != "" {
		dst.CWD = src.CWD
	}
	if src.Port > 0 {
		dst.Port = src.Port
	}
	if src.LastReport != "" {
		dst.LastReport = src.LastReport
	}
}

func sortThreads(items []ThreadSummary) {
	sort.SliceStable(items, func(i, j int) bool {
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
