package heterociworkflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociowner"
)

// ManifestLoader 是 workflow 使用方提供的严格清单加载端口。
type ManifestLoader func(path string) (heteroci.Manifest, error)

const (
	// ResultStatusNotVerified 表示本地观察尚未取得远程权威资格。
	ResultStatusNotVerified = heteroci.ResultStatusNotVerified
)

// SnapshotAcquirer 是 workflow 使用方提供的实时节点快照端口。
type SnapshotAcquirer func(
	ctx context.Context,
	manifest heteroci.Manifest,
	ownerEpoch heteroci.RoutingOwnerEpoch,
	transport heteroci.ProbeTransport,
	now time.Time,
	heartbeatTTL time.Duration,
) (heteroci.SnapshotSet, error)

// DurableOwner 是 workflow 对 durable owner 的最小 mutation/read 端口。
type DurableOwner interface {
	Acquire(ctx context.Context) (heteroci.OwnerFence, error)
	LocateRun(ctx context.Context, runID heteroci.TopologyRunID) ([]heterociowner.DurableRunState, error)
	PlanAndReserve(
		ctx context.Context,
		fence heteroci.OwnerFence,
		request heteroci.TopologyPlanRequest,
	) (heterociowner.DurableReservedRoutingPlan, error)
	BindExecutors(
		ctx context.Context,
		mutation heteroci.ExecutorBindingMutation,
	) (heteroci.ExecutorBindingMutationResult, error)
	Start(
		ctx context.Context,
		fence heteroci.OwnerFence,
		operationID heteroci.OperationID,
		runID heteroci.TopologyRunID,
		unitID string,
		currentSnapshot heteroci.SnapshotSet,
	) (heteroci.StartedRoutingLease, error)
	Finish(
		ctx context.Context,
		fence heteroci.OwnerFence,
		operationID heteroci.OperationID,
		runID heteroci.TopologyRunID,
		unitID string,
		terminal heteroci.RoutingLeaseState,
	) (heterociowner.DurableLeaseTransition, error)
	CancelPlanned(
		ctx context.Context,
		fence heteroci.OwnerFence,
		operationID heteroci.OperationID,
		runID heteroci.TopologyRunID,
		unitID string,
	) (heterociowner.DurableLeaseTransition, error)
	ReadRun(ctx context.Context, runID heteroci.TopologyRunID) (heterociowner.DurableRunState, error)
}

// BatchExecutor 是单个已绑定执行端的批次执行占位接口。
// 返回值仅是本地非权威的 canonical per-unit outcome，不得写入 remote PASS authority。
type BatchExecutor interface {
	ExecuteBatch(ctx context.Context, batch ExecutionBatch) (BatchResult, error)
}

// BatchReceiptReader 读取 executor 已持久化的单次 batch receipt。
// ExecuteBatch 返回错误时，workflow 只能依赖该 receipt 决定终态，不能猜测失败或重执行。
type BatchReceiptReader interface {
	ReadBatchReceipt(ctx context.Context, batch ExecutionBatch) (BatchResult, bool, error)
}

// ExecutorBinding 是一个 executor key 与其完整 batch identity 集合的绑定计划项。
// BatchIDs 必须按字典序排列且在整个计划中唯一；它们是 binder 校验 lane 覆盖的稳定输入。
type ExecutorBinding struct {
	NodeID          string                `json:"node_id"`
	NodeIDDigest    string                `json:"node_id_digest"`
	ExecutorKind    heteroci.ExecutorKind `json:"executor_kind"`
	BatchIDs        []BatchID             `json:"batch_ids"`
	ShardDigest     string                `json:"shard_digest"`
	BatchSetDigest  string                `json:"batch_set_digest"`
	UnitSetDigest   string                `json:"unit_set_digest"`
	NodeFactsDigest string                `json:"node_facts_digest"`
}

// ExecutorBindingPlan 是一次 run 的完整、排序且去重的 executor binding 计划。
// 计划在任何 Start mutation 前一次性交给 binder，禁止按 key 逐次绑定。
type ExecutorBindingPlan struct {
	SchemaVersion          string                     `json:"schema_version"`
	ExternalOperationID    heteroci.OperationID       `json:"external_operation_id"`
	DurableRunID           heteroci.TopologyRunID     `json:"durable_run_id"`
	RequestDigest          string                     `json:"request_digest"`
	ManifestDigest         string                     `json:"manifest_digest"`
	ExternalBindingDigest  string                     `json:"external_binding_digest"`
	ManifestBlobSHA256     string                     `json:"manifest_blob_sha256"`
	RuntimePolicyDigest    string                     `json:"runtime_policy_digest"`
	TopologyManifestDigest string                     `json:"topology_manifest_digest"`
	PlanDigest             string                     `json:"plan_digest"`
	AuthorityGeneration    heteroci.RoutingOwnerEpoch `json:"authority_generation"`
	Bindings               []ExecutorBinding          `json:"bindings"`
	BindingDigest          string                     `json:"binding_digest"`
}

// ExecutorBinder 在所有 Start 发生前为一个 run 一次绑定全部 executor。
type ExecutorBinder interface {
	Bind(plan ExecutorBindingPlan) (map[ExecutorKey]BatchExecutor, error)
}

// ExecutorKey 标识一个不可替换的 local node execution lane。
type ExecutorKey struct {
	Kind   heteroci.ExecutorKind
	NodeID string
}

// ExecutionBatch 是一次恰好一次的本地节点波次批次。
type ExecutionBatch struct {
	BatchID     BatchID
	RunID       string
	Key         ExecutorKey
	Wave        int
	Units       []heteroci.ExecutionUnit
	Assignments []heteroci.RouteAssignment
	Started     []heteroci.StartedRoutingLease
	// MaterialDigest 绑定本批次完整执行材料；由 Start 后的 workflow 生成。
	MaterialDigest string
}

// BatchOutcome 是 executor 对单个 unit 的 canonical 非权威终态观察。
type BatchOutcome struct {
	UnitID string
	State  heteroci.RoutingLeaseState
}

// BatchResult 是一次 batch 的完整 per-unit 非权威结果集合。
type BatchResult struct {
	// BatchID 必须与本次 ExecutionBatch 的稳定 identity 完全一致。
	BatchID BatchID `json:"batch_id"`
	// RunID 必须与本次 ExecutionBatch 的 durable run 完全一致。
	RunID string `json:"run_id"`
	// Executor 必须与本次已绑定 executor lane 完全一致。
	Executor ExecutorKey `json:"executor"`
	// MaterialDigest 必须与本次完整 ExecutionBatch 材料摘要完全一致。
	MaterialDigest string `json:"material_digest"`
	// CommandDigest 必须是 executor 对 canonical command 的稳定摘要。
	CommandDigest string `json:"command_digest"`
	// Authority 必须明确为 LOCAL_NON_AUTHORITATIVE，不能被解释为 remote PASS。
	Authority string `json:"authority"`
	// Status 固定为 NOT_VERIFIED，表示本地执行观察尚未取得远程权威资格。
	Status   string         `json:"status"`
	Outcomes []BatchOutcome `json:"outcomes"`
}

// Dependencies 是 workflow 的显式运行时依赖集合。
type Dependencies struct {
	Owner          DurableOwner
	ProbeTransport heteroci.ProbeTransport
	Executors      ExecutorBinder
	// LifecycleObserver 在 production composition 中必须显式绑定；通用 domain tests 可省略。
	LifecycleObserver LifecycleObserver
	// CapacityBlockedObserver 在 production composition 中必须显式绑定；通用 domain tests 可省略。
	CapacityBlockedObserver CapacityBlockedObserver
	LoadManifest            ManifestLoader
	AcquireSnapshot         SnapshotAcquirer
	Now                     func() time.Time
	CleanupTimeout          time.Duration
}

// RunRequest 是一次异构 workflow 执行请求。
type RunRequest struct {
	SchemaVersion          string
	ExternalOperationID    heteroci.OperationID
	DurableRunID           heteroci.TopologyRunID
	RequestDigest          string
	ManifestDigest         string
	ExternalBindingDigest  string
	ManifestBlobSHA256     string
	RuntimePolicyDigest    string
	TopologyManifestDigest string
	ManifestPath           string
	RepositoryID           string
	SourceTreeSHA          string
	RunID                  string
	Units                  []heteroci.ExecutionUnit
	HeartbeatTTL           time.Duration
}

// RunResult 是 workflow 完成后的本地非权威投影。
type RunResult struct {
	Authority             string
	Status                string
	Fence                 heteroci.OwnerFence
	Manifest              heteroci.Manifest
	Plan                  heteroci.PackingPlan
	Decision              heteroci.RoutingDecision
	Leases                []heteroci.RoutingLease
	Units                 []UnitResult
	BindingPlan           ExecutorBindingPlan
	ExecutorBindingDigest string
}

// UnitResult 记录一个 unit 的 durable terminal lease 状态和执行 batch。
type UnitResult struct {
	Authority string
	Status    string
	UnitID    string
	BatchID   BatchID
	State     heteroci.RoutingLeaseState
}

// BatchOutcomeError 表示 executor 明确报告某个 unit 未成功。
// 该错误只表达本地非权威观察，不代表 remote PASS/FAIL authority。
type BatchOutcomeError struct {
	BatchID BatchID
	UnitID  string
}

// Error 返回不泄露 endpoint 或探针载荷的稳定失败描述。
func (err *BatchOutcomeError) Error() string {
	return fmt.Sprintf("heteroci workflow batch %q reported failed unit %q", err.BatchID, err.UnitID)
}

// Unwrap 允许调用方用 errors.Is 识别 batch failure 类别。
func (*BatchOutcomeError) Unwrap() error {
	return ErrBatchFailed
}

// Workflow 持有一次运行所需的已校验依赖。
type Workflow struct {
	deps Dependencies
}

// Sentinel errors classify fail-fast workflow boundaries without exposing endpoint data.
var (
	ErrInvalidDependencies  = errors.New("heteroci workflow dependencies are invalid")
	ErrInvalidRequest       = errors.New("heteroci workflow request is invalid")
	ErrExecutionState       = errors.New("heteroci workflow execution state is unknown")
	ErrBatchResult          = errors.New("heteroci workflow batch result is invalid")
	ErrExecutorBinding      = errors.New("heteroci workflow executor binding failed")
	ErrCleanupIncomplete    = errors.New("heteroci workflow cleanup is incomplete")
	ErrBatchReceiptRequired = errors.New("heteroci workflow durable batch receipt is required")
	ErrBatchFailed          = errors.New("heteroci workflow batch reported failed unit")
)

// New 校验 workflow 依赖；所有生产端口必须由调用方显式绑定，不使用隐式 fallback。
func New(deps Dependencies) (*Workflow, error) {
	if isNilWorkflowValue(deps.Owner) || isNilWorkflowValue(deps.ProbeTransport) || isNilWorkflowValue(deps.Executors) {
		return nil, ErrInvalidDependencies
	}
	if _, ok := deps.Owner.(interface {
		LocateRun(context.Context, heteroci.TopologyRunID) ([]heterociowner.DurableRunState, error)
	}); !ok {
		return nil, fmt.Errorf("%w: durable run locator is required", ErrInvalidDependencies)
	}
	if deps.LoadManifest == nil || deps.AcquireSnapshot == nil || deps.Now == nil || deps.CleanupTimeout <= 0 {
		return nil, fmt.Errorf("%w: explicit loader, snapshot acquirer, clock, and cleanup timeout are required", ErrInvalidDependencies)
	}
	return &Workflow{deps: deps}, nil
}

// isNilWorkflowValue 识别 typed nil dependency，避免把 nil executor 当作已绑定。
func isNilWorkflowValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
