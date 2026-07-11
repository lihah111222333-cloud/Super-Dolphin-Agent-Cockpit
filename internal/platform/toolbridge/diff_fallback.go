package toolbridge

import (
	"context"
	"errors"
	"strings"
	"sync"

	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/difftracker"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// diffFallbackTracker 兜住主快照链路未能发布 diff 的工具结束事件。
// 只会在 patch_edit 调用、callID 未标记已发布、agent cwd 可解析且当前 git diff 非空时补发；
// 其它情况直接跳过，避免无关工具或未知工作区产生误报。
type diffFallbackTracker struct {
	emitter         difftracker.DiffEmitter
	resolver        difftracker.WorkDirResolver
	readCurrentDiff func(context.Context, string) (string, []string, bool)
	seen            sync.Map // map[string]struct{}，按 callID 去重主链路和补发链路的 diff。
	logger          *pkglogger.Logger
}

// newDiffFallbackTracker 装配 ToolCallEnd diff 补发器。
// logger 未由 Fx 注入时立即绑定全局 logger；warn 内部仍保留 nil 防护，保证测试或手工构造不会 panic。
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

// MarkSeen 标记某次工具调用已经发过 diff，避免 ToolCallEnd fallback 重复发送。
func (t *diffFallbackTracker) MarkSeen(callID string) {
	if t == nil {
		return
	}
	if callID = strings.TrimSpace(callID); callID != "" {
		t.seen.Store(callID, struct{}{})
	}
}

// handleToolCallEnd 在工具结束事件上执行 diff 补发判断。
// 该路径不能替代 begin/end 快照链路：它只读取当前工作区状态，遇到 cwd 缺失或空 diff 会放弃，
// 避免把其它并发改动包装成当前 tool call 的结果。
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

// hasSeen 判断 callID 是否已发过 diff；空 callID 不能参与去重。
func (t *diffFallbackTracker) hasSeen(callID string) bool {
	if callID == "" {
		return false
	}
	_, ok := t.seen.Load(callID)
	return ok
}

// resolveCWD 通过 agentID 找到工具调用所在工作目录。
// 解析失败只记录告警并停止补发，因为没有可信 cwd 时无法安全归因 diff。
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

// currentGitDiff 读取当前工作区 diff；测试可注入 readCurrentDiff 避免真实 git 依赖。
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

// shouldFallbackDiffTool 收窄允许触发补发的工具集合。
// 当前只允许 canonical patch_edit，后续扩展到其它写文件工具时必须确认其事件与 diff 归因边界。
func shouldFallbackDiffTool(toolName string) bool {
	switch canonicalToolName(toolName) {
	case "patch_edit":
		return true
	default:
		return false
	}
}

// fallbackDiffResult 生成补发用 DiffResult。
// 结果沿用 ToolCallEnd 的 agent/thread/call 归因；调用方必须先确认 cwd 和 diff 属于可信补发范围。
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

// warn 统一记录 fallback 告警。
// 正常构造会使用 Fx 注入或 newDiffFallbackTracker 绑定的 logger；nil receiver 场景回退全局 logger。
func (t *diffFallbackTracker) warn(msg string, args ...any) {
	logger := t.logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	logger.Warn(msg, args...)
}
