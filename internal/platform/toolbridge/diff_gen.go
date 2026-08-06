package toolbridge

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/difftracker"
)

// beginToolDiffSnapshot 在工具调用前捕获 git 快照。
// 只有可追踪的 patch_edit 调用且能确定 cwd 时才创建快照，避免无关工具产生额外 git 开销。
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

// emitToolDiff 在工具调用结束后发布相对 begin 快照的 diff。
// 只有主链路成功发出 diff 才标记 callID 已见；这样 ToolCallEnd fallback 只补漏，
// 不会和已发布的快照 diff 重复。
func (h *Handler) emitToolDiff(ctx context.Context, req ToolCallRequest, snapshot *difftracker.Snapshot) {
	if h == nil || snapshot == nil || h.emitter == nil {
		return
	}
	started := time.Now()
	diffText, files, err := difftracker.EmitGitDiff(ctx, snapshot)
	if err != nil {
		h.recordToolTrace(ctx, h.toolDiffTraceEvent(req, difftracker.DiffResult{Files: files}, time.Since(started), err))
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
		h.recordToolTrace(ctx, h.toolDiffTraceEvent(req, diff, time.Since(started), err))
		return
	}
	h.recordToolTrace(ctx, h.toolDiffTraceEvent(req, diff, time.Since(started), nil))
	if h.diffFallback != nil {
		h.diffFallback.MarkSeen(req.CallID)
	}
}

// shouldTrackDiff 判断工具调用是否需要 diff 追踪；目前只追踪带 patch 的 LSP patch_edit。
func shouldTrackDiff(toolName string, arguments json.RawMessage) bool {
	switch canonicalToolName(toolName) {
	case "patch_edit":
		return patchEditPatchIsDiff(arguments)
	}
	return false
}

// patchEditPatchIsDiff 从 LSP patch_edit 参数中确认 patch 非空，避免空 edit 被误当成文件改动。
func patchEditPatchIsDiff(arguments json.RawMessage) bool {
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
