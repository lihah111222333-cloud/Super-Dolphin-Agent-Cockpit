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
	WakeupStore
	WorkerLeaseStore
}

// OrchestrationStore is the narrow port consumed by cmd/mcp-orch/orchestration
// for public DAG CRUD/update flows.
type OrchestrationStore interface {
	UnitOfWorkStore
	DAGReadStore
	NodeStatusStore
}

type UnitOfWorkStore interface {
	WithTx(ctx context.Context, fn func(txStore DAGMutationStore) error) error
}

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
