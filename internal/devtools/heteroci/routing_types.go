package heteroci

import (
	"fmt"
	"sort"
	"strings"
)

// RoutingDecisionSchemaVersion 是异构 CI 路由决策 wire schema 的冻结版本。
const RoutingDecisionSchemaVersion = "heteroci-routing-decision/v2"

// ExecutorKind 表示执行工作单元的唯一执行器类别。
type ExecutorKind string

const (
	// ExecutorKindLocalNode 表示使用本地异构 CI 节点执行。
	ExecutorKindLocalNode ExecutorKind = "local_node"
	// ExecutorKindAliyunECI 表示使用阿里云 ECI 执行。
	ExecutorKindAliyunECI ExecutorKind = "aliyun_eci"
)

// ECIResourceTier 表示阿里云 ECI 的固定资源档位。
type ECIResourceTier string

const (
	// ECITier2C4GiB 表示 2 CPU、4 GiB 内存的 ECI 档位。
	ECITier2C4GiB ECIResourceTier = "aliyun-eci/2c4g"
	// ECITier4C8GiB 表示 4 CPU、8 GiB 内存的 ECI 档位。
	ECITier4C8GiB ECIResourceTier = "aliyun-eci/4c8g"
	// ECITier8C16GiB 表示 8 CPU、16 GiB 内存的 ECI 档位。
	ECITier8C16GiB ECIResourceTier = "aliyun-eci/8c16g"
)

const gibibyte = int64(1 << 30)

// Capacity 返回 ECI 档位的固定资源容量；未知档位返回 false。
func (tier ECIResourceTier) Capacity() (ResourceCapacity, bool) {
	switch tier {
	case ECITier2C4GiB:
		return ResourceCapacity{CPUMilli: 2_000, MemoryBytes: 4 * gibibyte, MaxConcurrency: 1}, true
	case ECITier4C8GiB:
		return ResourceCapacity{CPUMilli: 4_000, MemoryBytes: 8 * gibibyte, MaxConcurrency: 1}, true
	case ECITier8C16GiB:
		return ResourceCapacity{CPUMilli: 8_000, MemoryBytes: 16 * gibibyte, MaxConcurrency: 1}, true
	default:
		return ResourceCapacity{}, false
	}
}

// RouteAssignment 将一个工作单元绑定到唯一执行器及其资源位置。
type RouteAssignment struct {
	// UnitID 是工作单元的稳定标识。
	UnitID string `json:"unit_id"`
	// Executor 是承载工作单元的执行器类别。
	Executor ExecutorKind `json:"executor"`
	// NodeID 是本地执行器节点标识；ECI 执行时为空。
	NodeID string `json:"node_id"`
	// Endpoint 是执行器连接端点；本地执行时必须等于快照端点，ECI 执行时必须为空。
	Endpoint string `json:"endpoint"`
	// ProbeIdentity 是执行器探针声明的稳定身份；本地执行时必须等于快照身份，ECI 执行时必须为空。
	ProbeIdentity string `json:"probe_identity"`
	// ProbeBootID 是执行器探针声明的启动实例标识；本地执行时必须等于快照启动标识，ECI 执行时必须为空。
	ProbeBootID string `json:"probe_boot_id"`
	// ECITier 是 ECI 执行时使用的固定资源档位；本地执行时为空。
	ECITier ECIResourceTier `json:"eci_tier"`
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

// smallestFittingECITier 返回同时满足工作单元 CPU 与内存需求的最小 ECI 档位。
func smallestFittingECITier(unit ExecutionUnit) (ECIResourceTier, bool) {
	if unit.CPUMilli <= 0 || unit.MemoryBytes <= 0 {
		return "", false
	}
	for _, tier := range []ECIResourceTier{ECITier2C4GiB, ECITier4C8GiB, ECITier8C16GiB} {
		capacity, ok := tier.Capacity()
		if !ok {
			return "", false
		}
		if unit.CPUMilli <= capacity.CPUMilli && unit.MemoryBytes <= capacity.MemoryBytes {
			return tier, true
		}
	}
	return "", false
}
