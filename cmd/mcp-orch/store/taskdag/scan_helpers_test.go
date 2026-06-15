//go:build legacy_pg_fake

package taskdag

import (
	"fmt"
	"reflect"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func intervalDuration(value sqlc.Interval) time.Duration {
	return time.Duration(value) * time.Millisecond
}

func sameInt8(left, right sqlc.Int8) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func sameTimestamp(left, right sqlc.Timestamptz) bool {
	return left == right
}

type stubTaskDAGRows struct {
	rows [][]any
	idx  int
}

func (r *stubTaskDAGRows) Close()                                       { _ = r }
func (r *stubTaskDAGRows) Err() error                                   { return nil }
func (r *stubTaskDAGRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *stubTaskDAGRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *stubTaskDAGRows) RawValues() [][]byte                          { return nil }
func (r *stubTaskDAGRows) Conn() *pgx.Conn                              { return nil }

func (r *stubTaskDAGRows) Next() bool {
	if r.idx >= len(r.rows) {
		return false
	}
	r.idx++
	return true
}

func (r *stubTaskDAGRows) Scan(dest ...any) error {
	if r.idx == 0 || r.idx > len(r.rows) {
		return fmt.Errorf("scan without current row")
	}
	return scanTaskDAGValues(dest, r.rows[r.idx-1])
}

func (r *stubTaskDAGRows) Values() ([]any, error) {
	if r.idx == 0 || r.idx > len(r.rows) {
		return nil, fmt.Errorf("values without current row")
	}
	return append([]any(nil), r.rows[r.idx-1]...), nil
}

type stubTaskDAGRow struct {
	values []any
	err    error
}

func (r stubTaskDAGRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return scanTaskDAGValues(dest, r.values)
}

func scanTaskDAGValues(dest []any, values []any) error {
	if len(dest) != len(values) {
		return fmt.Errorf("dest len = %d, values len = %d", len(dest), len(values))
	}
	for i, target := range dest {
		if err := assignTaskDAGValue(target, values[i]); err != nil {
			return fmt.Errorf("scan column %d: %w", i, err)
		}
	}
	return nil
}

func assignTaskDAGValue(target any, value any) error {
	rv := reflect.ValueOf(target)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("target must be non-nil pointer")
	}
	elem := rv.Elem()
	if value == nil {
		elem.Set(reflect.Zero(elem.Type()))
		return nil
	}
	if typed, ok := value.([]byte); ok {
		elem.Set(reflect.ValueOf(append([]byte(nil), typed...)))
		return nil
	}
	vv := reflect.ValueOf(value)
	if vv.Type().AssignableTo(elem.Type()) {
		elem.Set(vv)
		return nil
	}
	if vv.Type().ConvertibleTo(elem.Type()) {
		elem.Set(vv.Convert(elem.Type()))
		return nil
	}
	return fmt.Errorf("cannot assign %T to %s", value, elem.Type())
}

func taskDagWakeupValues(row sqlc.TaskDagWakeup) []any {
	return []any{
		row.ID, row.DagKey, row.NodeKey, row.WakeupKind, row.TargetAgentID,
		append([]byte(nil), row.PromptPayload...), row.IdempotencyKey, row.Status,
		row.AttemptCount, row.NextRetryAt, row.ClaimedAt, row.ClaimedBy,
		row.LeaseExpiresAt, row.SentAt, row.BoundTurnID, row.TurnBoundAt,
		row.LastError, row.CreatedAt, row.UpdatedAt, row.RunID,
	}
}

func taskDagNodeValues(row sqlc.TaskDagNode) []any {
	return []any{
		row.ID, row.DagKey, row.NodeKey, row.Title, row.NodeType, row.AssignedTo,
		append([]byte(nil), row.DependsOn...), row.Status, row.CommandRef,
		append([]byte(nil), row.Config...), append([]byte(nil), row.Result...),
		row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt,
		row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.RunID,
		append([]byte(nil), row.Reads...), append([]byte(nil), row.Writes...),
		row.SpawningThreadID,
	}
}

func timestamptzValue(value time.Time) sqlc.Timestamptz {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixMilli()
}
