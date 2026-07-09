package uistate

import (
	"sort"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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

// pushRecentTurn 将 turn 合并进最近列表，并按更新时间倒序截断。
// 相同 turn 会被新快照替换，避免 sidebar 同时显示旧状态和新状态。
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

// markThreadStopped 将线程终态写入 sidebar 快照；deleted 会直接移除可见线程。
// 非 created 终态会清空 agent 关联，避免停止后的旧 agent 继续影响状态推导。
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

// normalizeToolName 归一化工具名，去掉 MCP namespace。
// 统计表只识别 grep/xref 等短名；不做归一化会让 MCP 工具调用落入默认分支，导致计数为零。
func normalizeToolName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	if s == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(s, "mcp__"); ok {
		if _, tool, ok := strings.Cut(rest, "__"); ok {
			return canonicalLSPToolName(tool)
		}
	}
	return canonicalLSPToolName(s)
}

// canonicalLSPToolName 规范化当前前端统计使用的 LSP 工具名。
func canonicalLSPToolName(name string) string {
	switch strings.TrimSpace(name) {
	case "patch_edit":
		return "patch_edit"
	default:
		return strings.TrimSpace(name)
	}
}

func isLSPActivityTool(name string) bool {
	trimmed := strings.TrimSpace(name)
	switch trimmed {
	case "file", "grep", "inspect", "xref", "structure", "patch_edit", "completion":
		return true
	default:
		return false
	}
}

func classifyToolActivity(toolName string) string {
	name := normalizeToolName(toolName)
	switch name {
	case "spawn_agent",
		"wait_agent",
		"send_input",
		"resume_agent",
		"close_agent":
		return "collab"
	case "":
		return ""
	default:
		if isOrchestrationCollabTool(name) {
			return "collab"
		}
		return "tool"
	}
}

func isOrchestrationCollabTool(name string) bool {
	switch canonicalOrchestrationToolName(name) {
	case "launch_agent", "send_message", "stop_agent", "get_agent_report", "list_agents":
		return true
	default:
		return false
	}
}

func canonicalOrchestrationToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	for _, canonical := range contract.OrchestrationToolCanonicalNames() {
		if trimmed == canonical {
			return canonical
		}
	}
	return trimmed
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

// shouldPreserveIdleThreadStatusLocked 判断是否保留当前 idle 状态。
// 当线程没有活跃 turn 或本地活动时，已完成/已中断的最近 turn 不应被空闲 agent 状态误改。
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

// resolveThreadIDByAgentLocked 通过 agentID 找回 sidebar 使用的公开 threadID。
// Codex 事件可能只携带 provider 侧线程标识，后续状态投影必须先收敛到公开线程。
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
