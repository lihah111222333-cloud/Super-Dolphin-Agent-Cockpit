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
// PromoteRootNodesToReady 时、通过扩展接口 DAGMutationWithRunStore
// （commit 2 引入）拿到联合语义，不遭 InterfaceIsolation。
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
type CompleteNodeWithDownstreamResult struct {
	Node                *Node
	ScheduledDownstream []ScheduledDownstreamWakeup
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

// RunStore 是 task_dag_runs 的窄接口（T1.2 接通；T0.5 archtest 守护至此转正向）。
// 接口签名：
//   - CreateRun:                StartDAG 调用，新建一条 run 记录
//   - GetRun:                   按 run_key 取一条
//   - ListRuns:                 按 dag_key + 可选 status 列出最近 run
//     （默认 ORDER BY started_at DESC）
//   - CountActiveRunsByDagKey:  StartDAG 多 run 并发 reject 用（T1.2-mid 限制；
//     F6.5 升级 multi-run 后此方法不再被 StartDAG 调用）
//   - PromoteRootNodesToReady:  StartDAG 在新 run 创建后调用，把 dag_key 下
//     depends_on=[] 且 status='pending' 的根节点提为
//     'ready'。返回受影响行数
type RunStore interface {
	CreateRun(ctx context.Context, input CreateRunInput) (*Run, error)
	GetRun(ctx context.Context, runKey string) (*Run, error)
	ListRuns(ctx context.Context, filter ListRunsFilter) ([]Run, error)
	CountActiveRunsByDagKey(ctx context.Context, dagKey string) (int64, error)
	PromoteRootNodesToReady(ctx context.Context, dagKey string) (int64, error)
}
