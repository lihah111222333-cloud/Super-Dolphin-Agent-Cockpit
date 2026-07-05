// Package taskdag 提供 task_dag 系列表（DAG 定义、节点、运行记录、wakeup、worker lease）
// 的存储层实现，是 mcp-orch DAG 编排的核心数据访问入口。
package taskdag

import (
	"context"
	"encoding/json"
	"time"
)

// Store 是 taskdag 存储模块和底层测试仍使用的聚合接口。
// 生产调用方应优先依赖下方窄端口，避免把完整方法集扩散到业务层。
type Store interface {
	OrchestrationStore
	DAGMutationStore
	DAGLockStore
	RunningNodeStore
	NodeFlowStore
	WakeupStore
	WorkerLeaseStore
}

// NodeSpawningThreadLookup 不嵌入 Store，避免聚合接口继续膨胀。
// DAG 订阅者和 thread.stopped 兜底消费者通过 fx 直接拿窄端口，测试也能注入轻量 mock。

// OrchestrationStore 是 orchestration 层公开 DAG CRUD/update 流程使用的窄端口。
// RunStore 不嵌入这里，service 层以独立字段持有 runStore，保持模板 DAG 与执行实例边界清晰。
type OrchestrationStore interface {
	UnitOfWorkStore
	DAGReadStore
	NodeStatusStore
}

// UnitOfWorkStore 是事务入口接口，调用方在 fn 内执行的所有变更在同一事务内。
type UnitOfWorkStore interface {
	WithTx(ctx context.Context, fn func(txStore DAGMutationStore) error) error
}

// DAGMutationStore 是 WithTx fn 接收的事务内变更接口。
// RunStore 不嵌入这里；需要同时操作 run 和模板节点的路径应使用专门事务端口，
// 避免普通 DAG 变更接口意外承担执行实例语义。
type DAGMutationStore interface {
	DAGDetailStore
	UpsertDAG(ctx context.Context, dag DAG) (*DAG, error)
	UpsertNode(ctx context.Context, node Node) (*Node, error)
}

// DAGReadStore 是 DAG 只读查询接口，包含列表与详情。
type DAGReadStore interface {
	DAGDetailStore
	ListDAGs(ctx context.Context, filter ListDAGsFilter) ([]DAG, error)
}

// DAGDetailStore 是 DAG 单条查询与节点列表的最小只读接口。
type DAGDetailStore interface {
	GetDAG(ctx context.Context, dagKey string) (*DAG, error)
	ListNodes(ctx context.Context, dagKey string) ([]Node, error)
}

// RunNodeReadStore 按 run_id 读取 runtime 节点快照。
// run_id=0 保留给模板节点读取，调用方必须显式区分模板行和执行实例行。
type RunNodeReadStore interface {
	ListRunNodes(ctx context.Context, dagKey string, runID int64) ([]Node, error)
}

// NodeStatusStore 提供节点状态更新的通用入口。
type NodeStatusStore interface {
	UpdateNodeStatus(ctx context.Context, input NodeStatusUpdate) (*Node, error)
}

// NodeConfigPatchStore 是 dispatcher smart retry 更新 node.config 的窄端口。
// 它只在旧 config fence 命中时 patch 单列，避免复用 UpsertNode 覆盖整行。
type NodeConfigPatchStore interface {
	PatchNodeConfigIfUnchanged(ctx context.Context, input NodeConfigPatchInput) (*Node, error)
}

// SmartRetryConfigStore 是 dispatcher 准备 smart retry 的原子写端口。
// wakeup retry fence 和 node.config CAS patch 必须同事务提交；patch miss 或 DB
// 错误会回滚 retry，让调用方显式失败或停放同一条已领取 wakeup。
type SmartRetryConfigStore interface {
	RetryWakeupWithNodeConfigPatch(ctx context.Context, input RetryWakeupWithNodeConfigPatchInput) (int64, error)
}

// WakeupNodeFailureStore 是 dispatcher 提交永久投递失败的事务端口。
// 它把 wakeup failed、DAG 节点失败和下游级联写在同一事务，避免状态分裂。
type WakeupNodeFailureStore interface {
	FailWakeupAndFailNodeAndCancelDownstream(ctx context.Context, wakeup FailWakeupInput, node FailNodeInput) (int64, *FailNodeResult, error)
}

// DAGOpsStore 是 task_dag_apply_ops 业务的窄接口。包含：
//   - GetDAGVersionForUpdate: SELECT version FROM task_dags WHERE dag_key = ?
//     FOR UPDATE — 拿当前 OCC 版本，并在事务内锁定行避免双写。
//   - BumpDAGVersion:        UPDATE task_dags SET version = version + 1
//     语句片段：WHERE dag_key = ? AND version = ? RETURNING version；
//     0 行受影响 → expected/actual 不匹配，调用方判 OCC 冲突。
//   - UpsertNode + ListNodes: 复用既有 store 接口。
//   - CountRunningRunsByDagKey: 在 DAG row FOR UPDATE 锁内读取 active run；
//     与 StartDAG 曾经的事务外预检不同，ApplyOps 用它来保护运行中的模板节点。
//
// 设计取舍：单独窄接口而非塞进 DAGMutationStore。ApplyOps 需要 OCC 版本辅助和
// 节点删除/patch 能力，放入通用变更接口会让普通 DAG 写路径承担过多方法。
type DAGOpsStore interface {
	DAGDetailStore // GetDAG / ListNodes
	GetDAGVersionForUpdate(ctx context.Context, dagKey string) (int64, error)
	BumpDAGVersion(ctx context.Context, dagKey string, expectedVersion int64) (int64, error)
	CountRunningRunsByDagKey(ctx context.Context, dagKey string) (int64, error)
	GetDAGSchedule(ctx context.Context, dagKey string) (DAGSchedule, error)
	UpdateDAGPatch(ctx context.Context, input UpdateDAGPatchInput) (int64, error)
	UpsertNode(ctx context.Context, node Node) (*Node, error)
	DeleteNode(ctx context.Context, dagKey, nodeKey string) (int64, error)
}

// DAGOpsTxRunner 是 task_dag_apply_ops 在 DB 事务内跑业务的窄接口。调用方传
// fn，在同一事务内调 GetDAGVersionForUpdate / UpsertNode / BumpDAGVersion
// 将 OCC 校验、节点写入和 version 推进原子化。
//
// 实现上与 UnitOfWorkStore.WithTx 同走事务 helper，但传出去的 store 接口是
// DAGOpsStore 而非 DAGMutationStore，避免造成 DAGMutationStore 超 Interface
// Isolation 预算。
type DAGOpsTxRunner interface {
	WithDAGOpsTx(ctx context.Context, fn func(tx DAGOpsStore) error) error
}

// DAGVersionReader 是事务外读取 DAG version 的窄端口。
// ApplyOps 空 ops 短路只需检查 base_version，不应为无写入路径额外加锁。
//
// 与 DAGOpsStore 上的 GetDAGVersionForUpdate 区别：
//   - DAGVersionReader.GetDAGVersion：事务外、只读、不加锁。
//   - DAGOpsStore.GetDAGVersionForUpdate：事务内、SELECT … FOR UPDATE。
type DAGVersionReader interface {
	GetDAGVersion(ctx context.Context, dagKey string) (int64, error)
}

// DAGDeleteStore 是 DAG 级联删除的窄接口。
type DAGDeleteStore interface {
	DeleteDAG(ctx context.Context, dagKey string) (int64, error)
}

// DAGLockStore 提供 DAG 和节点的 FOR UPDATE 加锁读取接口，供事务内串行化使用。
type DAGLockStore interface {
	GetDAGForUpdate(ctx context.Context, dagKey string) (*DAG, error)
	GetNodesForUpdate(ctx context.Context, dagKey string) ([]Node, error)
}

// RecoveryStore 是 orchestration 恢复路径的窄接口：查找某 assignee 下仍在 running
// 的节点，以及按 id 读取 wakeup 快照。
type RecoveryStore interface {
	ListRunningNodesByAssignee(ctx context.Context, assignee string) ([]Node, error)
	GetWakeup(ctx context.Context, id int64) (*Wakeup, error)
}

// RunningNodeStore 是运行中节点的操作接口，包含 turn 绑定、事件触摸和状态推进。
type RunningNodeStore interface {
	RecoveryStore
	BindRunningNodeTurn(ctx context.Context, input BindRunningNodeTurnInput) (*Node, error)
	TouchRunningNodeEvent(ctx context.Context, input TouchRunningNodeEventInput) (*Node, error)
	UpdateRunningNodeStatus(ctx context.Context, input RunningNodeStatusUpdate) (*Node, error)
	CompleteNode(ctx context.Context, input CompleteNodeInput) (*Node, error)
}

// NodeFlowStore 是带 DAG 拓扑副作用的节点生命周期端口。
// complete/fail 节点时需要同事务调度或取消下游，因此和只更新运行中节点的端口分开。
type NodeFlowStore interface {
	CompleteNodeAndScheduleDownstream(ctx context.Context, input CompleteNodeInput) (*CompleteNodeWithDownstreamResult, error)
	FailNodeAndCancelDownstream(ctx context.Context, input FailNodeInput) (*FailNodeResult, error)
}

// NodeSpawnRecorderStore 是 nodeexec.AgentExecutor 写回 spawning_thread_id 的窄端口。
// 写入节点字段和 node_spawn 事件必须在 store 层保持一致；不嵌入大接口，避免调用方误拿全集合 Store。
type NodeSpawnRecorderStore interface {
	RecordNodeSpawn(ctx context.Context, input RecordNodeSpawnInput) (*RecordNodeSpawnResult, error)
}

// NodeSpawningThreadLookup 根据 child thread id 反查生成它的 DAG 节点。
// 重试和恢复链路可能返回多行，因为 spawning_thread_id 不是唯一约束；订阅者必须逐条幂等推进。
// 保持单方法窄端口，方便 turn.completed 和 thread.stopped 兜底路径注入轻量 mock。
type NodeSpawningThreadLookup interface {
	LookupNodesBySpawningThread(ctx context.Context, threadID string) ([]Node, error)
}

// DispatchNodeStore 是 task_dispatch_node MCP 工具需要的窄端口：
//   - DAGDetailStore:    保留 GetDAG + 模板 ListNodes 兼容读取能力
//   - RunNodeReadStore:  按 run_id 读取 runtime node
//   - AssignNodeAndEnqueueWakeup: 在同一事务里赋值 assigned_to 并入队 wakeup
//   - MarkDispatchIncompleteIfMissingWakeup: 标记历史半写节点，阻断重复派发
//
// 生产依赖仍然是同一个 *store (由 ProvideDispatchNodeStore type-assert)，
// 此处窄接口仅为了避免 service 层頻繁拿到全集合 Store 接口。
type DispatchNodeStore interface {
	DAGDetailStore
	RunNodeReadStore
	AssignNodeAndEnqueueWakeup(ctx context.Context, input AssignNodeAndEnqueueWakeupInput) (*AssignNodeAndEnqueueWakeupResult, error)
	MarkDispatchIncompleteIfMissingWakeup(ctx context.Context, input MarkDispatchIncompleteInput) (*MarkDispatchIncompleteResult, error)
}

// RecordNodeSpawnInput 是 RecordNodeSpawn 的入参。
// ThreadID 为空表示未拿到 child thread id，store 会 fail-fast 拒绝写入，避免覆盖旧 thread id。
// DagKey / NodeKey 必须 trim 后非空，RunID 用来保持执行实例隔离。
type RecordNodeSpawnInput struct {
	DagKey      string
	NodeKey     string
	RunID       int64
	ThreadID    string
	WakeupID    int64
	WakeupFence WakeupFence
}

// RecordNodeSpawnResult 报告本次 spawn 写入结果。
// PreviousThreadID 记录更新前的 thread id；发生覆盖时，store 会在同事务内写入
// node_spawn 事件。RunKey 为空表示未命中 running run，只是不追加事件，不影响节点写入。
type RecordNodeSpawnResult struct {
	Node             *Node
	PreviousThreadID string
	AppendedEvent    bool
	RunKey           string
}

// WakeupLeaseRenewer 只暴露 dispatching wakeup 的租约续约能力。
type WakeupLeaseRenewer interface {
	RenewWakeupLease(ctx context.Context, input RenewWakeupLeaseInput) (*Wakeup, int64, error)
}

// WakeupStore 管理 task_dag_wakeups 表的生命周期：入队、认领、发送、绑定 turn、
// 重试、失败、查询和回收过期 dispatching 条目。续约能力由 WakeupLeaseRenewer 单独暴露。
type WakeupStore interface {
	EnqueueWakeup(ctx context.Context, input EnqueueWakeupInput) (int64, error)
	ClaimDueWakeups(ctx context.Context, input ClaimDueWakeupsInput) ([]Wakeup, error)
	MarkWakeupSent(ctx context.Context, input MarkWakeupSentInput) (int64, error)
	BindWakeupTurn(ctx context.Context, input BindWakeupTurnInput) (int64, error)
	RetryWakeup(ctx context.Context, input RetryWakeupInput) (int64, error)
	FailWakeup(ctx context.Context, input FailWakeupInput) (int64, error)
	ReclaimStaleDispatchingWakeups(ctx context.Context) (int64, error)
	ListSentUnboundWakeups(ctx context.Context, targetAgentID string) ([]Wakeup, error)
	ListPendingOrDispatchingWakeups(ctx context.Context) ([]Wakeup, error)
	GetWakeup(ctx context.Context, id int64) (*Wakeup, error)
}

// DispatchIncompleteRecoveryStore 是启动/周期恢复扫描使用的窄端口。
// 它不嵌入 Store，避免普通 wakeup 测试 stub 被迫实现恢复扫描方法。
type DispatchIncompleteRecoveryStore interface {
	MarkDispatchIncompleteNodesWithoutActiveWakeup(ctx context.Context) ([]Node, error)
}

// WorkerLeaseStore 管理 task_dag_worker_leases 表：获取、续约和释放 worker 级独占锁。
type WorkerLeaseStore interface {
	AcquireWorkerLease(ctx context.Context, input AcquireWorkerLeaseInput) (int64, error)
	RenewWorkerLease(ctx context.Context, input RenewWorkerLeaseInput) (int64, error)
	ReleaseWorkerLease(ctx context.Context, input ReleaseWorkerLeaseInput) error
}

// ListDAGsFilter 是 ListDAGs 的过滤条件。
type ListDAGsFilter struct {
	Status  string
	Keyword string
	Limit   int32
}

// NodeStatusUpdate 是 UpdateNodeStatus 的入参。
type NodeStatusUpdate struct {
	Status         string
	ExpectedStatus string
	Result         json.RawMessage
	DagKey         string
	NodeKey        string
	RunID          int64
	WakeupID       int64
	WakeupAttempt  int32
}

// AssignNodeInput 是 AssignNode 的入参。
type AssignNodeInput struct {
	DagKey     string
	NodeKey    string
	RunID      int64
	AssignedTo string
}

// AssignNodeAndEnqueueWakeupInput 把 task_dispatch_node 的 assignment 和
// manual_dispatch wakeup 写入绑定为一个事务；两个子输入必须指向同一个 runtime node。
type AssignNodeAndEnqueueWakeupInput struct {
	Assign AssignNodeInput
	Wakeup EnqueueWakeupInput
}

// AssignNodeAndEnqueueWakeupResult 返回事务内更新后的节点和入队 wakeup id。
type AssignNodeAndEnqueueWakeupResult struct {
	Node     *Node
	WakeupID int64
}

// MarkDispatchIncompleteInput 描述需要恢复标记的 runtime node。
// AssignedTo 为空时不限定 assignee；非空时必须与节点当前 assigned_to 一致才会标记。
type MarkDispatchIncompleteInput struct {
	DagKey     string
	NodeKey    string
	RunID      int64
	AssignedTo string
}

// MarkDispatchIncompleteResult 报告 preflight 是否发现并标记了历史半写。
type MarkDispatchIncompleteResult struct {
	Marked       bool
	ActiveWakeup bool
	Node         *Node
}

// BindRunningNodeTurnInput 是 BindRunningNodeTurn 的入参，要求 RunID 非零。
type BindRunningNodeTurnInput struct {
	TurnID   string
	DagKey   string
	NodeKey  string
	RunID    int64
	WakeupID int64
}

// TouchRunningNodeEventInput 是 TouchRunningNodeEvent 的入参，ObservedAt 来自 turn 事件时间戳。
type TouchRunningNodeEventInput struct {
	ObservedAt time.Time
	DagKey     string
	NodeKey    string
	RunID      int64
	TurnID     string
}

// RunningNodeStatusUpdate 是 UpdateRunningNodeStatus 的入参，WakeupID 用于 fence 防止旧副本覆盖。
type RunningNodeStatusUpdate struct {
	Status      string
	Result      json.RawMessage
	WakeupID    int64
	WakeupFence WakeupFence
	DagKey      string
	NodeKey     string
	RunID       int64
}

// WakeupFence captures the dispatching wakeup lease that authorizes node launch side effects.
type WakeupFence struct {
	WakeupID       int64
	WakeupAttempt  int32
	ClaimedBy      string
	ClaimedAt      time.Time
	LeaseExpiresAt time.Time
}

type wakeupFenceContextKey struct{}

// ContextWithWakeupFence 将已领取 wakeup 的 lease fence 放入 ctx，供无法改签名的适配器透传。
func ContextWithWakeupFence(ctx context.Context, fence WakeupFence) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, wakeupFenceContextKey{}, fence)
}

// WakeupFenceFromContext 从 ctx 读取 dispatch wakeup fence；不存在时返回 ok=false。
func WakeupFenceFromContext(ctx context.Context) (WakeupFence, bool) {
	if ctx == nil {
		return WakeupFence{}, false
	}
	fence, ok := ctx.Value(wakeupFenceContextKey{}).(WakeupFence)
	return fence, ok
}

// CompleteNodeInput 是 CompleteNode / CompleteNodeAndScheduleDownstream 的入参。
type CompleteNodeInput struct {
	Status        string
	Result        json.RawMessage
	DagKey        string
	NodeKey       string
	RunID         int64
	WakeupID      int64
	WakeupAttempt int32
}

// NodeConfigPatchInput 是 smart retry 原子 patch runtime node.config 的入参。
type NodeConfigPatchInput struct {
	DagKey         string
	NodeKey        string
	RunID          int64
	PreviousConfig json.RawMessage
	Config         json.RawMessage
}

// RetryWakeupWithNodeConfigPatchInput 将 wakeup retry 和 node.config patch 绑定在同一事务。
type RetryWakeupWithNodeConfigPatchInput struct {
	RetryWakeup RetryWakeupInput
	NodeConfig  NodeConfigPatchInput
}

// OutputMaterializationClaimInput 是 ClaimNodeOutputMaterialization 的入参。
type OutputMaterializationClaimInput struct {
	Result  json.RawMessage
	DagKey  string
	NodeKey string
	RunID   int64
}

// CompleteNodeWithDownstreamResult 是 CompleteNodeAndScheduleDownstream 的返回值。
// Node 是刚完成的节点；ScheduledDownstream 只包含本事务中新插入 wakeup 的下游节点，
// 已因幂等冲突跳过的行不会出现在切片中。
//
// FinalizedRun 不为 nil 表示本次 complete 后所有节点已进入终态，
// store 同事务内把 task_dag_runs.status 从 'running' 推进到了对应终态；
// nil 表示 run 仍保持 'running'（还有非终态节点或本 dag_key 下无 running run）。
type CompleteNodeWithDownstreamResult struct {
	Node                *Node
	ScheduledDownstream []ScheduledDownstreamWakeup
	FinalizedRun        *FinalizedRunInfo
	// PromotedDownstream 列出本次 CompleteNode 同事务内被从 pending 推进到
	// ready 的下游节点。它与 ScheduledDownstream 的区别：
	//   - ScheduledDownstream：仅含「assigned_to 非空 + 成功 insert wakeup」的节点
	//   - PromotedDownstream：含所有「依赖满足 + status 从 pending 转为 ready」的节点，
	//     无论 assigned_to 是否为空（路由跳过仅影响 wakeup enqueue，
	//     不影响状态机推进）。
	PromotedDownstream []PromotedDownstreamNode
}

// PromotedDownstreamNode 描述本次 CompleteNode 事务内从 pending 推进到 ready
// 的一个下游节点。仅反映状态转移；wakeup 路由以 ScheduledDownstreamWakeup 为准。
type PromotedDownstreamNode struct {
	DagKey  string
	NodeKey string
	RunID   int64
}

// FinalizedRunInfo 是 maybeFinalizeRun 报告给上层的最小投影。
// 只暴露被推进的 run_key 和新 status，避免调用方依赖整行 run 快照。
type FinalizedRunInfo struct {
	RunKey string
	Status string
}

// ScheduledDownstreamWakeup 描述完成上游节点时顺带入队的下游 wakeup。
// 只记录新插入行的跨模块字段，供 dispatcher 和事件上报使用。
type ScheduledDownstreamWakeup struct {
	DagKey         string
	NodeKey        string
	RunID          int64
	TargetAgentID  string
	IdempotencyKey string
}

// DownstreamWakeupPayload 是写入 task_dag_wakeups.prompt_payload 的 wire 形状。
// 当前 DAG 路由主要通过 inputs.from_nodes 和 node.result 取上游上下文；
// UpstreamOutputs 仅保留给旧手工 wakeup payload 的兼容读取。
type DownstreamWakeupPayload struct {
	AgentID         string                  `json:"agent_id,omitempty"`
	UpstreamOutputs []DownstreamUpstreamRef `json:"upstream_outputs,omitempty"`
}

// DownstreamUpstreamRef 指向旧 wakeup payload 消费者可读取的上游产物路径。
type DownstreamUpstreamRef struct {
	NodeKey string `json:"node_key"`
	Path    string `json:"path"`
}

// FailNodeInput 是 FailNodeAndCancelDownstream 的入参。
// Reason 会写入节点 result 供排障；FailFast 保留给调用方区分是否取消无依赖失败的其它分支。
// 直接或间接依赖失败节点且仍 pending 的下游节点总会终态化，避免 run 卡住。
type FailNodeInput struct {
	DagKey        string
	NodeKey       string
	RunID         int64
	Reason        string
	FailFast      bool
	WakeupID      int64
	WakeupAttempt int32
}

// FailNodeResult 报告失败事务实际触达的节点。
// Node 是刚失败的主节点；CanceledDownstream 只列出同事务中从 pending 转 failed 的下游节点。
type FailNodeResult struct {
	Node               *Node
	OldStatus          string
	CanceledDownstream []CanceledDownstreamNode
	FinalizedRun       *FinalizedRunInfo
}

// CanceledDownstreamNode 描述一次 fail-fast 级联中被自动置失败的下游节点。
type CanceledDownstreamNode struct {
	DagKey  string
	NodeKey string
	RunID   int64
}

// failNodeReason 是 FailNodeAndCancelDownstream 写入 task_dag_nodes.result 的 JSON 形状。
// 主节点使用 exhausted_retries，下游级联节点使用 cascade，便于 UI 和日志区分原因。
type failNodeReason struct {
	Kind         string `json:"kind"`
	Reason       string `json:"reason,omitempty"`
	CausedByNode string `json:"caused_by_node,omitempty"`
}

// EnqueueWakeupInput 是 EnqueueWakeup 的入参，RunID 必须非零。
type EnqueueWakeupInput struct {
	DagKey         string
	NodeKey        string
	RunID          int64
	WakeupKind     string
	TargetAgentID  string
	PromptPayload  json.RawMessage
	IdempotencyKey string
}

// ClaimDueWakeupsInput 是 ClaimDueWakeups 的入参，LeaseInterval 格式同 intervalValue。
type ClaimDueWakeupsInput struct {
	ClaimedBy     string
	LeaseInterval string
	Limit         int32
}

// RenewWakeupLeaseInput 是执行副作用前的 CAS 续约入参，fence 字段来自当前 claim 行。
type RenewWakeupLeaseInput struct {
	LeaseInterval  string
	ID             int64
	ClaimedAt      time.Time
	ClaimedBy      string
	LeaseExpiresAt time.Time
}

// MarkWakeupSentInput 是 MarkWakeupSent 的入参，fence 字段来自 ClaimDueWakeups 返回行。
type MarkWakeupSentInput struct {
	ID             int64
	ClaimedAt      time.Time
	ClaimedBy      string
	LeaseExpiresAt time.Time
}

// BindWakeupTurnInput 是 BindWakeupTurn 的入参。
type BindWakeupTurnInput struct {
	TurnID string
	ID     int64
}

// RetryWakeupInput 是 RetryWakeup 的入参，fence 字段防止过期的 claim 覆盖新一轮调度。
type RetryWakeupInput struct {
	RetryInterval  string
	LastError      string
	MaxAttempts    int
	ID             int64
	ClaimedAt      time.Time
	ClaimedBy      string
	LeaseExpiresAt time.Time
}

// FailWakeupInput 是 FailWakeup 的入参，fence 字段同 RetryWakeupInput。
type FailWakeupInput struct {
	LastError      string
	ID             int64
	ClaimedAt      time.Time
	ClaimedBy      string
	LeaseExpiresAt time.Time
}

// AcquireWorkerLeaseInput 是 AcquireWorkerLease 的入参。
type AcquireWorkerLeaseInput struct {
	TargetAgentID string
	OwnerID       string
	LeaseInterval string
}

// RenewWorkerLeaseInput 是 RenewWorkerLease 的入参，OwnerID 不一致时续约失败（fencing）。
type RenewWorkerLeaseInput struct {
	LeaseInterval string
	TargetAgentID string
	OwnerID       string
}

// ReleaseWorkerLeaseInput 是 ReleaseWorkerLease 的入参，只允许同一 OwnerID 释放。
type ReleaseWorkerLeaseInput struct {
	TargetAgentID string
	OwnerID       string
}

// DAG 是 task_dags 表的一行，代表一个 DAG 模板定义。
type DAG struct {
	ID          int64
	DagKey      string
	Version     int64
	Title       string
	Description string
	Status      string
	CreatedBy   string
	Metadata    json.RawMessage
	Trigger     string
	CronExpr    string
	NextRunAt   *time.Time
	StartedAt   *time.Time
	FinishedAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// DAGSchedule 是 DAG 调度配置的只读投影，供 ApplyOps 读取后决定 cron 变更。
type DAGSchedule struct {
	Trigger  string
	CronExpr string
}

// UpdateDAGPatchInput 是 UpdateDAGPatch 的入参，指针字段为 nil 表示"不改该列"。
type UpdateDAGPatchInput struct {
	DagKey          string
	Title           *string
	Description     *string
	Trigger         *string
	CronExpr        *string
	OwnerID         *string
	NextRunAt       *time.Time
	ScheduleEnabled *bool
}

// Node 是 task_dag_nodes 表的一行，RunID 非 nil 时代表 run-scoped runtime 副本。
type Node struct {
	ID             int64
	DagKey         string
	NodeKey        string
	RunID          *int64
	Title          string
	NodeType       string
	AssignedTo     string
	DependsOn      json.RawMessage
	Status         string
	CommandRef     string
	Config         json.RawMessage
	Result         json.RawMessage
	StartedAt      *time.Time
	FinishedAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ActiveTurnID   *string
	ActiveWakeupID *int64
	LastEventAt    *time.Time
	// SpawningThreadID 记录 AgentExecutor 最近一次 spawn 出的 child agent thread id。
	// NULL 表示从未 spawn 或本节点非 agent；重试覆盖由 RecordNodeSpawn 同步写字段和事件。
	SpawningThreadID *string
}

// Wakeup 是 task_dag_wakeups 表的一行运行时投递记录。
type Wakeup struct {
	ID             int64
	DagKey         string
	NodeKey        string
	RunID          *int64
	WakeupKind     string
	TargetAgentID  string
	PromptPayload  json.RawMessage
	IdempotencyKey string
	Status         string
	AttemptCount   int32
	NextRetryAt    time.Time
	ClaimedAt      *time.Time
	ClaimedBy      string
	LeaseExpiresAt *time.Time
	SentAt         *time.Time
	BoundTurnID    *string
	TurnBoundAt    *time.Time
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// WorkerLease 是 runtime worker 对目标 agent 的租约记录。
type WorkerLease struct {
	TargetAgentID  string
	OwnerID        string
	LeaseExpiresAt time.Time
	UpdatedAt      time.Time
}

// Run 是 task_dag_runs 表的一行，代表 DAG 模板的一次执行实例。
// 模板定义和 run-scoped runtime 节点分开存储，避免多次运行互相覆盖状态。
type Run struct {
	ID                 int64
	RunKey             string
	DagKey             string
	DagVersionSnapshot int64
	TriggerSource      string // manual | auto | scheduled | external
	Status             string // running | succeeded | failed | cancelled
	StartedAt          time.Time
	FinishedAt         *time.Time
	Events             json.RawMessage // 字段位（Temporal-style replay）
	BudgetUsed         int64
	BudgetLimit        *int64
	Metadata           json.RawMessage
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// CreateRunInput 是 RunStore.CreateRun 的入参。
type CreateRunInput struct {
	RunKey             string
	DagKey             string
	DagVersionSnapshot int64
	TriggerSource      string
	Metadata           json.RawMessage
	BudgetLimit        *int64
}

// ListRunsFilter 是 RunStore.ListRuns 的过滤条件。
type ListRunsFilter struct {
	DagKey string // 必填
	Status string // 可选
	Limit  int32  // 0 表示走默认上限
}

// TerminateRunInput 取消一个 runtime run 及其所有非终态节点。
// RunID 必填，用来保证多 run 场景下只终止指定执行实例。
type TerminateRunInput struct {
	DagKey string
	RunKey string
	RunID  int64
	Reason string
}

// TerminateRunResult 返回终止 run 时事务内收集到的 spawned thread ids。
type TerminateRunResult struct {
	SpawnedThreadIDs []string
}

// RunTerminationStore 是只需要取消 run 的生命周期窄端口。
// RunStore 也包含该方法，让生产接线通过编译期断言保证实现完整。
type RunTerminationStore interface {
	TerminateRun(ctx context.Context, input TerminateRunInput) (TerminateRunResult, error)
}

// RunStore 是 task_dag_runs 的窄接口。
// 接口签名按 run 实例生命周期组织：
//   - CreateRun:                StartDAG 调用，新建一条 run 记录
//   - GetRun:                   按 run_key 取一条（也是 StartDAG GetRun-first 幂等路径）
//   - ListRuns:                 按 dag_key + 可选 status 列出最近 run
//     （默认 ORDER BY started_at DESC）
//   - PromoteRootNodesToReady:  StartDAG 在新 run 创建后调用，把 dag_key 下
//     depends_on=[] 且 status='pending' 的根节点提为
//     'ready'。返回受影响行数
//   - ScheduleRootWakeups:      StartDAG 在根节点 ready 后调用，只给有 assigned_to
//     的 runtime 根节点入队 wakeup；无 assigned_to 的根节点保持 ready 等人工接管
//   - TerminateRun:             取消一条 running run 及其非终态 runtime 节点 / wakeups
//     并返回事务内捕获的 spawned thread IDs
//   - WithRunTx:                在单一 DB 事务内组合调用其它 RunStore 方法
//     （例：StartDAG 原子性 CreateRun + Promote）。不
//     嵌入 OrchestrationStore / DAGMutationStore 是为了
//     保 InterfaceIsolation 预算，service 层独立持
//     有 runStore 字段。
//
// 活跃 run 并发限制由 DB 唯一约束兜底，不在 service 层保留事务外预检方法，
// 避免未来重新引入读后写竞态。
type RunStore interface {
	CreateRun(ctx context.Context, input CreateRunInput) (*Run, error)
	GetRun(ctx context.Context, runKey string) (*Run, error)
	ListRuns(ctx context.Context, filter ListRunsFilter) ([]Run, error)
	CloneNodesForRun(ctx context.Context, dagKey string, runID int64) (int64, error)
	PromoteRootNodesToReady(ctx context.Context, dagKey string, runID int64) (int64, error)
	ScheduleRootWakeups(ctx context.Context, dagKey string, runID int64) (int64, error)
	TerminateRun(ctx context.Context, input TerminateRunInput) (TerminateRunResult, error)
	WithRunTx(ctx context.Context, fn func(tx RunStore) error) error
}

// ScheduledStartTxStore 是 scheduled start 事务内需要的 run 和 DAG 写接口。
type ScheduledStartTxStore interface {
	RunStore
	GetDAGForUpdate(ctx context.Context, dagKey string) (*DAG, error)
	UpdateScheduledDAGNextRun(ctx context.Context, dagKey string, dueAt, nextRunAt time.Time) (int64, error)
}

// ScheduledStartStore 是 cron scheduled start 入口需要的幂等 run 和事务接口。
type ScheduledStartStore interface {
	GetRun(ctx context.Context, runKey string) (*Run, error)
	WithScheduledStartTx(ctx context.Context, fn func(tx ScheduledStartTxStore) error) error
}
