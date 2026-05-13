package nodeexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/util/idgen"
)

// AgentExecutor —— DAG 改造蓝图 v2 §1.1（F1.1 真实实现）+ F1.5 写回。
//
// 职责：把 node_type=agent 节点的 config.exec 解码成 typed AgentExecConfig，
// 通过注入的 AgentLauncher（生产实现 = service.LaunchAgentSnapshot 或 wrapper）
// 拉起子 agent；把 launcher 返回的 error 映射到 NodeOutcome.FailureClass，
// 让上层 dispatcher（F1.4 智能重试）能按 by_class 分发策略。
//
// F1.5 新增：launch 成功 + 拿到 child thread_id 后，调注入的
// NodeSpawnRecorder 把 thread_id 写回 task_dag_nodes.spawning_thread_id
// （重试时旧值进 task_dag_runs.events 形成历史链）。recorder 为 nil 时降级
// 为「仅 launch、不写回」的 F1.1 行为，便于既有测试与早期 wiring 渐进迁移。
//
// AgentExecutor wires the agent execution pathway: decode node.config.exec
// into a typed AgentExecConfig, ask an AgentLauncher to start a sub-agent,
// translate launch errors into classified NodeOutcomes so the smart-retry
// dispatcher (F1.4) can dispatch by_class strategies, and — new in F1.5 —
// once a child thread_id is returned, hand it to a NodeSpawnRecorder so
// task_dag_nodes.spawning_thread_id is written back (ADR-009). Passing a nil
// recorder downgrades to the F1.1 behaviour (launch-only, no write-back) so
// pre-F1.5 tests and wiring keep compiling without changes elsewhere.
//
// 与 wakeup_dispatcher 的边界：本 task 落地 NodeExecutor 抽象，dispatcher 当前
// 仍可直接调 service.LaunchAgent（向后兼容）。F2/F3 才统一切到 NodeExecutor，
// 届时 dispatcher 路径也会自动走 F1.5 写回。
type AgentExecutor struct {
	launcher AgentLauncher
	recorder NodeSpawnRecorder
}

// AgentLauncher 是 AgentExecutor 拉起子 agent 的最小接口面。
//
// F1.5 签名升级：返回值 `(threadID, error)` —— 成功时 threadID 是 child agent
// 的 thread_id（用于回写 task_dag_nodes.spawning_thread_id），空串表示 launcher
// 未能拿到 thread_id（recorder 会 fail-fast 拒写，避免错误覆盖之前的值）。
// 生产实现可以适配 service.LaunchAgentSnapshot —— 其 (AgentSnapshot, error)
// 返回值天然包含 ThreadID 字段。
//
// 接口签名刻意复用 contract.LaunchRequest（orchestration / dispatcher 同源），
// 避免再造一份 launch 入参类型。
//
// AgentLauncher is the narrow surface AgentExecutor calls to start a child
// agent. F1.5 widened the return to `(threadID, error)`: on success threadID
// carries the child's thread id (consumed by NodeSpawnRecorder to write back
// task_dag_nodes.spawning_thread_id); an empty string signals the launcher
// could not surface a thread id (the recorder fails fast to avoid erasing a
// previously recorded one). Production wiring adapts
// service.LaunchAgentSnapshot whose (AgentSnapshot, error) result naturally
// carries ThreadID.
type AgentLauncher interface {
	LaunchAgent(ctx context.Context, req contract.LaunchRequest) (threadID string, err error)
}

// NodeSpawnRecorder 是 F1.5 / ADR-009 引入的 thread_id 写回端口。
// 生产实现是 store/taskdag.NodeSpawnRecorderStore —— *store 类型同时满足该
// 接口；测试注入 stub recorder 断言写回入参与重试 events 是否被 append。
//
// NodeSpawnRecorder is the F1.5 / ADR-009 write-back port. Production wiring
// binds it to store/taskdag.NodeSpawnRecorderStore (*store satisfies it);
// tests inject a stub that captures the inputs and returns an injected error.
type NodeSpawnRecorder interface {
	RecordNodeSpawn(ctx context.Context, dagKey, nodeKey, threadID string) error
}

// PrevNodeResultReader 以及 SharedfileReader 的 F1.2 端口已于收敛 batch 走。
// AgentExecutor 现在统一从 RunContext 拿：
//   - inputs.from_nodes 读取 → RunContext.PrevResults（dispatcher 预取后填入）；
//   - inputs.from_sharedfiles 读取 → RunContext.SharedFileReader（三态返值端口）。
//
// 这样 AgentExecutor 与 AutomationExecutor 走同一个 inputs 端口语义，避免两条路径漂移。
// PrevNodeResultReader / SharedfileReader 两个接口已被删除；生产侧 inputs 调用方
// 只需填 RunContext。
//
// F1.2 prev/sharedfile readers were collapsed into RunContext fields so the
// two executors share one inputs surface. PrevNodeResultReader and
// SharedfileReader interfaces have been removed; callers wire data into
// RunContext.PrevResults / RunContext.SharedFileReader instead.

// Option configures an AgentExecutor at construction time. 端口收敛 batch 把双构造器
// （NewAgentExecutor / NewAgentExecutorWithInputs）折叠到 functional options：
// inputs 数据走 RunContext，构造器只用来锁 launcher / recorder / 未来辅助端口。
//
// Option follows the functional-options pattern so future ports (e.g. metrics
// hook, lifecycle observer) can be added without breaking existing callers.
type Option func(*AgentExecutor)

// WithRecorder 注入 NodeSpawnRecorder（F1.5 / ADR-009 spawning_thread_id 写回端口）。
// 不传该 option → recorder 为 nil → Execute 跳过 F1.5 写回（仅 launch），保留 F1.1 旧行为；
// 这种「静默降级」是有意的渐进 wiring，与 inputs 端口（nil 即视为未履行）的 fail-loud 不同。
//
// WithRecorder injects the F1.5 NodeSpawnRecorder. Omitting it leaves recorder
// nil, which preserves the F1.1 launch-only behaviour by design — write-back
// is auxiliary, not part of agent semantics.
func WithRecorder(recorder NodeSpawnRecorder) Option {
	return func(e *AgentExecutor) { e.recorder = recorder }
}

// NewAgentExecutor 构造一个 AgentExecutor。launcher 为 nil 时仍返回非 nil
// executor —— Execute 在 launch 阶段把它归为 validation 失败（让 dispatcher
// 走 by_class[validation] 策略，不至于直接 panic）。
//
// 端口收敛 batch 把 recorder 从位置参数改为 functional option (WithRecorder)；
// 不传 option 等价于过去的 NewAgentExecutor(launcher, nil)。inputs 端口（prev
// results / sharedfile reader）走 RunContext，不在此构造器。
//
// NewAgentExecutor returns an executor; passing a nil launcher does not panic
// — Execute classifies it as a validation failure so the dispatcher can
// decide how to surface the misconfiguration instead of crashing the run
// loop. The recorder moved from positional arg to WithRecorder option;
// passing no options is equivalent to the former (launcher, nil) call.
func NewAgentExecutor(launcher AgentLauncher, opts ...Option) *AgentExecutor {
	e := &AgentExecutor{launcher: launcher}
	for _, opt := range opts {
		if opt != nil {
			opt(e)
		}
	}
	return e
}

// Execute 解码 node.config.exec → 调 launcher.LaunchAgent → 包装成 NodeOutcome。
// 失败也是正常返回（NodeOutcome.Status=failed + FailureClass），只有 framework
// 级错误（panic recover / context cancel）走 error 通道。
//
// Execute is the NodeExecutor entry: decode config, call the launcher, wrap
// success/failure into NodeOutcome. Decoding errors map to validation;
// launcher errors flow through classifyAgentLaunchError for triage.
func (e *AgentExecutor) Execute(ctx context.Context, node Node, runCtx RunContext) (NodeOutcome, error) {
	if ctx == nil {
		// nil ctx 兜底：与 dispatcher.ProcessBatch / Tick 一致的防御式取默认值。
		ctx = context.Background()
	}

	// 1. 解码 typed config。
	cfg, parseErr := ParseAgentConfig(node.Config)
	if parseErr != nil {
		return NodeOutcome{
			Status:       NodeStatusFailed,
			FailureClass: FailureClassValidation,
			ErrorSummary: truncateErrSummary("decode agent config: " + parseErr.Error()),
		}, nil
	}
	if cfg == nil {
		// ParseAgentConfig 不返回 nil cfg，但防御式判一下。
		return NodeOutcome{
			Status:       NodeStatusFailed,
			FailureClass: FailureClassValidation,
			ErrorSummary: "decode agent config: nil parsed config",
		}, nil
	}
	if failure := validateAgentOutputs(node.Config, cfg); failure != nil {
		return *failure, nil
	}

	// 2. 形状校验：agent_key 是 launcher 路由 prompt template 的关键字段，
	//    缺它即 validation 失败（与 ADR 0001 §2.10 enum/必填基线对齐）。
	if strings.TrimSpace(cfg.Exec.AgentKey) == "" {
		return NodeOutcome{
			Status:       NodeStatusFailed,
			FailureClass: FailureClassValidation,
			ErrorSummary: "agent_key required in node.config.exec",
		}, nil
	}
	provider, providerErr := validateAgentLaunchProvider(cfg.Exec.Provider)
	if providerErr != nil {
		return NodeOutcome{
			Status:       NodeStatusFailed,
			FailureClass: FailureClassValidation,
			ErrorSummary: truncateErrSummary(providerErr.Error()),
		}, nil
	}
	cfg.Exec.Provider = provider

	// 3. F1.2 Inputs 装载：拉取 prev nodes result + sharedfiles 作为 prompt 前缀。
	//    任一环节失败 → 如果指向 missing node_key / sharedfile path 则 validation；
	//    其他错误（例如 DB 挑问）走 classifyAgentLaunchError 使 transient。
	//    完整的拼接格式见 inputs.go::assembleInputsPrompt。
	//    F1.2 Inputs assembly: gather prev node results + sharedfiles into a
	//    prompt prefix. Missing refs map to validation; infra errors flow
	//    through classifyAgentLaunchError (transient by default).
	inputsPrefix, ierr := e.assembleInputs(ctx, cfg, runCtx, node)
	if ierr != nil {
		return NodeOutcome{
			Status:       NodeStatusFailed,
			FailureClass: ierr.Class,
			ErrorSummary: truncateErrSummary("assemble inputs: " + ierr.Error()),
		}, nil
	}

	// 4. launcher == nil → validation：调用方拼线漏了 launcher，是配置问题。
	if e.launcher == nil {
		return NodeOutcome{
			Status:       NodeStatusFailed,
			FailureClass: FailureClassValidation,
			ErrorSummary: "agent executor: launcher not wired",
		}, nil
	}

	// 5. 构造 LaunchRequest 并调 launcher；F1.2 inputsPrefix 拼在 first_turn 之前。
	req := buildLaunchRequestFromAgentConfig(cfg, node, runCtx)
	req.Prompt = composePrompt(inputsPrefix, cfg.FirstTurn)
	threadID, launchErr := e.launcher.LaunchAgent(ctx, req)
	if launchErr == nil {
		// F1.5 写回逻辑外刷到 spawnWriteback，避免 Execute 本体圈复杂度 CC 超阈。
		errorSummary := e.spawnWriteback(ctx, node, runCtx, threadID)
		return finalizeAgentOutcome(errorSummary), nil
	}

	// 7. 失败 → 分类。F1.4 智能重试 dispatcher 据此查 cfg.Exec.OnFailure.ByClass。
	class := classifyAgentLaunchError(launchErr)
	return NodeOutcome{
		Status:       NodeStatusFailed,
		FailureClass: class,
		ErrorSummary: truncateErrSummary(fmt.Sprintf("launch agent: %v", launchErr)),
		// F1.4 留位：RetryHint 可在分类基础上给 SuggestedDelay / SuggestedModel；
		// 本 task 仅枚举 FailureClass，不给 hint。
		// F1.4 placeholder: future smart-retry can populate RetryHint here.
	}, nil
}

// Hooks 返回 lifecycle hooks。F13 真实实现；骨架阶段返回 nil。
//
// Hooks reports lifecycle hook handlers. F13 wires real hooks; today nil.
func (e *AgentExecutor) Hooks() map[HookPoint]HookHandler { return nil }

var agentOutputsForbiddenKeys = []string{"webhook_url", "command_ref"}

func validateAgentOutputs(raw json.RawMessage, _ *AgentNodeConfig) *NodeOutcome {
	return validateOutputsForbiddenKeys(raw, agentOutputsForbiddenKeys, func(key string) NodeOutcome {
		return NodeOutcome{
			Status:       NodeStatusFailed,
			FailureClass: FailureClassValidation,
			ErrorSummary: truncateErrSummary(fmt.Sprintf("agent outputs cannot include external capability field %q", key)),
		}
	})
}

func finalizeAgentOutcome(errorSummary string) NodeOutcome {
	return NodeOutcome{Status: NodeStatusDone, ErrorSummary: errorSummary}
}

// spawnWriteback 是 F1.5 写回路径的独立 helper：luach 成功 → 调 recorder 记录
// spawning_thread_id。返回值是 NodeOutcome.ErrorSummary 的候选填值：
//   - 空串：recorder 跳过或写入成功，调用方保持原 outcome；
//   - 非空：recorder 写入失败但 launch 已成功，调用方仅填 summary 不降级状态。
//
// 拆出独立函数主要为了把 Execute 的 CC 压住代码守卫上限（§10）。
//
// spawnWriteback is the F1.5 writeback helper. It returns the value to assign
// to NodeOutcome.ErrorSummary: empty when the recorder was either skipped or
// successful, non-empty when the recorder failed (callers preserve the
// successful launch status and only annotate ErrorSummary). Pulled out so
// Execute stays under the cyclomatic-complexity guard (10).
func (e *AgentExecutor) spawnWriteback(ctx context.Context, node Node, runCtx RunContext, threadID string) string {
	if e.recorder == nil {
		return ""
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}
	dagKey, nodeKey := resolveSpawnKeys(node, runCtx)
	if dagKey == "" || nodeKey == "" {
		return ""
	}
	if err := e.recorder.RecordNodeSpawn(ctx, dagKey, nodeKey, threadID); err != nil {
		return truncateErrSummary(fmt.Sprintf("spawning_thread_id write-back failed: %v", err))
	}
	return ""
}

// resolveSpawnKeys 从 RunContext / node 中提取 dagKey + nodeKey，优先 RunContext
// （dispatcher 传的 truth source），缺失时回退到 node 自身字段。两者都空意味
// 着上层 wiring 路跳了节点闭包，跳过写回不报错。
//
// resolveSpawnKeys extracts dagKey/nodeKey, preferring RunContext (the
// dispatcher-supplied truth source) and falling back to the node's own
// fields. Empty on both sides means the caller-side wiring skipped the
// closure; the writeback is then silently a no-op.
func resolveSpawnKeys(node Node, runCtx RunContext) (string, string) {
	dagKey := strings.TrimSpace(runCtx.DagKey)
	if dagKey == "" {
		dagKey = strings.TrimSpace(node.DagKey)
	}
	nodeKey := strings.TrimSpace(runCtx.NodeKey)
	if nodeKey == "" {
		nodeKey = strings.TrimSpace(node.NodeKey)
	}
	return dagKey, nodeKey
}

// buildLaunchRequestFromAgentConfig 把 AgentNodeConfig 映射到 contract.LaunchRequest。
// 命名/字段语义与 wakeup_dispatcher.go::buildLaunchRequestFromWakeup 同源，避免
// 两条 launch 入口（dispatcher vs NodeExecutor）行为漂移。
//
// 关键映射：
//   - AgentID 使用项目统一 agent_{monotonicNumericTimestamp} 生成器；
//   - Name 取 node.Title，去掉控制符并限制长度，供日志/UI 展示；
//   - AgentKey/Language 直填同名字段；
//   - Provider/Model/Effort 通过 LaunchRequest.Env 传递给 remoteLauncher；
//   - FirstTurn 作为初始 Prompt 注入；
//   - RunContext 暂无 parent agent id 字段，ParentID 先保持空。
func buildLaunchRequestFromAgentConfig(cfg *AgentNodeConfig, node Node, _ RunContext) contract.LaunchRequest {
	if cfg == nil {
		return contract.LaunchRequest{}
	}
	req := contract.LaunchRequest{
		AgentID:   idgen.NewAgentID(),
		Name:      sanitizeLaunchName(node.Title),
		AgentKey:  strings.TrimSpace(cfg.Exec.AgentKey),
		Language:  strings.TrimSpace(cfg.Exec.Language),
		Prompt:    cfg.FirstTurn,
		AgentType: node.NodeType, // "agent" 占位，F2/F3 hybrid 再细化
		Env:       agentLaunchEnv(cfg.Exec),
	}
	return req
}

func validateAgentLaunchProvider(raw string) (string, error) {
	provider := strings.ToLower(strings.TrimSpace(raw))
	switch provider {
	case "", "codex", "claude":
		return provider, nil
	default:
		return "", fmt.Errorf("invalid provider %q: must be codex or claude", strings.TrimSpace(raw))
	}
}

func agentLaunchEnv(exec AgentExecConfig) []string {
	env := make([]string, 0, 4)
	if provider := strings.ToLower(strings.TrimSpace(exec.Provider)); provider != "" {
		env = append(env, "AGENT_PROVIDER="+provider)
	}
	if model := strings.TrimSpace(exec.Model); model != "" {
		env = append(env, "AGENT_MODEL="+model)
	}
	if effort := strings.TrimSpace(exec.Effort); effort != "" {
		env = append(env, "AGENT_EFFORT="+effort)
	}
	if disabledTools := joinTrimmed(exec.DisabledTools); disabledTools != "" {
		env = append(env, "AGENT_DISABLED_TOOLS="+disabledTools)
	}
	return env
}

func joinTrimmed(values []string) string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return strings.Join(out, ",")
}

func sanitizeLaunchName(value string) string {
	const limit = 80
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	runes := make([]rune, 0, len(value))
	for _, r := range value {
		if unicode.IsControl(r) {
			continue
		}
		runes = append(runes, r)
		if len(runes) == limit {
			break
		}
	}
	return strings.TrimSpace(string(runes))
}

// classifyAgentLaunchError 把 launcher 返回的 error 映射成 FailureClass。
//
// 与 cmd/mcp-orch/orchestration/service_launcher_errors.go::classifyLaunchError
// 同源思路（关键字命中），但目标空间不同：service 层只分 transient/permanent/
// unknown，nodeexec 这层细化到 FailureClass{transient,quota,validation}，
// 让 OnFailureConfig.ByClass 能直接路由。
//
// 优先级（高 → 低）：
//  1. quota（context length / usage limit / credits）—— 必须先于通用 permanent
//     匹配，否则会被 401/403 之类的关键字捷足先登。
//  2. permanent（401/403/auth/payment）→ validation。
//  3. transient（connection / timeout / rate limit）→ transient。
//  4. 未知 → transient（保守兜底，让 dispatcher 走 by_class[transient] 重试）。
//
// classifyAgentLaunchError maps a launcher error to a FailureClass. The
// priority order is quota → validation → transient → transient-fallback so
// that overlapping substrings (e.g. "context_length_exceeded 401") resolve to
// quota, matching the operator intent: quota issues need budget action, not
// retry. Unknown errors default to transient (conservative).
func classifyAgentLaunchError(err error) FailureClass {
	if err == nil {
		return FailureClassTransient
	}
	msg := strings.ToLower(err.Error())

	// 1. quota 关键字（先于 permanent，避免与 401/403 共词时漏判）。
	quotaKeywords := []string{
		"context_length_exceeded",
		"context length exceeded",
		"maximum context",
		"prompt is too long",
		"quota_exhausted",
		"insufficient_quota",
		"usage limit",
		"out of credits",
	}
	for _, k := range quotaKeywords {
		if strings.Contains(msg, k) {
			return FailureClassQuota
		}
	}

	// 2. permanent（鉴权 / 授权 / 支付）→ validation。
	permanentKeywords := []string{
		"401", "unauthoriz", "invalid api key", "invalid_api_key",
		"403", "forbidden", "permission denied",
		"402", "payment_required", "subscription expired",
	}
	for _, k := range permanentKeywords {
		if strings.Contains(msg, k) {
			return FailureClassValidation
		}
	}

	// 3. transient（连接 / 超时 / 限流）。
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return FailureClassTransient
	}
	transientKeywords := []string{
		"deadline exceeded",
		"connection refused",
		"transport unavailable",
		"empty thread id",
		"timed out",
		"i/o timeout",
		"rate limit",
		"rate-limit",
		"rate_limit",
		"too many requests",
		"http 429",
		" 429 ",
		"status 429",
		"status: 429",
	}
	for _, k := range transientKeywords {
		if strings.Contains(msg, k) {
			return FailureClassTransient
		}
	}

	// 4. 未知 → transient 兜底。与 service 层 launchClassUnknown 的语义对齐：
	//    宁可让 dispatcher 多试一次也不要因为不认识的关键字直接 hard fail。
	return FailureClassTransient
}

// truncateErrSummary 限制 ErrorSummary 长度（< 1KB 内），与 NodeOutcome 注释
// 「<1KB」约束对齐；超出截短并标记。
//
// truncateErrSummary keeps ErrorSummary within the <1KB envelope documented on
// NodeOutcome.ErrorSummary; longer messages get truncated with a marker.
func truncateErrSummary(s string) string {
	const limit = 1000
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…(truncated)"
}
