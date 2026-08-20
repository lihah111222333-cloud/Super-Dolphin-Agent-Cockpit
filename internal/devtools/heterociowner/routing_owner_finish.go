package heterociowner

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const (
	finishOperationKind        = "finish"
	cancelPlannedOperationKind = "cancel_planned"
)

// DurableLeaseTransition 是终态 mutation 提交后的 fence 和 lease 值。
type DurableLeaseTransition struct {
	Fence heteroci.OwnerFence   `json:"fence"`
	Lease heteroci.RoutingLease `json:"lease"`
}

type terminalOperationRequest struct {
	Fence  heteroci.OwnerFence        `json:"fence"`
	RunID  string                     `json:"run_id"`
	UnitID string                     `json:"unit_id"`
	State  heteroci.RoutingLeaseState `json:"state"`
}

// Finish 将 started lease 原子推进为 succeeded 或 failed，并在运行完成时释放预留。
func (owner *Owner) Finish(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID, unitID string, terminal heteroci.RoutingLeaseState) (DurableLeaseTransition, error) {
	if terminal != heteroci.LeaseStateSucceeded && terminal != heteroci.LeaseStateFailed {
		return DurableLeaseTransition{}, fmt.Errorf("durable finish state must be succeeded or failed")
	}
	return owner.transitionLease(ctx, fence, operationID, runID, unitID, terminal, finishOperationKind)
}

// CancelPlanned 将尚未启动的 lease 原子取消，并在运行完成时释放预留。
func (owner *Owner) CancelPlanned(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID, unitID string) (DurableLeaseTransition, error) {
	return owner.transitionLease(ctx, fence, operationID, runID, unitID, heteroci.LeaseStateCancelled, cancelPlannedOperationKind)
}

// transitionLease 执行终态入口校验、owner clock 取值和 immediate transaction。
func (owner *Owner) transitionLease(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID, unitID string, target heteroci.RoutingLeaseState, kind string) (DurableLeaseTransition, error) {
	if err := validateTerminalArguments(owner, fence, operationID, runID, unitID, target, kind); err != nil {
		return DurableLeaseTransition{}, err
	}
	nowUnixMilli, err := owner.nowUnixMilli()
	if err != nil {
		return DurableLeaseTransition{}, err
	}
	requestBytes, err := json.Marshal(terminalOperationRequest{Fence: fence, RunID: runID, UnitID: unitID, State: target})
	if err != nil {
		return DurableLeaseTransition{}, fmt.Errorf("encode durable terminal request: %w", err)
	}
	var transition DurableLeaseTransition
	err = WithImmediateTx(ctx, owner.db, func(tx *sql.Tx) error {
		return owner.transitionLeaseTx(ctx, tx, fence, operationID, runID, unitID, target, kind, time.UnixMilli(nowUnixMilli), nowUnixMilli, requestBytes, &transition)
	})
	if err != nil {
		return DurableLeaseTransition{}, err
	}
	return transition, nil
}

// validateTerminalArguments 校验 owner/fence/operation/run/unit 与目标状态矩阵。
func validateTerminalArguments(owner *Owner, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID, unitID string, target heteroci.RoutingLeaseState, kind string) error {
	if err := validatePlanOwnerBinding(owner, fence, operationID, runID); err != nil {
		return err
	}
	if !validOwnerRunID(unitID) {
		return fmt.Errorf("heteroci owner unit id is invalid")
	}
	if kind == finishOperationKind && target != heteroci.LeaseStateSucceeded && target != heteroci.LeaseStateFailed {
		return fmt.Errorf("durable finish state is invalid")
	}
	if kind == cancelPlannedOperationKind && target != heteroci.LeaseStateCancelled {
		return fmt.Errorf("durable cancel state is invalid")
	}
	return nil
}

// transitionLeaseTx 执行 exact replay 或新的 lease/run/reservation/journal CAS。
func (owner *Owner) transitionLeaseTx(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID, unitID string, target heteroci.RoutingLeaseState, kind string, now time.Time, nowUnixMilli int64, requestBytes []byte, transition *DurableLeaseTransition) error {
	existing, found, err := readOperationByID(ctx, tx, fence.Scope, fence.OwnerEpoch, operationID)
	if err != nil {
		return err
	}
	run, err := loadDurableRun(ctx, tx, fence, runID)
	if err != nil {
		return err
	}
	if found {
		return replayTerminalOperation(existing, requestBytes, fence, run, target, kind, now, transition)
	}
	if run.released {
		return fmt.Errorf("durable routing run is released")
	}
	if err := owner.requireCurrentFence(ctx, tx, fence); err != nil {
		return err
	}
	return commitTerminalTransition(ctx, tx, fence, operationID, runID, unitID, target, kind, run, now, nowUnixMilli, requestBytes, transition)
}

// replayTerminalOperation 返回 exact request 对应的 immutable terminal projection。
func replayTerminalOperation(existing OperationRecord, requestBytes []byte, fence heteroci.OwnerFence, run durableRunRecord, target heteroci.RoutingLeaseState, kind string, now time.Time, transition *DurableLeaseTransition) error {
	if existing.Kind != kind || !bytes.Equal(existing.RequestBytes, requestBytes) {
		return ErrOperationConflict
	}
	if err := json.Unmarshal(existing.ResultBytes, transition); err != nil {
		return fmt.Errorf("decode durable terminal replay: %w", err)
	}
	wantFence, err := heteroci.NewOwnerFence(fence.Scope, fence.OwnerID, fence.OwnerEpoch, existing.Revision)
	if err != nil || transition.Fence != wantFence || transition.Lease.State != target {
		return fmt.Errorf("durable terminal replay identity drifted")
	}
	if err := transition.Lease.ValidateAgainst(run.decision, now); err != nil {
		return fmt.Errorf("validate durable terminal replay: %w", err)
	}
	return nil
}

// commitTerminalTransition 构造终态 lease 并提交所有 SQLite projection。
func commitTerminalTransition(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID, unitID string, target heteroci.RoutingLeaseState, kind string, run durableRunRecord, now time.Time, nowUnixMilli int64, requestBytes []byte, transition *DurableLeaseTransition) error {
	lease, leaseRevision, err := loadDurableLease(ctx, tx, fence, runID, unitID)
	if err != nil {
		return err
	}
	nextFence, nextLease, err := buildTerminalTransition(fence, lease, run.decision, target, kind, now)
	if err != nil {
		return err
	}
	next := DurableLeaseTransition{Fence: nextFence, Lease: nextLease}
	resultBytes, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("encode durable terminal result: %w", err)
	}
	if err := persistTerminalLease(ctx, tx, fence, runID, unitID, leaseRevision, run.lastRevision, next); err != nil {
		return err
	}
	_, err = commitOperationTx(ctx, tx, OperationRequest{Fence: fence, OperationID: operationID, Kind: kind, RequestBytes: requestBytes, ResultBytes: resultBytes, ProjectionDigest: digestBytes(resultBytes), CommittedAtUnixMilli: nowUnixMilli})
	if err != nil {
		return err
	}
	*transition = next
	return nil
}

// buildTerminalTransition 校验 planned/started 来源状态与时间顺序。
func buildTerminalTransition(fence heteroci.OwnerFence, lease heteroci.RoutingLease, decision heteroci.RoutingDecision, target heteroci.RoutingLeaseState, kind string, now time.Time) (heteroci.OwnerFence, heteroci.RoutingLease, error) {
	if kind == finishOperationKind && lease.State != heteroci.LeaseStateStarted {
		return heteroci.OwnerFence{}, heteroci.RoutingLease{}, fmt.Errorf("durable finish lease is not started")
	}
	if kind == cancelPlannedOperationKind && lease.State != heteroci.LeaseStatePlanned {
		return heteroci.OwnerFence{}, heteroci.RoutingLease{}, fmt.Errorf("durable cancel lease is not planned")
	}
	nextFence, err := nextFence(fence)
	if err != nil {
		return heteroci.OwnerFence{}, heteroci.RoutingLease{}, err
	}
	next := lease
	next.State = target
	next.FinishedAtUnixMilli = now.UnixMilli()
	if target == heteroci.LeaseStateCancelled {
		next.StartedAtUnixMilli = 0
	}
	if err := next.ValidateAgainst(decision, now); err != nil {
		return heteroci.OwnerFence{}, heteroci.RoutingLease{}, fmt.Errorf("validate durable terminal lease: %w", err)
	}
	return nextFence, next, nil
}

// nextFence 将 owner fence revision 严格推进一位。
func nextFence(fence heteroci.OwnerFence) (heteroci.OwnerFence, error) {
	revision, err := nextOwnerRevision(fence)
	if err != nil {
		return heteroci.OwnerFence{}, err
	}
	return heteroci.NewOwnerFence(fence.Scope, fence.OwnerID, fence.OwnerEpoch, revision)
}

// persistTerminalLease 更新 lease，判断运行完成并同步释放 reservation。
func persistTerminalLease(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID, unitID string, leaseRevision, runRevision heteroci.RoutingOwnerRevision, next DurableLeaseTransition) error {
	leaseBytes, err := json.Marshal(next.Lease)
	if err != nil {
		return fmt.Errorf("encode durable terminal lease: %w", err)
	}
	result, err := tx.ExecContext(ctx, "UPDATE heteroci_routing_leases SET lease_json = ?, state = ?, started_at_unix_milli = ?, finished_at_unix_milli = ?, last_revision = ? WHERE scope = ? AND owner_epoch = ? AND run_id = ? AND unit_id = ? AND last_revision = ?", leaseBytes, next.Lease.State, next.Lease.StartedAtUnixMilli, next.Lease.FinishedAtUnixMilli, next.Fence.Revision, fence.Scope, fence.OwnerEpoch, runID, unitID, leaseRevision)
	if err != nil {
		return fmt.Errorf("update durable terminal lease: %w", err)
	}
	if err := requireSingleRow(result, "durable terminal lease CAS"); err != nil {
		return err
	}
	complete, err := durableRunComplete(ctx, tx, fence, runID)
	if err != nil {
		return err
	}
	if err := updateTerminalRun(ctx, tx, fence, runID, runRevision, next.Fence.Revision, complete); err != nil {
		return err
	}
	if complete {
		return releaseDurableReservations(ctx, tx, fence, runID, next.Fence.Revision)
	}
	return nil
}

// durableRunComplete 检查所有 lease 都处于 terminal/cancelled 状态。
func durableRunComplete(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID string) (bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT state FROM heteroci_routing_leases WHERE scope = ? AND owner_epoch = ? AND run_id = ? ORDER BY unit_id", fence.Scope, fence.OwnerEpoch, runID)
	if err != nil {
		return false, fmt.Errorf("read durable run lease states: %w", err)
	}
	defer rows.Close()
	count := 0
	complete := true
	for rows.Next() {
		var state heteroci.RoutingLeaseState
		if err := rows.Scan(&state); err != nil {
			return false, fmt.Errorf("scan durable run lease state: %w", err)
		}
		count++
		complete = complete && durableTerminalState(state)
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate durable run lease states: %w", err)
	}
	if count == 0 {
		return false, fmt.Errorf("durable routing run has no leases")
	}
	return complete, nil
}

// durableTerminalState 判断状态是否不再允许启动或执行。
func durableTerminalState(state heteroci.RoutingLeaseState) bool {
	return state == heteroci.LeaseStateSucceeded || state == heteroci.LeaseStateFailed || state == heteroci.LeaseStateCancelled
}

// updateTerminalRun 以 run revision CAS 同步 last_revision 和 released。
func updateTerminalRun(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID string, oldRevision, nextRevision heteroci.RoutingOwnerRevision, complete bool) error {
	released := 0
	if complete {
		released = 1
	}
	result, err := tx.ExecContext(ctx, "UPDATE heteroci_routing_runs SET last_revision = ?, released = ? WHERE scope = ? AND owner_epoch = ? AND run_id = ? AND released = 0 AND last_revision = ?", nextRevision, released, fence.Scope, fence.OwnerEpoch, runID, oldRevision)
	if err != nil {
		return fmt.Errorf("update durable terminal run: %w", err)
	}
	return requireSingleRow(result, "durable terminal run CAS")
}

// releaseDurableReservations 将完成运行的当前 epoch 预留标记为 released。
func releaseDurableReservations(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID string, revision heteroci.RoutingOwnerRevision) error {
	_, err := tx.ExecContext(ctx, "UPDATE heteroci_routing_reservations SET released = 1, last_revision = ? WHERE scope = ? AND owner_epoch = ? AND run_id = ? AND released = 0", revision, fence.Scope, fence.OwnerEpoch, runID)
	if err != nil {
		return fmt.Errorf("release durable routing reservations: %w", err)
	}
	return nil
}
