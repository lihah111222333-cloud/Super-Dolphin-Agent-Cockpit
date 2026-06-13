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
	"github.com/jackc/pgx/v5/pgtype"
)

func TestUpdateDAGPatch_ExecutesNarrowMetadataUpdate(t *testing.T) {
	t.Parallel()

	db := &captureUpdateDAGPatchDB{rows: 1}
	store := NewStore(db)
	updater := store.(interface {
		UpdateDAGPatch(context.Context, UpdateDAGPatchInput) (int64, error)
	})
	title := "Daily"
	description := "Morning"
	trigger := "scheduled"
	cronExpr := "0 8 * * *"
	ownerID := "owner-1"
	nextRunAt := time.Unix(1700003600, 0).UTC()
	scheduleEnabled := true

	rows, err := updater.UpdateDAGPatch(context.Background(), UpdateDAGPatchInput{
		DagKey:          "dag-1",
		Title:           &title,
		Description:     &description,
		Trigger:         &trigger,
		CronExpr:        &cronExpr,
		OwnerID:         &ownerID,
		NextRunAt:       &nextRunAt,
		ScheduleEnabled: &scheduleEnabled,
	})
	if err != nil {
		t.Fatalf("UpdateDAGPatch() error = %v", err)
	}
	if rows != 1 {
		t.Fatalf("UpdateDAGPatch() rows = %d, want 1", rows)
	}
	assertUpdateDAGPatchSQL(t, db.sql)
	assertUpdateDAGPatchArgs(t, db.args, nextRunAt)
}

func assertUpdateDAGPatchSQL(t *testing.T, sql string) {
	t.Helper()

	assertSQLContainsAll(t, sql, []string{
		"UPDATE task_dags",
		"title = COALESCE",
		"description = COALESCE",
		"trigger = COALESCE",
		"cron_expr = COALESCE",
		"owner_id = COALESCE",
		"next_run_at = CASE",
		"$6::boolean IS NOT NULL",
		"$6::boolean = FALSE THEN NULL",
		"$6::boolean = TRUE THEN COALESCE($7, next_run_at)",
		"COALESCE($7, next_run_at)",
		"WHERE dag_key = $8",
	})
	if strings.Contains(sql, "THEN NOW()") {
		t.Fatalf("UpdateDAGPatch SQL must not initialize next_run_at to NOW():\n%s", sql)
	}
}

func assertSQLContainsAll(t *testing.T, sql string, wants []string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(sql, want) {
			t.Fatalf("UpdateDAGPatch SQL missing %q:\n%s", want, sql)
		}
	}
}

func assertUpdateDAGPatchArgs(t *testing.T, args []any, nextRunAt time.Time) {
	t.Helper()

	want := []any{
		pgtype.Text{String: "Daily", Valid: true},
		pgtype.Text{String: "Morning", Valid: true},
		pgtype.Text{String: "scheduled", Valid: true},
		pgtype.Text{String: "0 8 * * *", Valid: true},
		pgtype.Text{String: "owner-1", Valid: true},
		pgtype.Bool{Bool: true, Valid: true},
		pgtype.Timestamptz{Time: nextRunAt, Valid: true},
		"dag-1",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestUpdateDAGPatch_NilFieldsBecomeSQLNullArgs(t *testing.T) {
	t.Parallel()

	db := &captureUpdateDAGPatchDB{rows: 1}
	store := NewStore(db)
	updater := store.(interface {
		UpdateDAGPatch(context.Context, UpdateDAGPatchInput) (int64, error)
	})

	rows, err := updater.UpdateDAGPatch(context.Background(), UpdateDAGPatchInput{DagKey: "dag-1"})
	if err != nil {
		t.Fatalf("UpdateDAGPatch() error = %v", err)
	}
	if rows != 1 {
		t.Fatalf("UpdateDAGPatch() rows = %d, want 1", rows)
	}
	assertUpdateDAGPatchNullArgs(t, db.args)
}

func assertUpdateDAGPatchNullArgs(t *testing.T, args []any) {
	t.Helper()

	if got, want := len(args), 8; got != want {
		t.Fatalf("args len = %d, want %d", got, want)
	}
	for i := 0; i < 5; i++ {
		assertInvalidTextArg(t, args, i)
	}
	assertInvalidBoolArg(t, args, 5)
	assertInvalidTimestamptzArg(t, args, 6)
	if args[7] != "dag-1" {
		t.Fatalf("args[7] = %#v, want dag key", args[7])
	}
}

func assertInvalidTextArg(t *testing.T, args []any, index int) {
	t.Helper()

	arg, ok := args[index].(pgtype.Text)
	if !ok || arg.Valid {
		t.Fatalf("args[%d] = %#v, want invalid pgtype.Text", index, args[index])
	}
}

func assertInvalidBoolArg(t *testing.T, args []any, index int) {
	t.Helper()

	arg, ok := args[index].(pgtype.Bool)
	if !ok || arg.Valid {
		t.Fatalf("args[%d] = %#v, want invalid pgtype.Bool", index, args[index])
	}
}

func assertInvalidTimestamptzArg(t *testing.T, args []any, index int) {
	t.Helper()

	arg, ok := args[index].(pgtype.Timestamptz)
	if !ok || arg.Valid {
		t.Fatalf("args[%d] = %#v, want invalid pgtype.Timestamptz", index, args[index])
	}
}

func TestGetDAGSchedule_ReadsRawScheduleColumns(t *testing.T) {
	t.Parallel()

	db := &captureUpdateDAGPatchDB{
		schedule: DAGSchedule{Trigger: "scheduled", CronExpr: "0 8 * * *"},
	}
	store := NewStore(db)
	reader := store.(interface {
		GetDAGSchedule(context.Context, string) (DAGSchedule, error)
	})

	got, err := reader.GetDAGSchedule(context.Background(), "dag-1")
	if err != nil {
		t.Fatalf("GetDAGSchedule() error = %v", err)
	}
	if got.Trigger != "scheduled" || got.CronExpr != "0 8 * * *" {
		t.Fatalf("GetDAGSchedule() = %+v, want scheduled cron", got)
	}
	if !strings.Contains(db.query, "SELECT trigger, cron_expr") || !strings.Contains(db.query, "FROM task_dags") {
		t.Fatalf("GetDAGSchedule SQL must read trigger/cron_expr:\n%s", db.query)
	}
	if got, want := len(db.queryArgs), 1; got != want {
		t.Fatalf("query args len = %d, want %d", got, want)
	}
	if db.queryArgs[0] != "dag-1" {
		t.Fatalf("query args = %#v, want dag key", db.queryArgs)
	}
}

type captureUpdateDAGPatchDB struct {
	sql       string
	args      []any
	query     string
	queryArgs []any
	rows      int64
	err       error
	rowErr    error
	schedule  DAGSchedule
}

func (db *captureUpdateDAGPatchDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	db.sql = sql
	db.args = append([]any(nil), args...)
	if db.err != nil {
		return pgconn.CommandTag{}, db.err
	}
	return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", db.rows)), nil
}

func (*captureUpdateDAGPatchDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected Query call")
}

func (db *captureUpdateDAGPatchDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	db.query = sql
	db.queryArgs = append([]any(nil), args...)
	return captureUpdateDAGPatchRow{schedule: db.schedule, err: db.rowErr}
}

type captureUpdateDAGPatchRow struct {
	schedule DAGSchedule
	err      error
}

func (r captureUpdateDAGPatchRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 2 {
		return fmt.Errorf("scan dest len = %d, want 2", len(dest))
	}
	trigger, ok := dest[0].(*string)
	if !ok {
		return fmt.Errorf("scan dest[0] type = %T, want *string", dest[0])
	}
	cronExpr, ok := dest[1].(*string)
	if !ok {
		return fmt.Errorf("scan dest[1] type = %T, want *string", dest[1])
	}
	*trigger = r.schedule.Trigger
	*cronExpr = r.schedule.CronExpr
	return nil
}
