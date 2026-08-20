package heteroci

import (
	"fmt"
	"time"
)

// ValidateAgainst 校验路由租约与决策身份、执行绑定及生命周期时间戳的一致性。
// decision 的 manifest、快照和装箱上下文由调用方负责校验，本方法不重复执行。
func (lease RoutingLease) ValidateAgainst(decision RoutingDecision, now time.Time) error {
	if err := validateRoutingLeaseIdentity(lease, decision); err != nil {
		return err
	}
	assignment, err := routingLeaseAssignment(decision.Assignments, lease.UnitID)
	if err != nil {
		return err
	}
	if err := validateRoutingLeaseBinding(lease, assignment); err != nil {
		return err
	}
	nowUnixMilli, err := checkedUnixMilli(now)
	if err != nil {
		return fmt.Errorf("convert routing lease validation time: %w", err)
	}
	if err := validateRoutingLeaseReservation(lease.ReservedAtUnixMilli, nowUnixMilli); err != nil {
		return err
	}
	return validateRoutingLeaseTimestamps(lease, nowUnixMilli)
}

// validateRoutingLeaseIdentity 校验租约与决策的稳定 schema、权威和摘要身份。
func validateRoutingLeaseIdentity(lease RoutingLease, decision RoutingDecision) error {
	if lease.SchemaVersion != RoutingLeaseSchemaVersion {
		return fmt.Errorf("routing lease schema version %q is not canonical", lease.SchemaVersion)
	}
	if lease.Authority != AuthorityLocalNonAuthoritative {
		return fmt.Errorf("routing lease authority %q is not local non-authoritative", lease.Authority)
	}
	if !validRoutingRunID(lease.RunID) {
		return fmt.Errorf("routing lease run id %q is invalid", lease.RunID)
	}
	if decision.SchemaVersion != RoutingDecisionSchemaVersion {
		return fmt.Errorf("routing decision schema version %q is not canonical", decision.SchemaVersion)
	}
	if decision.Authority != AuthorityLocalNonAuthoritative {
		return fmt.Errorf("routing decision authority %q is not local non-authoritative", decision.Authority)
	}
	if decision.DecisionDigest == "" {
		return fmt.Errorf("routing decision digest must be non-empty")
	}
	if lease.DecisionDigest == "" {
		return fmt.Errorf("routing lease decision digest must be non-empty")
	}
	if lease.DecisionDigest != decision.DecisionDigest {
		return fmt.Errorf("routing lease decision digest %q does not match decision digest", lease.DecisionDigest)
	}
	return nil
}

// validateRoutingLeaseBinding 校验租约执行器、节点、端点、探针身份和 ECI 档位精确复用决策绑定。
func validateRoutingLeaseBinding(lease RoutingLease, assignment RouteAssignment) error {
	if lease.Executor != assignment.Executor {
		return routingLeaseBindingFieldMismatch(lease.UnitID, "executor")
	}
	if lease.NodeID != assignment.NodeID {
		return routingLeaseBindingFieldMismatch(lease.UnitID, "node_id")
	}
	if lease.Endpoint != assignment.Endpoint {
		return routingLeaseBindingFieldMismatch(lease.UnitID, "endpoint")
	}
	if lease.ProbeIdentity != assignment.ProbeIdentity {
		return routingLeaseBindingFieldMismatch(lease.UnitID, "probe_identity")
	}
	if lease.ProbeBootID != assignment.ProbeBootID {
		return routingLeaseBindingFieldMismatch(lease.UnitID, "probe_boot_id")
	}
	if lease.ECITier != assignment.ECITier {
		return routingLeaseBindingFieldMismatch(lease.UnitID, "eci_tier")
	}
	return nil
}

// routingLeaseBindingFieldMismatch 返回不泄露执行器身份值的绑定错误。
func routingLeaseBindingFieldMismatch(unitID, field string) error {
	return fmt.Errorf("routing lease unit %q assignment field %s mismatch", unitID, field)
}

// validateRoutingLeaseReservation 校验预留时间严格为正且不晚于当前时间。
func validateRoutingLeaseReservation(reservedAtUnixMilli, nowUnixMilli int64) error {
	if reservedAtUnixMilli <= 0 || reservedAtUnixMilli > nowUnixMilli {
		return fmt.Errorf("routing lease reserved timestamp %d is outside (0,%d]", reservedAtUnixMilli, nowUnixMilli)
	}
	return nil
}

// routingLeaseAssignment 返回决策中与租约单元精确对应的唯一绑定。
func routingLeaseAssignment(assignments []RouteAssignment, unitID string) (RouteAssignment, error) {
	var match RouteAssignment
	found := false
	for _, assignment := range assignments {
		if assignment.UnitID != unitID {
			continue
		}
		if found {
			return RouteAssignment{}, fmt.Errorf("routing lease unit %q appears more than once in decision assignments", unitID)
		}
		match = assignment
		found = true
	}
	if !found {
		return RouteAssignment{}, fmt.Errorf("routing lease unit %q is absent from decision assignments", unitID)
	}
	return match, nil
}

// validateRoutingLeaseTimestamps 校验租约状态与三个生命周期时间戳的严格矩阵。
func validateRoutingLeaseTimestamps(lease RoutingLease, nowUnixMilli int64) error {
	switch lease.State {
	case LeaseStatePlanned:
		return validatePlannedRoutingLease(lease)
	case LeaseStateStarted:
		return validateStartedRoutingLease(lease, nowUnixMilli)
	case LeaseStateSucceeded, LeaseStateFailed:
		return validateTerminalRoutingLease(lease, nowUnixMilli)
	case LeaseStateCancelled:
		return validateCancelledRoutingLease(lease, nowUnixMilli)
	default:
		return fmt.Errorf("routing lease state %q is unknown", lease.State)
	}
}

// validatePlannedRoutingLease 校验 planned 状态不携带执行起止时间。
func validatePlannedRoutingLease(lease RoutingLease) error {
	if lease.StartedAtUnixMilli != 0 || lease.FinishedAtUnixMilli != 0 {
		return fmt.Errorf("planned routing lease must have zero start and finish timestamps")
	}
	return nil
}

// validateStartedRoutingLease 校验 started 状态的开始时间及未完成约束。
func validateStartedRoutingLease(lease RoutingLease, nowUnixMilli int64) error {
	if err := validateRoutingLeaseInterval("started", lease.StartedAtUnixMilli, lease.ReservedAtUnixMilli, nowUnixMilli); err != nil {
		return err
	}
	if lease.FinishedAtUnixMilli != 0 {
		return fmt.Errorf("started routing lease must have zero finish timestamp")
	}
	return nil
}

// validateTerminalRoutingLease 校验 succeeded/failed 状态的完整时间顺序。
func validateTerminalRoutingLease(lease RoutingLease, nowUnixMilli int64) error {
	if err := validateRoutingLeaseInterval("terminal start", lease.StartedAtUnixMilli, lease.ReservedAtUnixMilli, nowUnixMilli); err != nil {
		return err
	}
	return validateRoutingLeaseInterval("terminal finish", lease.FinishedAtUnixMilli, lease.StartedAtUnixMilli, nowUnixMilli)
}

// validateCancelledRoutingLease 校验 cancelled 状态不曾启动且已完成终止。
func validateCancelledRoutingLease(lease RoutingLease, nowUnixMilli int64) error {
	if lease.StartedAtUnixMilli != 0 {
		return fmt.Errorf("cancelled routing lease must have zero start timestamp")
	}
	return validateRoutingLeaseInterval("cancelled finish", lease.FinishedAtUnixMilli, lease.ReservedAtUnixMilli, nowUnixMilli)
}

// validateRoutingLeaseInterval 校验包含上下界的 Unix 毫秒时间区间。
func validateRoutingLeaseInterval(label string, value, lower, upper int64) error {
	if value < lower || value > upper {
		return fmt.Errorf("routing lease %s timestamp %d is outside [%d,%d]", label, value, lower, upper)
	}
	return nil
}
