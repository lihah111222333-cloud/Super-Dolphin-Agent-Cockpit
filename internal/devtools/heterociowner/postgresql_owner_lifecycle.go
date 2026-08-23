package heterociowner

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const (
	postgresqlStartOperationKind  = "topology_start/v1"
	postgresqlFinishOperationKind = "topology_finish/v1"
	postgresqlCancelOperationKind = "topology_cancel_planned/v1"
)

type postgresqlStartRequest struct {
	Fence           heteroci.OwnerFence    `json:"fence"`
	RunID           heteroci.TopologyRunID `json:"run_id"`
	UnitID          string                 `json:"unit_id"`
	CurrentSnapshot heteroci.SnapshotSet   `json:"current_snapshot"`
}

type postgresqlTerminalRequest struct {
	Fence  heteroci.OwnerFence        `json:"fence"`
	RunID  heteroci.TopologyRunID     `json:"run_id"`
	UnitID string                     `json:"unit_id"`
	State  heteroci.RoutingLeaseState `json:"state"`
}

type postgresqlRunProjection struct {
	Fence         heteroci.OwnerFence
	RunID         string
	RepositoryID  string
	SourceTreeSHA string
	PlanOperation heteroci.OperationID
	Manifest      heteroci.Manifest
	Units         []heteroci.ExecutionUnit
	Decision      heteroci.RoutingDecision
	Leases        []heteroci.RoutingLease
	HeartbeatTTL  time.Duration
	LastRevision  heteroci.RoutingOwnerRevision
	PlanResult    heteroci.TopologyReservedPlan
}

type postgresqlRunRow struct {
	OwnerEpoch      int64
	RunID           string
	RepositoryID    string
	SourceTreeSHA   string
	PlanOperationID string
	HeartbeatTTLMs  int64
	LastRevision    int64
}

type postgresqlLeaseProjection struct {
	Base         heteroci.RoutingLease
	Started      heteroci.StartedRoutingLease
	Terminal     heteroci.RoutingLease
	State        string
	LastRevision heteroci.RoutingOwnerRevision
}

type postgresqlLeaseRow struct {
	State         string
	LeaseBytes    []byte
	StartedBytes  []byte
	TerminalBytes []byte
	LastRevision  int64
	StartedAt     sql.NullInt64
	FinishedAt    sql.NullInt64
}

// Start 将远程 PG 中的 planned lease 原子推进为 started，并记录不可变 journal。
func (owner *PostgreSQLOwner) Start(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string, currentSnapshot heteroci.SnapshotSet) (heteroci.StartedRoutingLease, error) {
	if err := validatePostgreSQLStartArguments(owner, fence, operationID, runID, unitID, currentSnapshot); err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	nowUnixMilli, err := owner.nowUnixMilli()
	if err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	requestBytes, err := json.Marshal(postgresqlStartRequest{Fence: fence, RunID: runID, UnitID: unitID, CurrentSnapshot: currentSnapshot})
	if err != nil {
		return heteroci.StartedRoutingLease{}, fmt.Errorf("encode PostgreSQL start request: %w", err)
	}
	started, err := owner.startPostgreSQLOnce(ctx, fence, operationID, runID, unitID, currentSnapshot, requestBytes, nowUnixMilli)
	if err != nil {
		return heteroci.StartedRoutingLease{}, wrapPostgreSQLOwnerMutationError("start", err)
	}
	return started, nil
}

func (owner *PostgreSQLOwner) startPostgreSQLOnce(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string, snapshot heteroci.SnapshotSet, requestBytes []byte, nowUnixMilli int64) (heteroci.StartedRoutingLease, error) {
	tx, err := owner.beginPostgreSQLOwnerTx(ctx)
	if err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	started, err := executePostgreSQLStartTransaction(ctx, tx, fence, operationID, runID, unitID, snapshot, requestBytes, nowUnixMilli)
	if err != nil {
		return heteroci.StartedRoutingLease{}, rollbackPostgreSQLOwnerTx(tx, err)
	}
	if err := tx.Commit(); err != nil {
		replayed, found, reconcileErr := owner.reconcilePostgreSQLStarted(ctx, fence, operationID, requestBytes, err)
		if reconcileErr != nil {
			return heteroci.StartedRoutingLease{}, reconcileErr
		}
		if found {
			return replayed, nil
		}
		return heteroci.StartedRoutingLease{}, errors.Join(ErrPostgreSQLOwnerCommitUncertain, err, errPostgreSQLOwnerCommitNotRecorded)
	}
	return started, nil
}

func executePostgreSQLStartTransaction(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string, snapshot heteroci.SnapshotSet, requestBytes []byte, nowUnixMilli int64) (heteroci.StartedRoutingLease, error) {
	record, found, err := readPostgreSQLOperation(ctx, tx, fence.Scope, operationID)
	if err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	if found {
		return replayPostgreSQLStarted(record, fence, requestBytes, string(runID), unitID, nowUnixMilli)
	}
	if err := requirePostgreSQLCurrentFence(ctx, tx, fence); err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	run, lease, err := loadPostgreSQLStartState(ctx, tx, fence, runID, unitID, snapshot, nowUnixMilli)
	if err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	return commitPostgreSQLStart(ctx, tx, fence, operationID, runID, unitID, run, lease, requestBytes, nowUnixMilli)
}

// loadPostgreSQLStartState 读取并校验 Start 所需的 run、snapshot 和 planned lease。
func loadPostgreSQLStartState(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID heteroci.TopologyRunID, unitID string, snapshot heteroci.SnapshotSet, nowUnixMilli int64) (postgresqlRunProjection, postgresqlLeaseProjection, error) {
	now := time.UnixMilli(nowUnixMilli)
	run, err := loadPostgreSQLRun(ctx, tx, fence.Scope, fence.OwnerEpoch, runID, now)
	if err != nil {
		return postgresqlRunProjection{}, postgresqlLeaseProjection{}, err
	}
	if err := validatePostgreSQLStartRun(run, fence); err != nil {
		return postgresqlRunProjection{}, postgresqlLeaseProjection{}, err
	}
	if err := validatePostgreSQLStartSnapshot(snapshot, run, fence); err != nil {
		return postgresqlRunProjection{}, postgresqlLeaseProjection{}, err
	}
	lease, err := loadPostgreSQLLease(ctx, tx, fence, run.RunID, unitID, run.Decision, now)
	if err != nil {
		return postgresqlRunProjection{}, postgresqlLeaseProjection{}, err
	}
	if lease.State != string(heteroci.LeaseStatePlanned) || lease.Base.State != heteroci.LeaseStatePlanned {
		return postgresqlRunProjection{}, postgresqlLeaseProjection{}, fmt.Errorf("PostgreSQL routing lease is not planned")
	}
	return run, lease, nil
}

func validatePostgreSQLStartRun(run postgresqlRunProjection, fence heteroci.OwnerFence) error {
	if run.Fence.OwnerID != fence.OwnerID || run.Fence.OwnerEpoch != fence.OwnerEpoch || run.LastRevision != fence.Revision {
		return fmt.Errorf("PostgreSQL start fence is stale")
	}
	if run.PlanResult.Fence.Revision <= 1 || run.PlanResult.Fence.Revision > fence.Revision {
		return fmt.Errorf("PostgreSQL start plan revision is invalid")
	}
	return nil
}

func validatePostgreSQLStartSnapshot(snapshot heteroci.SnapshotSet, run postgresqlRunProjection, fence heteroci.OwnerFence) error {
	if snapshot.OwnerEpoch != fence.OwnerEpoch || !reflect.DeepEqual(snapshot, run.PlanResult.SourceSnapshot) {
		return fmt.Errorf("PostgreSQL current snapshot identity is invalid")
	}
	return nil
}

// commitPostgreSQLStart 写入 started projection、run revision 和 immutable journal。
func commitPostgreSQLStart(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string, run postgresqlRunProjection, lease postgresqlLeaseProjection, requestBytes []byte, nowUnixMilli int64) (heteroci.StartedRoutingLease, error) {
	now := time.UnixMilli(nowUnixMilli)
	nextFence, nextLease, err := buildPostgreSQLStartedTransition(fence, lease.Base, run.Decision, now)
	if err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	started, err := heteroci.NewStartedRoutingLease(nextFence, nextLease, run.Decision, now)
	if err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	resultBytes, err := json.Marshal(started)
	if err != nil {
		return heteroci.StartedRoutingLease{}, fmt.Errorf("encode PostgreSQL started result: %w", err)
	}
	if err := persistPostgreSQLStarted(ctx, tx, fence, string(runID), unitID, nextFence.Revision, lease.LastRevision, run.LastRevision, nextLease, started, run.PlanResult, run.Leases); err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	if err := persistPostgreSQLLifecycleOperation(ctx, tx, fence, operationID, postgresqlStartOperationKind, requestBytes, resultBytes, nowUnixMilli); err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	return started, nil
}

func (owner *PostgreSQLOwner) reconcilePostgreSQLStarted(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, requestBytes []byte, commitErr error) (heteroci.StartedRoutingLease, bool, error) {
	record, found, err := owner.reconcilePostgreSQLOperation(ctx, fence, operationID, requestBytes)
	if err != nil {
		return heteroci.StartedRoutingLease{}, false, errors.Join(ErrPostgreSQLOwnerCommitUncertain, commitErr, err)
	}
	if !found {
		return heteroci.StartedRoutingLease{}, false, nil
	}
	nowUnixMilli, err := owner.nowUnixMilli()
	if err != nil {
		return heteroci.StartedRoutingLease{}, false, err
	}
	started, err := replayPostgreSQLStarted(record, fence, requestBytes, "", "", nowUnixMilli)
	if err != nil {
		return heteroci.StartedRoutingLease{}, false, errors.Join(ErrPostgreSQLOwnerCommitUncertain, commitErr, err)
	}
	return started, true, nil
}

// Finish 将远程 PG 中的 started lease 原子推进为 succeeded 或 failed。
func (owner *PostgreSQLOwner) Finish(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string, terminal heteroci.RoutingLeaseState) (DurableLeaseTransition, error) {
	if terminal != heteroci.LeaseStateSucceeded && terminal != heteroci.LeaseStateFailed {
		return DurableLeaseTransition{}, fmt.Errorf("PostgreSQL finish state must be succeeded or failed")
	}
	return owner.transitionPostgreSQLLease(ctx, fence, operationID, runID, unitID, terminal, postgresqlFinishOperationKind)
}

// CancelPlanned 将远程 PG 中尚未启动的 planned lease 原子取消。
func (owner *PostgreSQLOwner) CancelPlanned(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string) (DurableLeaseTransition, error) {
	return owner.transitionPostgreSQLLease(ctx, fence, operationID, runID, unitID, heteroci.LeaseStateCancelled, postgresqlCancelOperationKind)
}

func (owner *PostgreSQLOwner) transitionPostgreSQLLease(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string, target heteroci.RoutingLeaseState, kind string) (DurableLeaseTransition, error) {
	if err := validatePostgreSQLTerminalArguments(owner, fence, operationID, runID, unitID, target, kind); err != nil {
		return DurableLeaseTransition{}, err
	}
	nowUnixMilli, err := owner.nowUnixMilli()
	if err != nil {
		return DurableLeaseTransition{}, err
	}
	requestBytes, err := json.Marshal(postgresqlTerminalRequest{Fence: fence, RunID: runID, UnitID: unitID, State: target})
	if err != nil {
		return DurableLeaseTransition{}, fmt.Errorf("encode PostgreSQL terminal request: %w", err)
	}
	transition, err := owner.transitionPostgreSQLOnce(ctx, fence, operationID, runID, unitID, target, kind, requestBytes, nowUnixMilli)
	if err != nil {
		return DurableLeaseTransition{}, wrapPostgreSQLOwnerMutationError("transition", err)
	}
	return transition, nil
}

func (owner *PostgreSQLOwner) transitionPostgreSQLOnce(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string, target heteroci.RoutingLeaseState, kind string, requestBytes []byte, nowUnixMilli int64) (DurableLeaseTransition, error) {
	tx, err := owner.beginPostgreSQLOwnerTx(ctx)
	if err != nil {
		return DurableLeaseTransition{}, err
	}
	transition, err := executePostgreSQLTerminalTransaction(ctx, tx, fence, operationID, runID, unitID, target, kind, requestBytes, nowUnixMilli)
	if err != nil {
		return DurableLeaseTransition{}, rollbackPostgreSQLOwnerTx(tx, err)
	}
	if err := tx.Commit(); err != nil {
		replayed, found, reconcileErr := owner.reconcilePostgreSQLTransition(ctx, fence, operationID, requestBytes, target, err)
		if reconcileErr != nil {
			return DurableLeaseTransition{}, reconcileErr
		}
		if found {
			return replayed, nil
		}
		return DurableLeaseTransition{}, errors.Join(ErrPostgreSQLOwnerCommitUncertain, err, errPostgreSQLOwnerCommitNotRecorded)
	}
	return transition, nil
}

func executePostgreSQLTerminalTransaction(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string, target heteroci.RoutingLeaseState, kind string, requestBytes []byte, nowUnixMilli int64) (DurableLeaseTransition, error) {
	record, found, err := readPostgreSQLOperation(ctx, tx, fence.Scope, operationID)
	if err != nil {
		return DurableLeaseTransition{}, err
	}
	if found {
		return replayPostgreSQLTransition(record, fence, requestBytes, target, nowUnixMilli)
	}
	if err := requirePostgreSQLCurrentFence(ctx, tx, fence); err != nil {
		return DurableLeaseTransition{}, err
	}
	run, lease, err := loadPostgreSQLTerminalState(ctx, tx, fence, runID, unitID, kind, nowUnixMilli)
	if err != nil {
		return DurableLeaseTransition{}, err
	}
	return commitPostgreSQLTerminal(ctx, tx, fence, operationID, runID, unitID, target, kind, run, lease, requestBytes, nowUnixMilli)
}

// loadPostgreSQLTerminalState 读取终态 mutation 所需的 run 和 lease projection。
func loadPostgreSQLTerminalState(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID heteroci.TopologyRunID, unitID, kind string, nowUnixMilli int64) (postgresqlRunProjection, postgresqlLeaseProjection, error) {
	now := time.UnixMilli(nowUnixMilli)
	run, err := loadPostgreSQLRun(ctx, tx, fence.Scope, fence.OwnerEpoch, runID, now)
	if err != nil {
		return postgresqlRunProjection{}, postgresqlLeaseProjection{}, err
	}
	if run.LastRevision != fence.Revision || run.Fence.OwnerID != fence.OwnerID {
		return postgresqlRunProjection{}, postgresqlLeaseProjection{}, fmt.Errorf("PostgreSQL terminal fence is stale")
	}
	lease, err := loadPostgreSQLLease(ctx, tx, fence, run.RunID, unitID, run.Decision, now)
	if err != nil {
		return postgresqlRunProjection{}, postgresqlLeaseProjection{}, err
	}
	if !validPostgreSQLTerminalSource(lease, kind) {
		return postgresqlRunProjection{}, postgresqlLeaseProjection{}, fmt.Errorf("PostgreSQL terminal lease source state is invalid")
	}
	return run, lease, nil
}

// commitPostgreSQLTerminal 写入 terminal projection、release fact 和 immutable journal。
func commitPostgreSQLTerminal(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string, target heteroci.RoutingLeaseState, kind string, run postgresqlRunProjection, lease postgresqlLeaseProjection, requestBytes []byte, nowUnixMilli int64) (DurableLeaseTransition, error) {
	source := lease.Base
	if kind == postgresqlFinishOperationKind {
		source = lease.Started.Lease
	}
	now := time.UnixMilli(nowUnixMilli)
	nextFence, nextLease, err := buildPostgreSQLTerminalTransition(fence, source, run.Decision, target, kind, now)
	if err != nil {
		return DurableLeaseTransition{}, err
	}
	transition := DurableLeaseTransition{Fence: nextFence, Lease: nextLease}
	resultBytes, err := json.Marshal(transition)
	if err != nil {
		return DurableLeaseTransition{}, fmt.Errorf("encode PostgreSQL terminal result: %w", err)
	}
	complete, err := postgresqlRunWillBeComplete(ctx, tx, fence, run.RunID, unitID)
	if err != nil {
		return DurableLeaseTransition{}, err
	}
	if err := persistPostgreSQLTerminal(ctx, tx, fence, string(runID), unitID, lease.LastRevision, run.LastRevision, nextFence.Revision, nextLease, complete, run.PlanResult, run.Leases); err != nil {
		return DurableLeaseTransition{}, err
	}
	if err := persistPostgreSQLLifecycleOperation(ctx, tx, fence, operationID, kind, requestBytes, resultBytes, nowUnixMilli); err != nil {
		return DurableLeaseTransition{}, err
	}
	return transition, nil
}

func (owner *PostgreSQLOwner) reconcilePostgreSQLTransition(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, requestBytes []byte, target heteroci.RoutingLeaseState, commitErr error) (DurableLeaseTransition, bool, error) {
	record, found, err := owner.reconcilePostgreSQLOperation(ctx, fence, operationID, requestBytes)
	if err != nil {
		return DurableLeaseTransition{}, false, errors.Join(ErrPostgreSQLOwnerCommitUncertain, commitErr, err)
	}
	if !found {
		return DurableLeaseTransition{}, false, nil
	}
	nowUnixMilli, err := owner.nowUnixMilli()
	if err != nil {
		return DurableLeaseTransition{}, false, err
	}
	transition, err := replayPostgreSQLTransition(record, fence, requestBytes, target, nowUnixMilli)
	if err != nil {
		return DurableLeaseTransition{}, false, errors.Join(ErrPostgreSQLOwnerCommitUncertain, commitErr, err)
	}
	return transition, true, nil
}

func (owner *PostgreSQLOwner) reconcilePostgreSQLOperation(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, requestBytes []byte) (postgresqlOperationRecord, bool, error) {
	tx, err := owner.beginPostgreSQLOwnerTx(ctx)
	if err != nil {
		return postgresqlOperationRecord{}, false, err
	}
	record, found, err := readPostgreSQLOperation(ctx, tx, fence.Scope, operationID)
	if err != nil {
		return postgresqlOperationRecord{}, false, rollbackPostgreSQLOwnerTx(tx, err)
	}
	if found {
		if err := validatePostgreSQLLifecycleReplayRecord(record, fence, operationID, requestBytes); err != nil {
			return postgresqlOperationRecord{}, false, rollbackPostgreSQLOwnerTx(tx, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return postgresqlOperationRecord{}, false, err
	}
	return record, found, nil
}

func validatePostgreSQLLifecycleReplayRecord(record postgresqlOperationRecord, fence heteroci.OwnerFence, operationID heteroci.OperationID, requestBytes []byte) error {
	if record.Scope != fence.Scope || record.OwnerID != fence.OwnerID || record.OwnerEpoch != fence.OwnerEpoch || record.OperationID != operationID {
		return fmt.Errorf("PostgreSQL lifecycle replay owner identity drifted")
	}
	expectedRevision, err := nextOwnerRevision(fence)
	if err != nil {
		return err
	}
	if record.Revision != expectedRevision {
		return fmt.Errorf("PostgreSQL lifecycle replay revision drifted")
	}
	if !bytes.Equal(record.RequestBytes, requestBytes) || record.RequestDigest != digestBytes(requestBytes) {
		return ErrOperationConflict
	}
	request := OperationRequest{Fence: fence, OperationID: operationID, Kind: record.Kind, RequestBytes: record.RequestBytes, ResultBytes: record.ResultBytes, ProjectionDigest: record.ProjectionDigest, CommittedAtUnixMilli: record.CommittedAtUnixMilli}
	if journalChainDigest(fence, request, record.PreviousJournal, record.RequestDigest, record.ResultDigest, record.Revision) != record.JournalDigest {
		return fmt.Errorf("PostgreSQL lifecycle replay journal chain drifted")
	}
	return nil
}

func validatePostgreSQLStartArguments(owner *PostgreSQLOwner, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string, snapshot heteroci.SnapshotSet) error {
	if err := validatePostgreSQLMutationIdentity(owner, fence, operationID, runID, unitID); err != nil {
		return err
	}
	if !validOwnerRunID(unitID) || snapshot.OwnerEpoch != fence.OwnerEpoch {
		return fmt.Errorf("heteroci PostgreSQL start identity is invalid")
	}
	return nil
}

func validatePostgreSQLTerminalArguments(owner *PostgreSQLOwner, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string, target heteroci.RoutingLeaseState, kind string) error {
	if err := validatePostgreSQLMutationIdentity(owner, fence, operationID, runID, unitID); err != nil {
		return err
	}
	if kind == postgresqlFinishOperationKind && (target != heteroci.LeaseStateSucceeded && target != heteroci.LeaseStateFailed) {
		return fmt.Errorf("heteroci PostgreSQL finish state is invalid")
	}
	if kind == postgresqlCancelOperationKind && target != heteroci.LeaseStateCancelled {
		return fmt.Errorf("heteroci PostgreSQL cancel state is invalid")
	}
	return nil
}

func validatePostgreSQLMutationIdentity(owner *PostgreSQLOwner, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string) error {
	if owner == nil || owner.db == nil {
		return fmt.Errorf("heteroci PostgreSQL owner is nil")
	}
	if err := fence.Validate(); err != nil {
		return err
	}
	if fence.Scope != owner.scope {
		return fmt.Errorf("heteroci PostgreSQL owner fence scope is not bound")
	}
	if _, err := heteroci.ParseOperationID(string(operationID)); err != nil {
		return err
	}
	if err := runID.Validate(); err != nil {
		return err
	}
	if !validOwnerRunID(unitID) {
		return fmt.Errorf("heteroci PostgreSQL unit identity is invalid")
	}
	return nil
}

func requirePostgreSQLCurrentFence(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence) error {
	current, found, err := readPostgreSQLOwnerHead(ctx, tx, fence.Scope, true)
	if err != nil {
		return err
	}
	if !found || current != fence {
		return fmt.Errorf("heteroci PostgreSQL owner fence is stale")
	}
	return nil
}

func buildPostgreSQLStartedTransition(fence heteroci.OwnerFence, lease heteroci.RoutingLease, decision heteroci.RoutingDecision, now time.Time) (heteroci.OwnerFence, heteroci.RoutingLease, error) {
	nextFence, err := nextPostgreSQLFence(fence)
	if err != nil {
		return heteroci.OwnerFence{}, heteroci.RoutingLease{}, err
	}
	next := lease
	next.State = heteroci.LeaseStateStarted
	next.StartedAtUnixMilli = now.UnixMilli()
	next.FinishedAtUnixMilli = 0
	if err := next.ValidateAgainst(decision, now); err != nil {
		return heteroci.OwnerFence{}, heteroci.RoutingLease{}, fmt.Errorf("validate PostgreSQL started lease: %w", err)
	}
	return nextFence, next, nil
}

func buildPostgreSQLTerminalTransition(fence heteroci.OwnerFence, lease heteroci.RoutingLease, decision heteroci.RoutingDecision, target heteroci.RoutingLeaseState, kind string, now time.Time) (heteroci.OwnerFence, heteroci.RoutingLease, error) {
	if kind == postgresqlFinishOperationKind && lease.State != heteroci.LeaseStateStarted {
		return heteroci.OwnerFence{}, heteroci.RoutingLease{}, fmt.Errorf("PostgreSQL finish lease is not started")
	}
	if kind == postgresqlCancelOperationKind && lease.State != heteroci.LeaseStatePlanned {
		return heteroci.OwnerFence{}, heteroci.RoutingLease{}, fmt.Errorf("PostgreSQL cancel lease is not planned")
	}
	nextFence, err := nextPostgreSQLFence(fence)
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
		return heteroci.OwnerFence{}, heteroci.RoutingLease{}, fmt.Errorf("validate PostgreSQL terminal lease: %w", err)
	}
	return nextFence, next, nil
}

func nextPostgreSQLFence(fence heteroci.OwnerFence) (heteroci.OwnerFence, error) {
	revision, err := nextOwnerRevision(fence)
	if err != nil {
		return heteroci.OwnerFence{}, err
	}
	return heteroci.NewOwnerFence(fence.Scope, fence.OwnerID, fence.OwnerEpoch, revision)
}

func validPostgreSQLTerminalSource(lease postgresqlLeaseProjection, kind string) bool {
	if kind == postgresqlFinishOperationKind {
		return lease.State == string(heteroci.LeaseStateStarted) && lease.Started.Lease.State == heteroci.LeaseStateStarted
	}
	return lease.State == string(heteroci.LeaseStatePlanned) && lease.Base.State == heteroci.LeaseStatePlanned
}

func persistPostgreSQLStarted(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID, unitID string, nextRevision, leaseRevision, runRevision heteroci.RoutingOwnerRevision, lease heteroci.RoutingLease, started heteroci.StartedRoutingLease, plan heteroci.TopologyReservedPlan, leases []heteroci.RoutingLease) error {
	startedBytes, err := json.Marshal(started)
	if err != nil {
		return fmt.Errorf("encode PostgreSQL started lease: %w", err)
	}
	result, err := tx.ExecContext(ctx, "UPDATE heteroci_owner.routing_leases SET state = $1, started_bytes = $2, last_revision = $3, started_at_unix_milli = $4 WHERE scope = $5 AND owner_epoch = $6 AND run_id = $7 AND unit_id = $8 AND state = $9 AND last_revision = $10", "started", startedBytes, nextRevision, lease.StartedAtUnixMilli, fence.Scope, fence.OwnerEpoch, runID, unitID, "planned", leaseRevision)
	if err != nil {
		return fmt.Errorf("update PostgreSQL started lease: %w", err)
	}
	if err := requireOnePostgreSQLRow(result, "update PostgreSQL started lease"); err != nil {
		return err
	}
	if err := syncPostgreSQLStartedReservations(ctx, tx, fence, runID, nextRevision, plan, leases, unitID, lease); err != nil {
		return err
	}
	return updatePostgreSQLRunRevision(ctx, tx, fence, runID, runRevision, nextRevision)
}

func updatePostgreSQLRunRevision(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID string, oldRevision, nextRevision heteroci.RoutingOwnerRevision) error {
	result, err := tx.ExecContext(ctx, "UPDATE heteroci_owner.routing_runs SET last_revision = $1 WHERE scope = $2 AND owner_epoch = $3 AND run_id = $4 AND last_revision = $5", nextRevision, fence.Scope, fence.OwnerEpoch, runID, oldRevision)
	if err != nil {
		return fmt.Errorf("update PostgreSQL routing run revision: %w", err)
	}
	return requireOnePostgreSQLRow(result, "update PostgreSQL routing run revision")
}

func postgresqlRunWillBeComplete(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID, currentUnitID string) (bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT unit_id, state FROM heteroci_owner.routing_leases WHERE scope = $1 AND owner_epoch = $2 AND run_id = $3 ORDER BY unit_id", fence.Scope, fence.OwnerEpoch, runID)
	if err != nil {
		return false, fmt.Errorf("read PostgreSQL routing lease states: %w", err)
	}
	defer rows.Close()
	count := 0
	complete := true
	for rows.Next() {
		var unitID, state string
		if err := rows.Scan(&unitID, &state); err != nil {
			return false, fmt.Errorf("scan PostgreSQL routing lease state: %w", err)
		}
		count++
		if unitID == currentUnitID {
			state = "finished"
		}
		complete = complete && state == "finished"
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate PostgreSQL routing lease states: %w", err)
	}
	if count == 0 {
		return false, fmt.Errorf("PostgreSQL routing run has no leases")
	}
	return complete, nil
}

func persistPostgreSQLLifecycleOperation(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, operationID heteroci.OperationID, kind string, requestBytes, resultBytes []byte, nowUnixMilli int64) error {
	previousJournal, err := readPostgreSQLJournalHead(ctx, tx, fence.Scope)
	if err != nil {
		return err
	}
	request := OperationRequest{Fence: fence, OperationID: operationID, Kind: kind, RequestBytes: requestBytes, ResultBytes: resultBytes, ProjectionDigest: digestBytes(resultBytes), CommittedAtUnixMilli: nowUnixMilli}
	requestDigest, resultDigest := digestBytes(requestBytes), digestBytes(resultBytes)
	revision, err := nextOwnerRevision(fence)
	if err != nil {
		return err
	}
	journalDigest := journalChainDigest(fence, request, previousJournal, requestDigest, resultDigest, revision)
	if _, err := tx.ExecContext(ctx, "INSERT INTO heteroci_owner.operation_journal (scope, operation_id, owner_id, owner_epoch, revision, kind, request_bytes, request_digest, result_bytes, result_digest, previous_journal_digest, journal_digest, projection_digest, committed_at_unix_milli) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)", fence.Scope, operationID, fence.OwnerID, fence.OwnerEpoch, revision, kind, requestBytes, requestDigest, resultBytes, resultDigest, previousJournal, journalDigest, request.ProjectionDigest, nowUnixMilli); err != nil {
		return fmt.Errorf("insert PostgreSQL lifecycle operation: %w", err)
	}
	updated, err := tx.ExecContext(ctx, "UPDATE heteroci_owner.owner_heads SET revision = $1, journal_digest = $2, projection_digest = $3, updated_at_unix_milli = $4 WHERE scope = $5 AND owner_id = $6 AND owner_epoch = $7 AND revision = $8 AND journal_digest = $9", revision, journalDigest, request.ProjectionDigest, nowUnixMilli, fence.Scope, fence.OwnerID, fence.OwnerEpoch, fence.Revision, previousJournal)
	if err != nil {
		return fmt.Errorf("advance PostgreSQL lifecycle owner head: %w", err)
	}
	return requireOnePostgreSQLRow(updated, "advance PostgreSQL lifecycle owner head")
}

func replayPostgreSQLStarted(record postgresqlOperationRecord, fence heteroci.OwnerFence, requestBytes []byte, runID, unitID string, nowUnixMilli int64) (heteroci.StartedRoutingLease, error) {
	if err := validatePostgreSQLLifecycleReplayRecord(record, fence, record.OperationID, requestBytes); err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	if record.Kind != postgresqlStartOperationKind {
		return heteroci.StartedRoutingLease{}, ErrOperationConflict
	}
	var started heteroci.StartedRoutingLease
	if err := json.Unmarshal(record.ResultBytes, &started); err != nil {
		return heteroci.StartedRoutingLease{}, fmt.Errorf("decode PostgreSQL started replay: %w", err)
	}
	if err := validatePostgreSQLStartedReplayRequest(record.RequestBytes, fence, runID, unitID, started.Lease); err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	if err := validatePostgreSQLStartedReplayIdentity(started, fence, record.Revision); err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	if err := validatePostgreSQLStartedReplayUnit(started.Lease, runID, unitID); err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	if nowUnixMilli > 0 && started.Lease.StartedAtUnixMilli > nowUnixMilli {
		return heteroci.StartedRoutingLease{}, fmt.Errorf("PostgreSQL started replay timestamp drifted")
	}
	if err := validatePostgreSQLStartedReplayPayload(started); err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	return started, nil
}

func validatePostgreSQLStartedReplayRequest(data []byte, fence heteroci.OwnerFence, runID, unitID string, lease heteroci.RoutingLease) error {
	var request postgresqlStartRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return fmt.Errorf("decode PostgreSQL started replay request: %w", err)
	}
	if err := validatePostgreSQLStartedReplayRequestFence(request, fence); err != nil {
		return err
	}
	if err := validatePostgreSQLStartedReplayCaller(request, runID, unitID); err != nil {
		return err
	}
	return validatePostgreSQLStartedReplayResult(request, lease)
}

func validatePostgreSQLStartedReplayRequestFence(request postgresqlStartRequest, fence heteroci.OwnerFence) error {
	if request.Fence != fence {
		return fmt.Errorf("PostgreSQL started replay request fence drifted")
	}
	if request.RunID == "" || request.UnitID == "" {
		return fmt.Errorf("PostgreSQL started replay request identity drifted")
	}
	return nil
}

func validatePostgreSQLStartedReplayCaller(request postgresqlStartRequest, runID, unitID string) error {
	if runID != "" && request.RunID != heteroci.TopologyRunID(runID) {
		return fmt.Errorf("PostgreSQL started replay run identity drifted")
	}
	if unitID != "" && request.UnitID != unitID {
		return fmt.Errorf("PostgreSQL started replay unit identity drifted")
	}
	return nil
}

func validatePostgreSQLStartedReplayResult(request postgresqlStartRequest, lease heteroci.RoutingLease) error {
	if lease.RunID != string(request.RunID) || lease.UnitID != request.UnitID {
		return fmt.Errorf("PostgreSQL started replay result identity drifted")
	}
	return nil
}

func validatePostgreSQLStartedReplayIdentity(started heteroci.StartedRoutingLease, fence heteroci.OwnerFence, revision heteroci.RoutingOwnerRevision) error {
	if started.Fence.Scope != fence.Scope || started.Fence.OwnerID != fence.OwnerID || started.Fence.OwnerEpoch != fence.OwnerEpoch {
		return fmt.Errorf("PostgreSQL started replay owner identity drifted")
	}
	if started.Fence.Revision != revision {
		return fmt.Errorf("PostgreSQL started replay fence revision drifted")
	}
	return nil
}

func validatePostgreSQLStartedReplayUnit(lease heteroci.RoutingLease, runID, unitID string) error {
	if runID == "" {
		return nil
	}
	if lease.RunID != runID || lease.UnitID != unitID {
		return fmt.Errorf("PostgreSQL started replay identity drifted")
	}
	return nil
}

func validatePostgreSQLStartedReplayPayload(started heteroci.StartedRoutingLease) error {
	if err := started.Fence.Validate(); err != nil {
		return fmt.Errorf("validate PostgreSQL started replay fence: %w", err)
	}
	if err := validatePostgreSQLStartedReplayLease(started.Lease); err != nil {
		return err
	}
	if err := validatePostgreSQLStartedReplayOccupancy(started); err != nil {
		return err
	}
	return nil
}

func validatePostgreSQLStartedReplayLease(lease heteroci.RoutingLease) error {
	if lease.State != heteroci.LeaseStateStarted || lease.FinishedAtUnixMilli != 0 {
		return fmt.Errorf("PostgreSQL started replay lease state is invalid")
	}
	if lease.StartedAtUnixMilli < lease.ReservedAtUnixMilli || !validOwnerDigest(lease.DecisionDigest) {
		return fmt.Errorf("PostgreSQL started replay lease timestamp is invalid")
	}
	return nil
}

func validatePostgreSQLStartedReplayOccupancy(started heteroci.StartedRoutingLease) error {
	if err := started.Occupancy.Validate(); err != nil {
		return fmt.Errorf("validate PostgreSQL started replay occupancy: %w", err)
	}
	if started.Occupancy.OwnerEpoch != started.Fence.OwnerEpoch || started.Occupancy.RunID != started.Lease.RunID || started.Occupancy.UnitID != started.Lease.UnitID || started.Occupancy.DecisionDigest != started.Lease.DecisionDigest {
		return fmt.Errorf("PostgreSQL started replay occupancy drifted")
	}
	return nil
}

func replayPostgreSQLTransition(record postgresqlOperationRecord, fence heteroci.OwnerFence, requestBytes []byte, target heteroci.RoutingLeaseState, nowUnixMilli int64) (DurableLeaseTransition, error) {
	if err := validatePostgreSQLLifecycleReplayRecord(record, fence, record.OperationID, requestBytes); err != nil {
		return DurableLeaseTransition{}, err
	}
	if err := validatePostgreSQLTerminalReplayKind(record.Kind, target); err != nil {
		return DurableLeaseTransition{}, ErrOperationConflict
	}
	var transition DurableLeaseTransition
	if err := json.Unmarshal(record.ResultBytes, &transition); err != nil {
		return DurableLeaseTransition{}, fmt.Errorf("decode PostgreSQL terminal replay: %w", err)
	}
	if err := validatePostgreSQLTerminalReplayRequest(record.RequestBytes, fence, target, transition.Lease); err != nil {
		return DurableLeaseTransition{}, err
	}
	if err := validatePostgreSQLTerminalReplayIdentity(transition, fence, record.Revision, target); err != nil {
		return DurableLeaseTransition{}, err
	}
	if nowUnixMilli > 0 && transition.Lease.FinishedAtUnixMilli > nowUnixMilli {
		return DurableLeaseTransition{}, fmt.Errorf("PostgreSQL terminal replay timestamp drifted")
	}
	if err := transition.Fence.Validate(); err != nil {
		return DurableLeaseTransition{}, fmt.Errorf("validate PostgreSQL terminal replay fence: %w", err)
	}
	if err := validatePostgreSQLTerminalReplayLease(transition.Lease); err != nil {
		return DurableLeaseTransition{}, err
	}
	return transition, nil
}

func validatePostgreSQLTerminalReplayRequest(data []byte, fence heteroci.OwnerFence, target heteroci.RoutingLeaseState, lease heteroci.RoutingLease) error {
	var request postgresqlTerminalRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return fmt.Errorf("decode PostgreSQL terminal replay request: %w", err)
	}
	if request.Fence != fence {
		return fmt.Errorf("PostgreSQL terminal replay request fence drifted")
	}
	if request.RunID == "" || request.UnitID == "" || request.State != target {
		return fmt.Errorf("PostgreSQL terminal replay request identity drifted")
	}
	if lease.RunID != string(request.RunID) || lease.UnitID != request.UnitID {
		return fmt.Errorf("PostgreSQL terminal replay result identity drifted")
	}
	return nil
}

// validatePostgreSQLTerminalReplayIdentity 校验 terminal replay 的 owner fence 与目标状态。
func validatePostgreSQLTerminalReplayIdentity(transition DurableLeaseTransition, fence heteroci.OwnerFence, revision heteroci.RoutingOwnerRevision, target heteroci.RoutingLeaseState) error {
	if transition.Fence.Scope != fence.Scope || transition.Fence.OwnerID != fence.OwnerID || transition.Fence.OwnerEpoch != fence.OwnerEpoch {
		return fmt.Errorf("PostgreSQL terminal replay owner identity drifted")
	}
	if transition.Fence.Revision != revision || transition.Lease.State != target {
		return fmt.Errorf("PostgreSQL terminal replay revision or state drifted")
	}
	return nil
}

// validatePostgreSQLTerminalReplayLease 校验 terminal lease 的状态、时间和 digest。
func validatePostgreSQLTerminalReplayLease(lease heteroci.RoutingLease) error {
	if !validPostgreSQLTerminalState(lease.State) || lease.ReservedAtUnixMilli <= 0 {
		return fmt.Errorf("PostgreSQL terminal replay lease state is invalid")
	}
	if lease.FinishedAtUnixMilli < lease.ReservedAtUnixMilli || !validOwnerDigest(lease.DecisionDigest) {
		return fmt.Errorf("PostgreSQL terminal replay lease timestamp is invalid")
	}
	return nil
}
