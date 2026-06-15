package uistate

import (
	"sort"
	"strings"
	"time"

	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
)

type threadActivity struct {
	turnDepth          int
	commandDepth       int
	editDepth          int
	toolDepth          int
	approvalDepth      int
	inputApprovalDepth int
	collabDepth        int
}

// pushRecentTurn 处理pushrecentturn。
func pushRecentTurn(items []TurnSummary, next TurnSummary, limit int) []TurnSummary {
	next.ID = strings.TrimSpace(next.ID)
	if next.ID == "" {
		return items
	}
	updated := false
	for i := range items {
		if items[i].ID != next.ID {
			continue
		}
		items[i] = next
		updated = true
		break
	}
	if !updated {
		items = append(items, next)
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := recentTurnTime(items[i]), recentTurnTime(items[j])
		if !left.Equal(right) {
			return left.After(right)
		}
		return items[i].ID < items[j].ID
	})
	if limit > 0 && len(items) > limit {
		items = append([]TurnSummary(nil), items[:limit]...)
	}
	return items
}

// markThreadStopped 标记线程stopped。
func markThreadStopped(items []ThreadSummary, threadID, status string) []ThreadSummary {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return items
	}
	if status = strings.TrimSpace(status); status == "" {
		status = "stopped"
	}
	if strings.EqualFold(status, "deleted") {
		return removeThreadSummary(items, threadID)
	}
	for i := range items {
		if items[i].ID != threadID {
			continue
		}
		if !strings.EqualFold(status, "created") {
			items[i].AgentID = ""
		}
		items[i].LifecycleStatus = status
		items[i].State = status
		items[i].ThreadStatus = status
		return items
	}
	return append(items, ThreadSummary{ID: threadID, LifecycleStatus: status, State: status})
}

func removeThreadSummary(items []ThreadSummary, threadID string) []ThreadSummary {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || len(items) == 0 {
		return items
	}
	out := items[:0]
	for _, item := range items {
		if item.ID == threadID {
			continue
		}
		out = append(out, item)
	}
	return out
}

func recentTurnTime(value TurnSummary) time.Time {
	if value.CompletedAt != nil {
		return *value.CompletedAt
	}
	return zeroTime(value.StartedAt)
}

func (s *service) threadActivityLocked(threadID string) *threadActivity {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	if s.activityByThread == nil {
		s.activityByThread = map[string]*threadActivity{}
	}
	if current := s.activityByThread[threadID]; current != nil {
		return current
	}
	current := &threadActivity{}
	s.activityByThread[threadID] = current
	return current
}

func (s *service) clearThreadActivityLocked(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || s.activityByThread == nil {
		return
	}
	delete(s.activityByThread, threadID)
}

func (s *service) clearActiveTurnLocked(threadID string) {
	if s.state.ActiveTurn == nil {
		return
	}
	if strings.TrimSpace(s.state.ActiveTurn.ThreadID) == strings.TrimSpace(threadID) {
		s.state.ActiveTurn = nil
	}
}

func (rt *threadActivity) startTurn() {
	if rt == nil {
		return
	}
	rt.turnDepth = 1
	rt.commandDepth = 0
	rt.editDepth = 0
	rt.toolDepth = 0
	rt.collabDepth = 0
}

func adjustDepth(current, delta int) int {
	if current+delta <= 0 {
		return 0
	}
	return current + delta
}

func classifyItemActivity(itemType, rawType, command, file string) string {
	if strings.TrimSpace(file) != "" {
		return "editing"
	}
	if strings.TrimSpace(command) != "" {
		return "command"
	}
	joined := strings.ToLower(strings.TrimSpace(itemType + " " + rawType))
	switch {
	case strings.Contains(joined, "file"):
		return "editing"
	case strings.Contains(joined, "command"):
		return "command"
	default:
		return "command"
	}
}

// normalizeToolName strips the MCP namespace prefix ("mcp__<server>__"),
// lowercases the tool name, and maps legacy LSP names to the canonical short
// form. Runtime-emitted ToolName may carry the full MCP method
// (e.g. mcp__lsp__lsp_grep or mcp__lsp__grep), while the classification tables
// and prefix gates here use short names (e.g. grep). Without this normalization
// every MCP-served tool falls
// through into the default branch and counters silently zero out.
func normalizeToolName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "mcp__") {
		rest := strings.TrimPrefix(s, "mcp__")
		if i := strings.Index(rest, "__"); i >= 0 {
			return canonicalLSPToolName(rest[i+2:])
		}
	}
	return canonicalLSPToolName(s)
}

// canonicalLSPToolName 处理canonicalLSP工具名称。
func canonicalLSPToolName(name string) string {
	switch strings.TrimSpace(name) {
	case "lsp_file":
		return "file"
	case "lsp_grep":
		return "grep"
	case "lsp_inspect":
		return "inspect"
	case "lsp_xref":
		return "xref"
	case "lsp_structure":
		return "structure"
	case "lsp_edit":
		return "edit"
	case "lsp_completion":
		return "completion"
	case "lsp_format_preview":
		return "format_preview"
	default:
		return strings.TrimSpace(name)
	}
}

func isLSPActivityTool(name string) bool {
	trimmed := strings.TrimSpace(name)
	switch trimmed {
	case "file", "grep", "inspect", "xref", "structure", "edit", "completion", "format_preview":
		return true
	default:
		return strings.HasPrefix(trimmed, "lsp_")
	}
}

func classifyToolActivity(toolName string) string {
	switch normalizeToolName(toolName) {
	case "spawn_agent",
		"wait_agent",
		"send_input",
		"resume_agent",
		"close_agent",
		"orchestration_launch_agent",
		"orchestration_send_message",
		"orchestration_stop_agent",
		"orchestration_get_agent_report",
		"orchestration_list_agents":
		return "collab"
	case "":
		return ""
	default:
		return "tool"
	}
}

func hasApprovalActivity(rt *threadActivity) bool {
	return rt != nil && rt.approvalDepth > 0
}

func hasEditingActivity(rt *threadActivity) bool {
	return rt != nil && rt.editDepth > 0
}

func hasRunningActivity(rt *threadActivity) bool {
	return rt != nil && (rt.commandDepth > 0 || rt.toolDepth > 0 || rt.collabDepth > 0)
}

func hasThinkingActivity(rt *threadActivity) bool {
	return rt != nil && rt.turnDepth > 0
}

func deriveActivityStatus(rt *threadActivity) string {
	switch {
	case hasApprovalActivity(rt):
		return "waiting"
	case hasEditingActivity(rt):
		return "editing"
	case hasRunningActivity(rt):
		return "running"
	case hasThinkingActivity(rt):
		return "thinking"
	default:
		return ""
	}
}

func fallbackThreadStatus(agentState string) string {
	switch agentState {
	case "error":
		return "error"
	case "waiting", "running", "editing", "thinking", "starting", "syncing":
		return agentState
	default:
		return "idle"
	}
}

func deriveThreadStatus(rt *threadActivity, agentState string) string {
	agentState = normalizeAgentLifecycleState(agentState)
	if status := deriveActivityStatus(rt); status != "" {
		return status
	}
	return fallbackThreadStatus(agentState)
}

func (s *service) hasActiveTurnForThreadLocked(threadID string) bool {
	return s.state.ActiveTurn != nil && strings.TrimSpace(s.state.ActiveTurn.ThreadID) == threadID
}

func (s *service) hasLocalThreadActivityLocked(threadID string) bool {
	activity := s.activityByThread[threadID]
	return hasApprovalActivity(activity) ||
		hasEditingActivity(activity) ||
		hasRunningActivity(activity) ||
		hasThinkingActivity(activity)
}

func shouldPreserveIdleAgentState(rawAgentState string) bool {
	switch strings.ToLower(strings.TrimSpace(rawAgentState)) {
	case "", "idle", "stopped", "stopping":
		return true
	default:
		return false
	}
}

// shouldPreserveIdleThreadStatusLocked 判断preserveidle线程状态locked是否可用。
func (s *service) shouldPreserveIdleThreadStatusLocked(threadID, currentStatus, rawAgentState string) bool {
	if currentStatus != "idle" || s.hasActiveTurnForThreadLocked(threadID) || s.hasLocalThreadActivityLocked(threadID) {
		return false
	}
	if shouldPreserveIdleAgentState(rawAgentState) {
		return true
	}
	for _, item := range s.state.RecentTurns {
		if strings.TrimSpace(item.ThreadID) == threadID {
			return strings.EqualFold(item.Status, "completed") || strings.EqualFold(item.Status, "interrupted")
		}
	}
	return false
}

func (s *service) threadIDForAgentLocked(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ""
	}
	for _, item := range s.state.Agents {
		if strings.TrimSpace(item.ID) == agentID {
			return strings.TrimSpace(item.ThreadID)
		}
	}
	return ""
}

func (s *service) agentIDForThreadLocked(threadID string) string {
	if current, ok := s.threadSummaryLocked(threadID); ok {
		return strings.TrimSpace(current.AgentID)
	}
	return ""
}

func (s *service) resolveDerivedStateIDsLocked(threadID, agentID string) (string, string) {
	threadID = strings.TrimSpace(threadID)
	agentID = strings.TrimSpace(agentID)
	if threadID == "" {
		threadID = s.threadIDForAgentLocked(agentID)
	}
	if agentID == "" {
		agentID = s.agentIDForThreadLocked(threadID)
	}
	return threadID, agentID
}

func (s *service) agentSummaryLocked(agentID string) (AgentSummary, bool) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return AgentSummary{}, false
	}
	for _, item := range s.state.Agents {
		if strings.TrimSpace(item.ID) == agentID {
			return item, true
		}
	}
	return AgentSummary{}, false
}

// resolveThreadIDByAgentLocked finds the public thread ID for the given agent.
// Codex events use providerThreadID as ThreadID; this resolves back to the
// public ID that the sidebar uses.
func (s *service) resolveThreadIDByAgentLocked(agentID string) string {
	if summary, ok := s.agentSummaryLocked(agentID); ok {
		if tid := strings.TrimSpace(summary.ThreadID); tid != "" {
			return tid
		}
	}
	return ""
}

func (s *service) rawAgentStateForThreadLocked(threadID, agentID string) string {
	if summary, ok := s.agentSummaryLocked(agentID); ok {
		return firstNonEmptyString(summary.State, summary.AgentState, summary.ThreadStatus)
	}
	if current, ok := s.threadSummaryLocked(threadID); ok {
		return firstNonEmptyString(current.AgentState, current.ThreadStatus, current.State)
	}
	return ""
}

func (s *service) currentThreadStatusLocked(threadID string) string {
	if current, ok := s.threadSummaryLocked(threadID); ok {
		return patchStatus(firstNonEmptyString(current.ThreadStatus, current.State))
	}
	return ""
}

func (s *service) applyDerivedThreadStateLocked(threadID, agentID, agentState, status string) {
	s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
		ID:           threadID,
		AgentID:      agentID,
		State:        status,
		ThreadStatus: status,
		AgentState:   agentState,
	})
	if agentID != "" {
		s.state.Agents = upsertAgentSummary(s.state.Agents, AgentSummary{
			ID:           agentID,
			ThreadID:     threadID,
			AgentState:   agentState,
			ThreadStatus: status,
		})
	}
	if s.hasActiveTurnForThreadLocked(threadID) {
		s.state.ActiveTurn.Status = status
	}
}

func (s *service) updateDerivedThreadStateLocked(threadID, agentID string) string {
	threadID, agentID = s.resolveDerivedStateIDsLocked(threadID, agentID)
	if threadID == "" {
		return ""
	}
	rawAgentState := s.rawAgentStateForThreadLocked(threadID, agentID)
	agentState := normalizeAgentLifecycleState(rawAgentState)
	status := deriveThreadStatus(s.threadActivityLocked(threadID), agentState)
	if s.shouldPreserveIdleThreadStatusLocked(threadID, s.currentThreadStatusLocked(threadID), rawAgentState) {
		status = "idle"
	}
	s.applyDerivedThreadStateLocked(threadID, agentID, agentState, status)
	return status
}

func (s *service) refreshThreadPatchLocked(threadID, agentID, source string) uidto.UIThreadPatch {
	status := s.updateDerivedThreadStateLocked(threadID, agentID)
	patch := s.sortedThreadPatchLocked(threadID, source)
	applyPatchStatus(&patch, status)
	return patch
}
