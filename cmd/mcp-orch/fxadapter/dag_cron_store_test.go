package fxadapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	orchcron "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/cron"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestSQLDAGScheduleStore_DueDAGsFiltersScheduledTrigger(t *testing.T) {
	t.Parallel()

	db := &captureScheduleQueryDB{}
	store, err := NewSQLDAGScheduleStore(sqlc.New(db))
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

func TestSQLDAGScheduleStore_UpdateNextRunUsesDueAtFenceAndRows(t *testing.T) {
	t.Parallel()

	db := &captureScheduleExecDB{tag: pgconn.NewCommandTag("UPDATE 1")}
	store, err := NewSQLDAGScheduleStore(sqlc.New(db))
	if err != nil {
		t.Fatalf("NewSQLDAGScheduleStore() error = %v", err)
	}
	dueAt := time.Unix(1700000000, 0).UTC()
	nextRunAt := dueAt.Add(time.Hour)
	if err := store.UpdateNextRun(context.Background(), "dag-1", dueAt, nextRunAt); err != nil {
		t.Fatalf("UpdateNextRun() error = %v", err)
	}
	if !strings.Contains(db.sql, "trigger = 'scheduled'") {
		t.Fatalf("UpdateNextRun SQL must fence scheduled trigger:\n%s", db.sql)
	}
	if !strings.Contains(db.sql, "cron_expr <> ''") {
		t.Fatalf("UpdateNextRun SQL must fence enabled cron expr:\n%s", db.sql)
	}
	if !strings.Contains(db.sql, "next_run_at = $3") {
		t.Fatalf("UpdateNextRun SQL must fence the scanned due time:\n%s", db.sql)
	}
	if len(db.args) != 3 {
		t.Fatalf("UpdateNextRun args = %#v, want nextRunAt, dagKey, dueAt", db.args)
	}
	if got := db.args[1]; got != "dag-1" {
		t.Fatalf("UpdateNextRun dagKey arg = %#v, want dag-1", got)
	}
	if got := db.args[2].(pgtype.Timestamptz).Time; !got.Equal(dueAt) {
		t.Fatalf("UpdateNextRun dueAt arg = %s, want %s", got, dueAt)
	}
}

func TestSQLDAGScheduleStore_UpdateNextRunRejectsStaleSchedule(t *testing.T) {
	t.Parallel()

	db := &captureScheduleExecDB{tag: pgconn.NewCommandTag("UPDATE 0")}
	store, err := NewSQLDAGScheduleStore(sqlc.New(db))
	if err != nil {
		t.Fatalf("NewSQLDAGScheduleStore() error = %v", err)
	}
	err = store.UpdateNextRun(context.Background(), "dag-1", time.Unix(1700000000, 0), time.Unix(1700003600, 0))
	if !errors.Is(err, orchcron.ErrScheduleStateChanged) {
		t.Fatalf("UpdateNextRun() error = %v, want ErrScheduleStateChanged", err)
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

func (*captureScheduleQueryDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return errScheduleRow{err: fmt.Errorf("unexpected QueryRow call")}
}

func (*captureScheduleQueryDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec call")
}

type errScheduleRow struct {
	err error
}

func (r errScheduleRow) Scan(...any) error { return r.err }

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

type captureScheduleExecDB struct {
	sql  string
	args []any
	tag  pgconn.CommandTag
	err  error
}

func (*captureScheduleExecDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected Query call")
}

func (*captureScheduleExecDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return errScheduleRow{err: fmt.Errorf("unexpected QueryRow call")}
}

func (db *captureScheduleExecDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	db.sql = sql
	db.args = append([]any(nil), args...)
	return db.tag, db.err
}

func TestPGAdvisoryLockHandle_UnlockFailureClosesHijackedConnection(t *testing.T) {
	unlockErr := errors.New("unlock failed")
	conn := &fakeAdvisoryLockConn{unlockErr: unlockErr}
	handle := &pgAdvisoryLockHandle{conn: conn, lockID: 42}

	err := handle.Unlock(context.Background())
	if !errors.Is(err, unlockErr) {
		t.Fatalf("Unlock() error = %v, want unlockErr", err)
	}
	if conn.releaseCalls != 0 {
		t.Fatalf("releaseCalls = %d, want 0 after failed unlock", conn.releaseCalls)
	}
	if conn.hijackCloseCalls != 1 {
		t.Fatalf("hijackCloseCalls = %d, want 1", conn.hijackCloseCalls)
	}
}

type fakeAdvisoryLockConn struct {
	acquired         bool
	tryErr           error
	unlocked         bool
	unlockErr        error
	releaseCalls     int
	hijackCloseCalls int
}

func (c *fakeAdvisoryLockConn) tryAdvisoryLock(context.Context, int64) (bool, error) {
	return c.acquired, c.tryErr
}

func (c *fakeAdvisoryLockConn) unlockAdvisoryLock(context.Context, int64) (bool, error) {
	return c.unlocked, c.unlockErr
}

func (c *fakeAdvisoryLockConn) release() {
	c.releaseCalls++
}

func (c *fakeAdvisoryLockConn) hijackAndClose(context.Context) error {
	c.hijackCloseCalls++
	return nil
}
