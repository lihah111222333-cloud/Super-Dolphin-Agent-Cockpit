package reconcilecoordinator

import (
	"context"
	"time"
)

// ListJournal 按 sequence 顺序返回 append-only 状态转移。
func (c *Coordinator) ListJournal(ctx context.Context, jobID string) ([]JournalRecord, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT sequence, job_id, event, state, COALESCE(claim_token,''), detail, created_at FROM reconcile_journal WHERE (? = '' OR job_id = ?) ORDER BY sequence`, jobID, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]JournalRecord, 0)
	for rows.Next() {
		var record JournalRecord
		var state, created string
		if err := rows.Scan(&record.Sequence, &record.JobID, &record.Event, &state, &record.ClaimToken, &record.Detail, &created); err != nil {
			return nil, err
		}
		record.State = JobState(state)
		parsed, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, ErrSchemaMismatch
		}
		record.CreatedAt = parsed
		result = append(result, record)
	}
	return result, rows.Err()
}

// ListAudit 按 sequence 顺序返回 append-only reconcile 结果。
func (c *Coordinator) ListAudit(ctx context.Context, jobID string) ([]AuditRecord, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT sequence, job_id, event, state, status, detail, owner_id, owner_epoch, created_at FROM reconcile_audit WHERE (? = '' OR job_id = ?) ORDER BY sequence`, jobID, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]AuditRecord, 0)
	for rows.Next() {
		var record AuditRecord
		var state, created string
		if err := rows.Scan(&record.Sequence, &record.JobID, &record.Event, &state, &record.Status, &record.Detail, &record.OwnerID, &record.OwnerEpoch, &created); err != nil {
			return nil, err
		}
		record.State = JobState(state)
		parsed, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, ErrSchemaMismatch
		}
		record.CreatedAt = parsed
		result = append(result, record)
	}
	return result, rows.Err()
}
