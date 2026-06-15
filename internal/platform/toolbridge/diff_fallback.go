package toolbridge

import (
	"context"
	"errors"
	"strings"
	"sync"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// diffFallbackTracker subscribes to ToolCallEnd and emits a git diff fallback.
type diffFallbackTracker struct {
	emitter         difftracker.DiffEmitter
	resolver        difftracker.WorkDirResolver
	readCurrentDiff func(context.Context, string) (string, []string, bool)
	seen            sync.Map // map[string]struct{} — call IDs already emitted by Phase 1/fallback
	logger          *pkglogger.Logger
}

func newDiffFallbackTracker(
	emitter difftracker.DiffEmitter,
	resolver difftracker.WorkDirResolver,
	logger *pkglogger.Logger,
) *diffFallbackTracker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &diffFallbackTracker{emitter: emitter, resolver: resolver, logger: logger}
}

// MarkSeen is called by Phase 1 emitToolDiff after it emits a call diff.
// MarkSeen 标记seen。
func (t *diffFallbackTracker) MarkSeen(callID string) {
	if t == nil {
		return
	}
	if callID = strings.TrimSpace(callID); callID != "" {
		t.seen.Store(callID, struct{}{})
	}
}

// handleToolCallEnd handles ToolCallEnd events not already covered by Phase 1.
// handleToolCallEnd 处理工具callend。
func (t *diffFallbackTracker) handleToolCallEnd(ev tooldto.ToolCallEnd) {
	if t == nil || t.emitter == nil {
		return
	}
	callID := strings.TrimSpace(ev.CallID)
	if t.hasSeen(callID) || !shouldFallbackDiffTool(ev.ToolName) {
		return
	}
	ctx := context.Background()
	cwd, ok := t.resolveCWD(ctx, ev.AgentID)
	if !ok {
		return
	}
	diffText, files, ok := t.currentGitDiff(ctx, cwd)
	if !ok {
		return
	}
	if err := t.emitter(ctx, fallbackDiffResult(ev, callID, diffText, files)); err != nil {
		t.warn("toolbridge: diff fallback emit failed", "call_id", callID, "error", err)
		return
	}
	t.MarkSeen(callID)
}

func (t *diffFallbackTracker) hasSeen(callID string) bool {
	if callID == "" {
		return false
	}
	_, ok := t.seen.Load(callID)
	return ok
}

func (t *diffFallbackTracker) resolveCWD(ctx context.Context, agentID string) (string, bool) {
	if t.resolver == nil {
		return "", false
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", false
	}
	cwd, err := t.resolver.ResolveAgentCWD(ctx, agentID)
	if err != nil {
		t.warn("toolbridge: diff fallback cwd resolve failed", "agent_id", agentID, "error", err)
		return "", false
	}
	cwd = strings.TrimSpace(cwd)
	return cwd, cwd != ""
}

func (t *diffFallbackTracker) currentGitDiff(ctx context.Context, cwd string) (string, []string, bool) {
	if t.readCurrentDiff != nil {
		return t.readCurrentDiff(ctx, cwd)
	}
	diffText, files, err := difftracker.EmitCurrentGitDiff(ctx, cwd)
	if err != nil {
		if !errors.Is(err, difftracker.ErrNotGitRepository) {
			t.warn("toolbridge: diff fallback git diff failed", "cwd", cwd, "error", err)
		}
		return "", nil, false
	}
	return diffText, files, strings.TrimSpace(diffText) != ""
}

func shouldFallbackDiffTool(toolName string) bool {
	switch canonicalToolName(toolName) {
	case "edit":
		return true
	default:
		return false
	}
}

func fallbackDiffResult(ev tooldto.ToolCallEnd, callID, diffText string, files []string) difftracker.DiffResult {
	return difftracker.DiffResult{
		AgentID:  strings.TrimSpace(ev.AgentID),
		ThreadID: strings.TrimSpace(ev.ThreadID),
		CallID:   callID,
		ToolName: strings.TrimSpace(ev.ToolName),
		DiffText: diffText,
		Files:    files,
	}
}

func (t *diffFallbackTracker) warn(msg string, args ...any) {
	logger := t.logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	logger.Warn(msg, args...)
}
