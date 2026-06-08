package fxadapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

func (r *emptyScheduleRows) Close()                                     { _ = r }
func (*emptyScheduleRows) Err() error                                   { return nil }
func (*emptyScheduleRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (*emptyScheduleRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (*emptyScheduleRows) RawValues() [][]byte                          { return nil }
func (*emptyScheduleRows) Conn() *pgx.Conn                              { return nil }
func (*emptyScheduleRows) Next() bool                                   { return false }
func (*emptyScheduleRows) Scan(...any) error                            { return fmt.Errorf("unexpected Scan call") }
func (*emptyScheduleRows) Values() ([]any, error)                       { return nil, nil }

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
