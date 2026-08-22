package heterociowner

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const postgresqlExecutorBindingOperationKind = "executor_bind/v1"

type postgresqlBindingEnvelope struct {
	Mutation heteroci.ExecutorBindingMutation
}
type postgresqlBindingOperationResult struct {
	Fence heteroci.OwnerFence
	Plan  heteroci.ExecutorBindingPlan
}

// BindExecutors 在 scope advisory lock + SERIALIZABLE owner transaction 内持久化一次 binding。
func (owner *PostgreSQLOwner) BindExecutors(ctx context.Context, mutation heteroci.ExecutorBindingMutation) (heteroci.ExecutorBindingMutationResult, error) {
	if err := validatePostgreSQLBindingArguments(owner, mutation); err != nil {
		return heteroci.ExecutorBindingMutationResult{}, err
	}
	requestBytes, err := json.Marshal(postgresqlBindingEnvelope{Mutation: mutation})
	if err != nil {
		return heteroci.ExecutorBindingMutationResult{}, fmt.Errorf("encode PostgreSQL executor binding request: %w", err)
	}
	nowUnixMilli, err := owner.nowUnixMilli()
	if err != nil {
		return heteroci.ExecutorBindingMutationResult{}, err
	}
	result, err := owner.bindExecutorsOnce(ctx, mutation, requestBytes, nowUnixMilli)
	if err != nil {
		return heteroci.ExecutorBindingMutationResult{}, wrapPostgreSQLOwnerMutationError("bind executors", err)
	}
	return result, nil
}
func (owner *PostgreSQLOwner) bindExecutorsOnce(ctx context.Context, mutation heteroci.ExecutorBindingMutation, requestBytes []byte, nowUnixMilli int64) (heteroci.ExecutorBindingMutationResult, error) {
	tx, err := owner.beginPostgreSQLOwnerTx(ctx)
	if err != nil {
		return heteroci.ExecutorBindingMutationResult{}, err
	}
	record, found, err := readPostgreSQLOperation(ctx, tx, mutation.Fence.Scope, mutation.Plan.ExternalOperationID)
	if err != nil {
		return heteroci.ExecutorBindingMutationResult{}, rollbackPostgreSQLOwnerTx(tx, err)
	}
	if found {
		return finishPostgreSQLBindingReplay(ctx, tx, record, mutation, requestBytes)
	}
	current, found, err := readPostgreSQLOwnerHead(ctx, tx, mutation.Fence.Scope, true)
	if err != nil || !found {
		if err == nil {
			err = fmt.Errorf("heteroci PostgreSQL owner has not been acquired")
		}
		return heteroci.ExecutorBindingMutationResult{}, rollbackPostgreSQLOwnerTx(tx, err)
	}
	if current != mutation.Fence {
		return heteroci.ExecutorBindingMutationResult{}, rollbackPostgreSQLOwnerTx(tx, fmt.Errorf("heteroci PostgreSQL binding fence is stale"))
	}
	if err := validatePostgreSQLBindingAgainstPlan(ctx, tx, mutation); err != nil {
		return heteroci.ExecutorBindingMutationResult{}, rollbackPostgreSQLOwnerTx(tx, err)
	}
	return owner.commitPostgreSQLBindingMutation(ctx, tx, current, mutation, requestBytes, nowUnixMilli)
}
func finishPostgreSQLBindingReplay(ctx context.Context, tx *sql.Tx, record postgresqlOperationRecord, mutation heteroci.ExecutorBindingMutation, requestBytes []byte) (heteroci.ExecutorBindingMutationResult, error) {
	result, err := replayPostgreSQLBinding(ctx, tx, record, mutation, requestBytes)
	if err != nil {
		return heteroci.ExecutorBindingMutationResult{}, rollbackPostgreSQLOwnerTx(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return heteroci.ExecutorBindingMutationResult{}, errors.Join(ErrPostgreSQLOwnerCommitUncertain, err)
	}
	return result, nil
}
func (owner *PostgreSQLOwner) commitPostgreSQLBindingMutation(ctx context.Context, tx *sql.Tx, current heteroci.OwnerFence, mutation heteroci.ExecutorBindingMutation, requestBytes []byte, nowUnixMilli int64) (heteroci.ExecutorBindingMutationResult, error) {
	committedFence, err := expectedPostgreSQLBindingFence(current)
	if err != nil {
		return heteroci.ExecutorBindingMutationResult{}, rollbackPostgreSQLOwnerTx(tx, err)
	}
	revision := committedFence.Revision
	result := heteroci.ExecutorBindingMutationResult{Fence: committedFence, Plan: mutation.Plan}
	resultBytes, err := json.Marshal(postgresqlBindingOperationResult{Fence: result.Fence, Plan: result.Plan})
	if err != nil {
		return heteroci.ExecutorBindingMutationResult{}, rollbackPostgreSQLOwnerTx(tx, err)
	}
	if err := persistPostgreSQLBindingRows(ctx, tx, current, mutation, requestBytes, resultBytes, revision, nowUnixMilli); err != nil {
		return heteroci.ExecutorBindingMutationResult{}, rollbackPostgreSQLOwnerTx(tx, err)
	}
	if err := tx.Commit(); err != nil {
		replayed, found, replayErr := owner.reconcilePostgreSQLBinding(ctx, mutation, requestBytes)
		if replayErr != nil {
			return heteroci.ExecutorBindingMutationResult{}, errors.Join(ErrPostgreSQLOwnerCommitUncertain, err, replayErr)
		}
		if found {
			return replayed, nil
		}
		return heteroci.ExecutorBindingMutationResult{}, errors.Join(ErrPostgreSQLOwnerCommitUncertain, err)
	}
	return result, nil
}
func persistPostgreSQLBindingRows(ctx context.Context, tx *sql.Tx, current heteroci.OwnerFence, mutation heteroci.ExecutorBindingMutation, requestBytes, resultBytes []byte, revision heteroci.RoutingOwnerRevision, nowUnixMilli int64) error {
	previousJournal, err := readPostgreSQLJournalHead(ctx, tx, current.Scope)
	if err != nil {
		return err
	}
	requestDigest, resultDigest := digestBytes(requestBytes), digestBytes(resultBytes)
	operation := OperationRequest{Fence: current, OperationID: mutation.Plan.ExternalOperationID, Kind: postgresqlExecutorBindingOperationKind, RequestBytes: requestBytes, ResultBytes: resultBytes, ProjectionDigest: resultDigest, CommittedAtUnixMilli: nowUnixMilli}
	journalDigest := journalChainDigest(current, operation, previousJournal, requestDigest, resultDigest, revision)
	if _, err := tx.ExecContext(ctx, "INSERT INTO heteroci_owner.operation_journal (scope, operation_id, owner_id, owner_epoch, revision, kind, request_bytes, request_digest, result_bytes, result_digest, previous_journal_digest, journal_digest, projection_digest, committed_at_unix_milli) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)", current.Scope, mutation.Plan.ExternalOperationID, current.OwnerID, current.OwnerEpoch, revision, operation.Kind, requestBytes, requestDigest, resultBytes, resultDigest, previousJournal, journalDigest, resultDigest, nowUnixMilli); err != nil {
		return err
	}
	runUpdated, err := tx.ExecContext(ctx, "UPDATE heteroci_owner.routing_runs SET last_revision = $1 WHERE scope = $2 AND owner_epoch = $3 AND run_id = $4 AND last_revision = $5", revision, current.Scope, current.OwnerEpoch, mutation.Plan.DurableRunID, current.Revision)
	if err != nil {
		return err
	}
	if err := requireOnePostgreSQLRow(runUpdated, "advance PostgreSQL executor binding run revision"); err != nil {
		return err
	}
	updated, err := tx.ExecContext(ctx, "UPDATE heteroci_owner.owner_heads SET revision = $1, journal_digest = $2, projection_digest = $3, updated_at_unix_milli = $4 WHERE scope = $5 AND owner_id = $6 AND owner_epoch = $7 AND revision = $8", revision, journalDigest, resultDigest, nowUnixMilli, current.Scope, current.OwnerID, current.OwnerEpoch, current.Revision)
	if err != nil {
		return err
	}
	return requireOnePostgreSQLRow(updated, "advance PostgreSQL executor binding owner head")
}
func replayPostgreSQLBinding(ctx context.Context, tx *sql.Tx, record postgresqlOperationRecord, mutation heteroci.ExecutorBindingMutation, requestBytes []byte) (heteroci.ExecutorBindingMutationResult, error) {
	if record.Kind != postgresqlExecutorBindingOperationKind || !bytes.Equal(record.RequestBytes, requestBytes) || record.OwnerID != mutation.Fence.OwnerID || record.OwnerEpoch != mutation.Fence.OwnerEpoch {
		return heteroci.ExecutorBindingMutationResult{}, ErrOperationConflict
	}
	var persisted postgresqlBindingOperationResult
	if err := json.Unmarshal(record.ResultBytes, &persisted); err != nil {
		return heteroci.ExecutorBindingMutationResult{}, fmt.Errorf("decode PostgreSQL executor binding replay: %w", err)
	}
	expectedFence, err := expectedPostgreSQLBindingFence(mutation.Fence)
	if err != nil {
		return heteroci.ExecutorBindingMutationResult{}, err
	}
	if err := validatePostgreSQLBindingJournal(ctx, tx, record, mutation, requestBytes, persisted, expectedFence); err != nil {
		return heteroci.ExecutorBindingMutationResult{}, err
	}
	if !reflect.DeepEqual(persisted.Plan, mutation.Plan) {
		return heteroci.ExecutorBindingMutationResult{}, ErrOperationConflict
	}
	return heteroci.ExecutorBindingMutationResult{Fence: persisted.Fence, Plan: persisted.Plan}, nil
}
func expectedPostgreSQLBindingFence(input heteroci.OwnerFence) (heteroci.OwnerFence, error) {
	revision, err := nextOwnerRevision(input)
	if err != nil {
		return heteroci.OwnerFence{}, err
	}
	return heteroci.NewOwnerFence(input.Scope, input.OwnerID, input.OwnerEpoch, revision)
}
func validatePostgreSQLBindingJournal(ctx context.Context, tx *sql.Tx, record postgresqlOperationRecord, mutation heteroci.ExecutorBindingMutation, requestBytes []byte, persisted postgresqlBindingOperationResult, expectedFence heteroci.OwnerFence) error {
	if err := validatePostgreSQLBindingRecordIdentity(record, mutation, persisted, expectedFence); err != nil {
		return err
	}
	if err := validatePostgreSQLBindingRecordDigests(record, requestBytes); err != nil {
		return err
	}
	if err := validatePostgreSQLBindingJournalChain(record, mutation.Fence); err != nil {
		return err
	}
	return validatePostgreSQLBindingHead(ctx, tx, record, expectedFence)
}
func validatePostgreSQLBindingRecordIdentity(record postgresqlOperationRecord, mutation heteroci.ExecutorBindingMutation, persisted postgresqlBindingOperationResult, expectedFence heteroci.OwnerFence) error {
	if record.Kind != postgresqlExecutorBindingOperationKind || record.Scope != expectedFence.Scope || record.OperationID != mutation.Plan.ExternalOperationID || record.OwnerID != expectedFence.OwnerID || record.OwnerEpoch != expectedFence.OwnerEpoch || record.Revision != expectedFence.Revision || persisted.Fence != expectedFence {
		return ErrOperationConflict
	}
	return nil
}
func validatePostgreSQLBindingRecordDigests(record postgresqlOperationRecord, requestBytes []byte) error {
	if record.RequestDigest != digestBytes(requestBytes) || record.ResultDigest != digestBytes(record.ResultBytes) {
		return ErrOperationConflict
	}
	return nil
}
func validatePostgreSQLBindingJournalChain(record postgresqlOperationRecord, fence heteroci.OwnerFence) error {
	operation := OperationRequest{Fence: fence, OperationID: record.OperationID, Kind: record.Kind, RequestBytes: record.RequestBytes, ResultBytes: record.ResultBytes, ProjectionDigest: record.ProjectionDigest, CommittedAtUnixMilli: record.CommittedAtUnixMilli}
	wantJournal := journalChainDigest(fence, operation, record.PreviousJournal, record.RequestDigest, record.ResultDigest, record.Revision)
	if wantJournal != record.JournalDigest {
		return ErrOperationConflict
	}
	return nil
}
func validatePostgreSQLBindingHead(ctx context.Context, tx *sql.Tx, record postgresqlOperationRecord, expectedFence heteroci.OwnerFence) error {
	head, headJournal, headProjection, found, err := readPostgreSQLBindingHead(ctx, tx, expectedFence.Scope)
	if err != nil || !found || head != expectedFence || headJournal != record.JournalDigest || headProjection != record.ProjectionDigest {
		return ErrOperationConflict
	}
	return nil
}
func readPostgreSQLBindingHead(ctx context.Context, queryer postgresqlOwnerQueryRower, scope heteroci.RoutingOwnerScope) (heteroci.OwnerFence, string, string, bool, error) {
	head, found, err := readPostgreSQLOwnerHead(ctx, queryer, scope, false)
	if err != nil || !found {
		return heteroci.OwnerFence{}, "", "", found, err
	}
	var journalDigest, projectionDigest string
	if err := queryer.QueryRowContext(ctx, "SELECT journal_digest, projection_digest FROM heteroci_owner.owner_heads WHERE scope = $1", scope).Scan(&journalDigest, &projectionDigest); err != nil {
		return heteroci.OwnerFence{}, "", "", false, err
	}
	return head, journalDigest, projectionDigest, true, nil
}
func (owner *PostgreSQLOwner) reconcilePostgreSQLBinding(ctx context.Context, mutation heteroci.ExecutorBindingMutation, requestBytes []byte) (heteroci.ExecutorBindingMutationResult, bool, error) {
	tx, err := owner.beginPostgreSQLOwnerTx(ctx)
	if err != nil {
		return heteroci.ExecutorBindingMutationResult{}, false, err
	}
	record, found, err := readPostgreSQLOperation(ctx, tx, mutation.Fence.Scope, mutation.Plan.ExternalOperationID)
	if err != nil {
		return heteroci.ExecutorBindingMutationResult{}, false, rollbackPostgreSQLOwnerTx(tx, err)
	}
	if !found {
		if err := tx.Commit(); err != nil {
			return heteroci.ExecutorBindingMutationResult{}, false, err
		}
		return heteroci.ExecutorBindingMutationResult{}, false, nil
	}
	result, err := replayPostgreSQLBinding(ctx, tx, record, mutation, requestBytes)
	if err != nil {
		return heteroci.ExecutorBindingMutationResult{}, false, rollbackPostgreSQLOwnerTx(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return heteroci.ExecutorBindingMutationResult{}, false, err
	}
	return result, true, nil
}
