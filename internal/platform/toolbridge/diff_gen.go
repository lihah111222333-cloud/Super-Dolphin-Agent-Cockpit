package toolbridge

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
)

func (h *Handler) beginToolDiffSnapshot(ctx context.Context, req ToolCallRequest) *difftracker.Snapshot {
	if h == nil || h.resolver == nil {
		return nil
	}
	if !shouldTrackDiff(req.Name, req.Arguments) || strings.TrimSpace(req.AgentID) == "" {
		return nil
	}
	cwd, err := h.resolver.ResolveAgentCWD(ctx, req.AgentID)
	if err != nil || cwd == "" {
		return nil
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
	switch strings.TrimSpace(toolName) {
	case "lsp_edit":
		return lspEditActionIsDiff(arguments)
	}
	return false
}

func lspEditActionIsDiff(arguments json.RawMessage) bool {
	action := lspEditAction(arguments)
	switch action {
	case "rename", "replace_range":
		return true
	}
	return false
}
