package heteroci

// RoutingLeaseSchemaVersion 是异构 CI 路由租约 wire schema 的冻结版本。
const RoutingLeaseSchemaVersion = "heteroci-routing-lease/v4"

// RoutingLeaseState 表示路由租约的生命周期状态。
type RoutingLeaseState string

const (
	// LeaseStatePlanned 表示租约已规划但尚未启动。
	LeaseStatePlanned RoutingLeaseState = "planned"
	// LeaseStateStarted 表示租约已开始执行。
	LeaseStateStarted RoutingLeaseState = "started"
	// LeaseStateSucceeded 表示租约执行成功。
	LeaseStateSucceeded RoutingLeaseState = "succeeded"
	// LeaseStateFailed 表示租约执行失败。
	LeaseStateFailed RoutingLeaseState = "failed"
	// LeaseStateCancelled 表示租约已取消。
	LeaseStateCancelled RoutingLeaseState = "cancelled"
)

// RoutingLease 描述一次路由决策对单个工作单元的执行租约。
type RoutingLease struct {
	// SchemaVersion 是本租约使用的 wire schema 版本。
	SchemaVersion string `json:"schema_version"`
	// Authority 是生成本租约的权威标识。
	Authority string `json:"authority"`
	// RunID 是关联本次执行运行的稳定标识。
	RunID string `json:"run_id"`
	// DecisionDigest 是关联路由决策的完整内容摘要。
	DecisionDigest string `json:"decision_digest"`
	// UnitID 是被租约绑定的工作单元稳定标识。
	UnitID string `json:"unit_id"`
	// Executor 是承载工作单元的执行器类别。
	Executor ExecutorKind `json:"executor"`
	// NodeID 是本地执行器节点标识。
	NodeID string `json:"node_id"`
	// Endpoint 是本地执行器节点的执行端点。
	Endpoint string `json:"endpoint"`
	// ProbeIdentity 是绑定节点容量与探针生命周期的稳定身份摘要。
	ProbeIdentity string `json:"probe_identity"`
	// ProbeBootID 是绑定节点当前启动生命周期的探针启动标识。
	ProbeBootID string `json:"probe_boot_id"`
	// State 是租约当前的生命周期状态。
	State RoutingLeaseState `json:"state"`
	// ReservedAtUnixMilli 是租约预留时间的 Unix 毫秒时间戳。
	ReservedAtUnixMilli int64 `json:"reserved_at_unix_milli"`
	// StartedAtUnixMilli 是租约开始执行时间的 Unix 毫秒时间戳。
	StartedAtUnixMilli int64 `json:"started_at_unix_milli"`
	// FinishedAtUnixMilli 是租约完成或终止时间的 Unix 毫秒时间戳。
	FinishedAtUnixMilli int64 `json:"finished_at_unix_milli"`
}

// validRoutingRunID 校验路由租约运行标识不为空、无首尾空白且不含控制字符。
func validRoutingRunID(value string) bool {
	return validExecutionUnitID(value)
}
