package nodeexec

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// 本文件定义 DAG 节点执行层的公共 wire 类型。
// 这些类型被 dispatcher、store adapter 和各类 executor 共用，字段变更会影响持久化和跨模块调用。

// NodeStatus 是节点生命周期状态。
// 合法转换由 ValidateTransition 统一校验，调用方不能直接改写状态绕过调度流程。
type NodeStatus string

const (
	NodeStatusPending      NodeStatus = "pending"       // 上游未全 done
	NodeStatusReady        NodeStatus = "ready"         // deps 满足，等 dispatcher pick
	NodeStatusRunning      NodeStatus = "running"       // executor 已启动
	NodeStatusRetrying     NodeStatus = "retrying"      // 失败但有 attempts 余量，等下次拉起
	NodeStatusDone         NodeStatus = "done"          // 成功终态
	NodeStatusFailed       NodeStatus = "failed"        // 失败终态
	NodeStatusCancelled    NodeStatus = "cancelled"     // 被上游 fail_fast 级联取消
	NodeStatusSkipped      NodeStatus = "skipped"       // on_failure=skip 时跳过
	NodeStatusWaitingHuman NodeStatus = "waiting_human" // HITL 暂停；当前仅保留枚举兼容
)

func persistedNodeStatuses() []NodeStatus {
	return []NodeStatus{
		NodeStatusPending,
		NodeStatusReady,
		NodeStatusRunning,
		NodeStatusRetrying,
		NodeStatusDone,
		NodeStatusFailed,
		NodeStatusCancelled,
		NodeStatusSkipped,
		NodeStatusWaitingHuman,
	}
}

func isPersistedNodeStatus(raw string) bool {
	switch NodeStatus(strings.TrimSpace(raw)) {
	case NodeStatusPending, NodeStatusReady, NodeStatusRunning, NodeStatusRetrying,
		NodeStatusDone, NodeStatusFailed, NodeStatusCancelled, NodeStatusSkipped,
		NodeStatusWaitingHuman:
		return true
	default:
		return false
	}
}

func isReservedOrLegacyNodeStatus(raw string) bool {
	switch strings.TrimSpace(raw) {
	case string(NodeStatusSkipped), string(NodeStatusWaitingHuman), "awaiting_verify":
		return true
	default:
		return false
	}
}

// FailureClass 是节点失败的分类。
// dispatcher 据此查 OnFailureConfig.ByClass 选 OnFailureStrategy。
type FailureClass string

const (
	FailureClassTransient      FailureClass = "transient"      // 网络抖动 / CLI 启动失败 / 临时限流
	FailureClassQuota          FailureClass = "quota"          // token 超限 / context 过长
	FailureClassValidation     FailureClass = "validation"     // 输出不符 outputs.schema
	FailureClassCapability     FailureClass = "capability"     // 模型能力不够（升级 model 可救）
	FailureClassHard           FailureClass = "hard"           // 业务层认定不可恢复
	FailureClassNeedsHuman     FailureClass = "needs_human"    // 需要人决策
	FailureClassInfrastructure FailureClass = "infrastructure" // 数据库或外部服务不可用。
)

// OnFailureStrategy 是节点失败时的策略选择。
// 对应 node.config.exec.on_failure.{default, by_class}，缺省策略由调度层解释。
type OnFailureStrategy string

const (
	OnFailureRetry         OnFailureStrategy = "retry"          // 简单重试（默认）
	OnFailureEscalateModel OnFailureStrategy = "escalate_model" // 升级 model 重跑（capability 类）
	OnFailureAppendError   OnFailureStrategy = "append_error"   // 错误注入 prompt 重跑（validation 类）
	OnFailureReplan        OnFailureStrategy = "replan"         // spawn planner agent 改图
	OnFailureSkip          OnFailureStrategy = "skip"           // 跳过此节点，下游照常
	OnFailureFailFast      OnFailureStrategy = "fail_fast"      // 立即失败，级联取消下游
	OnFailureAskHuman      OnFailureStrategy = "ask_human"      // 转 waiting_human 等待审核
)

// HookPoint 是 lifecycle hook 触发点。
// NodeExecutorRouter 在 dispatch 和终态路径上 best-effort 触发，hook 失败不改变节点状态。
type HookPoint string

const (
	HookBeforeExecute HookPoint = "before_execute"  // Execute 调用前
	HookAfterExecute  HookPoint = "after_execute"   // Execute 调用后（无论成败）
	HookOnStateChange HookPoint = "on_state_change" // status 转换时
	HookOnFailure     HookPoint = "on_failure"      // 终态失败时
)

// HookHandler 是 hook 触发时的回调。handler 失败只影响 hook side effect，
// 不改写 node execution outcome。
type HookHandler interface {
	Handle(ctx context.Context, point HookPoint, node Node, outcome NodeOutcome) error
}

// Node 是 NodeExecutor 看到的最小执行视图。
// 与持久化层 store/taskdag.Node 解耦：持久化字段（id / created_at /
// updated_at / depends_on / status 等）不在此层；dispatcher 在派发前
// 把 taskdag.Node 映射成 Node。
type Node struct {
	DagKey           string
	NodeKey          string
	NodeType         string // agent | automation | hybrid
	Title            string
	Config           json.RawMessage // 由 ParseNodeConfig 解码成 typed struct
	SpawningThreadID string          // 已记录的 child thread id，用于恢复启动后写回流程
}

// RunContext 是 Execute 调用时的运行时上下文。
//
// 字段说明：
//   - DagKey/NodeKey/RunID：调度上下文 ID 三元组，用于持久化写回和审计关联；
//   - PrevResults：上游节点 result（按 node_key 索引），dispatcher 在派发前预取并填入；
//     执行器不会主动查 store，让 dispatcher 决定何时如何读取历史 result。
//   - SharedFileReader/SharedFileWriter：sharedfile 读/写端口，分别承载 inputs.from_sharedfiles 与
//     outputs.to_sharedfile 行为；nil 表示未注入能力，配置引用时会被归为 validation 失败。
type RunContext struct {
	DagKey  string
	NodeKey string
	RunID   int64

	// PrevResults 是 dispatcher 传入的上游节点结果快照。
	// 仅传 cfg.Inputs.FromNodes 关心的子集即可；缺 key 时执行器走 validation 失败。
	PrevResults map[string]json.RawMessage

	// SharedFileReader 是 inputs.from_sharedfiles 的读取入口；nil 表示未注入。
	SharedFileReader SharedFileReader

	// SharedFileWriter 是 outputs.to_sharedfile 的写入入口；nil 表示未注入。
	SharedFileWriter SharedFileWriter
}

// SharedFileReader 是 RunContext.SharedFileReader 的最小接口面。
//
// 语义合约：
//   - 文件存在 → (content, true, nil)；
//   - 文件不存在 → ("", false, nil)，调用方据此归类为 validation 失败；
//   - IO / DB / 解码等基础设施错误 → ("", false, err)，调用方走 classify 默认 transient。
//
// 生产实现可由 store/sharedfile.Reader.Get 适配（包装 platformdb.ErrNotFound 为
// exists=false）；测试注入 stub 断言入参。
type SharedFileReader interface {
	ReadSharedFile(ctx context.Context, path string) (content string, exists bool, err error)
}

// SharedFileWriter 是 RunContext.SharedFileWriter 的最小接口面。
// 生产实现可由 store/sharedfile.Store 的 Upsert 适配；测试注入 stub 验证写入路径与内容。
type SharedFileWriter interface {
	WriteSharedFile(ctx context.Context, path, content string) error
}

// SharedFileWriteRequest 是带审计元数据的 sharedfile 写入请求。
type SharedFileWriteRequest struct {
	Path          string
	Content       string
	ContentType   string
	OwnerNode     string
	ProducerActor string
	RunID         int64
	PromptRef     string
}

// SharedFileMetadataWriter 由支持记录 owner/producer 元数据的 writer 实现。
type SharedFileMetadataWriter interface {
	WriteSharedFileWithMetadata(ctx context.Context, req SharedFileWriteRequest) error
}

// NodeOutcome 是 NodeExecutor.Execute 的结构化返回值。
//
// 失败也是一种正常返回——带 FailureClass 分类信息的 NodeOutcome；
// 只有 framework 级错误（panic recover / context cancel）才走 error 通道。
// 这是智能重试 dispatcher 能按 by_class 分发策略的前提。
type NodeOutcome struct {
	// Status 是 Execute 之后的终态：done / failed / skipped / waiting_human。
	Status NodeStatus

	// Result 是成功时的结构化输出。
	// 仅适合小摘要；大输出必须经 outputs.to_sharedfile 写入 sharedfile，避免 task_dag_nodes.result 膨胀。
	Result json.RawMessage

	// FailureClass 在 Status == NodeStatusFailed 时填入；其他状态留空。
	FailureClass FailureClass

	// ErrorSummary 是失败时的简短描述（< 1KB），用于注入 retry prompt
	// 或显示在 UI 节点行。详细错误应去 sharedfile 看。
	ErrorSummary string

	// RetryHint 是 executor 给 dispatcher 的可选退避建议；可被全局策略 override。
	RetryHint *RetryHint
}

// RetryHint 是 executor 给 dispatcher 的退避建议。
type RetryHint struct {
	// SuggestedDelay 是建议的下次重试退避时长。
	SuggestedDelay time.Duration
	// SuggestedModel 是建议升级到的 model（capability 类失败时常用，例如 sonnet → opus）。
	SuggestedModel string
}

// NodeExecutor 是节点执行器统一接口。
// 三种 node_type 各自实现，但必须共享同一调度入口和 NodeOutcome 失败分类。
// 实现可返回 nil Hooks 表示不注册 lifecycle side effect；生产 wiring 会给 agent/automation 注入默认 hook set。
type NodeExecutor interface {
	// Execute 执行节点。失败也是正常返回（NodeOutcome.Status=failed +
	// FailureClass）；只有框架级错误（panic / context cancel）才走 error 通道。
	Execute(ctx context.Context, node Node, runCtx RunContext) (NodeOutcome, error)

	// Hooks 返回该 executor 注册的 lifecycle hooks；nil 表示没有附加副作用。
	Hooks() map[HookPoint]HookHandler
}
