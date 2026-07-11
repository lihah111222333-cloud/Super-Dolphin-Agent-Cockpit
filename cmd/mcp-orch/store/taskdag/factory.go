package taskdag

import (
	"context"
	"errors"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

// wakeupFence 是 wakeup 操作的防重字段集合，防止过期 claim 覆盖新一轮调度结果。
type wakeupFence struct {
	ID             int64
	ClaimedAt      time.Time
	ClaimedBy      string
	LeaseExpiresAt time.Time
}

// queryOne 调用 call 取单行并用 mapper 转换，错误时包装域错误。
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

// queryMany 调用 call 取多行并用 mapper 批量转换，错误时包装域错误。
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

// queryManyWrite 带写重试地调用 call 取多行并批量转换，适用于 SQLite busy 场景。
func queryManyWrite[Row any, Out any](
	ctx context.Context,
	call func() ([]Row, error),
	operation, entity string,
	mapper func(Row) Out,
) ([]Out, error) {
	var mapped []Out
	err := sqlctx.WithWriteRetry(ctx, func() error {
		rows, err := call()
		if err != nil {
			return wrapTaskDAGError(err, operation, entity)
		}
		mapped = mapRows(rows, mapper)
		return nil
	})
	return mapped, err
}

// queryValue 调用 call 取单个标量值，错误时包装域错误。
func queryValue[T any](call func() (T, error), operation, entity string) (T, error) {
	value, err := call()
	if err != nil {
		var zero T
		return zero, wrapTaskDAGError(err, operation, entity)
	}
	return value, nil
}

// queryOneWrite 带写重试地调用 call 取单行并用 mapper 转换。
func queryOneWrite[Row any, Out any](
	ctx context.Context,
	call func() (Row, error),
	operation, entity string,
	mapper func(Row) Out,
) (*Out, error) {
	var mapped *Out
	err := sqlctx.WithWriteRetry(ctx, func() error {
		row, err := call()
		if err != nil {
			return wrapTaskDAGError(err, operation, entity)
		}
		out := mapper(row)
		mapped = &out
		return nil
	})
	return mapped, err
}

// queryValueWrite 带写重试地调用 call 取单个标量值。
func queryValueWrite[T any](ctx context.Context, call func() (T, error), operation, entity string) (T, error) {
	var value T
	err := sqlctx.WithWriteRetry(ctx, func() error {
		next, err := call()
		if err != nil {
			return wrapTaskDAGError(err, operation, entity)
		}
		value = next
		return nil
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return value, nil
}

// mapRows 把 []In 按 mapper 批量转换为 []Out。
func mapRows[In any, Out any](rows []In, mapper func(In) Out) []Out {
	out := make([]Out, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapper(row))
	}
	return out
}

// updateNodeStatus 是节点状态更新的只读路径统一封装，无写重试。
func updateNodeStatus[Row any](call func() (Row, error), operation string, mapper func(Row) Node) (*Node, error) {
	return queryOne(call, operation, "task_dag_node", mapper)
}

// updateNodeStatusWrite 是节点状态更新的写路径统一封装，带 SQLite busy 重试。
func updateNodeStatusWrite[Row any](ctx context.Context, call func() (Row, error), operation string, mapper func(Row) Node) (*Node, error) {
	return queryOneWrite(ctx, call, operation, "task_dag_node", mapper)
}

// parseLeaseDuration 解析 lease interval 字符串并包装解析错误为域错误。
func parseLeaseDuration(value, operation, entity string) (sqlc.Interval, error) {
	interval, err := intervalValue(value)
	if err != nil {
		return 0, wrapTaskDAGError(err, operation, entity)
	}
	return interval, nil
}

// bindWakeupTurnTx 把 sent wakeup 和 turn_id 绑起来。
// requireBound=true 时，绑不上就报错，避免节点指向没有 wakeup 的 turn。
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

// fencedWakeupMutation 统一包装 wakeup 变更操作的错误，不含写重试（调用方管事务）。
// rows=0 表示 claim fence miss，由调用方决定语义，不在此转换为错误。
func fencedWakeupMutation(
	operation string,
	fence wakeupFence,
	call func(wakeupFence) (int64, error),
) (int64, error) {
	return queryValue(func() (int64, error) {
		return call(fence)
	}, operation, "task_dag_wakeup")
}

// fencedWakeupMutationWrite 带写重试地执行 wakeup 变更，适用于直接写路径（非事务内）。
func fencedWakeupMutationWrite(
	ctx context.Context,
	operation string,
	fence wakeupFence,
	call func(wakeupFence) (int64, error),
) (int64, error) {
	return queryValueWrite(ctx, func() (int64, error) {
		return call(fence)
	}, operation, "task_dag_wakeup")
}

// wakeupFenceFromMark 从 MarkWakeupSentInput 提取 fence 字段。
func wakeupFenceFromMark(input MarkWakeupSentInput) wakeupFence {
	return wakeupFence(input)
}

// wakeupFenceFromRetry 从 RetryWakeupInput 提取 fence 字段。
func wakeupFenceFromRetry(input RetryWakeupInput) wakeupFence {
	return wakeupFence{
		ID:             input.ID,
		ClaimedAt:      input.ClaimedAt,
		ClaimedBy:      input.ClaimedBy,
		LeaseExpiresAt: input.LeaseExpiresAt,
	}
}

// wakeupFenceFromFail 从 FailWakeupInput 提取 fence 字段。
func wakeupFenceFromFail(input FailWakeupInput) wakeupFence {
	return wakeupFence{
		ID:             input.ID,
		ClaimedAt:      input.ClaimedAt,
		ClaimedBy:      input.ClaimedBy,
		LeaseExpiresAt: input.LeaseExpiresAt,
	}
}
