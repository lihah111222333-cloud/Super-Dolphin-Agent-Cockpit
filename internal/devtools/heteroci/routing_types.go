package heteroci

import (
	"fmt"
	"sort"
	"strings"
)

// RoutingDecisionSchemaVersion 是异构 CI 路由决策 wire schema 的冻结版本。
const RoutingDecisionSchemaVersion = "heteroci-routing-decision/v4"

// ExecutorKind 表示执行工作单元的唯一执行器类别。
type ExecutorKind string

const (
	// ExecutorKindLocalNode 表示使用本地异构 CI 节点执行。
	ExecutorKindLocalNode ExecutorKind = "local_node"
)

// RouteAssignment 将一个工作单元绑定到唯一执行器及其资源位置。
type RouteAssignment struct {
	// UnitID 是工作单元的稳定标识。
	UnitID string `json:"unit_id"`
	// Executor 是承载工作单元的执行器类别。
	Executor ExecutorKind `json:"executor"`
	// NodeID 是本地执行器节点标识。
	NodeID string `json:"node_id"`
	// Endpoint 是执行器连接端点，必须等于快照端点。
	Endpoint string `json:"endpoint"`
	// ProbeIdentity 是执行器探针声明的稳定身份，必须等于快照身份。
	ProbeIdentity string `json:"probe_identity"`
	// ProbeBootID 是执行器探针声明的启动实例标识，必须等于快照启动标识。
	ProbeBootID string `json:"probe_boot_id"`
}

// RoutingDecision 描述一次绑定唯一执行器的异构 CI 路由决策。
type RoutingDecision struct {
	// SchemaVersion 是本决策使用的 wire schema 版本。
	SchemaVersion string `json:"schema_version"`
	// Authority 是生成本决策的权威标识。
	Authority string `json:"authority"`
	// SnapshotCapturedAtUnixMilli 是决策所依据快照的采集时间。
	SnapshotCapturedAtUnixMilli int64 `json:"snapshot_captured_at_unix_milli"`
	// PackingPlanDigest 是关联装箱规划的摘要。
	PackingPlanDigest string `json:"packing_plan_digest"`
	// Assignments 是所有工作单元的确定性执行器绑定列表。
	Assignments []RouteAssignment `json:"assignments"`
	// DecisionDigest 是本决策完整内容的摘要。
	DecisionDigest string `json:"decision_digest"`
}

// CapacityBlockerReasonCapacityUnavailable 是稳定的容量不足 reason code。
const CapacityBlockerReasonCapacityUnavailable = "CAPACITY_UNAVAILABLE"

// CapacityBlocker 记录工作单元无法获得合规执行容量的原因。
type CapacityBlocker struct {
	// UnitID 是被阻塞工作单元的稳定标识。
	UnitID string `json:"unit_id"`
	// TargetPlatform 是工作单元要求的规范目标平台。
	TargetPlatform Platform `json:"target_platform"`
	// RequiredRuntime 是工作单元要求的规范执行运行时。
	RequiredRuntime RuntimeKind `json:"required_runtime"`
	// Reason 是不包含节点端点的稳定阻塞原因。
	Reason string `json:"reason"`
}

// CapacityBlockerError 表示一个或多个工作单元没有可用的合规容量。
type CapacityBlockerError struct {
	// Blockers 是按工作单元稳定标识排序的容量阻塞列表。
	Blockers []CapacityBlocker `json:"blockers"`
}

// Error 返回不包含节点端点且与输入顺序无关的容量阻塞描述。
func (err CapacityBlockerError) Error() string {
	if len(err.Blockers) == 0 {
		return "capacity blocker: no blocked units"
	}
	blockers := append([]CapacityBlocker(nil), err.Blockers...)
	sort.SliceStable(blockers, func(left, right int) bool {
		return capacityBlockerLess(blockers[left], blockers[right])
	})
	parts := make([]string, len(blockers))
	for index, blocker := range blockers {
		parts[index] = fmt.Sprintf(
			"unit_id=%q target_platform=%q required_runtime=%q: capacity unavailable",
			blocker.UnitID,
			blocker.TargetPlatform,
			blocker.RequiredRuntime,
		)
	}
	return "capacity blocker: " + strings.Join(parts, "; ")
}

// capacityBlockerLess 为容量阻塞错误提供完整且稳定的排序键。
func capacityBlockerLess(left, right CapacityBlocker) bool {
	if left.UnitID != right.UnitID {
		return left.UnitID < right.UnitID
	}
	if left.TargetPlatform != right.TargetPlatform {
		return left.TargetPlatform < right.TargetPlatform
	}
	if left.RequiredRuntime != right.RequiredRuntime {
		return left.RequiredRuntime < right.RequiredRuntime
	}
	return left.Reason < right.Reason
}
