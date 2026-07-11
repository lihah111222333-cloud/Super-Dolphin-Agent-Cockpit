package uistate

import (
	"context"
	"strings"

	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
)

// diffStateRequest 描述一次 UI state 请求是否需要携带 diff 以及前端已知 revision。
type diffStateRequest struct {
	threadID    string
	includeDiff bool
	known       int
}

// diffStateSnapshot 是读取锁内 diff 状态后传给快照输出阶段的副本。
type diffStateSnapshot struct {
	threadID string
	diffText string
	revision int64
}

// diffStateRequestKey 是 context 中保存 diff 请求参数的私有 key。
type diffStateRequestKey struct{}

// withDiffStateRequest 把 diff 请求参数注入 context，供 GetState 快照阶段读取。
func withDiffStateRequest(ctx context.Context, threadID string, includeDiff bool, known int) context.Context {
	return context.WithValue(ctx, diffStateRequestKey{}, diffStateRequest{
		threadID:    threadID,
		includeDiff: includeDiff,
		known:       known,
	})
}

// diffStateRequestFromContext 从 context 取回 diff 请求参数，缺失时返回零值。
func diffStateRequestFromContext(ctx context.Context) diffStateRequest {
	if ctx == nil {
		return diffStateRequest{}
	}
	value, _ := ctx.Value(diffStateRequestKey{}).(diffStateRequest)
	return value
}

// applyToolDiffUpdated 把工具 diff 事件写入 UI state，并在变更时发线程 patch。
func (s *service) applyToolDiffUpdated(ev tooldto.ToolDiffUpdated) {
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	if threadID == "" || agentID == "" {
		return
	}
	changed := false
	applyMutation(s, threadID, func() {
		changed = s.applyToolDiffUpdatedLocked(agentID, threadID, ev.DiffText, ev.Revision)
	}, func() uidto.UIThreadPatch {
		if !changed {
			return uidto.UIThreadPatch{}
		}
		return s.sortedThreadPatchLocked(threadID, "tool/diffUpdated")
	})
}

// applyToolDiffUpdatedLocked 在锁内更新指定 agent 的 diff 文本和 revision。
func (s *service) applyToolDiffUpdatedLocked(agentID, threadID, diffText string, revision int64) bool {
	agentID = strings.TrimSpace(agentID)
	threadID = strings.TrimSpace(threadID)
	if agentID == "" || threadID == "" {
		return false
	}
	if s.state.DiffTextByAgent == nil {
		s.state.DiffTextByAgent = map[string]string{}
	}
	if s.state.DiffRevisionByAgent == nil {
		s.state.DiffRevisionByAgent = map[string]int64{}
	}
	currentDiff := s.state.DiffTextByAgent[agentID]
	currentRevision := s.state.DiffRevisionByAgent[agentID]
	if currentDiff == diffText {
		if revision > currentRevision {
			s.state.DiffRevisionByAgent[agentID] = revision
			return true
		}
		return false
	}
	s.state.DiffTextByAgent[agentID] = diffText
	if revision > 0 {
		s.state.DiffRevisionByAgent[agentID] = revision
	} else {
		s.state.DiffRevisionByAgent[agentID]++
	}
	return true
}

// diffStateSnapshot 在读锁内提取请求线程当前应返回的 diff 文本。
func (s *service) diffStateSnapshot(ctx context.Context) diffStateSnapshot {
	req := diffStateRequestFromContext(ctx)
	threadID := strings.TrimSpace(req.threadID)
	if !req.includeDiff || threadID == "" {
		return diffStateSnapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	agentID := s.diffAgentIDLocked(threadID)
	if agentID == "" {
		return diffStateSnapshot{threadID: threadID}
	}
	return diffStateSnapshot{
		threadID: threadID,
		diffText: s.state.DiffTextByAgent[agentID],
		revision: s.state.DiffRevisionByAgent[agentID],
	}
}

// applyDiffStateSnapshot 将 diff 快照写入 UIState；revision 未变化时返回 unchanged。
func applyDiffStateSnapshot(ctx context.Context, snapshot *UIState, current diffStateSnapshot) {
	if snapshot == nil || current.threadID == "" {
		return
	}
	req := diffStateRequestFromContext(ctx)
	snapshot.DiffTextByAgent = map[string]string{current.threadID: ""}
	snapshot.DiffRevisionByAgent = map[string]int64{current.threadID: 0}
	if current.diffText == "" {
		snapshot.Unchanged = false
		return
	}
	snapshot.DiffRevisionByAgent[current.threadID] = current.revision
	if req.known > 0 && int64(req.known) == current.revision {
		snapshot.DiffTextByAgent = map[string]string{}
		snapshot.Unchanged = true
		return
	}
	snapshot.DiffTextByAgent[current.threadID] = current.diffText
	snapshot.Unchanged = false
}

// currentDiffTextLocked 返回当前线程对应 agent 的 diff 文本，调用方必须持有锁。
func (s *service) currentDiffTextLocked(threadID string) string {
	agentID := s.diffAgentIDLocked(threadID)
	if agentID == "" {
		return ""
	}
	return s.state.DiffTextByAgent[agentID]
}

// currentDiffRevisionLocked 返回当前线程对应 agent 的 diff revision，调用方必须持有锁。
func (s *service) currentDiffRevisionLocked(threadID string) int64 {
	agentID := s.diffAgentIDLocked(threadID)
	if agentID == "" {
		return 0
	}
	return s.state.DiffRevisionByAgent[agentID]
}

// diffAgentIDLocked 选择承载线程 diff 的 agent，优先 active 主 agent，其次线程摘要和列表匹配。
func (s *service) diffAgentIDLocked(threadID string) string {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}
	if agentID := s.activeDiffAgentIDLocked(threadID); agentID != "" {
		return agentID
	}
	if summary, ok := s.threadSummaryLocked(threadID); ok {
		if agentID := strings.TrimSpace(summary.AgentID); agentID != "" {
			return agentID
		}
	}
	for _, agent := range s.state.Agents {
		if strings.TrimSpace(agent.ThreadID) == threadID {
			return strings.TrimSpace(agent.ID)
		}
	}
	return ""
}

// activeDiffAgentIDLocked 只在请求线程是当前 active chat/cmd 线程时返回主 agent。
func (s *service) activeDiffAgentIDLocked(threadID string) string {
	if threadID != strings.TrimSpace(s.state.ActiveThreadID) && threadID != strings.TrimSpace(s.state.ActiveCmdThreadID) {
		return ""
	}
	mainAgentID := strings.TrimSpace(s.mainAgentIDLocked())
	if mainAgentID == "" {
		return ""
	}
	for _, agent := range s.state.Agents {
		if strings.TrimSpace(agent.ID) == mainAgentID && strings.TrimSpace(agent.ThreadID) == threadID {
			return mainAgentID
		}
	}
	return ""
}

// cloneActivityStatsByThread 深拷贝按线程聚合的活动统计。
func cloneActivityStatsByThread(input map[string]*ActivityStats) map[string]*ActivityStats {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]*ActivityStats, len(input))
	for threadID, stats := range input {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			continue
		}
		out[threadID] = cloneActivityStats(stats)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// cloneActivityStats 深拷贝单个线程活动统计。
func cloneActivityStats(input *ActivityStats) *ActivityStats {
	if input == nil {
		return nil
	}
	return &ActivityStats{
		LSPCalls:  input.LSPCalls,
		Commands:  input.Commands,
		FileEdits: input.FileEdits,
		ToolCalls: cloneInt64Map(input.ToolCalls),
	}
}

// threadActivityStatsLocked 返回线程活动统计桶，不存在时在锁内创建。
func (s *service) threadActivityStatsLocked(threadID string) *ActivityStats {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	if s.state.ActivityStatsByThread == nil {
		s.state.ActivityStatsByThread = map[string]*ActivityStats{}
	}
	if current := s.state.ActivityStatsByThread[threadID]; current != nil {
		return current
	}
	current := &ActivityStats{}
	s.state.ActivityStatsByThread[threadID] = current
	return current
}

// patchActivityStats 将内部活动统计转换为 UI patch DTO。
func patchActivityStats(input *ActivityStats) *uidto.PatchActivityStats {
	if input == nil {
		return nil
	}
	return &uidto.PatchActivityStats{
		LSPCalls:  input.LSPCalls,
		Commands:  input.Commands,
		FileEdits: input.FileEdits,
		ToolCalls: cloneInt64Map(input.ToolCalls),
	}
}
