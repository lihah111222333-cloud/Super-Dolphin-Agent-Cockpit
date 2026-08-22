package heteroci

import (
	"fmt"
	"sort"
	"time"
)

const (
	routingBlockerCapacity = CapacityBlockerReasonCapacityUnavailable
)

// BuildRoutingDecision 将已验证的装箱规划闭合为固定本地四节点路由决策。
func BuildRoutingDecision(
	manifest Manifest,
	snapshot SnapshotSet,
	units []ExecutionUnit,
	plan PackingPlan,
	now time.Time,
	heartbeatTTL time.Duration,
) (RoutingDecision, error) {
	if err := plan.ValidateAgainst(manifest, snapshot, units, now, heartbeatTTL); err != nil {
		return RoutingDecision{}, fmt.Errorf("validate routing plan: %w", err)
	}
	assignments, blockers, err := routingAssignmentsAndBlockers(manifest, snapshot, units, plan)
	if err != nil {
		return RoutingDecision{}, err
	}
	if len(blockers) != 0 {
		blockerErr, err := NewCapacityBlockerError(plan, blockers)
		if err != nil {
			return RoutingDecision{}, err
		}
		return RoutingDecision{}, blockerErr
	}
	return buildRoutingDecision(manifest, snapshot, units, plan, assignments, now, heartbeatTTL)
}

func routingAssignmentsAndBlockers(manifest Manifest, snapshot SnapshotSet, units []ExecutionUnit, plan PackingPlan) ([]RouteAssignment, []CapacityBlocker, error) {
	unitIndex, err := indexRoutingUnits(units)
	if err != nil {
		return nil, nil, fmt.Errorf("index routing units: %w", err)
	}
	assignments, err := routingLocalAssignments(plan, snapshot)
	if err != nil {
		return nil, nil, fmt.Errorf("build local routing assignments: %w", err)
	}
	blockers := make([]CapacityBlocker, 0, len(plan.UnassignedUnitIDs))
	for _, unitID := range plan.UnassignedUnitIDs {
		unit := unitIndex[unitID]
		assignment, blocker := routeUnassignedUnit(manifest, unit)
		if blocker != nil {
			blockers = append(blockers, *blocker)
			continue
		}
		assignments = append(assignments, assignment)
	}
	return assignments, blockers, nil
}

func buildRoutingDecision(manifest Manifest, snapshot SnapshotSet, units []ExecutionUnit, plan PackingPlan, assignments []RouteAssignment, now time.Time, heartbeatTTL time.Duration) (RoutingDecision, error) {
	sort.Slice(assignments, func(left, right int) bool { return assignments[left].UnitID < assignments[right].UnitID })
	packingDigest, err := plan.CanonicalDigest()
	if err != nil {
		return RoutingDecision{}, fmt.Errorf("compute routing packing digest: %w", err)
	}
	decision := RoutingDecision{SchemaVersion: RoutingDecisionSchemaVersion, Authority: AuthorityLocalNonAuthoritative, SnapshotCapturedAtUnixMilli: snapshot.CapturedAtUnixMilli, PackingPlanDigest: packingDigest, Assignments: assignments}
	decision.DecisionDigest, err = decision.CanonicalDigest()
	if err != nil {
		return RoutingDecision{}, fmt.Errorf("compute routing decision digest: %w", err)
	}
	if err := decision.ValidateAgainst(manifest, snapshot, units, plan, now, heartbeatTTL); err != nil {
		return RoutingDecision{}, fmt.Errorf("validate routing decision: %w", err)
	}
	return decision, nil
}

// PlanRoutingDecision 真实执行 Anchor/Wave 规划并构造固定节点路由决策。
func PlanRoutingDecision(
	manifest Manifest,
	snapshot SnapshotSet,
	units []ExecutionUnit,
	now time.Time,
	heartbeatTTL time.Duration,
) (PackingPlan, RoutingDecision, error) {
	plan, err := BuildAnchorWavePlan(manifest, snapshot, units, now, heartbeatTTL)
	if err != nil {
		return plan, RoutingDecision{}, fmt.Errorf("build anchor wave plan for routing: %w", err)
	}
	decision, err := BuildRoutingDecision(manifest, snapshot, units, plan, now, heartbeatTTL)
	if err != nil {
		return plan, RoutingDecision{}, err
	}
	return plan, decision, nil
}

// routingLocalAssignments 从同一快照的节点分片和波次生成本地执行绑定。
func routingLocalAssignments(plan PackingPlan, snapshot SnapshotSet) ([]RouteAssignment, error) {
	nodes := make(map[string]NodeSnapshot, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if _, exists := nodes[node.NodeID]; exists {
			return nil, fmt.Errorf("routing snapshot node %q is duplicated", node.NodeID)
		}
		nodes[node.NodeID] = node
	}
	assignments := make([]RouteAssignment, 0)
	for _, shard := range plan.NodeShards {
		node, exists := nodes[shard.NodeID]
		if !exists {
			return nil, fmt.Errorf("routing local assignment node %q is missing from snapshot", shard.NodeID)
		}
		if err := validateRoutingLocalNodeIdentity(node, shard.NodeID); err != nil {
			return nil, err
		}
		for _, wave := range shard.Waves {
			for _, unitID := range wave.UnitIDs {
				assignments = append(assignments, RouteAssignment{
					UnitID:        unitID,
					Executor:      ExecutorKindLocalNode,
					NodeID:        node.NodeID,
					Endpoint:      node.Endpoint,
					ProbeIdentity: node.ProbeIdentity,
					ProbeBootID:   node.ProbeBootID,
				})
			}
		}
	}
	return assignments, nil
}

// validateRoutingLocalNodeIdentity 校验本地路由复制所需的节点身份完整性。
func validateRoutingLocalNodeIdentity(node NodeSnapshot, expectedNodeID string) error {
	if node.NodeID != expectedNodeID {
		return fmt.Errorf("routing local assignment node_id %q does not match snapshot node %q", node.NodeID, expectedNodeID)
	}
	if node.Status != NodeStatusLive {
		return fmt.Errorf("routing local assignment node %q is not live", expectedNodeID)
	}
	if node.Endpoint == "" || node.ProbeIdentity == "" || node.ProbeBootID == "" {
		return fmt.Errorf("routing local assignment node %q identity is incomplete", expectedNodeID)
	}
	return nil
}

// routeUnassignedUnit 将未装入固定节点的单元统一形成为容量阻塞。
func routeUnassignedUnit(_ Manifest, unit ExecutionUnit) (RouteAssignment, *CapacityBlocker) {
	return RouteAssignment{}, newCapacityBlocker(unit, routingBlockerCapacity)
}

// newCapacityBlocker 生成不泄露节点端点且字段稳定的容量阻塞记录。
func newCapacityBlocker(unit ExecutionUnit, reason string) *CapacityBlocker {
	return &CapacityBlocker{
		UnitID:          unit.UnitID,
		TargetPlatform:  unit.TargetPlatform,
		RequiredRuntime: unit.RequiredRuntime,
		ReasonCode:      reason,
	}
}
