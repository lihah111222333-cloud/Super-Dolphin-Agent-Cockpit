package heterociowner

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const planAndReserveOperationKind = "plan_and_reserve"

// DurableReservedRoutingPlan 是 SQLite 提交后的不可变规划投影。
type DurableReservedRoutingPlan struct {
	Fence             heteroci.OwnerFence      `json:"fence"`
	OperationID       heteroci.OperationID     `json:"operation_id"`
	SourceSnapshot    heteroci.SnapshotSet     `json:"source_snapshot"`
	EffectiveSnapshot heteroci.SnapshotSet     `json:"effective_snapshot"`
	Plan              heteroci.PackingPlan     `json:"plan"`
	Decision          heteroci.RoutingDecision `json:"decision"`
	Leases            []heteroci.RoutingLease  `json:"leases"`
}

// planAndReserveRequest 是 journal 的完整幂等输入，不接受 caller 预计算的 plan/decision。
type planAndReserveRequest struct {
	Fence             heteroci.OwnerFence      `json:"fence"`
	RunID             string                   `json:"run_id"`
	Manifest          heteroci.Manifest        `json:"manifest"`
	SourceSnapshot    heteroci.SnapshotSet     `json:"source_snapshot"`
	Units             []heteroci.ExecutionUnit `json:"units"`
	HeartbeatTTLMilli int64                    `json:"heartbeat_ttl_milli"`
}

type durableReservationKey struct {
	nodeID        string
	probeIdentity string
	probeBootID   string
}

type durableReservation struct {
	key      durableReservationKey
	capacity heteroci.ResourceCapacity
}

// PlanAndReserve 在单个 immediate transaction 内从原始输入重算并持久化规划。
// owner 的 clock、SQLite CAS 和 operation journal 是这次 mutation 的唯一权威。
func (owner *Owner) PlanAndReserve(
	ctx context.Context,
	fence heteroci.OwnerFence,
	operationID heteroci.OperationID,
	runID string,
	manifest heteroci.Manifest,
	sourceSnapshot heteroci.SnapshotSet,
	units []heteroci.ExecutionUnit,
	heartbeatTTL time.Duration,
) (DurableReservedRoutingPlan, error) {
	if err := validatePlanAndReserveArguments(owner, fence, operationID, runID, manifest, sourceSnapshot, units, heartbeatTTL); err != nil {
		return DurableReservedRoutingPlan{}, err
	}
	nowUnixMilli, err := owner.nowUnixMilli()
	if err != nil {
		return DurableReservedRoutingPlan{}, err
	}
	now := time.UnixMilli(nowUnixMilli)
	requestBytes, err := json.Marshal(planAndReserveRequest{
		Fence:             fence,
		RunID:             runID,
		Manifest:          manifest,
		SourceSnapshot:    sourceSnapshot,
		Units:             units,
		HeartbeatTTLMilli: heartbeatTTL.Milliseconds(),
	})
	if err != nil {
		return DurableReservedRoutingPlan{}, fmt.Errorf("encode plan-and-reserve request: %w", err)
	}
	var result DurableReservedRoutingPlan
	err = WithImmediateTx(ctx, owner.db, func(tx *sql.Tx) error {
		return owner.planAndReserveTx(ctx, tx, fence, operationID, runID, manifest, sourceSnapshot, units, heartbeatTTL, now, nowUnixMilli, requestBytes, &result)
	})
	if err != nil {
		return DurableReservedRoutingPlan{}, err
	}
	return result, nil
}

// planAndReserveTx 在同一事务内执行 replay、重算、行写入和 journal CAS。
func (owner *Owner) planAndReserveTx(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID string, manifest heteroci.Manifest, sourceSnapshot heteroci.SnapshotSet, units []heteroci.ExecutionUnit, heartbeatTTL time.Duration, now time.Time, nowUnixMilli int64, requestBytes []byte, result *DurableReservedRoutingPlan) error {
	existing, found, err := readOperationByID(ctx, tx, fence.Scope, fence.OwnerEpoch, operationID)
	if err != nil {
		return err
	}
	if found {
		return replayPlanOperation(existing, requestBytes, fence, operationID, manifest, units, heartbeatTTL, now, result)
	}
	if err := owner.requireCurrentFence(ctx, tx, fence); err != nil {
		return err
	}
	planned, reservations, err := planFromCurrentDatabase(ctx, tx, fence, runID, manifest, sourceSnapshot, units, heartbeatTTL, now)
	if err != nil {
		return err
	}
	revision, err := nextOwnerRevision(fence)
	if err != nil {
		return err
	}
	committedFence, err := heteroci.NewOwnerFence(fence.Scope, fence.OwnerID, fence.OwnerEpoch, revision)
	if err != nil {
		return err
	}
	*result = DurableReservedRoutingPlan{Fence: committedFence, OperationID: operationID, SourceSnapshot: cloneSnapshot(sourceSnapshot), EffectiveSnapshot: cloneSnapshot(planned.EffectiveSnapshot), Plan: planned.Plan, Decision: planned.Decision, Leases: cloneLeases(planned.Leases)}
	resultBytes, err := json.Marshal(*result)
	if err != nil {
		return fmt.Errorf("encode durable plan result: %w", err)
	}
	if err := insertPlanRows(ctx, tx, fence, runID, manifest, units, heartbeatTTL, *result, reservations, revision); err != nil {
		return err
	}
	_, err = commitOperationTx(ctx, tx, OperationRequest{Fence: fence, OperationID: operationID, Kind: planAndReserveOperationKind, RequestBytes: requestBytes, ResultBytes: resultBytes, ProjectionDigest: digestBytes(resultBytes), CommittedAtUnixMilli: nowUnixMilli})
	return err
}

// replayPlanOperation 只在 request bytes 完全相同时返回已提交 projection。
func replayPlanOperation(existing OperationRecord, requestBytes []byte, fence heteroci.OwnerFence, operationID heteroci.OperationID, manifest heteroci.Manifest, units []heteroci.ExecutionUnit, heartbeatTTL time.Duration, now time.Time, result *DurableReservedRoutingPlan) error {
	if existing.Kind != planAndReserveOperationKind || !bytes.Equal(existing.RequestBytes, requestBytes) {
		return ErrOperationConflict
	}
	if err := json.Unmarshal(existing.ResultBytes, result); err != nil {
		return fmt.Errorf("decode durable plan replay: %w", err)
	}
	return validateDurablePlanResult(*result, fence, operationID, manifest, units, heartbeatTTL, now)
}

// validatePlanAndReserveArguments 校验 durable 规划入口的身份、清单和时限。
func validatePlanAndReserveArguments(owner *Owner, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID string, manifest heteroci.Manifest, snapshot heteroci.SnapshotSet, units []heteroci.ExecutionUnit, heartbeatTTL time.Duration) error {
	if err := validatePlanOwnerBinding(owner, fence, operationID, runID); err != nil {
		return err
	}
	if err := manifest.Validate(); err != nil {
		return err
	}
	if snapshot.OwnerEpoch != fence.OwnerEpoch {
		return fmt.Errorf("heteroci source snapshot owner epoch does not match fence")
	}
	return validatePlanPayload(units, heartbeatTTL)
}

// validatePlanOwnerBinding 校验 owner、fence、operation 和运行标识的绑定。
func validatePlanOwnerBinding(owner *Owner, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID string) error {
	if owner == nil || owner.db == nil {
		return fmt.Errorf("heteroci owner is nil")
	}
	if err := fence.Validate(); err != nil {
		return err
	}
	if fence.Scope != owner.scope {
		return fmt.Errorf("heteroci owner fence scope is not bound to owner")
	}
	if _, err := heteroci.ParseOperationID(string(operationID)); err != nil {
		return err
	}
	if !validOwnerRunID(runID) {
		return fmt.Errorf("heteroci owner run id is invalid")
	}
	return nil
}

// validatePlanPayload 校验工作单元集合和 owner heartbeat TTL。
func validatePlanPayload(units []heteroci.ExecutionUnit, heartbeatTTL time.Duration) error {
	if err := heteroci.ValidateExecutionUnits(units); err != nil {
		return err
	}
	if heartbeatTTL <= 0 || heartbeatTTL.Milliseconds() <= 0 {
		return fmt.Errorf("heteroci owner heartbeat TTL must be positive")
	}
	return nil
}

// planFromCurrentDatabase 在事务快照上完成 effective snapshot、规划和租约构造。
func planFromCurrentDatabase(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID string, manifest heteroci.Manifest, sourceSnapshot heteroci.SnapshotSet, units []heteroci.ExecutionUnit, heartbeatTTL time.Duration, now time.Time) (DurableReservedRoutingPlan, []durableReservation, error) {
	if err := sourceSnapshot.ValidateAgainst(manifest, now, heartbeatTTL); err != nil {
		return DurableReservedRoutingPlan{}, nil, fmt.Errorf("validate durable source snapshot: %w", err)
	}
	current, err := readActiveReservations(ctx, tx, fence)
	if err != nil {
		return DurableReservedRoutingPlan{}, nil, err
	}
	effective, err := effectiveSnapshot(sourceSnapshot, current)
	if err != nil {
		return DurableReservedRoutingPlan{}, nil, err
	}
	plan, decision, err := heteroci.PlanRoutingDecision(manifest, effective, units, now, heartbeatTTL)
	if err != nil {
		return DurableReservedRoutingPlan{}, nil, err
	}
	requested, err := buildDurableReservations(plan, effective)
	if err != nil {
		return DurableReservedRoutingPlan{}, nil, err
	}
	if err := validateRequestedCapacity(requested, effective); err != nil {
		return DurableReservedRoutingPlan{}, nil, err
	}
	reservedAt := now.UnixMilli()
	leases, err := buildDurableLeases(runID, decision, reservedAt)
	if err != nil {
		return DurableReservedRoutingPlan{}, nil, err
	}
	return DurableReservedRoutingPlan{Fence: fence, SourceSnapshot: sourceSnapshot, EffectiveSnapshot: effective, Plan: plan, Decision: decision, Leases: leases}, requested, nil
}

// readActiveReservations 读取当前 owner epoch 尚未释放的容量预留。
func readActiveReservations(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence) (map[durableReservationKey]heteroci.ResourceCapacity, error) {
	rows, err := tx.QueryContext(ctx, "SELECT node_id, probe_identity, probe_boot_id, cpu_milli, memory_bytes, max_concurrency FROM heteroci_routing_reservations WHERE scope = ? AND owner_epoch = ? AND released = 0", fence.Scope, fence.OwnerEpoch)
	if err != nil {
		return nil, fmt.Errorf("read durable routing reservations: %w", err)
	}
	defer rows.Close()
	current := make(map[durableReservationKey]heteroci.ResourceCapacity)
	for rows.Next() {
		var key durableReservationKey
		var cpu, memory, concurrency int64
		if err := rows.Scan(&key.nodeID, &key.probeIdentity, &key.probeBootID, &cpu, &memory, &concurrency); err != nil {
			return nil, fmt.Errorf("scan durable routing reservation: %w", err)
		}
		if cpu < 0 || memory < 0 || concurrency < 0 || concurrency > int64(maxInt()) {
			return nil, fmt.Errorf("durable routing reservation capacity is invalid")
		}
		keyCapacity := heteroci.ResourceCapacity{CPUMilli: cpu, MemoryBytes: memory, MaxConcurrency: int(concurrency)}
		updated, err := addCapacity(current[key], keyCapacity)
		if err != nil {
			return nil, err
		}
		current[key] = updated
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate durable routing reservations: %w", err)
	}
	return current, nil
}

// effectiveSnapshot 只扣减当前 owner epoch 且探针身份完全匹配的预留。
func effectiveSnapshot(source heteroci.SnapshotSet, current map[durableReservationKey]heteroci.ResourceCapacity) (heteroci.SnapshotSet, error) {
	effective := cloneSnapshot(source)
	seen := make(map[durableReservationKey]bool, len(current))
	for index := range effective.Nodes {
		node := &effective.Nodes[index]
		key := durableReservationKey{nodeID: node.NodeID, probeIdentity: node.ProbeIdentity, probeBootID: node.ProbeBootID}
		reserved := current[key]
		if reserved == (heteroci.ResourceCapacity{}) {
			continue
		}
		updated, err := subtractCapacity(node.Available, reserved)
		if err != nil {
			return heteroci.SnapshotSet{}, fmt.Errorf("subtract durable routing reservation: %w", err)
		}
		node.Available = updated
		seen[key] = true
	}
	for key := range current {
		if !seen[key] {
			return heteroci.SnapshotSet{}, fmt.Errorf("durable reservation identity is absent from current snapshot")
		}
	}
	return effective, nil
}

// buildDurableReservations 从节点波次生成可持久化的峰值预留。
func buildDurableReservations(plan heteroci.PackingPlan, snapshot heteroci.SnapshotSet) ([]durableReservation, error) {
	byNode := make(map[string]heteroci.NodeSnapshot, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		byNode[node.NodeID] = node
	}
	reservations := make([]durableReservation, 0, len(plan.NodeShards))
	seen := make(map[string]bool, len(plan.NodeShards))
	for _, shard := range plan.NodeShards {
		if seen[shard.NodeID] {
			return nil, fmt.Errorf("durable plan repeats node shard")
		}
		seen[shard.NodeID] = true
		node, ok := byNode[shard.NodeID]
		if !ok || node.Status != heteroci.NodeStatusLive {
			return nil, fmt.Errorf("durable plan node is absent or not live")
		}
		capacity, err := reservationPeak(shard.Waves)
		if err != nil {
			return nil, err
		}
		reservations = append(reservations, durableReservation{key: durableReservationKey{nodeID: node.NodeID, probeIdentity: node.ProbeIdentity, probeBootID: node.ProbeBootID}, capacity: capacity})
	}
	sort.Slice(reservations, func(left, right int) bool { return reservations[left].key.nodeID < reservations[right].key.nodeID })
	return reservations, nil
}

// reservationPeak 计算单个节点波次的 componentwise 最大资源。
func reservationPeak(waves []heteroci.ExecutionWave) (heteroci.ResourceCapacity, error) {
	if len(waves) == 0 {
		return heteroci.ResourceCapacity{}, fmt.Errorf("durable plan node waves must not be empty")
	}
	peak := heteroci.ResourceCapacity{}
	for _, wave := range waves {
		if wave.CPUMilli <= 0 || wave.MemoryBytes <= 0 || wave.Concurrency <= 0 {
			return heteroci.ResourceCapacity{}, fmt.Errorf("durable plan wave resources must be positive")
		}
		if wave.CPUMilli > peak.CPUMilli {
			peak.CPUMilli = wave.CPUMilli
		}
		if wave.MemoryBytes > peak.MemoryBytes {
			peak.MemoryBytes = wave.MemoryBytes
		}
		if wave.Concurrency > peak.MaxConcurrency {
			peak.MaxConcurrency = wave.Concurrency
		}
	}
	return peak, nil
}

// validateRequestedCapacity 确保新运行不会超过 effective snapshot 的容量。
func validateRequestedCapacity(requested []durableReservation, snapshot heteroci.SnapshotSet) error {
	for _, reservation := range requested {
		for _, node := range snapshot.Nodes {
			if node.NodeID != reservation.key.nodeID || node.ProbeIdentity != reservation.key.probeIdentity || node.ProbeBootID != reservation.key.probeBootID {
				continue
			}
			if reservation.capacity.CPUMilli > node.Available.CPUMilli || reservation.capacity.MemoryBytes > node.Available.MemoryBytes || reservation.capacity.MaxConcurrency > node.Available.MaxConcurrency {
				return fmt.Errorf("durable plan reservation exceeds effective capacity")
			}
			goto next
		}
		return fmt.Errorf("durable plan reservation node is missing")
	next:
	}
	return nil
}

// buildDurableLeases 将 routing decision assignments 转换为 planned lease。
func buildDurableLeases(runID string, decision heteroci.RoutingDecision, reservedAt int64) ([]heteroci.RoutingLease, error) {
	leases := make([]heteroci.RoutingLease, 0, len(decision.Assignments))
	seen := make(map[string]bool, len(decision.Assignments))
	for _, assignment := range decision.Assignments {
		if seen[assignment.UnitID] {
			return nil, fmt.Errorf("durable decision assigns a unit twice")
		}
		seen[assignment.UnitID] = true
		leases = append(leases, heteroci.RoutingLease{SchemaVersion: heteroci.RoutingLeaseSchemaVersion, Authority: heteroci.AuthorityLocalNonAuthoritative, RunID: runID, DecisionDigest: decision.DecisionDigest, UnitID: assignment.UnitID, Executor: assignment.Executor, NodeID: assignment.NodeID, Endpoint: assignment.Endpoint, ProbeIdentity: assignment.ProbeIdentity, ProbeBootID: assignment.ProbeBootID, ECITier: assignment.ECITier, State: heteroci.LeaseStatePlanned, ReservedAtUnixMilli: reservedAt})
	}
	sort.Slice(leases, func(left, right int) bool { return leases[left].UnitID < leases[right].UnitID })
	return leases, nil
}

// insertPlanRows 写入 run、reservation 和 lease 投影；operation journal 由调用方随后写入。
func insertPlanRows(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID string, manifest heteroci.Manifest, units []heteroci.ExecutionUnit, heartbeatTTL time.Duration, result DurableReservedRoutingPlan, reservations []durableReservation, revision heteroci.RoutingOwnerRevision) error {
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode durable manifest: %w", err)
	}
	unitsBytes, err := json.Marshal(units)
	if err != nil {
		return fmt.Errorf("encode durable execution units: %w", err)
	}
	decisionBytes, err := json.Marshal(result.Decision)
	if err != nil {
		return fmt.Errorf("encode durable decision: %w", err)
	}
	ttl := heartbeatTTL.Milliseconds()
	if _, err := tx.ExecContext(ctx, "INSERT INTO heteroci_routing_runs(scope, owner_epoch, run_id, manifest_json, manifest_digest, units_json, units_digest, heartbeat_ttl_millis, decision_json, decision_digest, released, reserved_revision, last_revision) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", fence.Scope, fence.OwnerEpoch, runID, manifestBytes, digestBytes(manifestBytes), unitsBytes, digestBytes(unitsBytes), ttl, decisionBytes, result.Decision.DecisionDigest, 0, revision, revision); err != nil {
		return fmt.Errorf("insert durable routing run: %w", err)
	}
	for _, reservation := range reservations {
		if _, err := tx.ExecContext(ctx, "INSERT INTO heteroci_routing_reservations(scope, owner_epoch, run_id, node_id, probe_identity, probe_boot_id, cpu_milli, memory_bytes, max_concurrency, released, last_revision) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", fence.Scope, fence.OwnerEpoch, runID, reservation.key.nodeID, reservation.key.probeIdentity, reservation.key.probeBootID, reservation.capacity.CPUMilli, reservation.capacity.MemoryBytes, reservation.capacity.MaxConcurrency, 0, revision); err != nil {
			return fmt.Errorf("insert durable routing reservation: %w", err)
		}
	}
	for _, lease := range result.Leases {
		leaseBytes, err := json.Marshal(lease)
		if err != nil {
			return fmt.Errorf("encode durable lease: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO heteroci_routing_leases(scope, owner_epoch, run_id, unit_id, lease_json, state, reserved_at_unix_milli, started_at_unix_milli, finished_at_unix_milli, last_revision) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", fence.Scope, fence.OwnerEpoch, runID, lease.UnitID, leaseBytes, lease.State, lease.ReservedAtUnixMilli, lease.StartedAtUnixMilli, lease.FinishedAtUnixMilli, revision); err != nil {
			return fmt.Errorf("insert durable routing lease: %w", err)
		}
	}
	return nil
}

// validateDurablePlanResult 校验 journal replay 的结构、epoch、规划和租约绑定。
func validateDurablePlanResult(result DurableReservedRoutingPlan, fence heteroci.OwnerFence, operationID heteroci.OperationID, manifest heteroci.Manifest, units []heteroci.ExecutionUnit, heartbeatTTL time.Duration, now time.Time) error {
	if err := validateDurablePlanIdentity(result, fence, operationID); err != nil {
		return err
	}
	if err := validateDurablePlanSnapshots(result, manifest, heartbeatTTL, now); err != nil {
		return err
	}
	if err := result.Decision.ValidateAgainst(manifest, result.EffectiveSnapshot, units, result.Plan, now, heartbeatTTL); err != nil {
		return fmt.Errorf("validate durable replay decision: %w", err)
	}
	for _, lease := range result.Leases {
		if err := lease.ValidateAgainst(result.Decision, now); err != nil {
			return fmt.Errorf("validate durable replay lease: %w", err)
		}
	}
	return nil
}

// validateDurablePlanIdentity 校验 replay fence、operation 和 snapshot epoch。
func validateDurablePlanIdentity(result DurableReservedRoutingPlan, fence heteroci.OwnerFence, operationID heteroci.OperationID) error {
	revision, err := nextOwnerRevision(fence)
	if err != nil {
		return err
	}
	wantFence, err := heteroci.NewOwnerFence(fence.Scope, fence.OwnerID, fence.OwnerEpoch, revision)
	if err != nil {
		return err
	}
	if result.Fence != wantFence || result.OperationID != operationID || result.SourceSnapshot.OwnerEpoch != fence.OwnerEpoch || result.EffectiveSnapshot.OwnerEpoch != fence.OwnerEpoch {
		return fmt.Errorf("durable plan replay identity drifted")
	}
	return nil
}

// validateDurablePlanSnapshots 校验 source/effective snapshot 均仍满足原清单和 TTL。
func validateDurablePlanSnapshots(result DurableReservedRoutingPlan, manifest heteroci.Manifest, heartbeatTTL time.Duration, now time.Time) error {
	if err := result.SourceSnapshot.ValidateAgainst(manifest, now, heartbeatTTL); err != nil {
		return fmt.Errorf("validate durable replay source snapshot: %w", err)
	}
	if err := result.EffectiveSnapshot.ValidateAgainst(manifest, now, heartbeatTTL); err != nil {
		return fmt.Errorf("validate durable replay effective snapshot: %w", err)
	}
	return nil
}

// cloneSnapshot 深拷贝 SnapshotSet 的节点、能力和资格回执。
func cloneSnapshot(source heteroci.SnapshotSet) heteroci.SnapshotSet {
	clone := source
	if source.Nodes == nil {
		return clone
	}
	clone.Nodes = append([]heteroci.NodeSnapshot(nil), source.Nodes...)
	for index := range clone.Nodes {
		clone.Nodes[index].ActiveCapabilities = append([]heteroci.ActiveCapabilitySnapshot(nil), source.Nodes[index].ActiveCapabilities...)
		for capabilityIndex := range clone.Nodes[index].ActiveCapabilities {
			receipt := source.Nodes[index].ActiveCapabilities[capabilityIndex].Qualification
			if receipt != nil {
				receiptCopy := *receipt
				clone.Nodes[index].ActiveCapabilities[capabilityIndex].Qualification = &receiptCopy
			}
		}
	}
	return clone
}

// cloneLeases 深拷贝租约切片，避免 replay projection 共享底层数组。
func cloneLeases(source []heteroci.RoutingLease) []heteroci.RoutingLease {
	if source == nil {
		return nil
	}
	return append([]heteroci.RoutingLease(nil), source...)
}

// addCapacity 以非负资源执行不会溢出的 componentwise 加法。
func addCapacity(left, right heteroci.ResourceCapacity) (heteroci.ResourceCapacity, error) {
	if left.CPUMilli < 0 || left.MemoryBytes < 0 || left.MaxConcurrency < 0 || right.CPUMilli < 0 || right.MemoryBytes < 0 || right.MaxConcurrency < 0 {
		return heteroci.ResourceCapacity{}, fmt.Errorf("durable reservation capacity is negative")
	}
	if left.CPUMilli > math.MaxInt64-right.CPUMilli || left.MemoryBytes > math.MaxInt64-right.MemoryBytes || left.MaxConcurrency > maxInt()-right.MaxConcurrency {
		return heteroci.ResourceCapacity{}, fmt.Errorf("durable reservation capacity overflows")
	}
	return heteroci.ResourceCapacity{CPUMilli: left.CPUMilli + right.CPUMilli, MemoryBytes: left.MemoryBytes + right.MemoryBytes, MaxConcurrency: left.MaxConcurrency + right.MaxConcurrency}, nil
}

// subtractCapacity 从 gross capacity 中扣除当前 owner 的 reservation。
func subtractCapacity(left, right heteroci.ResourceCapacity) (heteroci.ResourceCapacity, error) {
	if left.CPUMilli < 0 || left.MemoryBytes < 0 || left.MaxConcurrency < 0 || right.CPUMilli < 0 || right.MemoryBytes < 0 || right.MaxConcurrency < 0 {
		return heteroci.ResourceCapacity{}, fmt.Errorf("durable effective capacity is negative")
	}
	if right.CPUMilli > left.CPUMilli || right.MemoryBytes > left.MemoryBytes || right.MaxConcurrency > left.MaxConcurrency {
		return heteroci.ResourceCapacity{}, fmt.Errorf("durable reservation exceeds source capacity")
	}
	return heteroci.ResourceCapacity{CPUMilli: left.CPUMilli - right.CPUMilli, MemoryBytes: left.MemoryBytes - right.MemoryBytes, MaxConcurrency: left.MaxConcurrency - right.MaxConcurrency}, nil
}

// nextOwnerRevision 计算本次 durable mutation 的下一条全局 revision。
func nextOwnerRevision(fence heteroci.OwnerFence) (heteroci.RoutingOwnerRevision, error) {
	if fence.Revision >= heteroci.RoutingOwnerRevision(math.MaxInt64-1) {
		return 0, fmt.Errorf("heteroci owner revision exhausted")
	}
	return fence.Revision + 1, nil
}

// validOwnerRunID 校验持久化运行标识的稳定文本边界。
func validOwnerRunID(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func maxInt() int { return int(^uint(0) >> 1) }
