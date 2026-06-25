// Package taskdag 提供 task_dag 系列表（DAG 定义、节点、运行记录、wakeup、worker lease）
// 的存储层实现，是 mcp-orch DAG 编排的核心数据访问入口。
package taskdag

import (
	"context"
	"encoding/json"
	"time"
)

// Store is the backwards-compatible aggregate used by the taskdag storage
// module and low-level store tests. Production callers should request the
// narrow ports below (for example OrchestrationStore or RecoveryStore) instead
// of depending on this full method set.
type Store interface {
	OrchestrationStore
	DAGMutationStore
	DAGLockStore
	RunningNodeStore
	NodeFlowStore
	WakeupStore
	WorkerLeaseStore
}

// NodeSpawningThreadLookup is intentionally NOT embedded in Store — same
// rationale as NodeSpawnRecorderStore (see comment block on its type): the
// InterfaceIsolation budget (≤7 embedded ports) is preserved by exposing
// the narrow port via a dedicated fx provider (module.go::ProvideNodeSpawningThreadLookup)
// + compile-time guard (store_compile_assertions_test.go::var _
// NodeSpawningThreadLookup = (*store)(nil)). DAG subscriber / thread.stopped
// fallback consumers receive the narrow port directly through fx, not
// through the aggregate Store.

// OrchestrationStore is the narrow port consumed by cmd/mcp-orch/orchestration
// for public DAG CRUD/update flows.
// Note: RunStore (task_dag_runs CRUD) is intentionally NOT embedded here
// to keep this port within the InterfaceIsolation budget (<=0 direct,
// <=3 embedded). service 层持有独立 runStore 字段、与 dagStore 并列。
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
// Note: RunStore 不嵌入这里为了保持 InterfaceIsolation 预算
// (<=2 direct, <=1 embedded)。StartDAG 事务内需要 CreateRun +
// PromoteRootNodesToReady 时、未来（F6.x 阶段）通过扩展接口
// DAGMutationWithRunStore 拿到联合语义，不遭 InterfaceIsolation。
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

// RunNodeReadStore exposes runtime node snapshots without widening the
// aggregate Store interface. RunID=0 is reserved for template/legacy rows.
type RunNodeReadStore interface {
	ListRunNodes(ctx context.Context, dagKey string, runID int64) ([]Node, error)
}

// NodeStatusStore 提供节点状态更新的通用入口。
type NodeStatusStore interface {
	UpdateNodeStatus(ctx context.Context, input NodeStatusUpdate) (*Node, error)
}

// NodeConfigPatchStore is the narrow port used by dispatcher smart retry. It
// patches only task_dag_nodes.config under an old-config fence, avoiding the
// full UpsertNode overwrite semantics used by ApplyOps.
type NodeConfigPatchStore interface {
	PatchNodeConfigIfUnchanged(ctx context.Context, input NodeConfigPatchInput) (*Node, error)
}

// SmartRetryConfigStore is the dispatcher-only port for atomic smart-retry
// preparation. RetryWakeupWithNodeConfigPatch writes the wakeup retry fence and
// the node.config CAS patch in one transaction: a patch miss or DB error rolls
// back the retry so the caller can fail/park the same claimed wakeup visibly.
type SmartRetryConfigStore interface {
	RetryWakeupWithNodeConfigPatch(ctx context.Context, input RetryWakeupWithNodeConfigPatchInput) (int64, error)
}

// WakeupNodeFailureStore is the dispatcher port for committing a permanent
// wakeup failure and its DAG node fail/cascade in one transaction. Keeping it
// outside WakeupStore / NodeFlowStore preserves their interface budgets.
type WakeupNodeFailureStore interface {
	FailWakeupAndFailNodeAndCancelDownstream(ctx context.Context, wakeup FailWakeupInput, node FailNodeInput) (int64, *FailNodeResult, error)
}

// DAGOpsStore 是 task_dag_apply_ops 业务（F4.1+）的窄接口。包含：
//   - GetDAGVersionForUpdate: SELECT version FROM task_dags WHERE dag_key = ?
//     FOR UPDATE — 拿当前 OCC 版本，并在事务内锁定行避免双写。
//   - BumpDAGVersion:        UPDATE task_dags SET version = version + 1
//     WHERE dag_key = ? AND version = ? RETURNING version；
//     0 行受影响 → expected/actual 不匹配，调用方判 OCC 冲突。
//   - UpsertNode + ListNodes: 复用既有 store 接口。
//   - CountRunningRunsByDagKey: 在 DAG row FOR UPDATE 锁内读取 active run；
//     与 StartDAG 曾经的事务外预检不同，ApplyOps 用它来保护运行中的模板节点。
//
// 设计取舍：单独窄接口而非塞进 DAGMutationStore，原因同 BatchUpsertingNodeStore
// 注释 — DAGMutationStore 当前 2 direct + 1 embedded 处于 InterfaceIsolation
// 预算上限，加方法破预算。F4.x 完整落地后整体重构再讨论是否升预算。
//
// DAGOpsStore is the narrow port consumed by ApplyOps (F4.1+). It carries the
// OCC version helpers absent from the original DAGMutationStore; *store keeps
// the latter within its InterfaceIsolation budget.
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

// DAGOpsTxRunner 是 task_dag_apply_ops 在 PG 事务内跑业务的窄接口。调用方传
// fn，在同一事务内调 GetDAGVersionForUpdate / UpsertNode / BumpDAGVersion
// 以下泉到一起：OCC 校验 + 节点写入 + version 推进 原子化。
//
// 实现上与 UnitOfWorkStore.WithTx 同走事务 helper，但传出去的 store 接口是
// DAGOpsStore 而非 DAGMutationStore，避免造成 DAGMutationStore 超 Interface
// Isolation 预算。
type DAGOpsTxRunner interface {
	WithDAGOpsTx(ctx context.Context, fn func(tx DAGOpsStore) error) error
}

// DAGVersionReader 是「事务外读 DAG version」的窄端口。仅 ApplyOps 空 ops 短路路径
// 使用：拿当前 version 判定 OCC base_version 是否同庄，不需加锁 / 不需事务。
//
// 与 DAGOpsStore 上的 GetDAGVersionForUpdate 区别：
//   - DAGVersionReader.GetDAGVersion：事务外、只读、不加锁。
//   - DAGOpsStore.GetDAGVersionForUpdate：事务内、SELECT … FOR UPDATE。
//
// R3 P2 #3 引入：避免 ApplyOps 空 ops 路径走事务 + SELECT FOR UPDATE、白付 OCC
// 锁代价。
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
	UpdateAwaitingVerifyNodeStatus(ctx context.Context, input AwaitingVerifyNodeStatusUpdate) (*Node, error)
	CompleteNode(ctx context.Context, input CompleteNodeInput) (*Node, error)
}

// NodeFlowStore is the narrow port for DAG topology-aware node lifecycle
// operations (complete + cancel-downstream coupling). Phase 3.4/3.5 added these
// when CompleteNode/FailNode operations also need to schedule/cancel downstream
// work atomically — splitting them out keeps RunningNodeStore focused on
// standalone node updates and avoids the interface budget creep.
type NodeFlowStore interface {
	CompleteNodeAndScheduleDownstream(ctx context.Context, input CompleteNodeInput) (*CompleteNodeWithDownstreamResult, error)
	FailNodeAndCancelDownstream(ctx context.Context, input FailNodeInput) (*FailNodeResult, error)
	UpdateNodeStatusFlexible(ctx context.Context, input FlexibleNodeStatusUpdate) (*Node, error)
}

// NodeSpawnRecorderStore 是 F1.5 / ADR-009 下 nodeexec.AgentExecutor 写回
// spawning_thread_id 的窄端口。实现同样是 *store（编译期由
// store_compile_assertions_test.go 里 var _ NodeSpawnRecorderStore =
// (*store)(nil) 守住），不嵌入 OrchestrationStore / RunStore 是为了不破
// InterfaceIsolation 预算。
//
// NodeSpawnRecorderStore is the narrow port nodeexec.AgentExecutor (F1.5 /
// ADR-009) uses to write task_dag_nodes.spawning_thread_id and the matching
// node_spawn event into task_dag_runs.events. The same *store implements it
// (guarded by var _ NodeSpawnRecorderStore = (*store)(nil) in
// store_compile_assertions_test.go); it is intentionally not embedded into
// OrchestrationStore / RunStore to keep the InterfaceIsolation budget intact.
type NodeSpawnRecorderStore interface {
	RecordNodeSpawn(ctx context.Context, input RecordNodeSpawnInput) (*RecordNodeSpawnResult, error)
}

// NodeSpawningThreadLookup is the narrow port consumed by the ADR-017 v1.2
// DAG turn.completed subscriber (and the hookConsumer thread.stopped DAG
// fallback). It reverses task_dag_nodes.spawning_thread_id back to the node
// rows that spawned the given child thread id (ev.ThreadID).
//
// The same *store implements this port (guarded by
// store_compile_assertions_test.go via
// `var _ NodeSpawningThreadLookup = (*store)(nil)`), kept as a single-method
// narrow port so unit tests for the subscriber / fallback can inject a
// trivial mock without pulling in OrchestrationStore / NodeFlowStore.
//
// N>1 results are a normal occurrence on retry / recovery chains: the
// partial index idx_task_dag_nodes_spawning_thread_id (migration 0083) has
// no UNIQUE clause and F1.5's write entry-point is not single-writer. The
// subscriber iterates every returned node and applies idempotent state
// machine advancement on each (ADR-017 §2.2).
type NodeSpawningThreadLookup interface {
	LookupNodesBySpawningThread(ctx context.Context, threadID string) ([]Node, error)
}

// DispatchNodeStore 是 task_dispatch_node MCP 工具需要的窄端口：
//   - DAGDetailStore:    保留 GetDAG + 模板 ListNodes 兼容读取能力
//   - RunNodeReadStore:  F6.5 按 run_id 读取 runtime node
//   - AssignNode:        赋值 runtime node assigned_to
//   - EnqueueWakeup:     入队一条带 run_id 的 wakeup 让 dispatcher 能 pick
//
// 生产依赖仍然是同一个 *store (由 ProvideDispatchNodeStore type-assert)，
// 此处窄接口仅为了避免 service 层頻繁拿到全集合 Store 接口。
//
// DispatchNodeStore is the narrow port for the task_dispatch_node MCP tool.
// The production binding is the same *store (resolved via type assertion in
// ProvideDispatchNodeStore); using a narrow interface here keeps the service
// layer from depending on the aggregate Store.
type DispatchNodeStore interface {
	DAGDetailStore
	RunNodeReadStore
	AssignNode(ctx context.Context, input AssignNodeInput) (*Node, error)
	EnqueueWakeup(ctx context.Context, input EnqueueWakeupInput) (int64, error)
}

// RecordNodeSpawnInput 是 RecordNodeSpawn 的入参。仅 ThreadID 允许为空串
// 例外语义：ThreadID == "" 表示未获取到 child thread id（例如 launcher
// 返回了 nil），store 拒绝写入（fail-fast）避免错误覆盖之前的 thread id。
// DagKey / NodeKey 严格需要 trim 后非空。
//
// RecordNodeSpawnInput is the input for RecordNodeSpawn. Only ThreadID can be
// empty as an exceptional case, which the store rejects (fail-fast) so a
// missing remote thread id never accidentally erases a real one. DagKey /
// NodeKey must be non-empty after trim.
type RecordNodeSpawnInput struct {
	DagKey   string
	NodeKey  string
	RunID    int64
	ThreadID string
}

// RecordNodeSpawnResult 报告本次 spawn 写入的后果。调用方据此判断是否
// 需要上报「重试历史跳走」。PreviousThreadID 不为空且与 ThreadID 不同
// 表示这是一次重试覆盖，store 同事务内已 append了一条 node_spawn 事件到
// task_dag_runs.events（AppendedEvent=true）；无 running run 时 silently 变
// false，不走错路。RunKey 为空表示未命中上述运行中 run。
//
// RecordNodeSpawnResult reports the outcome. PreviousThreadID is the value
// captured by the CTE before the UPDATE took effect (empty when this is the
// first spawn). AppendedEvent=true means the store, inside the same
// transaction, appended a node_spawn entry to the matching running run's
// events jsonb array; false either because there was no prior thread to
// record or there is no running run for the dag_key (the latter is treated as
// a soft miss, not an error).
type RecordNodeSpawnResult struct {
	Node             *Node
	PreviousThreadID string
	AppendedEvent    bool
	RunKey           string
}

// WakeupStore 管理 task_dag_wakeups 表的全生命周期：入队、认领、发送、绑定 turn、
// 重试、失败和回收过期 dispatching 条目。
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
	Status  string
	Result  json.RawMessage
	DagKey  string
	NodeKey string
	RunID   int64
}

// AssignNodeInput 是 AssignNode 的入参。
type AssignNodeInput struct {
	DagKey     string
	NodeKey    string
	RunID      int64
	AssignedTo string
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
	Status   string
	Result   json.RawMessage
	WakeupID int64
	DagKey   string
	NodeKey  string
	RunID    int64
}

// AwaitingVerifyNodeStatusUpdate 是 UpdateAwaitingVerifyNodeStatus 的入参。
type AwaitingVerifyNodeStatusUpdate struct {
	Status  string
	Result  json.RawMessage
	DagKey  string
	NodeKey string
	RunID   int64
}

// CompleteNodeInput 是 CompleteNode / CompleteNodeAndScheduleDownstream 的入参。
type CompleteNodeInput struct {
	Status  string
	Result  json.RawMessage
	DagKey  string
	NodeKey string
	RunID   int64
}

type NodeConfigPatchInput struct {
	DagKey         string
	NodeKey        string
	RunID          int64
	PreviousConfig json.RawMessage
	Config         json.RawMessage
}

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

// CompleteNodeWithDownstreamResult is returned by
// CompleteNodeAndScheduleDownstream. Node is the freshly completed node row;
// ScheduledDownstream lists every downstream node for which a wakeup row was
// inserted (i.e. ON CONFLICT-skipped duplicates are excluded so callers can
// rely on the slice length reflecting newly-inserted rows only).
//
// F6.2: FinalizedRun 不为 nil 表示本次 complete 后所有节点已进入终态，
// store 同事务内把 task_dag_runs.status 从 'running' 推进到了对应终态；
// nil 表示 run 仍保持 'running'（还有非终态节点或本 dag_key 下无 running run）。
//
// F6.2: FinalizedRun is non-nil when this CompleteNode call also pushed the
// matching task_dag_runs row from 'running' to a terminal status in the same
// transaction. nil means the run stays 'running' (either some nodes are still
// non-terminal or no 'running' run exists for the dag_key).
type CompleteNodeWithDownstreamResult struct {
	Node                *Node
	ScheduledDownstream []ScheduledDownstreamWakeup
	FinalizedRun        *FinalizedRunInfo
	// F6.3: PromotedDownstream 列出本次 CompleteNode 同事务内被从 pending 推进到
	// ready 的下游节点。与 ScheduledDownstream 区别：
	//   - ScheduledDownstream：仅含「assigned_to 非空 + 成功 insert wakeup」的节点
	//   - PromotedDownstream：含所有「依赖满足 + status 从 pending 转为 ready」的节点，
	//     无论 assigned_to 是否为空（F6.4 路由跳过仅影响 wakeup enqueue，
	//     不影响状态机推进）。
	// PromotedDownstream lists nodes flipped pending→ready in this tx
	// (state-machine truth). ScheduledDownstream is a strict subset filtered to
	// rows that also got a wakeup enqueued.
	PromotedDownstream []PromotedDownstreamNode
}

// PromotedDownstreamNode 描述本次 CompleteNode 事务内从 pending 推进到 ready
// 的一个下游节点。仅反映状态转移；wakeup 路由以 ScheduledDownstreamWakeup 为准。
type PromotedDownstreamNode struct {
	DagKey  string
	NodeKey string
	RunID   int64
}

// FinalizedRunInfo 是 maybeFinalizeRun 报告给上层的最小投影（被推进的 run_key + 新 status）。
// FinalizedRunInfo is the minimal projection reported back to callers when a run
// transitions from 'running' to one of succeeded / failed / cancelled.
type FinalizedRunInfo struct {
	RunKey string
	Status string
}

// ScheduledDownstreamWakeup describes a wakeup row enqueued as the side-effect
// of completing an upstream node.
type ScheduledDownstreamWakeup struct {
	DagKey         string
	NodeKey        string
	RunID          int64
	TargetAgentID  string
	IdempotencyKey string
}

// DownstreamWakeupPayload is the JSON shape written into task_dag_wakeups.prompt_payload.
// Router-driven DAG dispatch now uses explicit inputs.from_nodes plus node.result
// envelopes for upstream context; CompleteNodeAndScheduleDownstream only populates
// AgentID. UpstreamOutputs remains for legacy/manual wakeup compatibility.
type DownstreamWakeupPayload struct {
	AgentID         string                  `json:"agent_id,omitempty"`
	UpstreamOutputs []DownstreamUpstreamRef `json:"upstream_outputs,omitempty"`
}

// DownstreamUpstreamRef points legacy wakeup payload consumers at an upstream artifact path.
type DownstreamUpstreamRef struct {
	NodeKey string `json:"node_key"`
	Path    string `json:"path"`
}

// FailNodeInput is consumed by FailNodeAndCancelDownstream when dispatcher
// (or any caller) decides a node has exhausted its retry budget. Reason is
// stored in the node's result column for forensic visibility; FailFast
// chooses whether to cascade into still-pending downstream nodes.
type FailNodeInput struct {
	DagKey   string
	NodeKey  string
	RunID    int64
	Reason   string
	FailFast bool
}

// FailNodeResult reports what the cascade actually touched. Node is the
// freshly-failed primary node; CanceledDownstream lists every transitive
// downstream node that was switched from `pending` to `failed` in the same
// transaction (empty when FailFast=false or no pending downstream existed).
type FailNodeResult struct {
	Node               *Node
	OldStatus          string
	CanceledDownstream []CanceledDownstreamNode
	FinalizedRun       *FinalizedRunInfo
}

// CanceledDownstreamNode describes a single downstream node that was
// auto-failed because of a fail-fast cascade.
type CanceledDownstreamNode struct {
	DagKey  string
	NodeKey string
	RunID   int64
}

// failNodeReason is the JSON shape written into task_dag_nodes.result when a
// node is failed via FailNodeAndCancelDownstream. Kind="exhausted_retries"
// for the primary node, Kind="cascade" for downstream nodes auto-failed by
// fail-fast.
type failNodeReason struct {
	Kind         string `json:"kind"`
	Reason       string `json:"reason,omitempty"`
	CausedByNode string `json:"caused_by_node,omitempty"`
}

// FlexibleNodeStatusUpdate 是 UpdateNodeStatusFlexible 的入参，不做状态机前置检查。
type FlexibleNodeStatusUpdate struct {
	Status  string
	Result  json.RawMessage
	DagKey  string
	NodeKey string
	RunID   int64
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
	// SpawningThreadID — DAG v2 F1.5 / ADR-009：AgentExecutor spawn 出的最近一次
	// child agent thread id；NULL 表示从未 spawn 或本节点非 agent。重试覆盖语义
	// 由 RecordNodeSpawn 写入，历史链走 task_dag_runs.events。
	SpawningThreadID *string
}

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

type WorkerLease struct {
	TargetAgentID  string
	OwnerID        string
	LeaseExpiresAt time.Time
	UpdatedAt      time.Time
}

// =====================================================
// DAG v2 骨架阶段 S3.5: task_dag_runs 类型 + RunStore 接口位
// =====================================================
//
// 蓝图 v2 §5 决策 C 混合：DAG 主表是模板，task_dag_runs 存每次执行实例。
// 骨架阶段：仅类型 + 接口签名；T1.2 加 SQL 与真实实现，并把 RunStore 并入 Store
// 聚合接口。当前 RunStore 不被任何聚合引用，避免破坏既有 store / 测试桩。

// Run 是 task_dag_runs 表的一行（参见 migrations/0074_dag_v2_runs.sql）。
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

// TerminateRunInput cancels one runtime run and all of its non-terminal nodes.
// RunID is mandatory to keep multi-run isolation exact.
type TerminateRunInput struct {
	DagKey string
	RunKey string
	RunID  int64
	Reason string
}

type TerminateRunResult struct {
	SpawnedThreadIDs []string
}

// RunTerminationStore is the narrow lifecycle port for callers that only need
// run cancellation. RunStore also embeds this method so production wiring gets
// a compile-time guarantee instead of a runtime-only type assertion.
type RunTerminationStore interface {
	TerminateRun(ctx context.Context, input TerminateRunInput) (TerminateRunResult, error)
}

// RunStore 是 task_dag_runs 的窄接口。接口实现由 store_compile_assertions_test.go
// 的 var _ RunStore = (*store)(nil) 编译期断言守护。
// 接口签名：
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
//   - WithRunTx:                在单一 PG 事务内组合调用其它 RunStore 方法
//     （例：StartDAG 原子性 CreateRun + Promote）。不
//     嵌入 OrchestrationStore / DAGMutationStore 是为了
//     保 InterfaceIsolation 预算，service 层独立持
//     有 runStore 字段。
//
// 历史注：原接口还含 CountActiveRunsByDagKey，service 用于多 run 并发预检。
// L3 根治后该预检被 DB partial unique（0076 migration）下沉到 DB 兑底，应
// 用层 CountActiveRunsByDagKey 变 dead method 后从接口删除避免未来再写 race。
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

type ScheduledStartTxStore interface {
	RunStore
	GetDAGForUpdate(ctx context.Context, dagKey string) (*DAG, error)
	UpdateScheduledDAGNextRun(ctx context.Context, dagKey string, dueAt, nextRunAt time.Time) (int64, error)
}

type ScheduledStartStore interface {
	GetRun(ctx context.Context, runKey string) (*Run, error)
	WithScheduledStartTx(ctx context.Context, fn func(tx ScheduledStartTxStore) error) error
}
