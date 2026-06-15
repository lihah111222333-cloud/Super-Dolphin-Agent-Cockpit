//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestListDAGsReadsScheduleColumns(t *testing.T) {
	t.Parallel()

	now := time.Unix(1700000000, 0).UTC()
	db := &captureListDAGsDB{
		rows: [][]any{scheduleTaskDAGValues(now)},
	}
	store := NewStore(db)

	got, err := store.ListDAGs(context.Background(), ListDAGsFilter{Status: "ready", Keyword: "brief", Limit: 10})
	if err != nil {
		t.Fatalf("ListDAGs() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListDAGs() len = %d, want 1", len(got))
	}
	assertSQLContainsAll(t, db.query, []string{"version", "trigger", "cron_expr", "next_run_at"})
	assertInt64Field(t, got[0], "Version", 42)
	assertStringField(t, got[0], "Trigger", "scheduled")
	assertStringField(t, got[0], "CronExpr", "0 8 * * *")
	requireNextRunAt(t, got[0])
	if !reflect.DeepEqual(db.args, []any{"ready", "brief", int32(10)}) {
		t.Fatalf("ListDAGs args = %#v, want status/keyword/limit", db.args)
	}
}

func TestGetDAGReadsScheduleColumns(t *testing.T) {
	t.Parallel()

	db := &captureListDAGsDB{row: scheduleTaskDAGValues(time.Unix(1700000000, 0).UTC())}
	store := NewStore(db)

	got, err := store.GetDAG(context.Background(), "daily-brief")
	if err != nil {
		t.Fatalf("GetDAG() error = %v", err)
	}
	assertSQLContainsAll(t, db.queryRow, []string{"version", "trigger", "cron_expr", "next_run_at"})
	requireDAGSchedule(t, *got)
	requireNextRunAt(t, *got)
	assertInt64Field(t, *got, "Version", 42)
}

func TestGetDAGForUpdateReadsScheduleColumns(t *testing.T) {
	t.Parallel()

	db := &captureListDAGsDB{row: scheduleTaskDAGValues(time.Unix(1700000000, 0).UTC())}
	store := NewStore(db).(DAGLockStore)

	got, err := store.GetDAGForUpdate(context.Background(), "daily-brief")
	if err != nil {
		t.Fatalf("GetDAGForUpdate() error = %v", err)
	}
	assertSQLContainsAll(t, db.queryRow, []string{"version", "trigger", "cron_expr", "next_run_at", "FOR UPDATE"})
	requireDAGSchedule(t, *got)
	requireNextRunAt(t, *got)
	assertInt64Field(t, *got, "Version", 42)
}

func TestUpsertDAGReturnsScheduleColumns(t *testing.T) {
	t.Parallel()

	db := &captureListDAGsDB{row: scheduleTaskDAGValues(time.Unix(1700000000, 0).UTC())}
	store := NewStore(db)

	got, err := store.UpsertDAG(context.Background(), DAG{
		DagKey:      "daily-brief",
		Title:       "Daily Brief",
		Description: "Morning report",
		Status:      "ready",
		CreatedBy:   "agent-1",
		Metadata:    []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("UpsertDAG() error = %v", err)
	}
	assertSQLContainsAll(t, db.queryRow, []string{"RETURNING", "version", "trigger", "cron_expr", "next_run_at"})
	if strings.Contains(db.queryRow, "DO UPDATE") {
		t.Fatalf("UpsertDAG SQL must stay create-only, got upsert update:\n%s", db.queryRow)
	}
	requireDAGSchedule(t, *got)
	requireNextRunAt(t, *got)
	assertInt64Field(t, *got, "Version", 42)
}

func scheduleTaskDAGValues(now time.Time) []any {
	return []any{
		int64(7), "daily-brief", "Daily Brief", "Morning report", "ready", "agent-1", []byte(`{}`),
		timestamptzValue(now), timestamptzValue(time.Time{}), timestamptzValue(now), timestamptzValue(now),
		"scheduled", "owner-1", "0 8 * * *", timestamptzValue(now.Add(time.Hour)), int64(42),
	}
}

func requireDAGSchedule(t *testing.T, got DAG) {
	t.Helper()
	assertStringField(t, got, "Trigger", "scheduled")
	assertStringField(t, got, "CronExpr", "0 8 * * *")
}

func requireNextRunAt(t *testing.T, got DAG) {
	t.Helper()
	if got.NextRunAt == nil || got.NextRunAt.IsZero() {
		t.Fatalf("NextRunAt = %#v, want scheduled next run", got.NextRunAt)
	}
}

func assertStringField(t *testing.T, value any, fieldName, want string) {
	t.Helper()

	field := reflect.ValueOf(value).FieldByName(fieldName)
	if !field.IsValid() {
		t.Fatalf("%T missing field %s", value, fieldName)
	}
	if got := field.String(); got != want {
		t.Fatalf("%s = %q, want %q", fieldName, got, want)
	}
}

func assertInt64Field(t *testing.T, value any, fieldName string, want int64) {
	t.Helper()

	field := reflect.ValueOf(value).FieldByName(fieldName)
	if !field.IsValid() {
		t.Fatalf("%T missing field %s", value, fieldName)
	}
	if got := field.Int(); got != want {
		t.Fatalf("%s = %d, want %d", fieldName, got, want)
	}
}

type captureListDAGsDB struct {
	query        string
	args         []any
	rows         [][]any
	queryRow     string
	queryRowArgs []any
	row          []any
}

func (*captureListDAGsDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec call")
}

func (db *captureListDAGsDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	db.query = sql
	db.args = append([]any(nil), args...)
	return &stubTaskDAGRows{rows: db.rows}, nil
}

func (db *captureListDAGsDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	db.queryRow = sql
	db.queryRowArgs = append([]any(nil), args...)
	return stubTaskDAGRow{values: db.row}
}
