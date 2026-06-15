package uistate

import (
	"context"
	"strings"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
)

type diffStateRequest struct {
	threadID    string
	includeDiff bool
	known       int
}

type diffStateSnapshot struct {
	threadID string
	diffText string
	revision int64
}

type diffStateRequestKey struct{}

func withDiffStateRequest(ctx context.Context, threadID string, includeDiff bool, known int) context.Context {
	return context.WithValue(ctx, diffStateRequestKey{}, diffStateRequest{
		threadID:    threadID,
		includeDiff: includeDiff,
		known:       known,
	})
}

func diffStateRequestFromContext(ctx context.Context) diffStateRequest {
	if ctx == nil {
		return diffStateRequest{}
	}
	value, _ := ctx.Value(diffStateRequestKey{}).(diffStateRequest)
	return value
}

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

// applyToolDiffUpdatedLocked 应用工具diffupdatedlocked。
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

// applyDiffStateSnapshot 应用diff状态快照。
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

func (s *service) currentDiffTextLocked(threadID string) string {
	agentID := s.diffAgentIDLocked(threadID)
	if agentID == "" {
		return ""
	}
	return s.state.DiffTextByAgent[agentID]
}

func (s *service) currentDiffRevisionLocked(threadID string) int64 {
	agentID := s.diffAgentIDLocked(threadID)
	if agentID == "" {
		return 0
	}
	return s.state.DiffRevisionByAgent[agentID]
}

// diffAgentIDLocked 处理diff代理IDlocked。
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

// activeDiffAgentIDLocked 处理activediff代理IDlocked。
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
