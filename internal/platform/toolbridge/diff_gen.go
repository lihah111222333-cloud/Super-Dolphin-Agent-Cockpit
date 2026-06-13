package toolbridge

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
)

// beginToolDiffSnapshot 处理begin工具diff快照。
func (h *Handler) beginToolDiffSnapshot(ctx context.Context, req ToolCallRequest) *difftracker.Snapshot {
	if h == nil {
		return nil
	}
	if !shouldTrackDiff(req.Name, req.Arguments) || strings.TrimSpace(req.AgentID) == "" {
		return nil
	}
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		if h.resolver == nil {
			return nil
		}
		resolved, err := h.resolver.ResolveAgentCWD(ctx, req.AgentID)
		if err != nil {
			return nil
		}
		cwd = strings.TrimSpace(resolved)
		if cwd == "" {
			return nil
		}
	}
	snapshot, _ := difftracker.BeginSnapshot(ctx, cwd)
	return snapshot
}

// emitToolDiff 处理emit工具diff。
func (h *Handler) emitToolDiff(ctx context.Context, req ToolCallRequest, snapshot *difftracker.Snapshot) {
	if h == nil || snapshot == nil || h.emitter == nil {
		return
	}
	started := time.Now()
	diffText, files, err := difftracker.EmitGitDiff(ctx, snapshot)
	if err != nil {
		h.recordToolTrace(ctx, toolDiffTraceEvent(req, difftracker.DiffResult{Files: files}, time.Since(started), err))
		return
	}
	if diffText == "" {
		return
	}
	diff := difftracker.DiffResult{
		AgentID:  req.AgentID,
		ThreadID: req.ThreadID,
		CallID:   req.CallID,
		ToolName: req.Name,
		DiffText: diffText,
		Files:    files,
	}
	if err := h.emitter(ctx, diff); err != nil {
		h.recordToolTrace(ctx, toolDiffTraceEvent(req, diff, time.Since(started), err))
		return
	}
	h.recordToolTrace(ctx, toolDiffTraceEvent(req, diff, time.Since(started), nil))
	if h.diffFallback != nil {
		h.diffFallback.MarkSeen(req.CallID)
	}
}

// shouldTrackDiff 判断工具调用是否需要 diff 追踪
func shouldTrackDiff(toolName string, arguments json.RawMessage) bool {
	switch canonicalToolName(toolName) {
	case "edit":
		return lspEditPatchIsDiff(arguments)
	}
	return false
}

func lspEditPatchIsDiff(arguments json.RawMessage) bool {
	if len(strings.TrimSpace(string(arguments))) == 0 {
		return false
	}
	var payload struct {
		Patch string `json:"patch"`
	}
	if err := json.Unmarshal(arguments, &payload); err != nil {
		return false
	}
	return strings.TrimSpace(payload.Patch) != ""
}
