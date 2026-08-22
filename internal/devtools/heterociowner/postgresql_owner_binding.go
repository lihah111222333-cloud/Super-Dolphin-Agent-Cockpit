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
		return finishPostgreSQLBindingReplay(tx, record, mutation, requestBytes)
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
func finishPostgreSQLBindingReplay(tx *sql.Tx, record postgresqlOperationRecord, mutation heteroci.ExecutorBindingMutation, requestBytes []byte) (heteroci.ExecutorBindingMutationResult, error) {
	result, err := replayPostgreSQLBinding(record, mutation, requestBytes)
	if err != nil {
		return heteroci.ExecutorBindingMutationResult{}, rollbackPostgreSQLOwnerTx(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return heteroci.ExecutorBindingMutationResult{}, errors.Join(ErrPostgreSQLOwnerCommitUncertain, err)
	}
	return result, nil
}
func (owner *PostgreSQLOwner) commitPostgreSQLBindingMutation(ctx context.Context, tx *sql.Tx, current heteroci.OwnerFence, mutation heteroci.ExecutorBindingMutation, requestBytes []byte, nowUnixMilli int64) (heteroci.ExecutorBindingMutationResult, error) {
	revision, err := nextOwnerRevision(current)
	if err != nil {
		return heteroci.ExecutorBindingMutationResult{}, rollbackPostgreSQLOwnerTx(tx, err)
	}
	committedFence, err := heteroci.NewOwnerFence(current.Scope, current.OwnerID, current.OwnerEpoch, revision)
	if err != nil {
		return heteroci.ExecutorBindingMutationResult{}, rollbackPostgreSQLOwnerTx(tx, err)
	}
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
	if _, err := tx.ExecContext(ctx, "INSERT INTO heteroci_owner.operation_journal (scope, operation_id, owner_id, owner_epoch, revision, kind, request_bytes, request_digest, result_bytes, result_digest, previous_journal_digest, journal_digest, projection_digest, committed_at_unix_milli) VALUES (, , , , , , , , , , , , , )", current.Scope, mutation.Plan.ExternalOperationID, current.OwnerID, current.OwnerEpoch, revision, operation.Kind, requestBytes, requestDigest, resultBytes, resultDigest, previousJournal, journalDigest, resultDigest, nowUnixMilli); err != nil {
		return err
	}
	runUpdated, err := tx.ExecContext(ctx, "UPDATE heteroci_owner.routing_runs SET last_revision =  WHERE scope =  AND owner_epoch =  AND run_id =  AND last_revision = ", revision, current.Scope, current.OwnerEpoch, mutation.Plan.DurableRunID, current.Revision)
	if err != nil {
		return err
	}
	if err := requireOnePostgreSQLRow(runUpdated, "advance PostgreSQL executor binding run revision"); err != nil {
		return err
	}
	updated, err := tx.ExecContext(ctx, "UPDATE heteroci_owner.owner_heads SET revision = , journal_digest = , projection_digest = , updated_at_unix_milli =  WHERE scope =  AND owner_id =  AND owner_epoch =  AND revision = ", revision, journalDigest, resultDigest, nowUnixMilli, current.Scope, current.OwnerID, current.OwnerEpoch, current.Revision)
	if err != nil {
		return err
	}
	return requireOnePostgreSQLRow(updated, "advance PostgreSQL executor binding owner head")
}
func replayPostgreSQLBinding(record postgresqlOperationRecord, mutation heteroci.ExecutorBindingMutation, requestBytes []byte) (heteroci.ExecutorBindingMutationResult, error) {
	if record.Kind != postgresqlExecutorBindingOperationKind || !bytes.Equal(record.RequestBytes, requestBytes) || record.OwnerID != mutation.Fence.OwnerID || record.OwnerEpoch != mutation.Fence.OwnerEpoch {
		return heteroci.ExecutorBindingMutationResult{}, ErrOperationConflict
	}
	var persisted postgresqlBindingOperationResult
	if err := json.Unmarshal(record.ResultBytes, &persisted); err != nil {
		return heteroci.ExecutorBindingMutationResult{}, fmt.Errorf("decode PostgreSQL executor binding replay: %w", err)
	}
	if !reflect.DeepEqual(persisted.Plan, mutation.Plan) || persisted.Fence.OwnerEpoch != mutation.Fence.OwnerEpoch {
		return heteroci.ExecutorBindingMutationResult{}, ErrOperationConflict
	}
	return heteroci.ExecutorBindingMutationResult{Fence: persisted.Fence, Plan: persisted.Plan}, nil
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
	result, err := replayPostgreSQLBinding(record, mutation, requestBytes)
	if err != nil {
		return heteroci.ExecutorBindingMutationResult{}, false, rollbackPostgreSQLOwnerTx(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return heteroci.ExecutorBindingMutationResult{}, false, err
	}
	return result, true, nil
}
