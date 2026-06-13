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
	violations = append(violations, silentFallbackReturnViolations(repoRoot, scanRoots)...)
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
	g.guardToolbridgePayloadSpoofingPattern()
	g.guardMissingContextSuppressionPattern()
	g.guardMultiAgentGlobalStatePattern()
	g.guardSilentTurnLeakagePattern()
	g.guardSessionIdentityPreservationPattern()
	g.guardLanguageAnchorPattern()
	g.guardEmptyCWDPropagationPattern()
	g.guardTerminalSignalCompletenessPattern()
	g.guardDSLTypeCoercionPattern()
	g.guardRetiredPromptClassifierPattern()
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
		"sqlctx.WithTxOrReuse",
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

func (g *errorPronePatternGuard) guardToolbridgePayloadSpoofingPattern() {
	const rel = "internal/provider/codexapp/session_enrich.go"
	g.requireContains(rel, "toolbridge payload enrichment must explicitly inject trusted cwd",
		"enrichToolCallParams(msg RawMessage, agentID, cwd string)",
		"injectToolCallMetadata(payload map[string]json.RawMessage, agentID, cwd string)",
	)
	g.requireContains(rel, "untrusted top-level aliases must be dropped",
		"delete(payload, \"cwd\")",
		"delete(payload, \"agent_id\")",
	)
}

func (g *errorPronePatternGuard) guardMissingContextSuppressionPattern() {
	const rel = "pkg/logger/relay.go"
	g.requireContains(rel, "global relay handlers must check for context-specific suppression",
		"relayDisabled(ctx)",
	)
}

func (g *errorPronePatternGuard) guardMultiAgentGlobalStatePattern() {
	const rel = "cmd/mcp-lsp/multilsp/manager.go"
	g.requireContains(rel, "managers must be explicitly instantiated without global singleton wrappers",
		"func NewManager(cfg Config) Manager",
	)
}

// guardSilentTurnLeakagePattern ensures keepalive (silent) turns are
// properly gated at the decode path, stripped from history reads, and that
// the heartbeat timer self-sustains even on ping failure.
func (g *errorPronePatternGuard) guardSilentTurnLeakagePattern() {
	// 1. Silent turn id must use a recognizable prefix constant.
	const silentTurnRel = "internal/provider/claudecli/session_silent_turn.go"
	g.requireContains(silentTurnRel, "silent turn id must use a recognizable prefix constant",
		"keepaliveTurnIDPrefix",
	)
	// 2. Event decode path must gate keepalive turns before dispatch.
	const eventsRel = "internal/provider/claudecli/session_events.go"
	g.requireContains(eventsRel, "decoded stream events must gate keepalive turns before dispatch",
		"isKeepaliveTurnEvent",
	)
	// 3. History reads must strip keepalive maintenance turns.
	const historyRel = "internal/module/thread/history.go"
	g.requireContains(historyRel, "history reads must strip keepalive maintenance turns",
		"dropKeepaliveTurns",
	)
	// 4. Keepalive timer must reschedule after both success and failure;
	// deliverPing must call ResetTimerByAgent unconditionally (no early
	// return on SendKeepalive error).
	const keepaliveRel = "internal/platform/cachekeepalive/manager.go"
	g.requireContains(keepaliveRel, "keepalive timer must reschedule after both success and failure",
		"kc.SendKeepalive(ctx)",
		"m.ResetTimerByAgent(",
	)
}

// guardSessionIdentityPreservationPattern ensures Stop does not erase the
// session UUID needed for post-stop history recovery, that binding
// persistence detects session UUID updates, and that codex history
// discovery falls back to session UUID.
func (g *errorPronePatternGuard) guardSessionIdentityPreservationPattern() {
	// 1. Stop must not erase session uuid needed for history recovery.
	const stopRel = "internal/module/thread/stop.go"
	g.requireNotContains(stopRel, "stop must not erase session uuid needed for history recovery",
		"cleanupStoppedBinding",
		`SessionUUID: ""`,
	)
	// 2. Binding persistence must detect and persist session uuid updates.
	const bindingRel = "internal/module/thread/binding_registration.go"
	g.requireContains(bindingRel, "binding persistence must detect and persist session uuid updates",
		"bindingNeedsSessionUUIDUpdate",
		"bindingRequiresSessionUUID",
	)
	// 3. Codex history discovery must fallback to session uuid.
	const historyRel = "internal/util/historyjsonl/history.go"
	g.requireContains(historyRel, "codex history discovery must fallback to session uuid when provider thread id is missing",
		"req.SessionUUID",
		"discoverCodexPath",
	)
}

// guardLanguageAnchorPattern ensures the language prompt section always
// anchors reply language — even when no explicit language is configured —
// to prevent mixed-language drift in multi-lingual corpora.
//
// Pre-condition: the guard activates once the fix is applied (i.e. the file
// contains languageDefaultSectionText). Before that, the guard is inert so
// main stays green while fix/language-mixing-anchor has not yet merged.
func (g *errorPronePatternGuard) guardLanguageAnchorPattern() {
	const rel = "internal/module/prompt/brief_provider.go"
	content, ok := g.read(rel)
	if !ok {
		return
	}
	// Activate only after the fix has landed.
	if !strings.Contains(content, "languageDefaultSectionText") {
		return
	}
	g.requireContains(rel, "language section must provide a default anchor when no explicit language is configured",
		"languageDefaultSectionText",
	)
	g.requireContains(rel, "default language anchor must prevent mid-response language mixing",
		"Do not mix languages",
	)
}

// guardEmptyCWDPropagationPattern ensures child agents launched via
// orchestration_launch_agent inherit their parent's cwd when the tool call
// omits it. Without this, empty cwd propagates to agent_threads.cwd and the
// sidebar snapshot, causing child agents to appear in every project view
// (the "lottery" symptom from 2026-05-15).
//
// Also ensures pool server spawn passes workDir from the session request.
func (g *errorPronePatternGuard) guardEmptyCWDPropagationPattern() {
	// 1. Orchestration launcher must apply cwd defaults before forwarding.
	const launcherRel = "cmd/mcp-orch/orchestration/service_launcher_bridge.go"
	g.requireContains(launcherRel, "child agents must inherit parent cwd when the tool call omits it",
		"applyLaunchRequestDefaults",
		"strings.TrimSpace(req.Cwd)",
	)
	// 2. Pool routing must pass workDir from session request to spawner.
	const poolRel = "internal/provider/codexapp/driver_pool_routing.go"
	g.requireContains(poolRel, "pool server spawn must pass thread cwd to spawner context",
		"withPoolSpawnWorkDir",
	)
}

// guardTerminalSignalCompletenessPattern ensures the frontend streaming
// finalization logic covers ALL terminal signals, not just turn/completed.
// Missing any terminal signal causes <pre> streaming placeholders to stick
// until the next turn (the regression from 2026-05-15 commit 600db7d8).
//
// The signal set must include: turn/completed, turn/interrupted,
// agent/stopped, thread/stopped, agent/failed.
func (g *errorPronePatternGuard) guardTerminalSignalCompletenessPattern() {
	const rel = "cmd/agent-terminal/frontend/vue-app/stores/thread-sync-helpers.js"
	g.requireContains(rel, "streaming finalization must cover all terminal signals, not just turn/completed",
		"turnTerminalSignal",
		"turn/interrupted",
		"agent/stopped",
		"thread/stopped",
		"agent/failed",
	)
}

// guardDSLTypeCoercionPattern locks the split between template match_when and
// section enable_when: template-level tags_has is retired and must fail closed,
// while section-level enable_when.tags_has keeps the dual-type evaluator.
func (g *errorPronePatternGuard) guardDSLTypeCoercionPattern() {
	const rel = "internal/module/prompt/enable_when.go"
	g.requireContains(rel, "section-level tags_has must keep dual-type evaluator",
		"case \"tags_has\":",
		"matchSectionTagsHas(want, userPrompt)",
	)
	g.requireContains(rel, "template-level tags_has must fail closed",
		"Template-level keyword routing is retired",
		"return false",
	)
	g.requireNotContains(rel, "template-level tags_has must not use string-only evaluator",
		"matchTagsHas(matchWhenStringValue(want)",
	)
}

// guardRetiredPromptClassifierPattern locks the PromptClassifier removal. The
// router now relies on template match_when + harness dynamic sections; the old
// forked Claude classifier must not re-enter the Go runtime surface.
// guardRetiredPromptClassifierPattern 检查retiredpromptclassifierpattern。
func (g *errorPronePatternGuard) guardRetiredPromptClassifierPattern() {
	classifierFiles, err := filepath.Glob(filepath.Join(g.repoRoot, "internal", "module", "prompt", "classifier", "*.go"))
	if err != nil {
		g.addViolation("internal/module/prompt/classifier", 1, "cannot scan retired PromptClassifier package: %v", err)
	} else if len(classifierFiles) > 0 {
		g.addViolation("internal/module/prompt/classifier", 1, "retired PromptClassifier package must not contain Go files")
	}

	g.requireNotContains("internal/contract/prompt.go", "PromptClassifier contract surface is retired",
		"PromptClassifier",
		"UseClassifier",
	)
	g.requireNotContains("internal/module/prompt/module.go", "prompt classifier fx wiring is retired",
		"prompt/classifier",
		"newPromptClassifier",
		"newClassifierFastPathFunc",
	)
	g.requireNotContains("internal/module/thread/contract.go", "thread start request must not expose classifier opt-in",
		"UseClassifier",
	)
	g.requireNotContains("internal/module/thread/router_resolve.go", "router classifier path is retired",
		"maybeClassifyPrompt",
		"classifyPromptWithBackend",
		"classifierCandidates",
	)
	g.requireNotContains("internal/module/thread/rpc_types.go", "thread RPC must not accept classifier opt-in",
		"UseClassifier",
		"use_classifier",
	)
	g.requireNotContains("internal/module/thread/spawn.go", "spawn path must not forward classifier opt-in",
		"UseClassifier",
		"use_classifier",
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

func (g *errorPronePatternGuard) requireNotContains(rel, note string, tokens ...string) {
	content, ok := g.read(rel)
	if !ok {
		return
	}
	for _, token := range tokens {
		if !strings.Contains(content, token) {
			continue
		}
		g.addViolation(rel, lineNumber(content, token), "%s: forbidden %q", note, token)
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

// functionBody 处理函数正文。
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
