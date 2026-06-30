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
	g.requireContains(runtimeSQLRel, "asynchronous completion fences must accept only canonical active states",
		"status IN ('ready', 'running')",
	)
	g.requireNotContains(runtimeSQLRel, "asynchronous completion fences must not accept legacy awaiting_verify",
		"status IN ('ready', 'running', 'awaiting_verify')",
	)
	const writeSQLRel = "cmd/mcp-orch/sql/queries/task_dag_node_write.sql"
	g.requireContains(writeSQLRel, "side-effect claim fences must reject terminal rows while accepting canonical active race states",
		"ClaimTaskDagNodeOutputMaterialization",
		"status IN ('ready', 'running')",
	)
	g.requireNotContains(writeSQLRel, "side-effect claim must not write legacy awaiting_verify",
		"SET status = 'awaiting_verify'",
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

// guardSilentTurnLeakagePattern 锁定 keepalive 静默 turn 的三处边界。
// 解码入口必须先拦截，记录读取必须剥离，心跳失败后也必须重置计时器，避免维护 turn 泄露到用户时间线。
func (g *errorPronePatternGuard) guardSilentTurnLeakagePattern() {
	// 1. 静默 turn id 必须使用可识别前缀，便于解码和记录读取复用同一判定。
	const silentTurnRel = "internal/provider/claudecli/session_silent_turn.go"
	g.requireContains(silentTurnRel, "silent turn id must use a recognizable prefix constant",
		"keepaliveTurnIDPrefix",
	)
	// 2. 事件解码必须在分发前截住 keepalive turn。
	const eventsRel = "internal/provider/claudecli/session_events.go"
	g.requireContains(eventsRel, "decoded stream events must gate keepalive turns before dispatch",
		"isKeepaliveTurnEvent",
	)
	// 3. 记录读取必须删除 keepalive 维护 turn，避免 UI 和上下文恢复看到内部心跳。
	const historyRel = "internal/module/thread/history.go"
	g.requireContains(historyRel, "history reads must strip keepalive maintenance turns",
		"dropKeepaliveTurns",
	)
	// 4. keepalive 成功或失败后都必须重新排期；SendKeepalive 报错不能提前跳过 ResetTimerByAgent。
	const keepaliveRel = "internal/platform/cachekeepalive/manager.go"
	g.requireContains(keepaliveRel, "keepalive timer must reschedule after both success and failure",
		"kc.SendKeepalive(ctx)",
		"m.ResetTimerByAgent(",
	)
}

// guardSessionIdentityPreservationPattern 锁定停止后仍可恢复会话记录所需的 session UUID。
// Stop 不能擦除该字段，binding 持久化必须识别 UUID 更新，Codex 记录查找在 provider thread 缺失时要回退到 UUID。
func (g *errorPronePatternGuard) guardSessionIdentityPreservationPattern() {
	// 1. Stop 不能擦除停止后记录恢复需要的 session uuid。
	const stopRel = "internal/module/thread/stop.go"
	g.requireNotContains(stopRel, "stop must not erase session uuid needed for history recovery",
		"cleanupStoppedBinding",
		`SessionUUID: ""`,
	)
	// 2. binding 持久化必须检测并写入 session uuid 更新。
	const bindingRel = "internal/module/thread/binding_registration.go"
	g.requireContains(bindingRel, "binding persistence must detect and persist session uuid updates",
		"bindingNeedsSessionUUIDUpdate",
		"bindingRequiresSessionUUID",
	)
	// 3. Codex 记录发现必须能在 provider thread id 缺失时使用 session uuid。
	const historyRel = "internal/util/historyjsonl/history.go"
	g.requireContains(historyRel, "codex history discovery must fallback to session uuid when provider thread id is missing",
		"req.SessionUUID",
		"discoverCodexPath",
	)
}

// guardLanguageAnchorPattern 锁定语言 prompt section 的默认锚点。
// 即使没有显式语言配置，也要给回复语言一个稳定约束，避免多语言语料下回答语言漂移。
//
// 文件中出现 languageDefaultSectionText 后才启用此守卫；未接入默认锚点的分支保持惯性通过。
func (g *errorPronePatternGuard) guardLanguageAnchorPattern() {
	const rel = "internal/module/prompt/brief_provider.go"
	content, ok := g.read(rel)
	if !ok {
		return
	}
	// 默认锚点落地后再校验具体文本，避免未接入分支被旧守卫阻断。
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

// guardEmptyCWDPropagationPattern 锁定子 agent 启动时的工作目录继承。
// orchestration_launch_agent 省略 cwd 时必须继承父级 cwd，否则空 cwd 会进入持久化和侧栏投影，使子任务出现在错误项目视图。
//
// 同时校验 pool server spawn 使用 session 请求里的 workDir，保证进程启动边界和 UI 归属一致。
func (g *errorPronePatternGuard) guardEmptyCWDPropagationPattern() {
	// 1. Orchestration launcher 转发前必须补齐 cwd 默认值。
	const launcherRel = "cmd/mcp-orch/orchestration/service_launcher_bridge.go"
	g.requireContains(launcherRel, "child agents must inherit parent cwd when the tool call omits it",
		"applyLaunchRequestDefaults",
		"strings.TrimSpace(req.Cwd)",
	)
	// 2. pool 路由必须把 session request 的 workDir 传给 spawner。
	const poolRel = "internal/provider/codexapp/driver_pool_routing.go"
	g.requireContains(poolRel, "pool server spawn must pass thread cwd to spawner context",
		"withPoolSpawnWorkDir",
	)
}

// guardDSLTypeCoercionPattern 锁定 template match_when 与 section enable_when 的类型边界。
// template 级 tags_has 必须关闭，section 级 enable_when.tags_has 继续保留双类型求值器。
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

// guardRetiredPromptClassifierPattern 锁定 PromptClassifier 从 Go runtime 面的移除。
// 路由现在依赖 template match_when 和 harness 动态 section，不能重新引入分叉的 Claude 分类器。
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

// functionBody 从缓存源码中截取指定函数体。
// 找不到函数时立即记录 guard 违规，避免后续模式检查在空字符串上误判通过。
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
