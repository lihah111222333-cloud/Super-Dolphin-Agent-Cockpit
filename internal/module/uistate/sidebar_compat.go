package uistate

import "strings"

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
		out[key] = cloneJSONMap(value)
	}
	return out
}

func (s *service) fillSidebarDerivedLocked(sidebar *Sidebar) {
	if s == nil || sidebar == nil {
		return
	}

	sidebar.Statuses = map[string]string{}
	sidebar.InterruptibleByThread = map[string]bool{}
	sidebar.StatusHeadersByThread = map[string]string{}
	sidebar.StatusDetailsByThread = map[string]string{}
	sidebar.AgentRuntimeByID = map[string]map[string]any{}

	agentsByID := make(map[string]*AgentSummary, len(sidebar.Agents))
	agentsByThread := make(map[string]*AgentSummary, len(sidebar.Agents))
	for i := range sidebar.Agents {
		agent := &sidebar.Agents[i]
		if agent.AgentState == "" {
			agent.AgentState = strings.TrimSpace(agent.State)
		}
		if agent.LastMessage == "" {
			agent.LastMessage = strings.TrimSpace(agent.LastReport)
		}
		agentID := strings.TrimSpace(agent.ID)
		threadID := strings.TrimSpace(agent.ThreadID)
		if agentID != "" {
			agentsByID[agentID] = agent
		}
		if threadID != "" {
			agentsByThread[threadID] = agent
			runtimeEntry := map[string]any{
				"agentId":          agentID,
				"state":            agent.AgentState,
				"providerThreadId": threadID,
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
			if agent.LastMessage != "" {
				runtimeEntry["lastMessage"] = agent.LastMessage
			}
			sidebar.AgentRuntimeByID[threadID] = runtimeEntry
		}
	}

	recentByThread := latestTurnsByThread(sidebar.ActiveTurn, sidebar.RecentTurns)
	for i := range sidebar.Threads {
		thread := &sidebar.Threads[i]
		threadID := strings.TrimSpace(thread.ID)
		agent := agentsByThread[threadID]
		if agent == nil && strings.TrimSpace(thread.AgentID) != "" {
			agent = agentsByID[strings.TrimSpace(thread.AgentID)]
		}
		if thread.AgentID == "" && agent != nil {
			thread.AgentID = strings.TrimSpace(agent.ID)
		}
		status := sidebarThreadStatus(thread, agent, sidebar.ActiveTurn)
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
		header, details := sidebarStatusText(status, thread.LastMessage)
		sidebar.Statuses[threadID] = status
		sidebar.InterruptibleByThread[threadID] = sidebarInterruptible(status)
		sidebar.StatusHeadersByThread[threadID] = header
		sidebar.StatusDetailsByThread[threadID] = details
	}
}

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

func sidebarThreadStatus(thread *ThreadSummary, agent *AgentSummary, active *TurnSummary) string {
	if active != nil && strings.TrimSpace(active.ThreadID) == strings.TrimSpace(thread.ID) {
		return normalizeSidebarStatus(firstNonEmptyString(active.Status, "running"))
	}
	if thread != nil {
		if status := normalizeSidebarStatus(firstNonEmptyString(thread.ThreadStatus, thread.State)); status != "" {
			return status
		}
	}
	if agent != nil {
		return normalizeSidebarStatus(firstNonEmptyString(agent.ThreadStatus, agent.AgentState, agent.State))
	}
	return "idle"
}

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
