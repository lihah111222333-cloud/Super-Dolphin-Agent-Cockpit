package waitoperation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CheckSchema 验证 marker 表及其不可变生命周期对象可供当前 owner 使用。
func (m *CommitMarkerStore) CheckSchema(ctx context.Context) error {
	if m == nil || m.db == nil || m.clock == nil {
		return ErrStoreUninitialized
	}
	if ctx == nil {
		return errors.Join(ErrStoreUninitialized, errors.New("commit marker context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var count int64
	if err := m.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM orchestration_wait_operation_commit_markers").Scan(&count); err != nil {
		return errors.Join(ErrSchemaUnavailable, err)
	}
	return checkCommitMarkerObjects(ctx, m.db)
}

func checkCommitMarkerObjects(ctx context.Context, db *sql.DB) error {
	const query = `SELECT COUNT(*) FROM sqlite_master
WHERE (type = 'table' AND name = 'orchestration_wait_operation_commit_markers')
   OR (type = 'index' AND name = 'idx_orchestration_wait_operation_commit_markers_unresolved')
   OR (type = 'trigger' AND name IN (
      'trg_orchestration_wait_operation_commit_marker_pending_insert',
      'trg_orchestration_wait_operation_commit_marker_immutable',
      'trg_orchestration_wait_operation_commit_marker_transition',
      'trg_orchestration_wait_operation_commit_marker_no_delete'))`
	var count int64
	if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return errors.Join(ErrSchemaUnavailable, err)
	}
	if count != 6 {
		return fmt.Errorf("%w: commit marker schema objects are incomplete (%d/6)", ErrSchemaUnavailable, count)
	}
	return nil
}

const (
	selectCommitMarkerSQL = `SELECT marker_key, operation_key, operation_digest, target_key, mutation_kind,
request_digest, pre_state_digest, post_state_digest, state, created_at, resolved_at, schema_version
FROM orchestration_wait_operation_commit_markers WHERE marker_key = ?`
	selectPendingCommitMarkerSQL = `SELECT marker_key, operation_key, operation_digest, target_key, mutation_kind,
request_digest, pre_state_digest, post_state_digest, state, created_at, resolved_at, schema_version
FROM orchestration_wait_operation_commit_markers
WHERE operation_key = ? AND operation_digest = ? AND state = 'pending'
ORDER BY marker_key LIMIT 1`
	insertCommitMarkerSQL = `INSERT INTO orchestration_wait_operation_commit_markers
(marker_key, operation_key, operation_digest, target_key, mutation_kind, request_digest,
 pre_state_digest, post_state_digest, state, created_at, resolved_at, schema_version)
VALUES (?, ?, ?, ?, ?, ?, ?, '', 'pending', ?, '', 1)`
	setCommitMarkerPostSQL = `UPDATE orchestration_wait_operation_commit_markers
SET post_state_digest = ?
WHERE marker_key = ? AND state = 'pending' AND post_state_digest = ''`
	resolveCommitMarkerSQL = `UPDATE orchestration_wait_operation_commit_markers
SET state = 'resolved', resolved_at = ?
WHERE marker_key = ? AND state = 'pending' AND post_state_digest = ?`
	countPendingCommitMarkersSQL = `SELECT COUNT(*) FROM orchestration_wait_operation_commit_markers WHERE state = 'pending'`
)
