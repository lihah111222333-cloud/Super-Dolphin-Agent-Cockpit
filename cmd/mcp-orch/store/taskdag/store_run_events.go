package taskdag

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlctx"
)

const maxTaskDagRunEvents = 50

func (s *store) appendTaskDagRunEvent(ctx context.Context, dagKey string, runID int64, event json.RawMessage) (string, error) {
	var runKey string
	err := sqlctx.WithImmediateTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, _ sqlc.DBTX) error {
		nextRunKey, err := appendTaskDagRunEventTx(ctx, txq, dagKey, runID, event)
		if err != nil {
			return err
		}
		runKey = nextRunKey
		return nil
	})
	if err != nil {
		return "", wrapTaskDAGError(err, "append_event", "task_dag_run")
	}
	return runKey, nil
}

func appendTaskDagRunEventTx(ctx context.Context, q *sqlc.Queries, dagKey string, runID int64, event json.RawMessage) (string, error) {
	if q == nil {
		return "", errors.New("append task dag run event: sqlc queries are nil")
	}
	if err := requireRuntimeRunID("append_run_event", runID); err != nil {
		return "", err
	}
	row, err := q.LoadTaskDagRunEventsForAppend(ctx, sqlc.LoadTaskDagRunEventsForAppendParams{
		DagKey: dagKey,
		RunID:  runID,
	})
	if err != nil {
		return "", err
	}
	nextEvents, err := appendRunEventJSON(row.Events, event)
	if err != nil {
		return "", err
	}
	rows, err := q.UpdateTaskDagRunEventsAfterAppend(ctx, sqlc.UpdateTaskDagRunEventsAfterAppendParams{
		Events: nextEvents,
		DagKey: dagKey,
		RunID:  runID,
	})
	if err != nil {
		return "", err
	}
	if rows != 1 {
		return "", sql.ErrNoRows
	}
	return row.RunKey, nil
}

func appendRunEventJSON(existing json.RawMessage, event json.RawMessage) (json.RawMessage, error) {
	if len(existing) == 0 {
		return nil, errors.New("task_dag_run events is empty; expected JSON array")
	}
	if len(event) == 0 {
		return nil, errors.New("task_dag_run event payload is empty")
	}
	if !json.Valid(event) {
		return nil, fmt.Errorf("task_dag_run event payload is invalid JSON: %s", string(event))
	}
	var events []json.RawMessage
	if err := json.Unmarshal(existing, &events); err != nil {
		return nil, fmt.Errorf("decode task_dag_run events array: %w", err)
	}
	events = append(events, append(json.RawMessage(nil), event...))
	if len(events) > maxTaskDagRunEvents {
		events = events[len(events)-maxTaskDagRunEvents:]
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		return nil, fmt.Errorf("encode task_dag_run events array: %w", err)
	}
	return json.RawMessage(encoded), nil
}
