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
	// NodeSpawnRecorderStore 是 F1.5 / ADR-009 加入的窄端口。嵌入聚合 Store
	// 接口让 unit tests 能用同一个 aggregate handle 调用 RecordNodeSpawn；
	// 生产 wiring 仍鼓励提取独立 NodeSpawnRecorderStore 使用。
	//
	// NodeSpawnRecorderStore was added in F1.5 / ADR-009. It is embedded into
	// the aggregate Store so unit tests can reach RecordNodeSpawn through the
	// same handle they already use for the other narrow ports; production
	// wiring should still depend on the narrow NodeSpawnRecorderStore alone.
	NodeSpawnRecorderStore
}

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

type DAGReadStore interface {
	DAGDetailStore
	ListDAGs(ctx context.Context, filter ListDAGsFilter) ([]DAG, error)
}

type DAGDetailStore interface {
	GetDAG(ctx context.Context, dagKey string) (*DAG, error)
	ListNodes(ctx context.Context, dagKey string) ([]Node, error)
}

type NodeStatusStore interface {
	UpdateNodeStatus(ctx context.Context, input NodeStatusUpdate) (*Node, error)
}

type DAGLockStore interface {
	GetDAGForUpdate(ctx context.Context, dagKey string) (*DAG, error)
	GetNodesForUpdate(ctx context.Context, dagKey string) ([]Node, error)
}

type RecoveryStore interface {
	ListRunningNodesByAssignee(ctx context.Context, assignee string) ([]Node, error)
	GetWakeup(ctx context.Context, id int64) (*Wakeup, error)
}

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
	Node               *Node
	PreviousThreadID   string
	AppendedEvent      bool
	RunKey             string
}

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

type WorkerLeaseStore interface {
	AcquireWorkerLease(ctx context.Context, input AcquireWorkerLeaseInput) (int64, error)
	RenewWorkerLease(ctx context.Context, input RenewWorkerLeaseInput) (int64, error)
	ReleaseWorkerLease(ctx context.Context, input ReleaseWorkerLeaseInput) error
}

type ListDAGsFilter struct {
	Status  string
	Keyword string
	Limit   int32
}

type NodeStatusUpdate struct {
	Status  string
	Result  json.RawMessage
	DagKey  string
	NodeKey string
}

type BindRunningNodeTurnInput struct {
	TurnID   string
	DagKey   string
	NodeKey  string
	WakeupID int64
}

type TouchRunningNodeEventInput struct {
	ObservedAt time.Time
	DagKey     string
	NodeKey    string
	TurnID     string
}

type RunningNodeStatusUpdate struct {
	Status   string
	Result   json.RawMessage
	WakeupID int64
	DagKey   string
	NodeKey  string
}

type AwaitingVerifyNodeStatusUpdate struct {
	Status  string
	Result  json.RawMessage
	DagKey  string
	NodeKey string
}

type CompleteNodeInput struct {
	Status  string
	Result  json.RawMessage
	DagKey  string
	NodeKey string
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
	TargetAgentID  string
	IdempotencyKey string
}

// DownstreamWakeupPayload is the JSON shape written into
// task_dag_wakeups.prompt_payload by CompleteNodeAndScheduleDownstream. Phase
// 3.4 only populates AgentID + UpstreamOutputs; Phase 3.9 will fill in the
// dispatcher-side prompt-rewriting that consumes UpstreamOutputs.
type DownstreamWakeupPayload struct {
	AgentID         string                  `json:"agent_id,omitempty"`
	UpstreamOutputs []DownstreamUpstreamRef `json:"upstream_outputs,omitempty"`
}

// DownstreamUpstreamRef points the downstream node at the producing upstream
// node's output artifact path. Path follows plan §3.4 convention
// `dag/<dagKey>/<prevKey>/output.json`.
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
	Reason   string
	FailFast bool
}

// FailNodeResult reports what the cascade actually touched. Node is the
// freshly-failed primary node; CanceledDownstream lists every transitive
// downstream node that was switched from `pending` to `failed` in the same
// transaction (empty when FailFast=false or no pending downstream existed).
type FailNodeResult struct {
	Node               *Node
	CanceledDownstream []CanceledDownstreamNode
}

// CanceledDownstreamNode describes a single downstream node that was
// auto-failed because of a fail-fast cascade.
type CanceledDownstreamNode struct {
	DagKey  string
	NodeKey string
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

type FlexibleNodeStatusUpdate struct {
	Status  string
	Result  json.RawMessage
	DagKey  string
	NodeKey string
}

type EnqueueWakeupInput struct {
	DagKey         string
	NodeKey        string
	WakeupKind     string
	TargetAgentID  string
	PromptPayload  json.RawMessage
	IdempotencyKey string
}

type ClaimDueWakeupsInput struct {
	ClaimedBy     string
	LeaseInterval string
	Limit         int32
}

type MarkWakeupSentInput struct {
	ID             int64
	ClaimedAt      time.Time
	ClaimedBy      string
	LeaseExpiresAt time.Time
}

type BindWakeupTurnInput struct {
	TurnID string
	ID     int64
}

type RetryWakeupInput struct {
	RetryInterval  string
	LastError      string
	ID             int64
	ClaimedAt      time.Time
	ClaimedBy      string
	LeaseExpiresAt time.Time
}

type FailWakeupInput struct {
	LastError      string
	ID             int64
	ClaimedAt      time.Time
	ClaimedBy      string
	LeaseExpiresAt time.Time
}

type AcquireWorkerLeaseInput struct {
	TargetAgentID string
	OwnerID       string
	LeaseInterval string
}

type RenewWorkerLeaseInput struct {
	LeaseInterval string
	TargetAgentID string
	OwnerID       string
}

type ReleaseWorkerLeaseInput struct {
	TargetAgentID string
	OwnerID       string
}

type DAG struct {
	ID          int64
	DagKey      string
	Title       string
	Description string
	Status      string
	CreatedBy   string
	Metadata    json.RawMessage
	StartedAt   *time.Time
	FinishedAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Node struct {
	ID             int64
	DagKey         string
	NodeKey        string
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
	PromoteRootNodesToReady(ctx context.Context, dagKey string) (int64, error)
	WithRunTx(ctx context.Context, fn func(tx RunStore) error) error
}
