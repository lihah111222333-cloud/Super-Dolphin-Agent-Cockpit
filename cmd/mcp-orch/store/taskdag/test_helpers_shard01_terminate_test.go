//go:build legacy_pg_fake

package taskdag

import (
	"encoding/json"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/jackc/pgx/v5"
)

func (db *fakeTaskDAGDB) cancelTaskDagRunNodes(args ...any) (int64, error) {
	rows, err := db.cancelTaskDagRunNodesReturningThreads(args...)
	return int64(len(rows)), err
}

func (db *fakeTaskDAGDB) cancelTaskDagRunNodesReturningThreads(args ...any) ([][]any, error) {
	if err := requireFakeTaskDAGArgs(args, 3, "cancel run nodes",
		fakeTaskDAGTypedArg[string](0, "dag key"),
		fakeTaskDAGInt8Arg(1, "run id"),
		fakeTaskDAGTypedArg[string](2, "reason")); err != nil {
		return nil, err
	}
	dagKey := args[0].(string)
	runID, err := fakeInt8Arg(args, 1, "run id")
	if err != nil {
		return nil, err
	}
	reason := args[2].(string)
	result, err := json.Marshal(map[string]string{
		"kind":   "run_cancelled",
		"reason": reason,
	})
	if err != nil {
		return nil, fmt.Errorf("encode cancel result: %w", err)
	}
	rows := make([][]any, 0)
	for key, row := range db.nodes {
		if row.DagKey != dagKey || fakeRunID(row) != runID || isFakeTerminalStatus(row.Status) {
			continue
		}
		threadID := row.SpawningThreadID
		row.Status = "cancelled"
		row.Result = append([]byte(nil), result...)
		row.ActiveTurnID = sqlc.Text{}
		row.ActiveWakeupID = sqlc.Int8{}
		if !row.FinishedAt.Valid {
			row.FinishedAt = timestamptzValue(db.now)
		}
		row.LastEventAt = timestamptzValue(db.now)
		row.UpdatedAt = timestamptzValue(db.now)
		db.nodes[key] = row
		rows = append(rows, []any{threadID})
	}
	return rows, nil
}

func (db *fakeTaskDAGDB) cancelTaskDagRunWakeups(args ...any) (int64, error) {
	if err := requireFakeTaskDAGArgs(args, 3, "cancel run wakeups",
		fakeTaskDAGTypedArg[string](0, "dag key"),
		fakeTaskDAGInt8Arg(1, "run id"),
		fakeTaskDAGTypedArg[string](2, "last error")); err != nil {
		return 0, err
	}
	dagKey := args[0].(string)
	runID, err := fakeInt8Arg(args, 1, "run id")
	if err != nil {
		return 0, err
	}
	lastError := args[2].(string)
	var failed int64
	for id, row := range db.wakeups {
		if row.DagKey != dagKey || fakeWakeupRunID(row) != runID || !isCancelableWakeupStatus(row.Status) {
			continue
		}
		row.Status = "failed"
		row.LastError = lastError
		row.ClaimedAt = sqlc.Timestamptz{}
		row.ClaimedBy = ""
		row.LeaseExpiresAt = sqlc.Timestamptz{}
		row.UpdatedAt = timestamptzValue(db.now)
		db.wakeups[id] = row
		failed++
	}
	return failed, nil
}

func isCancelableWakeupStatus(status string) bool {
	switch status {
	case "pending", "dispatching", "sent":
		return true
	default:
		return false
	}
}

func (db *fakeTaskDAGDB) cancelTaskDagRun(args ...any) ([]any, error) {
	if err := requireFakeTaskDAGArgs(args, 4, "cancel run",
		fakeTaskDAGTypedArg[string](0, "dag key"),
		fakeTaskDAGInt8Arg(1, "run id"),
		fakeTaskDAGTypedArg[string](2, "run key"),
		fakeTaskDAGTypedArg[[]byte](3, "event")); err != nil {
		return nil, err
	}
	dagKey := args[0].(string)
	runID, err := fakeInt8Arg(args, 1, "run id")
	if err != nil {
		return nil, err
	}
	runKey := args[2].(string)
	payload := args[3].([]byte)
	run, ok := db.runs[runKey]
	if !ok || !isMatchingRunningRun(run, dagKey, runID) {
		return nil, pgx.ErrNoRows
	}
	event, err := decodeRunEventPayload(payload)
	if err != nil {
		return nil, err
	}
	updated, err := appendRunEventPayload(run, event)
	if err != nil {
		return nil, err
	}
	updated.Status = "cancelled"
	updated.FinishedAt = timestamptzValue(db.now)
	updated.UpdatedAt = timestamptzValue(db.now)
	db.runs[runKey] = updated
	return taskDagRunValues(updated), nil
}

func (db *fakeTaskDAGDB) getTaskDagRun(args ...any) ([]any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("get run args len = %d, want 1", len(args))
	}
	runKey, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("run key arg = %T", args[0])
	}
	run, ok := db.runs[runKey]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return taskDagRunValues(run), nil
}

func (db *fakeTaskDAGDB) lockTaskDAGRunForCompletion(args ...any) ([]any, error) {
	if err := requireFakeTaskDAGArgs(args, 2, "lock run for completion",
		fakeTaskDAGTypedArg[string](0, "dag key"),
		fakeTaskDAGInt8Arg(1, "run id")); err != nil {
		return nil, err
	}
	dagKey := args[0].(string)
	runID, err := fakeInt8Arg(args, 1, "run id")
	if err != nil {
		return nil, err
	}
	db.ops = append(db.ops, "lock_run_for_completion")
	for _, run := range db.runs {
		if run.DagKey == dagKey && run.ID == runID && run.Status == "running" {
			return []any{run.ID}, nil
		}
	}
	return nil, pgx.ErrNoRows
}

func taskDagRunValues(row sqlc.TaskDagRun) []any {
	return []any{
		row.ID,
		row.RunKey,
		row.DagKey,
		row.DagVersionSnapshot,
		row.TriggerSource,
		row.Status,
		row.StartedAt,
		row.FinishedAt,
		append([]byte(nil), row.Events...),
		row.BudgetUsed,
		row.BudgetLimit,
		append([]byte(nil), row.Metadata...),
		row.CreatedAt,
		row.UpdatedAt,
	}
}
