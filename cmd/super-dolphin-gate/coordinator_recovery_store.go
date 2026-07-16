package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

var coordinatorRecoveryColumns = map[string]string{
	"deadline_at":               "TEXT",
	"active_gate_id":            "TEXT NOT NULL DEFAULT ''",
	"container_phase":           "TEXT NOT NULL DEFAULT ''",
	"container_id":              "TEXT NOT NULL DEFAULT ''",
	"container_labels_json":     "BLOB",
	"container_image_reference": "TEXT NOT NULL DEFAULT ''",
	"container_config_digest":   "TEXT NOT NULL DEFAULT ''",
	"source_snapshot_dir":       "TEXT NOT NULL DEFAULT ''",
	"removal_proof_digest":      "TEXT NOT NULL DEFAULT ''",
}

// ensureCoordinatorRecoverySchema 为旧数据库逐列补齐恢复字段，任何迁移错误都阻断 owner。
func ensureCoordinatorRecoverySchema(ctx context.Context, db *sql.DB) (retErr error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(coordinator_jobs)")
	if err != nil {
		return fmt.Errorf("inspect coordinator recovery schema: %w", err)
	}
	columns := make(map[string]struct{})
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			retErr = errors.Join(retErr, closeErr)
		}
	}()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("scan coordinator recovery schema: %w", err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate coordinator recovery schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close coordinator recovery schema rows: %w", err)
	}
	for name, definition := range coordinatorRecoveryColumns {
		if _, exists := columns[name]; exists {
			continue
		}
		statement := "ALTER TABLE coordinator_jobs ADD COLUMN " + name + " " + definition
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("add coordinator recovery column %q: %w", name, err)
		}
	}
	return nil
}

// jobs 按持久 enqueue 顺序读取 owner 启动时需要判定的全部记录。
func (store *coordinatorStore) jobs(ctx context.Context) (records []coordinatorJobRecord, retErr error) {
	rows, err := store.db.QueryContext(ctx, `SELECT invocation_id, job_id, enqueue_sequence, repository_root,
plan_json, profile, job_source_tree_sha, image_provenance_source_tree_sha, state, submitted_at,
started_at, deadline_at, completed_at, active_gate_id, container_phase, container_id,
container_labels_json, container_image_reference, container_config_digest, source_snapshot_dir,
removal_proof_digest, gate_results_json, error_text FROM coordinator_jobs ORDER BY enqueue_sequence`)
	if err != nil {
		return nil, fmt.Errorf("list coordinator jobs: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			retErr = errors.Join(retErr, closeErr)
		}
	}()
	for rows.Next() {
		record, err := scanCoordinatorJob(rows, "recovery list")
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate coordinator jobs: %w", err)
	}
	return records, nil
}

func (store *coordinatorStore) replaceRecoveredPassed(ctx context.Context, jobID, message string) error {
	result, err := store.db.ExecContext(ctx, `UPDATE coordinator_jobs SET state = ?, gate_results_json = NULL,
error_text = ? WHERE job_id = ? AND state = ?`, jobStateInfraFailed, message, jobID, jobStatePassed)
	if err != nil {
		return fmt.Errorf("invalidate recovered passed job: %w", err)
	}
	return requireOneCoordinatorRow(result, "invalidate recovered passed job")
}

// recordContainerLifecycle 在同一事务内校验并推进单个容器生命周期。
func (store *coordinatorStore) recordContainerLifecycle(
	ctx context.Context,
	jobID string,
	gateID gatecontract.GateID,
	labels map[string]string,
	event localci.FreshContainerLifecycleEvent,
) (retErr error) {
	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		return fmt.Errorf("encode coordinator container labels: %w", err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin coordinator lifecycle transition: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			retErr = errors.Join(retErr, rollbackErr)
		}
	}()
	record, err := scanCoordinatorJob(tx.QueryRowContext(ctx, `SELECT invocation_id, job_id, enqueue_sequence, repository_root,
plan_json, profile, job_source_tree_sha, image_provenance_source_tree_sha, state, submitted_at,
started_at, deadline_at, completed_at, active_gate_id, container_phase, container_id,
container_labels_json, container_image_reference, container_config_digest, source_snapshot_dir,
removal_proof_digest, gate_results_json, error_text FROM coordinator_jobs WHERE job_id = ?`, jobID), jobID)
	if err != nil {
		return err
	}
	if err := validateLifecycleTransition(record, gateID, labels, event); err != nil {
		return err
	}
	values := coordinatorLifecycleValues(record, event)
	result, err := tx.ExecContext(ctx, `UPDATE coordinator_jobs SET active_gate_id = ?, container_phase = ?,
container_id = ?, container_labels_json = ?, container_image_reference = ?, container_config_digest = ?,
source_snapshot_dir = ?, started_at = ?, deadline_at = ?, removal_proof_digest = ?
WHERE job_id = ? AND state = ?`, gateID, event.Phase, values.containerID, labelsJSON,
		event.ImageReference, event.ConfigDigest, event.SourceSnapshotDir, values.startedAt, values.deadline,
		values.removalProof, jobID, jobStateStarted)
	if err != nil {
		return fmt.Errorf("persist coordinator container lifecycle: %w", err)
	}
	if err := requireOneCoordinatorRow(result, "record coordinator container lifecycle"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit coordinator lifecycle transition: %w", err)
	}
	return nil
}

type coordinatorLifecycleUpdate struct {
	containerID  string
	startedAt    any
	deadline     any
	removalProof string
}

func coordinatorLifecycleValues(
	record coordinatorJobRecord,
	event localci.FreshContainerLifecycleEvent,
) coordinatorLifecycleUpdate {
	values := coordinatorLifecycleUpdate{
		containerID: event.ContainerID, startedAt: nullableCoordinatorTime(record.StartedAt),
		deadline: nullableCoordinatorTime(record.Deadline), removalProof: record.RemovalProofDigest,
	}
	if event.Phase == localci.FreshContainerPhaseStarting && record.StartedAt == nil {
		values.startedAt, values.deadline = event.StartedAt.Format(timeFormat), event.Deadline.Format(timeFormat)
	}
	if event.Phase == localci.FreshContainerPhasePrepared {
		values.containerID, values.removalProof = "", ""
	}
	if event.Phase == localci.FreshContainerPhaseRemoved {
		values.removalProof = event.RemovalProofDigest
	}
	return values
}

const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"

func nullableCoordinatorTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(timeFormat)
}

// validateLifecycleTransition 拒绝身份漂移、时钟重置和越序生命周期事件。
func validateLifecycleTransition(
	record coordinatorJobRecord,
	gateID gatecontract.GateID,
	labels map[string]string,
	event localci.FreshContainerLifecycleEvent,
) error {
	if err := validateLifecycleBase(record, gateID, labels, event); err != nil {
		return err
	}
	if err := validateLifecyclePhaseOrder(record.ContainerPhase, event.Phase); err != nil {
		return err
	}
	if err := validateLifecycleContainerID(record, event); err != nil {
		return err
	}
	if err := validateLifecycleClock(record, event); err != nil {
		return err
	}
	if event.Phase == localci.FreshContainerPhaseRemoved && event.RemovalProofDigest == "" {
		return fmt.Errorf("%w: lifecycle removal proof is required", errCoordinatorState)
	}
	return nil
}

// validateLifecycleBase 验证 lifecycle 事件携带的 job、gate 与不可变容器身份。
func validateLifecycleBase(
	record coordinatorJobRecord,
	gateID gatecontract.GateID,
	labels map[string]string,
	event localci.FreshContainerLifecycleEvent,
) error {
	if record.State != jobStateStarted || gateID == "" || len(labels) == 0 {
		return fmt.Errorf("%w: lifecycle job, gate, or labels are incomplete", errCoordinatorState)
	}
	if event.ImageReference == "" || event.ConfigDigest == "" || event.SourceSnapshotDir == "" {
		return fmt.Errorf("%w: lifecycle immutable container identity is incomplete", errCoordinatorState)
	}
	active := record.ContainerPhase != "" && record.ContainerPhase != localci.FreshContainerPhaseRemoved
	if active && record.ActiveGateID != gateID {
		return fmt.Errorf("%w: lifecycle gate identity drifted", errCoordinatorState)
	}
	return nil
}

// validateLifecycleContainerID 保证 create 后的事件始终引用同一个容器。
func validateLifecycleContainerID(
	record coordinatorJobRecord,
	event localci.FreshContainerLifecycleEvent,
) error {
	if event.Phase == localci.FreshContainerPhasePrepared {
		return nil
	}
	if event.ContainerID == "" && record.ContainerID == "" {
		return fmt.Errorf("%w: lifecycle container ID is missing", errCoordinatorState)
	}
	if record.ContainerID != "" && event.ContainerID != record.ContainerID {
		return fmt.Errorf("%w: lifecycle container ID drifted", errCoordinatorState)
	}
	return nil
}

// validateLifecycleClock 固定首次 start 的 started_at 与 deadline，拒绝重启重算。
func validateLifecycleClock(
	record coordinatorJobRecord,
	event localci.FreshContainerLifecycleEvent,
) error {
	if event.Phase != localci.FreshContainerPhaseStarting {
		return nil
	}
	if event.StartedAt.IsZero() || event.Deadline.IsZero() {
		return fmt.Errorf("%w: lifecycle start timestamps are incomplete", errCoordinatorState)
	}
	if record.StartedAt == nil && !event.Deadline.Equal(event.StartedAt.Add(coordinatorTimeout(record.Profile))) {
		return fmt.Errorf("%w: lifecycle deadline does not match first container start", errCoordinatorState)
	}
	if record.StartedAt != nil && (record.Deadline == nil || !record.Deadline.Equal(event.Deadline)) {
		return fmt.Errorf("%w: lifecycle attempted to reset the original deadline", errCoordinatorState)
	}
	return nil
}

func validateLifecyclePhaseOrder(current, next localci.FreshContainerLifecyclePhase) error {
	allowed := map[localci.FreshContainerLifecyclePhase][]localci.FreshContainerLifecyclePhase{
		"":                                  {localci.FreshContainerPhasePrepared},
		localci.FreshContainerPhasePrepared: {localci.FreshContainerPhasePrepared, localci.FreshContainerPhaseCreated},
		localci.FreshContainerPhaseCreated:  {localci.FreshContainerPhaseCreated, localci.FreshContainerPhaseStarting, localci.FreshContainerPhaseRemoved},
		localci.FreshContainerPhaseStarting: {localci.FreshContainerPhaseStarting, localci.FreshContainerPhaseStarted, localci.FreshContainerPhaseRemoved},
		localci.FreshContainerPhaseStarted:  {localci.FreshContainerPhaseStarted, localci.FreshContainerPhaseExited, localci.FreshContainerPhaseRemoved},
		localci.FreshContainerPhaseExited:   {localci.FreshContainerPhaseExited, localci.FreshContainerPhaseRemoved},
		localci.FreshContainerPhaseRemoved:  {localci.FreshContainerPhaseRemoved, localci.FreshContainerPhasePrepared},
	}
	if slices.Contains(allowed[current], next) {
		return nil
	}
	return fmt.Errorf("%w: container lifecycle transition %q -> %q is invalid", errCoordinatorState, current, next)
}
