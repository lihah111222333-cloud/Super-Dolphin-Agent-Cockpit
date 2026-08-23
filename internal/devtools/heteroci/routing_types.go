package heteroci

import (
	"errors"
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

// CapacityBlocker 记录工作单元无法获得合规执行容量的可重放证据。
type CapacityBlocker struct {
	// UnitID 是被阻塞工作单元的稳定标识。
	UnitID string `json:"unit_id"`
	// TargetPlatform 是工作单元要求的规范目标平台。
	TargetPlatform Platform `json:"target_platform"`
	// RequiredRuntime 是工作单元要求的规范执行运行时/能力。
	RequiredRuntime RuntimeKind `json:"required_runtime"`
	// ReasonCode 是不包含节点端点的稳定阻塞原因码。
	ReasonCode string `json:"reason_code"`
	// PackingPlanDigest 是产生该阻塞证据的 canonical plan 摘要。
	PackingPlanDigest string `json:"packing_plan_digest"`
}

// CapacityBlockerError 表示一个或多个工作单元没有可用的合规容量。
type CapacityBlockerError struct {
	// PackingPlanDigest 是所有 blocker 共同绑定的 canonical plan 摘要。
	PackingPlanDigest string `json:"packing_plan_digest"`
	// Blockers 是按稳定完整键排序的容量阻塞列表。
	Blockers []CapacityBlocker `json:"blockers"`
}

// CapacityBlockerProjection 是上层显式绑定 manifest digest 后的 remote blocker DTO。
type CapacityBlockerProjection struct {
	// ManifestDigest 是调用方显式提供的 manifest 摘要；core 不自行推导。
	ManifestDigest string `json:"manifest_digest"`
	// PackingPlanDigest 是 blocker 绑定的 canonical plan 摘要。
	PackingPlanDigest string `json:"packing_plan_digest"`
	// Blockers 是按稳定完整键排序的容量阻塞列表。
	Blockers []CapacityBlocker `json:"blockers"`
}

// Validate 校验 projection 的显式 manifest digest 与 typed blocker 证据。
func (projection CapacityBlockerProjection) Validate() error {
	if !isSHA256Digest(projection.ManifestDigest) {
		return errors.New("capacity blocker projection manifest digest is invalid")
	}
	return (CapacityBlockerError{PackingPlanDigest: projection.PackingPlanDigest, Blockers: projection.Blockers}).Validate()
}

// NewCapacityBlockerError 从 canonical plan 生成带 exact plan digest 的 typed blocker error。
func NewCapacityBlockerError(plan PackingPlan, blockers []CapacityBlocker) (*CapacityBlockerError, error) {
	digest, err := plan.CanonicalDigest()
	if err != nil {
		return nil, fmt.Errorf("compute capacity blocker plan digest: %w", err)
	}
	if !isSHA256Digest(digest) {
		return nil, errors.New("capacity blocker plan digest is invalid")
	}
	bound := append([]CapacityBlocker(nil), blockers...)
	for index := range bound {
		if bound[index].PackingPlanDigest != "" && bound[index].PackingPlanDigest != digest {
			return nil, errors.New("capacity blocker plan digest drifted")
		}
		bound[index].PackingPlanDigest = digest
	}
	sort.Slice(bound, func(left, right int) bool { return capacityBlockerLess(bound[left], bound[right]) })
	result := &CapacityBlockerError{PackingPlanDigest: digest, Blockers: bound}
	if err := result.Validate(); err != nil {
		return nil, err
	}
	return result, nil
}

// Validate 校验 plan digest、typed blocker 字段和稳定排序。
func (err CapacityBlockerError) Validate() error {
	if !isSHA256Digest(err.PackingPlanDigest) {
		return errors.New("capacity blocker error plan digest is invalid")
	}
	if len(err.Blockers) == 0 {
		return errors.New("capacity blocker error has no blockers")
	}
	for index, blocker := range err.Blockers {
		if validationErr := validateCapacityBlocker(blocker, err.PackingPlanDigest); validationErr != nil {
			return validationErr
		}
		if index > 0 && !capacityBlockerLess(err.Blockers[index-1], blocker) {
			return errors.New("capacity blockers must be strictly sorted")
		}
	}
	return nil
}

// ValidateAgainstPlan 校验 error 携带的 digest 与给定 canonical plan exact 相等。
func (err CapacityBlockerError) ValidateAgainstPlan(plan PackingPlan) error {
	if validationErr := err.Validate(); validationErr != nil {
		return validationErr
	}
	digest, digestErr := plan.CanonicalDigest()
	if digestErr != nil {
		return fmt.Errorf("compute capacity blocker plan digest: %w", digestErr)
	}
	if digest != err.PackingPlanDigest {
		return errors.New("capacity blocker plan digest does not match plan")
	}
	return nil
}

// Project 将显式提供的 manifest digest 绑定到 typed blocker DTO，不在 core 伪造 manifest 身份。
func (err CapacityBlockerError) Project(manifestDigest string) (CapacityBlockerProjection, error) {
	if validationErr := err.Validate(); validationErr != nil {
		return CapacityBlockerProjection{}, validationErr
	}
	if !isSHA256Digest(manifestDigest) {
		return CapacityBlockerProjection{}, errors.New("capacity blocker manifest digest is invalid")
	}
	return CapacityBlockerProjection{
		ManifestDigest: manifestDigest, PackingPlanDigest: err.PackingPlanDigest,
		Blockers: append([]CapacityBlocker(nil), err.Blockers...),
	}, nil
}

func validateCapacityBlocker(blocker CapacityBlocker, planDigest string) error {
	if !validExecutionUnitID(blocker.UnitID) || !isCanonicalPlatform(blocker.TargetPlatform) || !isCanonicalRuntime(blocker.RequiredRuntime) {
		return errors.New("capacity blocker identity is invalid")
	}
	if !executionUnitRuntimeSupportsPlatform(blocker.RequiredRuntime, blocker.TargetPlatform) {
		return errors.New("capacity blocker runtime does not support target platform")
	}
	if blocker.ReasonCode != CapacityBlockerReasonCapacityUnavailable {
		return errors.New("capacity blocker reason code is invalid")
	}
	if blocker.PackingPlanDigest != planDigest {
		return errors.New("capacity blocker plan digest does not match error")
	}
	return nil
}

// Error 返回不包含节点端点且与输入顺序无关的容量阻塞描述。
func (err CapacityBlockerError) Error() string {
	if len(err.Blockers) == 0 {
		return "capacity blocker: no blocked units"
	}
	blockers := append([]CapacityBlocker(nil), err.Blockers...)
	sort.SliceStable(blockers, func(left, right int) bool { return capacityBlockerLess(blockers[left], blockers[right]) })
	parts := make([]string, len(blockers))
	for index, blocker := range blockers {
		parts[index] = fmt.Sprintf("unit_id=%q target_platform=%q required_runtime=%q reason_code=%q packing_plan_digest=%q", blocker.UnitID, blocker.TargetPlatform, blocker.RequiredRuntime, blocker.ReasonCode, blocker.PackingPlanDigest)
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
	if left.ReasonCode != right.ReasonCode {
		return left.ReasonCode < right.ReasonCode
	}
	return left.PackingPlanDigest < right.PackingPlanDigest
}
