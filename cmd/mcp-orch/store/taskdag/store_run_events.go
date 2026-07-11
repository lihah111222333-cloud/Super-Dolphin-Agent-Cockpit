package taskdag

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

const maxTaskDagRunEvents = 50

// appendTaskDagRunEvent 在事务内追加一条事件到 task_dag_runs.events JSON 数组，
// 返回对应的 run_key；事务不存在时自动起新事务。
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

// appendTaskDagRunEventTx 是事务体：先 SELECT 当前 events，追加新事件（超上限时窗口截断），
// 再 UPDATE 回去；0 行受影响时返回 sql.ErrNoRows（表示 run 不存在或状态不对）。
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

// appendRunEventJSON 把新事件追加进现有 JSON 数组，超过 maxTaskDagRunEvents 时滑窗截断最旧事件。
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
