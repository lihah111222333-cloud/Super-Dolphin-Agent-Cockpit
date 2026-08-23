package heteroci

import (
	"fmt"
	"time"
)

// ValidateAgainst 校验路由决策与清单、快照、工作单元及装箱规划的严格一致性。
func (decision RoutingDecision) ValidateAgainst(
	manifest Manifest,
	snapshot SnapshotSet,
	units []ExecutionUnit,
	plan PackingPlan,
	now time.Time,
	heartbeatTTL time.Duration,
) error {
	if err := plan.ValidateAgainst(manifest, snapshot, units, now, heartbeatTTL); err != nil {
		return fmt.Errorf("validate routing packing plan: %w", err)
	}
	if err := validateRoutingIdentity(decision, snapshot, plan); err != nil {
		return err
	}
	unitIndex, err := indexRoutingUnits(units)
	if err != nil {
		return err
	}
	snapshotIndex, err := indexRoutingSnapshots(snapshot)
	if err != nil {
		return err
	}
	localUnits, _, err := indexRoutingPlanCoverage(plan)
	if err != nil {
		return err
	}
	if err := validateRoutingAssignments(decision.Assignments, unitIndex, snapshotIndex, localUnits); err != nil {
		return err
	}
	return validateRoutingDecisionDigest(decision)
}

// validateRoutingIdentity 校验路由决策的 schema、权威边界、快照捕获点和装箱摘要。
func validateRoutingIdentity(decision RoutingDecision, snapshot SnapshotSet, plan PackingPlan) error {
	if decision.SchemaVersion != RoutingDecisionSchemaVersion {
		return fmt.Errorf("routing decision schema_version %q is invalid", decision.SchemaVersion)
	}
	if decision.Authority != AuthorityLocalNonAuthoritative {
		return fmt.Errorf("routing decision authority %q is invalid", decision.Authority)
	}
	if decision.SnapshotCapturedAtUnixMilli != snapshot.CapturedAtUnixMilli {
		return fmt.Errorf("routing decision snapshot capture %d does not match snapshot %d", decision.SnapshotCapturedAtUnixMilli, snapshot.CapturedAtUnixMilli)
	}
	packingDigest, err := plan.CanonicalDigest()
	if err != nil {
		return fmt.Errorf("compute routing packing plan digest: %w", err)
	}
	if decision.PackingPlanDigest != packingDigest {
		return fmt.Errorf("routing decision packing plan digest %q does not match canonical digest %q", decision.PackingPlanDigest, packingDigest)
	}
	return nil
}

// indexRoutingUnits 建立稳定的工作单元索引并拒绝重复标识。
func indexRoutingUnits(units []ExecutionUnit) (map[string]ExecutionUnit, error) {
	index := make(map[string]ExecutionUnit, len(units))
	for _, unit := range units {
		if _, exists := index[unit.UnitID]; exists {
			return nil, fmt.Errorf("routing execution unit %q is duplicated", unit.UnitID)
		}
		index[unit.UnitID] = unit
	}
	return index, nil
}

// indexRoutingSnapshots 建立快照节点身份索引并拒绝重复 NodeID。
func indexRoutingSnapshots(snapshot SnapshotSet) (map[string]NodeSnapshot, error) {
	index := make(map[string]NodeSnapshot, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if _, exists := index[node.NodeID]; exists {
			return nil, fmt.Errorf("routing snapshot node %q is duplicated", node.NodeID)
		}
		index[node.NodeID] = node
	}
	return index, nil
}

// indexRoutingPlanCoverage 建立规划中本地分配和未分配工作单元的互斥集合。
func indexRoutingPlanCoverage(plan PackingPlan) (map[string]string, map[string]struct{}, error) {
	localUnits := make(map[string]string)
	unassignedUnits := make(map[string]struct{}, len(plan.UnassignedUnitIDs))
	for _, shard := range plan.NodeShards {
		for _, wave := range shard.Waves {
			for _, unitID := range wave.UnitIDs {
				if _, exists := localUnits[unitID]; exists {
					return nil, nil, fmt.Errorf("routing packing plan assigns execution unit %q more than once", unitID)
				}
				if _, exists := unassignedUnits[unitID]; exists {
					return nil, nil, fmt.Errorf("routing packing plan marks execution unit %q both local and unassigned", unitID)
				}
				localUnits[unitID] = shard.NodeID
			}
		}
	}
	for _, unitID := range plan.UnassignedUnitIDs {
		if _, exists := localUnits[unitID]; exists {
			return nil, nil, fmt.Errorf("routing packing plan marks execution unit %q both local and unassigned", unitID)
		}
		if _, exists := unassignedUnits[unitID]; exists {
			return nil, nil, fmt.Errorf("routing packing plan lists execution unit %q as unassigned more than once", unitID)
		}
		unassignedUnits[unitID] = struct{}{}
	}
	return localUnits, unassignedUnits, nil
}

// validateRoutingAssignments 校验排序、唯一覆盖和本地执行器约束。
func validateRoutingAssignments(
	assignments []RouteAssignment,
	units map[string]ExecutionUnit,
	snapshots map[string]NodeSnapshot,
	localUnits map[string]string,
) error {
	if assignments == nil {
		return fmt.Errorf("routing decision assignments must be non-nil")
	}
	if len(assignments) != len(units) {
		return fmt.Errorf("routing decision assignments count %d does not match execution unit count %d", len(assignments), len(units))
	}
	seen := make(map[string]struct{}, len(assignments))
	previousUnitID := ""
	for index, assignment := range assignments {
		if index > 0 && assignment.UnitID <= previousUnitID {
			return fmt.Errorf("routing decision assignments must be strictly ascending by unit_id")
		}
		_, exists := units[assignment.UnitID]
		if !exists {
			return fmt.Errorf("routing decision references unknown execution unit %q", assignment.UnitID)
		}
		if _, exists := seen[assignment.UnitID]; exists {
			return fmt.Errorf("routing decision assigns execution unit %q more than once", assignment.UnitID)
		}
		if err := validateRouteAssignment(assignment, snapshots, localUnits); err != nil {
			return fmt.Errorf("validate routing assignment for unit %q: %w", assignment.UnitID, err)
		}
		seen[assignment.UnitID] = struct{}{}
		previousUnitID = assignment.UnitID
	}
	if len(seen) != len(units) {
		return fmt.Errorf("routing decision covers %d of %d execution units", len(seen), len(units))
	}
	return nil
}

// validateRouteAssignment 校验单个本地路由的唯一执行边界。
func validateRouteAssignment(
	assignment RouteAssignment,
	snapshots map[string]NodeSnapshot,
	localUnits map[string]string,
) error {
	switch assignment.Executor {
	case ExecutorKindLocalNode:
		return validateLocalRouteAssignment(assignment, snapshots, localUnits)
	default:
		return fmt.Errorf("executor %q is unknown", assignment.Executor)
	}
}

// validateLocalRouteAssignment 校验本地节点与快照、装箱规划的精确绑定。
func validateLocalRouteAssignment(assignment RouteAssignment, snapshots map[string]NodeSnapshot, localUnits map[string]string) error {
	nodeID, exists := localUnits[assignment.UnitID]
	if !exists {
		return fmt.Errorf("local executor requires a unit assigned by the packing plan")
	}
	node, exists := snapshots[nodeID]
	if !exists {
		return fmt.Errorf("local node %q is missing from the routing snapshot", nodeID)
	}
	if assignment.NodeID != node.NodeID {
		return fmt.Errorf("local node_id field mismatch for snapshot node %q", node.NodeID)
	}
	if assignment.Endpoint != node.Endpoint {
		return fmt.Errorf("local endpoint field mismatch for snapshot node %q", node.NodeID)
	}
	if assignment.ProbeIdentity != node.ProbeIdentity {
		return fmt.Errorf("local probe_identity field mismatch for snapshot node %q", node.NodeID)
	}
	if assignment.ProbeBootID != node.ProbeBootID {
		return fmt.Errorf("local probe_boot_id field mismatch for snapshot node %q", node.NodeID)
	}
	return nil
}

// validateRoutingDecisionDigest 校验摘要覆盖完整决策且忽略 DecisionDigest 自身。
func validateRoutingDecisionDigest(decision RoutingDecision) error {
	expected, err := decision.CanonicalDigest()
	if err != nil {
		return fmt.Errorf("compute routing decision digest: %w", err)
	}
	if decision.DecisionDigest != expected {
		return fmt.Errorf("routing decision digest %q does not match canonical digest %q", decision.DecisionDigest, expected)
	}
	return nil
}
