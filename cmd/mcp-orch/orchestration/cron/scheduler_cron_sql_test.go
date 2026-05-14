package cron

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestSQLDAGScheduleStore_DueDAGsFiltersScheduledTrigger(t *testing.T) {
	t.Parallel()

	db := &captureScheduleQueryDB{}
	store, err := NewSQLDAGScheduleStore(db)
	if err != nil {
		t.Fatalf("NewSQLDAGScheduleStore() error = %v", err)
	}
	if _, err := store.DueDAGs(context.Background(), time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("DueDAGs() error = %v", err)
	}
	if !strings.Contains(db.query, "trigger = 'scheduled'") {
		t.Fatalf("DueDAGs SQL must filter trigger='scheduled':\n%s", db.query)
	}
}

type captureScheduleQueryDB struct {
	query string
	args  []any
}

func (db *captureScheduleQueryDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	db.query = sql
	db.args = append([]any(nil), args...)
	return &emptyScheduleRows{}, nil
}

func (*captureScheduleQueryDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec call")
}

type emptyScheduleRows struct{}

func (*emptyScheduleRows) Close()                                       {}
func (*emptyScheduleRows) Err() error                                   { return nil }
func (*emptyScheduleRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (*emptyScheduleRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (*emptyScheduleRows) RawValues() [][]byte                          { return nil }
func (*emptyScheduleRows) Conn() *pgx.Conn                              { return nil }
func (*emptyScheduleRows) Next() bool                                   { return false }
func (*emptyScheduleRows) Scan(...any) error                            { return fmt.Errorf("unexpected Scan call") }
func (*emptyScheduleRows) Values() ([]any, error)                       { return nil, nil }
