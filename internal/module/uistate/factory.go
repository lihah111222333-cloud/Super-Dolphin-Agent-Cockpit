package uistate

import (
	"strings"

	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/clone"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// applyMutation 统一 projection handler 的加锁、变更、构造 patch 和发事件流程。
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

// logFilePath 返回当前日志文件路径，供 UI 展示调试入口。
func logFilePath() string { return pkglogger.CurrentLogFilePath() }

// copyThreadGroups 深拷贝线程分组，避免快照调用方改写内部状态。
func copyThreadGroups(items []ThreadGroup) []ThreadGroup {
	out := make([]ThreadGroup, len(items))
	for i := range items {
		out[i] = ThreadGroup{Key: items[i].Key, Title: items[i].Title, Threads: cloneThreads(items[i].Threads)}
	}
	return out
}

// copyViewPrefs 深拷贝 chat/cmd 视图偏好。
func copyViewPrefs(value ViewPrefs) ViewPrefs {
	return ViewPrefs{Chat: clone.JSONMap(value.Chat), Cmd: clone.JSONMap(value.Cmd)}
}

// copyThreadCollections 深拷贝 chat/cmd 线程集合时间戳。
func copyThreadCollections(value ThreadCollections) ThreadCollections {
	return ThreadCollections{Chat: cloneTimestampMap(value.Chat), Cmd: cloneTimestampMap(value.Cmd)}
}

// threadLifecycleLocked 返回线程当前展示生命周期，调用方必须持有锁。
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

// agentLifecycleLocked 返回 agent 当前展示生命周期，调用方必须持有锁。
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

// recentTurnExistsLocked 判断 recent turn 列表中是否已有指定 turn。
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

// launchState 规范化 launch 中的空状态，保证 UI 至少看到 starting。
func launchState(current string) string {
	current = strings.TrimSpace(current)
	if current == "" || strings.EqualFold(current, "starting") {
		return "starting"
	}
	return current
}

// normalizeAgentLifecycleState 将后端生命周期状态映射为 UI 展示状态。
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

// sortedThreadPatchLocked 在发 patch 前稳定排序线程和 agent 列表。
func (s *service) sortedThreadPatchLocked(threadID, source string) uidto.UIThreadPatch {
	sortThreads(s.state.Threads)
	sortAgents(s.state.Agents)
	return s.threadPatchLocked(threadID, source)
}
