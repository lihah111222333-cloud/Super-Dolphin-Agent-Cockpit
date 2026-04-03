package uistate

import (
	"strings"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func logFilePath() string { return pkglogger.CurrentLogFilePath() }

func copyThreadGroups(items []ThreadGroup) []ThreadGroup {
	out := make([]ThreadGroup, len(items))
	for i := range items {
		out[i] = ThreadGroup{Key: items[i].Key, Title: items[i].Title, Threads: cloneThreads(items[i].Threads)}
	}
	return out
}

func copyViewPrefs(value ViewPrefs) ViewPrefs {
	return ViewPrefs{Chat: cloneJSONMap(value.Chat), Cmd: cloneJSONMap(value.Cmd)}
}

func copyThreadCollections(value ThreadCollections) ThreadCollections {
	return ThreadCollections{Chat: cloneTimestampMap(value.Chat), Cmd: cloneTimestampMap(value.Cmd)}
}

func (s *service) threadLifecycleLocked(threadID string) string {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}
	for _, item := range s.state.Threads {
		if item.ID == threadID {
			return normalizeAgentLifecycleState(firstNonEmptyString(item.AgentState, item.ThreadStatus, item.State))
		}
	}
	return ""
}

func (s *service) agentLifecycleLocked(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ""
	}
	for _, item := range s.state.Agents {
		if item.ID == agentID {
			return normalizeAgentLifecycleState(firstNonEmptyString(item.AgentState, item.ThreadStatus, item.State))
		}
	}
	return ""
}

func (s *service) recentTurnExistsLocked(turnID string) bool {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return false
	}
	for _, item := range s.state.RecentTurns {
		if item.ID == turnID {
			return true
		}
	}
	return false
}

func launchState(current string) string {
	current = strings.TrimSpace(current)
	if current == "" || strings.EqualFold(current, "starting") {
		return "starting"
	}
	return current
}
