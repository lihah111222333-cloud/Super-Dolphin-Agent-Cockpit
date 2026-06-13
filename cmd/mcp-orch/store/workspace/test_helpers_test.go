//go:build legacy_pg_fake

package workspace

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type stubWorkspaceDB struct {
	queryFunc    func(context.Context, string, ...any) (pgx.Rows, error)
	queryRowFunc func(context.Context, string, ...any) pgx.Row
}

func (db stubWorkspaceDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec call")
}

func (db stubWorkspaceDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if db.queryFunc == nil {
		return nil, fmt.Errorf("unexpected Query call")
	}
	return db.queryFunc(ctx, sql, args...)
}

func (db stubWorkspaceDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if db.queryRowFunc == nil {
		return stubWorkspaceRow{err: fmt.Errorf("unexpected QueryRow call")}
	}
	return db.queryRowFunc(ctx, sql, args...)
}

type stubWorkspaceRows struct {
	rows [][]any
	idx  int
	err  error
}

func (r *stubWorkspaceRows) Close() { _ = r }

func (r *stubWorkspaceRows) Err() error { return r.err }

func (r *stubWorkspaceRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func (r *stubWorkspaceRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (r *stubWorkspaceRows) Next() bool {
	if r.idx >= len(r.rows) {
		return false
	}
	r.idx++
	return true
}

func (r *stubWorkspaceRows) Scan(dest ...any) error {
	if r.idx == 0 || r.idx > len(r.rows) {
		return fmt.Errorf("Scan called without current row")
	}
	return scanWorkspaceInto(dest, r.rows[r.idx-1])
}

func (r *stubWorkspaceRows) Values() ([]any, error) {
	if r.idx == 0 || r.idx > len(r.rows) {
		return nil, fmt.Errorf("Values called without current row")
	}
	return append([]any(nil), r.rows[r.idx-1]...), nil
}

func (r *stubWorkspaceRows) RawValues() [][]byte { return nil }

func (r *stubWorkspaceRows) Conn() *pgx.Conn { return nil }

type stubWorkspaceRow struct {
	values []any
	err    error
}

func (r stubWorkspaceRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return scanWorkspaceInto(dest, r.values)
}

func scanWorkspaceInto(dest []any, values []any) error {
	if len(dest) != len(values) {
		return fmt.Errorf("dest len = %d, values len = %d", len(dest), len(values))
	}
	for i, target := range dest {
		if err := assignWorkspaceValue(target, values[i]); err != nil {
			return fmt.Errorf("scan column %d: %w", i, err)
		}
	}
	return nil
}

func assignWorkspaceValue(target any, value any) error {
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
		cloned := append([]byte(nil), typed...)
		elem.Set(reflect.ValueOf(cloned))
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

func workspaceRunValues(row sqlc.WorkspaceRun) []any {
	return []any{
		row.ID,
		row.RunKey,
		row.DagKey,
		row.SourceRoot,
		row.WorkspacePath,
		row.Status,
		row.CreatedBy,
		row.UpdatedBy,
		append([]byte(nil), row.Metadata...),
		row.CreatedAt,
		row.UpdatedAt,
		row.FinishedAt,
	}
}

func timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func timePtr(t time.Time) *time.Time { return &t }
