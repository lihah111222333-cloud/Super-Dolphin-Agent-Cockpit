package archtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type errorPronePatternGuard struct {
	repoRoot   string
	violations []Violation
}

func postScanViolations(repoRoot string, scanRoots []string, stats map[string]*packageStat) []Violation {
	violations := deadKeyViolations(repoRoot, scanRoots, stats)
	return append(violations, errorPronePatternViolations(repoRoot)...)
}

func errorPronePatternViolations(repoRoot string) []Violation {
	if !isSuperAgentV3Repo(repoRoot) {
		return nil
	}
	g := &errorPronePatternGuard{repoRoot: repoRoot}
	g.guardHighPrecisionTimestampParsing()
	g.guardStreamingStateMachinePattern()
	g.guardLongLivedSubscriptionPattern()
	g.guardClaimBeforeExternalSideEffectPattern()
	g.guardAsyncStateTransitionFencePattern()
	g.guardFailClosedPreparationPattern()
	g.guardAtomicConfigPatchPattern()
	return g.violations
}

func isSuperAgentV3Repo(repoRoot string) bool {
	data, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "module github.com/anthropic-ai/super-agent-v3")
}

func (g *errorPronePatternGuard) guardHighPrecisionTimestampParsing() {
	const rel = "cmd/agent-terminal/frontend/vue-app/stores/thread-time-utils.js"
	body, ok := g.functionBody(rel, "parseThreadCreatedAtFromID")
	if !ok {
		return
	}
	g.requireFunctionContains(rel, "parseThreadCreatedAtFromID", body,
		"high-precision timestamp ids must be truncated to millisecond precision before numeric parsing",
		"chunk.length < 10 || chunk.length > 19",
		"chunk.slice(0, 13)",
	)
	g.requireFunctionNotContains(rel, "parseThreadCreatedAtFromID", body,
		"high-precision timestamp ids must not be parsed as full-width JavaScript numbers",
		"parseEpochMillis(chunk)",
		"Number(chunk)",
	)
}

func (g *errorPronePatternGuard) guardStreamingStateMachinePattern() {
	const syncRel = "cmd/agent-terminal/frontend/vue-app/stores/thread-sync-helpers.js"
	g.requireContains(syncRel, "append-only streaming state machines must mark terminal placeholder state",
		"turnCompletedSignal && activeThreadTarget",
		"streamingFinalized: true",
	)
	g.requireContains(syncRel, "temporary streaming item ids must include the owning scope, not just wall-clock fallback",
		"evt?.payload?.turnId || evt?.payload?.turn_id",
		"${activeThreadTarget}-${turnId}-streaming",
		"${activeThreadTarget}-stream-${Date.now()}-streaming",
	)
	g.requireContains(syncRel, "streaming deltas must hydrate atomically instead of replacing local append buffers with partial snapshots",
		"syncThreadHistoryAtomic(ctx, activeThreadTarget)",
		"state.sync.streaming_throttle.failed",
		"state.sync.streaming_trailing.failed",
	)

	const markdownRel = "cmd/agent-terminal/frontend/vue-app/utils/assistant-markdown-streaming.js"
	g.requireContains(markdownRel, "append-only streaming state machines must report shrink/vanish regressions",
		"chat.streaming.text_vanished",
		"chat.streaming.text_shrunk",
	)
	g.requireContains(markdownRel, "terminal streaming items must clear pending/displayed state",
		"item?.done !== false || !itemId",
		"state.displayedByItemId.delete(itemId)",
		"state.pendingByItemId.delete(itemId)",
	)
}

func (g *errorPronePatternGuard) guardLongLivedSubscriptionPattern() {
	const rel = "cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go"
	g.requireContains(rel, "long-lived fx subscriptions must own an independent cancellable context",
		"context.WithCancel(context.Background())",
		"bus.ResilientSubscribe",
		"lifecycleCtx.Err()",
		"cancelSub()",
	)
}

func (g *errorPronePatternGuard) guardClaimBeforeExternalSideEffectPattern() {
	const rel = "cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go"
	g.requireContains(rel, "external writes must have a narrow claim/fence entrypoint",
		"type nodeOutputMaterializationClaimer interface",
		"ClaimNodeOutputMaterialization(context.Context, taskdag.OutputMaterializationClaimInput)",
		"claimNodeOutputMaterialization",
	)
	g.requireOrderInFunction(rel, "materializeSharedfileAfterClaim",
		"external side effects must claim durable state before writing files",
		"claimNodeOutputMaterialization(",
		"writeAgentTurnSharedfile(",
	)
}

func (g *errorPronePatternGuard) guardAsyncStateTransitionFencePattern() {
	const subscriberRel = "cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go"
	g.requireContains(subscriberRel, "asynchronous terminal event consumers must short-circuit already terminal rows before writes",
		"isTerminalNodeStatus",
		"IncIdempotentSkipped",
	)

	const runtimeSQLRel = "cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql"
	g.requireContains(runtimeSQLRel, "asynchronous completion fences must accept pre-running/running/verification states",
		"status IN ('ready', 'running', 'awaiting_verify')",
	)
	const writeSQLRel = "cmd/mcp-orch/sql/queries/task_dag_node_write.sql"
	g.requireContains(writeSQLRel, "side-effect claim fences must reject terminal rows while accepting active race states",
		"ClaimTaskDagNodeOutputMaterialization",
		"status IN ('ready', 'running', 'awaiting_verify')",
	)
}

func (g *errorPronePatternGuard) guardFailClosedPreparationPattern() {
	const rel = "cmd/mcp-orch/orchestration/retry_strategy.go"
	g.requireContains(rel, "preparation failures after a claim must fail closed instead of leaving ambiguous in-flight state",
		"failSmartRetryPrepare",
		"smart retry prepare failed:",
		"failSmartRetry(ctx, w, fence, failure, reason, failFast)",
	)
	g.requireContains(rel, "unsupported declared strategies must be terminal and visible",
		"failUnsupportedSmartRetryStrategy",
		"unsupported smart retry strategy",
	)
}

func (g *errorPronePatternGuard) guardAtomicConfigPatchPattern() {
	const retryRel = "cmd/mcp-orch/orchestration/retry_strategy.go"
	g.requireContains(retryRel, "multi-resource retry preparation must use a narrow atomic store port",
		"SmartRetryConfigStore",
		"RetryWakeupWithNodeConfigPatch",
	)

	const storeRel = "cmd/mcp-orch/store/taskdag/store_wakeup.go"
	g.requireContains(storeRel, "multi-resource retry preparation must combine retry state and config CAS in one transaction",
		"sqlc.WithTxOrReuse",
		"RetryTaskDagWakeup",
		"PatchTaskDagNodeConfigIfUnchanged",
		"wrapTaskDAGError(err, \"retry_with_config_patch\", \"task_dag_wakeup\")",
	)

	const contractRel = "cmd/mcp-orch/store/taskdag/contract.go"
	g.requireContains(contractRel, "callers must depend on a narrow atomic patch port rather than broad overwrite semantics",
		"type SmartRetryConfigStore interface",
		"RetryWakeupWithNodeConfigPatch(ctx context.Context, input RetryWakeupWithNodeConfigPatchInput) (int64, error)",
	)
}

func (g *errorPronePatternGuard) requireContains(rel, note string, tokens ...string) {
	content, ok := g.read(rel)
	if !ok {
		return
	}
	for _, token := range tokens {
		if strings.Contains(content, token) {
			continue
		}
		g.addViolation(rel, 1, "%s: missing %q", note, token)
	}
}

func (g *errorPronePatternGuard) requireFunctionContains(rel, name, body, note string, tokens ...string) {
	for _, token := range tokens {
		if strings.Contains(body, token) {
			continue
		}
		g.addViolation(rel, 1, "%s (%s): missing %q", note, name, token)
	}
}

func (g *errorPronePatternGuard) requireFunctionNotContains(rel, name, body, note string, tokens ...string) {
	for _, token := range tokens {
		if !strings.Contains(body, token) {
			continue
		}
		g.addViolation(rel, lineNumber(body, token), "%s (%s): forbidden %q", note, name, token)
	}
}

func (g *errorPronePatternGuard) requireOrderInFunction(rel, name, note, before, after string) {
	body, ok := g.functionBody(rel, name)
	if !ok {
		return
	}
	beforeIdx := strings.Index(body, before)
	afterIdx := strings.Index(body, after)
	switch {
	case beforeIdx < 0:
		g.addViolation(rel, 1, "%s (%s): missing %q", note, name, before)
	case afterIdx < 0:
		g.addViolation(rel, 1, "%s (%s): missing %q", note, name, after)
	case beforeIdx > afterIdx:
		g.addViolation(rel, lineNumber(body, after), "%s (%s): %q must appear before %q", note, name, before, after)
	}
}

func (g *errorPronePatternGuard) functionBody(rel, name string) (string, bool) {
	content, ok := g.read(rel)
	if !ok {
		return "", false
	}
	idx := functionStartIndex(content, name)
	if idx < 0 {
		g.addViolation(rel, 1, "pattern guard: missing function %s", name)
		return "", false
	}
	open := strings.Index(content[idx:], "{")
	if open < 0 {
		g.addViolation(rel, lineNumber(content, name), "pattern guard: function %s has no body", name)
		return "", false
	}
	start := idx + open
	depth := 0
	for pos := start; pos < len(content); pos++ {
		switch content[pos] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return content[start : pos+1], true
			}
		}
	}
	g.addViolation(rel, lineNumber(content, name), "pattern guard: function %s body is not closed", name)
	return "", false
}

func functionStartIndex(content, name string) int {
	candidates := []string{"function " + name, "func " + name}
	best := -1
	for _, candidate := range candidates {
		idx := strings.Index(content, candidate)
		if idx >= 0 && (best < 0 || idx < best) {
			best = idx
		}
	}
	return best
}

func (g *errorPronePatternGuard) read(rel string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(g.repoRoot, filepath.FromSlash(rel)))
	if err != nil {
		g.addViolation(rel, 1, "pattern guard: cannot read file: %v", err)
		return "", false
	}
	return string(data), true
}

func (g *errorPronePatternGuard) addViolation(rel string, line int, format string, args ...any) {
	g.violations = append(g.violations, Violation{
		Kind:    ViolationFile,
		File:    filepath.ToSlash(rel),
		Line:    line,
		Message: fmt.Sprintf("%s:%d error-prone pattern guard: %s", filepath.ToSlash(rel), line, fmt.Sprintf(format, args...)),
	})
}

func lineNumber(content, token string) int {
	idx := strings.Index(content, token)
	if idx < 0 {
		return 1
	}
	return strings.Count(content[:idx], "\n") + 1
}
