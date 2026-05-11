package nodeexec

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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

// NewAgentExecutor 构造一个 AgentExecutor。launcher 为 nil 时仍返回非 nil
// executor —— Execute 在 launch 阶段把它归为 validation 失败（让 dispatcher
// 走 by_class[validation] 策略，不至于直接 panic）。
//
// recorder 为 nil 时 Execute 跳过 F1.5 写回（仅 launch），保留 F1.1 旧行为；
// 这是有意的渐进 wiring，让既有调用方不必一次性升级。
//
// NewAgentExecutor returns an executor; passing a nil launcher does not panic
// — Execute classifies it as a validation failure so the dispatcher can
// decide how to surface the misconfiguration instead of crashing the run
// loop. A nil recorder simply skips the F1.5 write-back, preserving F1.1
// behaviour for callers that have not yet plumbed the store in.
func NewAgentExecutor(launcher AgentLauncher, recorder NodeSpawnRecorder) *AgentExecutor {
	return &AgentExecutor{launcher: launcher, recorder: recorder}
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

	// 2. 形状校验：agent_key 是 launcher 路由 prompt template 的关键字段，
	//    缺它即 validation 失败（与 ADR 0001 §2.10 enum/必填基线对齐）。
	if strings.TrimSpace(cfg.Exec.AgentKey) == "" {
		return NodeOutcome{
			Status:       NodeStatusFailed,
			FailureClass: FailureClassValidation,
			ErrorSummary: "agent_key required in node.config.exec",
		}, nil
	}

	// 3. F1.2 留位：Inputs 注入逻辑（from_nodes / from_sharedfiles / summarization）
	//    在 F1.2 实现；本 task 不读 cfg.Inputs，但保留字段以便 F1.2 直接拼接 prompt。
	//    F1.2 placeholder: Inputs injection (from_nodes / from_sharedfiles /
	//    summarization) will be wired here in F1.2; today Inputs is decoded but
	//    not consumed.
	_ = cfg.Inputs

	// 4. launcher == nil → validation：调用方拼线漏了 launcher，是配置问题。
	if e.launcher == nil {
		return NodeOutcome{
			Status:       NodeStatusFailed,
			FailureClass: FailureClassValidation,
			ErrorSummary: "agent executor: launcher not wired",
		}, nil
	}

	// 5. 构造 LaunchRequest 并调 launcher。
	req := buildLaunchRequestFromAgentConfig(cfg, node, runCtx)
	threadID, launchErr := e.launcher.LaunchAgent(ctx, req)
	if launchErr == nil {
		// 6. F1.5：launch 成功后把 child thread_id 写回 task_dag_nodes
		//    .spawning_thread_id。recorder 为 nil 或 threadID 空则跳过（详
		//    NewAgentExecutor 注释 + ADR-009 §3）。写回失败仅记录到
		//    NodeOutcome.ErrorSummary，不把 launch 翻成 failed —— launch 已经
		//    成功，spawn 历史是辅助审计；过强语义会让一次 DB 抖动把 child
		//    agent 误判为 failed。F1.4 dispatcher 仍然可以从 ErrorSummary
		//    日志层面感知 writeback 失败。
		//
		//    F1.5: after a successful launch, hand the child thread_id to the
		//    recorder so task_dag_nodes.spawning_thread_id (ADR-009) is
		//    written back. The write-back is an audit aid; failures are
		//    surfaced via NodeOutcome.ErrorSummary but never demote the node
		//    to failed — the child has launched successfully and demoting on a
		//    transient DB blip would create a phantom-failure cascade.
		outcome := NodeOutcome{Status: NodeStatusDone}
		if e.recorder != nil && strings.TrimSpace(threadID) != "" {
			dagKey := strings.TrimSpace(runCtx.DagKey)
			nodeKey := strings.TrimSpace(runCtx.NodeKey)
			if dagKey == "" {
				dagKey = strings.TrimSpace(node.DagKey)
			}
			if nodeKey == "" {
				nodeKey = strings.TrimSpace(node.NodeKey)
			}
			if dagKey != "" && nodeKey != "" {
				if recErr := e.recorder.RecordNodeSpawn(ctx, dagKey, nodeKey, threadID); recErr != nil {
					outcome.ErrorSummary = truncateErrSummary(
						fmt.Sprintf("spawning_thread_id write-back failed: %v", recErr),
					)
				}
			}
		}
		// F1.3 留位：outputs.to_sharedfile / to_node_result 在 F1.3 实现。
		// F1.3 placeholder: persist outputs into sharedfile / node result.
		return outcome, nil
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

// buildLaunchRequestFromAgentConfig 把 AgentNodeConfig 映射到 contract.LaunchRequest。
// 命名/字段语义与 wakeup_dispatcher.go::buildLaunchRequestFromWakeup 同源，避免
// 两条 launch 入口（dispatcher vs NodeExecutor）行为漂移。
//
// 关键映射：
//   - AgentKey/Language 直填同名字段；
//   - Provider/Model 暂留位（contract.LaunchRequest 当前结构不含 provider，由
//     service.LaunchAgent 内部按 agent_key/默认 provider 解析）；
//   - FirstTurn 作为初始 Prompt 注入；
//   - RunContext.DagKey/NodeKey 暂不写入 req（contract.LaunchRequest 无对应字段），
//     F1.2 inputs 注入时如需可在 prompt 里渲染。
func buildLaunchRequestFromAgentConfig(cfg *AgentNodeConfig, node Node, _ RunContext) contract.LaunchRequest {
	if cfg == nil {
		return contract.LaunchRequest{}
	}
	req := contract.LaunchRequest{
		AgentKey:  strings.TrimSpace(cfg.Exec.AgentKey),
		Language:  strings.TrimSpace(cfg.Exec.Language),
		Prompt:    cfg.FirstTurn,
		AgentType: node.NodeType, // "agent" 占位，F2/F3 hybrid 再细化
	}
	return req
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
