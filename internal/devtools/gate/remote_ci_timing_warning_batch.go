package gate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const remoteCITimingWarningInsertChunkSize = 500

// withSQLiteWriteTransactionContext 在 caller ctx 下执行有界写事务和 busy 重试。
func withSQLiteWriteTransactionContext(ctx context.Context, database *sql.DB, operation string, write func(*sql.Tx) error) error {
	if ctx == nil {
		return errors.New("SQLite write transaction context is required")
	}
	for attempt := range sqliteWriteBusyRetryAttempts {
		if err := ctx.Err(); err != nil {
			return err
		}
		transaction, err := database.BeginTx(ctx, nil)
		if err != nil {
			mapped := mapDurationLedgerSQLiteError("begin "+operation+" transaction", err)
			if !errors.Is(mapped, ErrDurationLedgerBusy) {
				return mapped
			}
			if err := waitSQLiteBusyRetry(ctx, attempt); err != nil {
				return err
			}
			continue
		}
		if err := runSQLiteWriteTransactionContext(ctx, transaction, operation, write, attempt); err != nil {
			if !errors.Is(err, ErrDurationLedgerBusy) {
				return err
			}
			continue
		}
		return nil
	}
	return fmt.Errorf("%w: %s transaction exceeded SQLite busy retry limit", ErrDurationLedgerBusy, operation)
}

// runSQLiteWriteTransactionContext 提交单次写入；仅 busy 时交回外层重试。
func runSQLiteWriteTransactionContext(ctx context.Context, transaction *sql.Tx, operation string, write func(*sql.Tx) error, attempt int) error {
	if err := write(transaction); err != nil {
		return retrySQLiteBusyContext(ctx, transaction, operation, attempt, err)
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, rollbackSQLiteTransaction(transaction, operation))
	}
	if err := transaction.Commit(); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return errors.Join(contextErr, rollbackSQLiteTransaction(transaction, operation))
		}
		return retrySQLiteBusyContext(ctx, transaction, operation, attempt, mapDurationLedgerSQLiteError("commit "+operation+" transaction", err))
	}
	return nil
}

// retrySQLiteBusyContext 回滚单次失败，并在 caller ctx 下等待下一次 busy 尝试。
func retrySQLiteBusyContext(ctx context.Context, transaction *sql.Tx, operation string, attempt int, cause error) error {
	rollbackErr := rollbackSQLiteTransaction(transaction, operation)
	if rollbackErr != nil || !errors.Is(cause, ErrDurationLedgerBusy) {
		return errors.Join(cause, rollbackErr)
	}
	if retryErr := waitSQLiteBusyRetry(ctx, attempt); retryErr != nil {
		return errors.Join(cause, retryErr)
	}
	return cause
}

func waitSQLiteBusyRetry(ctx context.Context, attempt int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if attempt+1 >= sqliteWriteBusyRetryAttempts {
		return nil
	}
	timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// RecordLiveRemoteCITimingWarnings 将同一轮告警规范化后批量写入；批量 SQL 从事务开始继承 caller ctx。
func (store *DurationLedgerStore) RecordLiveRemoteCITimingWarnings(ctx context.Context, warnings []RemoteCITimingWarning) ([]RemoteCITimingWarning, []bool, error) {
	canonical, database, err := prepareRemoteCITimingWarningBatch(store, ctx, warnings)
	if err != nil {
		return nil, nil, err
	}
	if len(canonical) == 0 {
		return nil, nil, nil
	}
	defer database.Close()
	stored := make([]RemoteCITimingWarning, 0, len(canonical))
	inserted := make([]bool, 0, len(canonical))
	err = withSQLiteWriteTransactionContext(ctx, database, "live remote CI timing warning batch", func(transaction *sql.Tx) error {
		if err := requireHistoricallyAcceptedGeneration(transaction, canonical[0].AcceptedGeneration); err != nil {
			return err
		}
		var latest time.Time
		var shouldAdvance bool
		stored, inserted, latest, shouldAdvance, err = recordRemoteCITimingWarningBatchRows(transaction, canonical, ctx)
		if err != nil {
			return err
		}
		if shouldAdvance {
			if err := advanceCIQueryRevision(transaction, latest); err != nil {
				return err
			}
		}
		return compactDurationLedgerAuthority(transaction)
	})
	if err != nil {
		return nil, nil, err
	}
	return stored, inserted, nil
}

func prepareRemoteCITimingWarningBatch(store *DurationLedgerStore, ctx context.Context, warnings []RemoteCITimingWarning) ([]RemoteCITimingWarning, *sql.DB, error) {
	if store == nil {
		return nil, nil, errors.New("duration ledger store is nil")
	}
	if ctx == nil {
		return nil, nil, errors.New("remote CI timing warning context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	canonical, err := canonicalRemoteCITimingWarningBatch(warnings)
	if err != nil || len(canonical) == 0 {
		return canonical, nil, err
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		database.Close()
		return nil, nil, err
	}
	return canonical, database, nil
}

func recordRemoteCITimingWarningBatchRows(transaction *sql.Tx, warnings []RemoteCITimingWarning, ctx context.Context) ([]RemoteCITimingWarning, []bool, time.Time, bool, error) {
	if err := requireRetainedRemoteCITimingWarningGeneration(transaction, warnings[0].AcceptedGeneration); err != nil {
		return nil, nil, time.Time{}, false, err
	}
	if err := rejectFinalizedRemoteCITimingWarningWrite(transaction, warnings[0].JobID); err != nil {
		return nil, nil, time.Time{}, false, err
	}
	stored, inserted, err := recordLiveRemoteCITimingWarningsInTransaction(transaction, warnings, ctx)
	if err != nil {
		return nil, nil, time.Time{}, false, err
	}
	latest, shouldAdvance := latestInsertedRemoteCITimingWarning(warnings, inserted)
	return stored, inserted, latest, shouldAdvance, nil
}

func latestInsertedRemoteCITimingWarning(warnings []RemoteCITimingWarning, inserted []bool) (time.Time, bool) {
	var latest time.Time
	for index, warning := range warnings {
		if !inserted[index] {
			continue
		}
		if latest.IsZero() || warning.ObservedAt.After(latest) {
			latest = warning.ObservedAt
		}
	}
	return latest, !latest.IsZero()
}

// canonicalRemoteCITimingWarningBatch 校验同 job 身份并去重、排序一轮告警。
func canonicalRemoteCITimingWarningBatch(warnings []RemoteCITimingWarning) ([]RemoteCITimingWarning, error) {
	if len(warnings) == 0 {
		return nil, nil
	}
	seen := make(map[remoteCITimingWarningKey]RemoteCITimingWarning, len(warnings))
	jobID, agentTokenDigest, acceptedGeneration := warnings[0].JobID, warnings[0].AgentTokenDigest, warnings[0].AcceptedGeneration
	for _, warning := range warnings {
		if err := warning.Validate(); err != nil {
			return nil, err
		}
		if warning.JobID != jobID {
			return nil, errors.New("remote CI timing warning batch must not mix jobs")
		}
		if warning.AgentTokenDigest != agentTokenDigest || warning.AcceptedGeneration != acceptedGeneration {
			return nil, errors.New("remote CI timing warning batch must not mix run identity")
		}
		if previous, exists := seen[warning.key()]; exists {
			if !remoteCITimingWarningEqual(previous, warning) {
				return nil, fmt.Errorf("remote CI timing warning %v conflicts within batch", warning.key())
			}
			continue
		}
		seen[warning.key()] = warning
	}
	canonical := make([]RemoteCITimingWarning, 0, len(seen))
	for _, warning := range seen {
		canonical = append(canonical, warning)
	}
	return canonicalRemoteCITimingWarnings(canonical), nil
}

type remoteCITimingWarningContextQueryer struct {
	transaction *sql.Tx
	ctx         context.Context
}

// QueryRow 使用 caller ctx 执行单行查询。
func (queryer remoteCITimingWarningContextQueryer) QueryRow(query string, args ...any) *sql.Row {
	return queryer.transaction.QueryRowContext(queryer.ctx, query, args...)
}

// Query 使用 caller ctx 执行流式查询。
func (queryer remoteCITimingWarningContextQueryer) Query(query string, args ...any) (*sql.Rows, error) {
	return queryer.transaction.QueryContext(queryer.ctx, query, args...)
}

// recordLiveRemoteCITimingWarningsInTransaction 在一个事务内插入并读回告警事实。
func recordLiveRemoteCITimingWarningsInTransaction(transaction *sql.Tx, warnings []RemoteCITimingWarning, ctx context.Context) ([]RemoteCITimingWarning, []bool, error) {
	insertedKeys, err := insertLiveRemoteCITimingWarnings(transaction, warnings, ctx)
	if err != nil {
		return nil, nil, err
	}
	queryer := remoteCITimingWarningContextQueryer{transaction: transaction, ctx: ctx}
	stored := make([]RemoteCITimingWarning, 0, len(warnings))
	inserted := make([]bool, 0, len(warnings))
	for _, warning := range warnings {
		wasInserted := insertedKeys[warning.key()]
		value := warning
		if !wasInserted {
			value, err = loadRemoteCITimingWarningByKey(queryer, cicontract.LiveTimingWarningsTable, warning.key())
			if err != nil {
				return nil, nil, err
			}
		}
		if !remoteCITimingWarningRetryMatches(value, warning) {
			return nil, nil, fmt.Errorf("remote CI timing warning %v conflicts with its first live observation", warning.key())
		}
		stored = append(stored, value)
		inserted = append(inserted, wasInserted)
	}
	return stored, inserted, nil
}
func insertLiveRemoteCITimingWarnings(transaction *sql.Tx, warnings []RemoteCITimingWarning, ctx context.Context) (map[remoteCITimingWarningKey]bool, error) {
	inserted := make(map[remoteCITimingWarningKey]bool, len(warnings))
	for start := 0; start < len(warnings); start += remoteCITimingWarningInsertChunkSize {
		end := min(start+remoteCITimingWarningInsertChunkSize, len(warnings))
		chunk, err := insertLiveRemoteCITimingWarningChunk(transaction, warnings[start:end], ctx)
		if err != nil {
			return nil, err
		}
		for key := range chunk {
			inserted[key] = true
		}
	}
	return inserted, nil
}

// insertLiveRemoteCITimingWarningChunk 以安全参数块执行幂等 INSERT RETURNING。
func insertLiveRemoteCITimingWarningChunk(transaction *sql.Tx, warnings []RemoteCITimingWarning, ctx context.Context) (map[remoteCITimingWarningKey]bool, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?),", len(warnings)), ",")
	arguments := make([]any, 0, len(warnings)*13)
	for _, warning := range warnings {
		arguments = append(arguments, remoteCITimingWarningSQLArguments(warning)...)
	}
	query := `INSERT INTO ci_live_timing_warnings (
		job_id, agent_token_digest, accepted_generation, scope, shard_identity, workload_id,
		evidence_kind, action, evidence_started_at_unix_ms, observed_at_unix_ms,
		evidence_duration_ms, target_ms, warning_text
	) VALUES ` + placeholders + `
	ON CONFLICT(job_id, scope, shard_identity, workload_id, evidence_kind, target_ms) DO NOTHING
	RETURNING scope, shard_identity, workload_id, evidence_kind, target_ms`
	rows, err := transaction.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("store live remote CI timing warning batch", err)
	}
	defer rows.Close()
	inserted := make(map[remoteCITimingWarningKey]bool, len(warnings))
	for rows.Next() {
		var scope, shardIdentity, workloadID, evidenceKind string
		var targetMS int64
		if err := rows.Scan(&scope, &shardIdentity, &workloadID, &evidenceKind, &targetMS); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan live remote CI timing warning batch", err)
		}
		inserted[remoteCITimingWarningKey{JobID: warnings[0].JobID, Scope: cicontract.TimingScope(scope), ShardIdentity: shardIdentity, WorkloadID: GateID(workloadID), EvidenceKind: cicontract.TimingWarningEvidenceKind(evidenceKind), TargetMS: targetMS}] = true
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate live remote CI timing warning batch", err)
	}
	return inserted, nil
}
