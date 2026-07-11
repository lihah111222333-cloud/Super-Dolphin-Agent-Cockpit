package uistate

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/clone"
	"path/filepath"
	"strings"
	"time"
)

const (
	overlayTypeMCPStartup = "mcp_startup"
	// 终端等待态最终通过 snapshot/patch 下发；实时事件只负责设置这层 overlay。
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

// buildAgentRuntimeEntry 将 AgentSummary 转成 sidebar runtime wire 字段。
// providerThreadId 优先使用 provider UUID，缺失时才回退公开 threadID，保持旧前端兼容。
func buildAgentRuntimeEntry(agent *AgentSummary, agentID, threadID string) map[string]any {
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
	// capabilities 是按 provider 静态推导的前端能力开关，不能从会话状态临时猜测。
	switch strings.ToLower(strings.TrimSpace(agent.Provider)) {
	case "codex":
		runtimeEntry["capabilities"] = []string{"context_compact", "model_switch"}
	default:
		runtimeEntry["capabilities"] = []string{}
	}
	return runtimeEntry
}

// buildLogPath 根据项目 CWD 生成 sidebar 兼容的日志目录。
// 该格式属于后端 wire 字段，前端只消费结果而不再承担路径推导。
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

// deriveThreadStatuses 为 sidebar 线程填充状态、agent 关联和最近错误信息。
// overlay 状态优先于运行态，避免启动/等待类提示被普通 agent 状态覆盖。
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

// deriveInterruptible 只为 sidebar 快照计算可中断状态。
// patch payload 和前端按钮还有独立 gate，不能把这个 map 当作全局中断权限来源。
func deriveInterruptible(sidebar *Sidebar) {
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

// deriveStatusHeaders 根据 overlay 或归一化状态生成 sidebar 标题和详情。
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

// sidebarThreadOverlay 返回线程 overlay 对应的状态、标题和详情。
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

// latestTurnsByThread 为每个线程选择最近的 turn，active turn 优先参与比较。
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

// sidebarThreadStatus 按生命周期、终止态、active turn、线程态和 agent 态推导展示状态。
// 顺序不能随意调整，否则 archived/error 等终态可能被运行中状态覆盖。
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

// lifecycleSidebarStatus 将线程生命周期终态映射为 sidebar 状态。
func lifecycleSidebarStatus(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "archived":
		return "archived", true
	default:
		return "", false
	}
}

// terminalSidebarStatus 将 archived/error/failed/stopped 等终止态锁定为 sidebar 展示状态。
// 一旦命中终止态，后续 agent 运行态不能再覆盖它，避免已结束线程被显示成运行中。
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

// normalizeSidebarStatus 将 provider/thread 原始状态映射为前端支持的状态集合。
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

// sidebarInterruptible 判断归一化状态是否允许前端展示 interrupt 控制。
func sidebarInterruptible(status string) bool {
	switch normalizeSidebarStatus(status) {
	case "starting", "thinking", "responding", "running", "editing", "waiting", "syncing":
		return true
	default:
		return false
	}
}

// sidebarStatusText 将 sidebar 状态映射为中文标题和详情文本。
// 错误态没有详情时给出固定提示，避免前端显示空错误说明。
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

// firstNonEmptyString 返回第一个非空白字符串，用于兼容多个旧字段来源。
func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}
