package uistate

import (
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
	"path/filepath"
	"strings"
	"time"
)

const (
	overlayTypeMCPStartup = "mcp_startup"
	// Terminal wait rendering is wired through snapshot/patch payloads, but a live
	// producer still needs raw terminal interaction events to set this overlay.
	overlayTypeTerminalWait     = "terminal_wait"
	overlayPriorityMCPStartup   = 40
	overlayPriorityTerminalWait = 90
	mcpStartupOverlayTTL        = 30 * time.Second
)

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func cloneBoolMap(input map[string]bool) map[string]bool {
	if len(input) == 0 {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func cloneInt64Map(input map[string]int64) map[string]int64 {
	if len(input) == 0 {
		return map[string]int64{}
	}
	out := make(map[string]int64, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func cloneRuntimeMap(input map[string]map[string]any) map[string]map[string]any {
	if len(input) == 0 {
		return map[string]map[string]any{}
	}
	out := make(map[string]map[string]any, len(input))
	for key, value := range input {
		out[key] = clone.JSONMap(value)
	}
	return out
}

func clearThreadOverlay(thread *ThreadSummary) {
	if thread == nil {
		return
	}
	thread.OverlayText = ""
	thread.OverlayType = ""
	thread.OverlayPriority = 0
}

func overlayStatus(overlayType string) string {
	switch strings.ToLower(strings.TrimSpace(overlayType)) {
	case overlayTypeMCPStartup:
		return "starting"
	case overlayTypeTerminalWait:
		return "waiting"
	default:
		return ""
	}
}

func overlayHeaderText(overlayType, text string) string {
	if text = strings.TrimSpace(text); text != "" {
		return text
	}
	switch strings.ToLower(strings.TrimSpace(overlayType)) {
	case overlayTypeMCPStartup:
		return "MCP 启动中"
	case overlayTypeTerminalWait:
		return "等待后台终端"
	default:
		return ""
	}
}

func overlayDetails(overlayType string) string {
	switch strings.ToLower(strings.TrimSpace(overlayType)) {
	case overlayTypeMCPStartup:
		return "正在初始化 MCP 服务"
	case overlayTypeTerminalWait:
		return "命令正在等待终端输入"
	default:
		return ""
	}
}

type sidebarAgentLookup struct {
	byID     map[string]*AgentSummary
	byThread map[string]*AgentSummary
}

func (s *service) fillSidebarDerivedLocked(sidebar *Sidebar) {
	if s == nil || sidebar == nil {
		return
	}
	resetSidebarDerived(sidebar)
	agents := deriveAgentRuntime(sidebar)
	recentByThread := latestTurnsByThread(sidebar.ActiveTurn, sidebar.RecentTurns)
	deriveThreadStatuses(sidebar, agents, recentByThread)
	deriveInterruptible(sidebar)
	deriveStatusHeaders(sidebar)
}

func resetSidebarDerived(sidebar *Sidebar) {
	sidebar.Statuses = map[string]string{}
	sidebar.InterruptibleByThread = map[string]bool{}
	sidebar.StatusHeadersByThread = map[string]string{}
	sidebar.StatusDetailsByThread = map[string]string{}
	sidebar.AgentRuntimeByID = map[string]map[string]any{}
}

func deriveAgentRuntime(sidebar *Sidebar) sidebarAgentLookup {
	agents := sidebarAgentLookup{
		byID:     make(map[string]*AgentSummary, len(sidebar.Agents)),
		byThread: make(map[string]*AgentSummary, len(sidebar.Agents)),
	}
	for i := range sidebar.Agents {
		agent := &sidebar.Agents[i]
		normalizeSidebarAgent(agent)
		agentID := strings.TrimSpace(agent.ID)
		threadID := strings.TrimSpace(agent.ThreadID)
		if agentID != "" {
			agents.byID[agentID] = agent
		}
		if threadID == "" {
			continue
		}
		agents.byThread[threadID] = agent
		sidebar.AgentRuntimeByID[threadID] = buildAgentRuntimeEntry(agent, agentID, threadID)
	}
	return agents
}

func normalizeSidebarAgent(agent *AgentSummary) {
	if agent.AgentState == "" {
		agent.AgentState = strings.TrimSpace(agent.State)
	}
	if agent.LastMessage == "" {
		agent.LastMessage = strings.TrimSpace(agent.LastReport)
	}
}

// buildAgentRuntimeEntry 构建代理运行时条目。
func buildAgentRuntimeEntry(agent *AgentSummary, agentID, threadID string) map[string]any {
	// providerThreadId: prefer codex UUID, fall back to public threadID
	providerTID := agent.ProviderThreadID
	if providerTID == "" {
		providerTID = threadID
	}
	runtimeEntry := map[string]any{
		"agentId":          agentID,
		"state":            agent.AgentState,
		"providerThreadId": providerTID,
	}
	if agent.Provider != "" {
		runtimeEntry["provider"] = agent.Provider
	}
	if agent.CWD != "" {
		runtimeEntry["cwd"] = agent.CWD
	}
	if agent.Port > 0 {
		runtimeEntry["port"] = agent.Port
	}
	if agent.Model != "" {
		runtimeEntry["model"] = agent.Model
	}
	if agent.LogPath != "" {
		runtimeEntry["logPath"] = agent.LogPath
	} else if agent.CWD != "" {
		runtimeEntry["logPath"] = buildLogPath(agent.CWD)
	}
	if agent.LastMessage != "" {
		runtimeEntry["lastMessage"] = agent.LastMessage
	}
	// Capabilities: derive from provider name (static per driver).
	// codex supports context_compact + model_switch; claude does not.
	switch strings.ToLower(strings.TrimSpace(agent.Provider)) {
	case "codex":
		runtimeEntry["capabilities"] = []string{"context_compact", "model_switch"}
	default:
		runtimeEntry["capabilities"] = []string{}
	}
	return runtimeEntry
}

// buildLogPath derives the conventional log directory from the project CWD.
// Matches the frontend's buildCwdLogPath in thread-copy-utils.js.
// buildLogPath 构建日志路径。
func buildLogPath(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || cwd == "." {
		return ""
	}
	name := filepath.Base(cwd)
	if name == "" || name == "." || name == "/" {
		return ""
	}
	return "~/.multi-agent/log/" + name + "/"
}

// deriveThreadStatuses 处理derive线程statuses。
func deriveThreadStatuses(sidebar *Sidebar, agents sidebarAgentLookup, recentByThread map[string]TurnSummary) {
	for i := range sidebar.Threads {
		thread := &sidebar.Threads[i]
		threadID := strings.TrimSpace(thread.ID)
		agent := resolveSidebarAgent(thread, agents)
		if thread.AgentID == "" && agent != nil {
			thread.AgentID = strings.TrimSpace(agent.ID)
		}
		status := sidebarThreadStatus(thread, agent, sidebar.ActiveTurn)
		if overlayStatus, _, _, ok := sidebarThreadOverlay(thread); ok {
			status = overlayStatus
		}
		thread.State = status
		thread.ThreadStatus = status
		if agent != nil {
			thread.AgentState = strings.TrimSpace(agent.AgentState)
			thread.LastMessage = firstNonEmptyString(thread.LastMessage, agent.LastMessage, agent.LastReport)
			agent.ThreadStatus = status
		}
		if turn, ok := recentByThread[threadID]; ok {
			thread.LastMessage = firstNonEmptyString(thread.LastMessage, turn.Error, turn.Reason)
		}
		sidebar.Statuses[threadID] = status
	}
}

func resolveSidebarAgent(thread *ThreadSummary, agents sidebarAgentLookup) *AgentSummary {
	threadID := strings.TrimSpace(thread.ID)
	if agent := agents.byThread[threadID]; agent != nil {
		return agent
	}
	agentID := strings.TrimSpace(thread.AgentID)
	if agentID == "" {
		return nil
	}
	return agents.byID[agentID]
}

// deriveInterruptible 处理deriveinterruptible。
func deriveInterruptible(sidebar *Sidebar) {
	// Sidebar snapshot gate only: patch payload interruptibility and frontend
	// controls are separate chains and must not infer coverage from this map.
	activeTurnID := ""
	activeThreadID := ""
	if sidebar.ActiveTurn != nil {
		activeTurnID = strings.TrimSpace(sidebar.ActiveTurn.ID)
		activeThreadID = strings.TrimSpace(sidebar.ActiveTurn.ThreadID)
	}
	for threadID, status := range sidebar.Statuses {
		threadIDMatch := activeThreadID != "" && activeThreadID == strings.TrimSpace(threadID)
		sidebar.InterruptibleByThread[threadID] = activeTurnID != "" && threadIDMatch && sidebarInterruptible(status)
	}
}

func deriveStatusHeaders(sidebar *Sidebar) {
	for i := range sidebar.Threads {
		thread := &sidebar.Threads[i]
		threadID := strings.TrimSpace(thread.ID)
		if _, header, details, ok := sidebarThreadOverlay(thread); ok {
			sidebar.StatusHeadersByThread[threadID] = header
			sidebar.StatusDetailsByThread[threadID] = details
			continue
		}
		header, details := sidebarStatusText(sidebar.Statuses[threadID], thread.LastMessage)
		sidebar.StatusHeadersByThread[threadID] = header
		sidebar.StatusDetailsByThread[threadID] = details
	}
}

func sidebarThreadOverlay(thread *ThreadSummary) (string, string, string, bool) {
	if thread == nil {
		return "", "", "", false
	}
	status := overlayStatus(thread.OverlayType)
	if status == "" {
		return "", "", "", false
	}
	return status, overlayHeaderText(thread.OverlayType, thread.OverlayText), overlayDetails(thread.OverlayType), true
}

// latestTurnsByThread 按线程处理latestturn。
func latestTurnsByThread(active *TurnSummary, items []TurnSummary) map[string]TurnSummary {
	out := make(map[string]TurnSummary, len(items)+1)
	if active != nil && strings.TrimSpace(active.ThreadID) != "" {
		out[strings.TrimSpace(active.ThreadID)] = *cloneTurn(active)
	}
	for _, item := range items {
		threadID := strings.TrimSpace(item.ThreadID)
		if threadID == "" {
			continue
		}
		current, ok := out[threadID]
		if !ok || recentTurnTime(current).Before(recentTurnTime(item)) {
			out[threadID] = item
		}
	}
	return out
}

// sidebarThreadStatus 处理sidebar线程状态。
func sidebarThreadStatus(thread *ThreadSummary, agent *AgentSummary, active *TurnSummary) string {
	if thread != nil {
		if status, ok := lifecycleSidebarStatus(thread.LifecycleStatus); ok {
			return status
		}
		if status, ok := terminalSidebarStatus(firstNonEmptyString(thread.ThreadStatus, thread.State)); ok {
			return status
		}
	}
	if active != nil && strings.TrimSpace(active.ThreadID) == strings.TrimSpace(thread.ID) {
		return normalizeSidebarStatus(firstNonEmptyString(active.Status, "running"))
	}
	if thread != nil {
		if raw := firstNonEmptyString(thread.ThreadStatus, thread.State); raw != "" {
			return normalizeSidebarStatus(raw)
		}
	}
	if agent != nil {
		if raw := firstNonEmptyString(agent.ThreadStatus, agent.AgentState, agent.State); raw != "" {
			return normalizeSidebarStatus(raw)
		}
	}
	return "idle"
}

func lifecycleSidebarStatus(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "archived":
		return "archived", true
	default:
		return "", false
	}
}

func terminalSidebarStatus(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "archived", "error", "failed":
		return normalizeSidebarStatus(raw), true
	case "stopped", "stopping":
		return "idle", true
	default:
		return "", false
	}
}

// normalizeSidebarStatus 规范化sidebar状态。
func normalizeSidebarStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "starting":
		return "starting"
	case "thinking":
		return "thinking"
	case "responding":
		return "responding"
	case "running":
		return "running"
	case "editing":
		return "editing"
	case "waiting":
		return "waiting"
	case "syncing", "recovering":
		return "syncing"
	case "archived":
		return "archived"
	case "error", "failed":
		return "error"
	default:
		return "idle"
	}
}

func sidebarInterruptible(status string) bool {
	switch normalizeSidebarStatus(status) {
	case "starting", "thinking", "responding", "running", "editing", "waiting", "syncing":
		return true
	default:
		return false
	}
}

// sidebarStatusText 处理sidebar状态文本。
func sidebarStatusText(status, lastMessage string) (string, string) {
	switch normalizeSidebarStatus(status) {
	case "starting":
		return "启动中", strings.TrimSpace(lastMessage)
	case "thinking", "responding", "running":
		return "工作中", strings.TrimSpace(lastMessage)
	case "editing":
		return "编辑中", strings.TrimSpace(lastMessage)
	case "waiting":
		return "等待确认", strings.TrimSpace(lastMessage)
	case "syncing":
		return "同步中", strings.TrimSpace(lastMessage)
	case "archived":
		return "已归档", strings.TrimSpace(lastMessage)
	case "error":
		details := firstNonEmptyString(lastMessage, "请查看最近输出")
		return "发生错误", details
	default:
		return "等待指示", strings.TrimSpace(lastMessage)
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}
