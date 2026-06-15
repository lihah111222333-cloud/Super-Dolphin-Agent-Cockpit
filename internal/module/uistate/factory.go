package uistate

import (
	"strings"

	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// applyMutation centralizes the lock/mutate/patch/unlock/emit flow used by projection handlers.
func applyMutation(s *service, threadID string, mutator func(), patchBuilder func() uidto.UIThreadPatch) {
	if s == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	s.mu.Lock()
	if mutator != nil {
		mutator()
	}
	patch := uidto.UIThreadPatch{}
	if patchBuilder != nil {
		patch = patchBuilder()
	} else if threadID != "" {
		patch.ThreadID = threadID
	}
	s.mu.Unlock()
	s.emitThreadPatchEvent(patch)
}

func logFilePath() string { return pkglogger.CurrentLogFilePath() }

func copyThreadGroups(items []ThreadGroup) []ThreadGroup {
	out := make([]ThreadGroup, len(items))
	for i := range items {
		out[i] = ThreadGroup{Key: items[i].Key, Title: items[i].Title, Threads: cloneThreads(items[i].Threads)}
	}
	return out
}

func copyViewPrefs(value ViewPrefs) ViewPrefs {
	return ViewPrefs{Chat: clone.JSONMap(value.Chat), Cmd: clone.JSONMap(value.Cmd)}
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

// normalizeAgentLifecycleState 规范化代理生命周期状态。
func normalizeAgentLifecycleState(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "provisioning", "turn_queued":
		return "starting"
	case "turn_starting":
		return "thinking"
	case "turn_running":
		return "running"
	case "awaiting_user_input":
		return "waiting"
	case "recovering":
		return "syncing"
	case "failed":
		return "error"
	case "stopping", "stopped", "idle":
		return "idle"
	default:
		return patchStatus(raw)
	}
}

func (s *service) sortedThreadPatchLocked(threadID, source string) uidto.UIThreadPatch {
	sortThreads(s.state.Threads)
	sortAgents(s.state.Agents)
	return s.threadPatchLocked(threadID, source)
}
