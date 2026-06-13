package taskdag

import (
	"context"
	"errors"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

type wakeupFence struct {
	ID             int64
	ClaimedAt      time.Time
	ClaimedBy      string
	LeaseExpiresAt time.Time
}

func queryOne[Row any, Out any](
	call func() (Row, error),
	operation, entity string,
	mapper func(Row) Out,
) (*Out, error) {
	row, err := call()
	if err != nil {
		return nil, wrapTaskDAGError(err, operation, entity)
	}
	mapped := mapper(row)
	return &mapped, nil
}

func queryMany[Row any, Out any](
	call func() ([]Row, error),
	operation, entity string,
	mapper func(Row) Out,
) ([]Out, error) {
	rows, err := call()
	if err != nil {
		return nil, wrapTaskDAGError(err, operation, entity)
	}
	return mapRows(rows, mapper), nil
}

func queryValue[T any](call func() (T, error), operation, entity string) (T, error) {
	value, err := call()
	if err != nil {
		var zero T
		return zero, wrapTaskDAGError(err, operation, entity)
	}
	return value, nil
}

func mapRows[In any, Out any](rows []In, mapper func(In) Out) []Out {
	out := make([]Out, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapper(row))
	}
	return out
}

func updateNodeStatus(call func() (sqlc.TaskDagNode, error), operation string) (*Node, error) {
	return queryOne(call, operation, "task_dag_node", fromNode)
}

func parseLeaseDuration(value, operation, entity string) (sqlc.Interval, error) {
	interval, err := intervalValue(value)
	if err != nil {
		return 0, wrapTaskDAGError(err, operation, entity)
	}
	return interval, nil
}

func bindWakeupTurnTx(
	ctx context.Context,
	txq *sqlc.Queries,
	input BindWakeupTurnInput,
	requireBound bool,
) (int64, error) {
	count, err := txq.BindTaskDagWakeupTurn(ctx, sqlc.BindTaskDagWakeupTurnParams{
		BoundTurnID: stringPtr(input.TurnID),
		ID:          input.ID,
	})
	if err != nil {
		return 0, wrapTaskDAGError(err, "bind_turn", "task_dag_wakeup")
	}
	if requireBound && count == 0 {
		return 0, wrapTaskDAGError(errors.New("wakeup turn binding conflict"), "bind_turn", "task_dag_wakeup")
	}
	return count, nil
}

func fencedWakeupMutation(
	operation string,
	fence wakeupFence,
	call func(wakeupFence) (int64, error),
) (int64, error) {
	return queryValue(func() (int64, error) {
		return call(fence)
	}, operation, "task_dag_wakeup")
}

func wakeupFenceFromMark(input MarkWakeupSentInput) wakeupFence {
	return wakeupFence(input)
}

func wakeupFenceFromRetry(input RetryWakeupInput) wakeupFence {
	return wakeupFence{
		ID:             input.ID,
		ClaimedAt:      input.ClaimedAt,
		ClaimedBy:      input.ClaimedBy,
		LeaseExpiresAt: input.LeaseExpiresAt,
	}
}

func wakeupFenceFromFail(input FailWakeupInput) wakeupFence {
	return wakeupFence{
		ID:             input.ID,
		ClaimedAt:      input.ClaimedAt,
		ClaimedBy:      input.ClaimedBy,
		LeaseExpiresAt: input.LeaseExpiresAt,
	}
}
