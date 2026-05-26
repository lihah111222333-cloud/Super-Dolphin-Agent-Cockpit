package toolbridge

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
)

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

func (h *Handler) emitToolDiff(ctx context.Context, req ToolCallRequest, snapshot *difftracker.Snapshot) {
	if h == nil || snapshot == nil || h.emitter == nil {
		return
	}
	diffText, files, err := difftracker.EmitGitDiff(ctx, snapshot)
	if err != nil || diffText == "" {
		return
	}
	if err := h.emitter(ctx, difftracker.DiffResult{
		AgentID:  req.AgentID,
		ThreadID: req.ThreadID,
		CallID:   req.CallID,
		ToolName: req.Name,
		DiffText: diffText,
		Files:    files,
	}); err != nil {
		return
	}
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
