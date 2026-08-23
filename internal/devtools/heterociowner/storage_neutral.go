package heterociowner

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// zeroDigest 是 owner/operation journal 的 storage-neutral 初始摘要。
const zeroDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// ErrOperationConflict 表示 operation identity 已存在但请求内容发生漂移。
var ErrOperationConflict = errors.New("heteroci owner operation conflict")

// OperationRequest 是 PostgreSQL journal 与 workflow 共用的幂等 mutation 输入。
type OperationRequest struct {
	Fence                heteroci.OwnerFence
	OperationID          heteroci.OperationID
	Kind                 string
	RequestBytes         []byte
	ResultBytes          []byte
	ProjectionDigest     string
	CommittedAtUnixMilli int64
}

// OperationRecord 是 PostgreSQL journal 行的 storage-neutral 内存投影。
type OperationRecord struct {
	Scope                heteroci.RoutingOwnerScope
	OwnerEpoch           heteroci.RoutingOwnerEpoch
	Revision             heteroci.RoutingOwnerRevision
	OperationID          heteroci.OperationID
	Kind                 string
	RequestBytes         []byte
	RequestDigest        string
	ResultBytes          []byte
	ResultDigest         string
	PreviousJournal      string
	JournalDigest        string
	ProjectionDigest     string
	CommittedAtUnixMilli int64
}

// DurableReservedRoutingPlan 是 PostgreSQL owner 提交后的不可变规划投影。
type DurableReservedRoutingPlan struct {
	Fence             heteroci.OwnerFence      `json:"fence"`
	OperationID       heteroci.OperationID     `json:"operation_id"`
	SourceSnapshot    heteroci.SnapshotSet     `json:"source_snapshot"`
	EffectiveSnapshot heteroci.SnapshotSet     `json:"effective_snapshot"`
	Plan              heteroci.PackingPlan     `json:"plan"`
	Decision          heteroci.RoutingDecision `json:"decision"`
	Leases            []heteroci.RoutingLease  `json:"leases"`
}

// DurableLeaseTransition 是 PostgreSQL 终态 mutation 提交后的 fence 和 lease 值。
type DurableLeaseTransition struct {
	Fence heteroci.OwnerFence   `json:"fence"`
	Lease heteroci.RoutingLease `json:"lease"`
}

// RunIdentityAmbiguousError 表示同一 scope 的 run_id 横跨多个 owner epoch。
type RunIdentityAmbiguousError struct{}

// Error 返回不包含运行载荷或节点身份的稳定错误。
func (RunIdentityAmbiguousError) Error() string { return "heteroci routing run identity is ambiguous" }

// RunRecoveryRequiredError 表示运行属于非当前 owner epoch，必须走显式恢复流程。
type RunRecoveryRequiredError struct{}

// Error 返回不包含运行载荷或节点身份的稳定错误。
func (RunRecoveryRequiredError) Error() string { return "heteroci routing run recovery is required" }

// DurableRunState 是 PostgreSQL owner 中一个运行及其租约的只读投影。
type DurableRunState struct {
	Fence        heteroci.OwnerFence           `json:"fence"`
	RunID        string                        `json:"run_id"`
	Manifest     heteroci.Manifest             `json:"manifest"`
	Units        []heteroci.ExecutionUnit      `json:"units"`
	Decision     heteroci.RoutingDecision      `json:"decision"`
	Leases       []heteroci.RoutingLease       `json:"leases"`
	HeartbeatTTL time.Duration                 `json:"heartbeat_ttl"`
	Released     bool                          `json:"released"`
	LastRevision heteroci.RoutingOwnerRevision `json:"last_revision"`
}

// DurableOperationState 是 PostgreSQL owner journal 中一个 operation 的只读投影。
type DurableOperationState struct {
	Fence         heteroci.OwnerFence           `json:"fence"`
	OperationID   heteroci.OperationID          `json:"operation_id"`
	Kind          string                        `json:"kind"`
	RequestDigest string                        `json:"request_digest"`
	ResultDigest  string                        `json:"result_digest"`
	ResultBytes   []byte                        `json:"result_bytes"`
	Revision      heteroci.RoutingOwnerRevision `json:"revision"`
}

// durableReservationKey 绑定 node 与 probe incarnation，避免只按 node_id 复用容量。
type durableReservationKey struct {
	nodeID        string
	probeIdentity string
	probeBootID   string
}

// durableReservation 是 planner 与 PostgreSQL projection 共用的容量预留值。
type durableReservation struct {
	key      durableReservationKey
	capacity heteroci.ResourceCapacity
}

// newOwnerID 生成 PostgreSQL owner incarnation 的随机 identity。
func newOwnerID() (heteroci.RoutingOwnerID, error) {
	raw := make([]byte, heteroci.RoutingOwnerIDHexLength/2)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate heteroci owner identity: %w", err)
	}
	ownerID, err := heteroci.ParseRoutingOwnerID(hex.EncodeToString(raw))
	if err != nil {
		return "", err
	}
	return ownerID, nil
}

// validateOperationKind 检查 journal operation kind 的稳定文本边界。
func validateOperationKind(kind string) error {
	if kind == "" || strings.TrimSpace(kind) != kind || strings.IndexFunc(kind, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 || len(kind) > 128 {
		return fmt.Errorf("heteroci owner operation kind is invalid")
	}
	return nil
}

// validateOperationRecordIntegrity 校验 PostgreSQL journal payload、kind、时间和摘要。
func validateOperationRecordIntegrity(record OperationRecord, requestDigest, resultDigest string) error {
	if len(record.RequestBytes) == 0 || len(record.ResultBytes) == 0 {
		return fmt.Errorf("heteroci owner operation payload is empty")
	}
	if err := validateOperationKind(record.Kind); err != nil {
		return fmt.Errorf("validate heteroci owner operation kind: %w", err)
	}
	if record.CommittedAtUnixMilli < 0 {
		return fmt.Errorf("heteroci owner operation commit time is invalid")
	}
	if want := digestBytes(record.RequestBytes); requestDigest != want {
		return fmt.Errorf("heteroci owner operation request digest mismatch")
	}
	if want := digestBytes(record.ResultBytes); resultDigest != want {
		return fmt.Errorf("heteroci owner operation result digest mismatch")
	}
	if err := validateOperationDigestSet(record); err != nil {
		return err
	}
	return nil
}

func validateOperationDigest(name, digest string) error {
	if !validOwnerDigest(digest) {
		return fmt.Errorf("heteroci owner operation %s digest is invalid", name)
	}
	return nil
}

func validateOperationDigestSet(record OperationRecord) error {
	if err := validateOperationDigest("previous journal", record.PreviousJournal); err != nil {
		return err
	}
	if err := validateOperationDigest("journal", record.JournalDigest); err != nil {
		return err
	}
	return validateOperationDigest("projection", record.ProjectionDigest)
}

// journalChainDigest 生成 owner journal 的 domain-separated chain digest。
func journalChainDigest(head heteroci.OwnerFence, request OperationRequest, previousJournal, requestDigest, resultDigest string, revision heteroci.RoutingOwnerRevision) string {
	hash := sha256.New()
	writeFrame := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	writeFrame("heteroci-routing-journal-chain/v1")
	writeFrame(previousJournal)
	writeFrame(string(head.Scope))
	writeFrame(string(head.OwnerID))
	writeFrame(fmt.Sprint(head.OwnerEpoch))
	writeFrame(fmt.Sprint(revision))
	writeFrame(string(request.OperationID))
	writeFrame(request.Kind)
	writeFrame(requestDigest)
	writeFrame(resultDigest)
	writeFrame(request.ProjectionDigest)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// validOwnerDigest 检查带 sha256 前缀的 64 位小写摘要。
func validOwnerDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// planFromReservations 在 PostgreSQL 当前容量快照上复用唯一 storage-neutral planner。
func planFromReservations(fence heteroci.OwnerFence, runID string, manifest heteroci.Manifest, sourceSnapshot heteroci.SnapshotSet, units []heteroci.ExecutionUnit, heartbeatTTL time.Duration, now time.Time, current map[durableReservationKey]heteroci.ResourceCapacity) (DurableReservedRoutingPlan, []durableReservation, error) {
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

// effectiveSnapshot 扣除同一 owner epoch 已提交的容量预留并校验 probe identity。
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

// buildDurableReservations 从 packing plan 计算按 probe incarnation 绑定的峰值预留。
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

// reservationPeak 计算单节点波次的 componentwise 最大资源。
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

// validateRequestedCapacity 校验新运行不会超过 effective snapshot 容量。
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
func buildDurableLeases(runID string, decision heteroci.RoutingDecision, reservedAt int64) ([]heteroci.RoutingLease, error) {
	leases := make([]heteroci.RoutingLease, 0, len(decision.Assignments))
	seen := make(map[string]bool, len(decision.Assignments))
	for _, assignment := range decision.Assignments {
		if seen[assignment.UnitID] {
			return nil, fmt.Errorf("durable decision assigns a unit twice")
		}
		seen[assignment.UnitID] = true
		leases = append(leases, heteroci.RoutingLease{SchemaVersion: heteroci.RoutingLeaseSchemaVersion, Authority: heteroci.AuthorityLocalNonAuthoritative, RunID: runID, DecisionDigest: decision.DecisionDigest, UnitID: assignment.UnitID, Executor: assignment.Executor, NodeID: assignment.NodeID, Endpoint: assignment.Endpoint, ProbeIdentity: assignment.ProbeIdentity, ProbeBootID: assignment.ProbeBootID, State: heteroci.LeaseStatePlanned, ReservedAtUnixMilli: reservedAt})
	}
	sort.Slice(leases, func(left, right int) bool { return leases[left].UnitID < leases[right].UnitID })
	return leases, nil
}

// validateDurablePlanResult 校验 PostgreSQL journal replay 的规划、快照和租约绑定。
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

// validateDurablePlanIdentity 校验 replay fence、operation 和 snapshot epoch 一致。
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
func validateDurablePlanSnapshots(result DurableReservedRoutingPlan, manifest heteroci.Manifest, heartbeatTTL time.Duration, now time.Time) error {
	if err := result.SourceSnapshot.ValidateAgainst(manifest, now, heartbeatTTL); err != nil {
		return fmt.Errorf("validate durable replay source snapshot: %w", err)
	}
	if err := result.EffectiveSnapshot.ValidateAgainst(manifest, now, heartbeatTTL); err != nil {
		return fmt.Errorf("validate durable replay effective snapshot: %w", err)
	}
	return nil
}

// cloneSnapshot 深拷贝 snapshot 节点、能力与资格回执，避免 replay 共享底层数组。
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
func cloneLeases(source []heteroci.RoutingLease) []heteroci.RoutingLease {
	if source == nil {
		return nil
	}
	return append([]heteroci.RoutingLease(nil), source...)
}

// addCapacity 对容量执行非负且不会溢出的 componentwise 加法。
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

type durableReadStateError struct{ cause error }

// Error 返回不泄露 durable payload、端点或探针身份的稳定读取错误。
func (err durableReadStateError) Error() string { return "heteroci owner durable state read failed" }

// Unwrap 保留内部根因供 errors.Is/As 判断，但不向调用方暴露底层错误文本。
func (err durableReadStateError) Unwrap() error { return err.cause }
func redactDurableReadError(cause error) error {
	if cause == nil {
		return nil
	}
	return durableReadStateError{cause: cause}
}
