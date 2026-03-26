package dbquery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestValidateQuery(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		query       string
		argCount    int
		wantErrText string
	}{
		{
			name:     "allows whitelisted cte query",
			query:    "WITH running AS (SELECT thread_id FROM agent_threads WHERE status = $1) SELECT thread_id FROM running",
			argCount: 1,
		},
		{
			name:  "allows whitelisted table query",
			query: "SELECT * FROM agent_threads",
		},
		{
			name:        "rejects mutation statement",
			query:       "UPDATE agent_threads SET status = $1 WHERE thread_id = $2",
			argCount:    2,
			wantErrText: "only supports SELECT",
		},
		{
			name:        "rejects dangerous function without table",
			query:       "SELECT pg_sleep(100)",
			wantErrText: "disallowed function",
		},
		{
			name:        "rejects tableless constant query",
			query:       "SELECT 1",
			wantErrText: "must reference at least one allowed table",
		},
		{
			name:        "rejects disallowed table",
			query:       "SELECT * FROM pg_stat_activity",
			wantErrText: "disallowed function",
		},
		{
			name:        "rejects placeholder mismatch",
			query:       "SELECT * FROM agent_threads WHERE status = $2",
			argCount:    1,
			wantErrText: "expected 2 args",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validateQuery(tc.query, tc.argCount)
			if tc.wantErrText == "" {
				if err != nil {
					t.Fatalf("validateQuery() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrText) {
				t.Fatalf("validateQuery() error = %v, want substring %q", err, tc.wantErrText)
			}
		})
	}
}

func TestCTEBypassBlocked(t *testing.T) {
	t.Parallel()

	err := validateQuery("WITH running AS (SELECT thread_id FROM agent_threads) SELECT current_timestamp", 0)
	if err == nil || !strings.Contains(err.Error(), "outer SELECT must reference a table") {
		t.Fatalf("validateQuery() error = %v", err)
	}
}

func TestValidateQueryRejectsExpandedDangerousFunctions(t *testing.T) {
	t.Parallel()

	queries := []string{
		"SELECT version() FROM agent_threads",
		"SELECT current_setting('server_version') FROM agent_threads",
		"SELECT current_user FROM agent_threads",
		"SELECT inet_server_addr() FROM agent_threads",
		"SELECT inet_server_port() FROM agent_threads",
		"SELECT pg_read_file('/etc/passwd') FROM agent_threads",
		"SELECT pg_read_binary_file('/etc/passwd') FROM agent_threads",
		"SELECT pg_ls_dir('.') FROM agent_threads",
		"SELECT pg_stat_get_activity(1) FROM agent_threads",
		"SELECT lo_import('/tmp/file') FROM agent_threads",
		"SELECT lo_export(1, '/tmp/file') FROM agent_threads",
	}
	for _, query := range queries {
		query := query
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			if err := validateQuery(query, 0); err == nil || !strings.Contains(err.Error(), "disallowed function") {
				t.Fatalf("validateQuery(%q) error = %v", query, err)
			}
		})
	}
}

func TestNormalizeArgsFloat64ToInt64(t *testing.T) {
	t.Parallel()

	args := normalizeArgs([]any{float64(42), float64(3.5), []any{float64(9)}, map[string]any{"nested": float64(7)}})
	if got, ok := args[0].(int64); !ok || got != 42 {
		t.Fatalf("args[0] = %#v", args[0])
	}
	if got, ok := args[1].(float64); !ok || got != 3.5 {
		t.Fatalf("args[1] = %#v", args[1])
	}
	nested, ok := args[2].([]any)
	if !ok || len(nested) != 1 || nested[0] != int64(9) {
		t.Fatalf("args[2] = %#v", args[2])
	}
	mapped, ok := args[3].(map[string]any)
	if !ok || mapped["nested"] != int64(7) {
		t.Fatalf("args[3] = %#v", args[3])
	}
}

func TestExecuteQueryNormalizesArgs(t *testing.T) {
	t.Parallel()

	queryer := &captureQueryer{rows: emptyRows()}
	_, err := executeQuery(context.Background(), queryer, defaultQueryTimeout, "SELECT * FROM agent_threads WHERE thread_id = $1 AND score > $2", float64(7), float64(1.5))
	if err != nil {
		t.Fatalf("executeQuery() error = %v", err)
	}
	if got, ok := queryer.args[0].(int64); !ok || got != 7 {
		t.Fatalf("args[0] = %#v", queryer.args[0])
	}
	if got, ok := queryer.args[1].(float64); !ok || got != 1.5 {
		t.Fatalf("args[1] = %#v", queryer.args[1])
	}
}

func TestExecuteQueryAppliesDBQueryTimeout(t *testing.T) {
	t.Parallel()

	_, err := executeQuery(context.Background(), timeoutQueryer{t: t}, defaultQueryTimeout, "SELECT * FROM agent_threads")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("executeQuery() error = %v, want %v", err, context.DeadlineExceeded)
	}
}

func TestExecuteQueryUsesReadOnlyTransaction(t *testing.T) {
	t.Parallel()

	tx := &captureTx{rows: emptyRows()}
	queryer := &beginTxQueryer{tx: tx}
	_, err := executeQuery(context.Background(), queryer, defaultQueryTimeout, "SELECT * FROM agent_threads WHERE thread_id = $1", "thread-1")
	if err != nil {
		t.Fatalf("executeQuery() error = %v", err)
	}
	if queryer.beginOptions.AccessMode != pgx.ReadOnly {
		t.Fatalf("BeginTx() access mode = %q, want %q", queryer.beginOptions.AccessMode, pgx.ReadOnly)
	}
	if !tx.committed || tx.rolledBack {
		t.Fatalf("transaction state commit=%v rollback=%v", tx.committed, tx.rolledBack)
	}
	if tx.querySQL != "SELECT * FROM agent_threads WHERE thread_id = $1" {
		t.Fatalf("tx.Query() sql = %q", tx.querySQL)
	}
}

func TestExecuteQueryRollsBackReadOnlyTransactionOnError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("query failed")
	queryer := &beginTxQueryer{tx: &captureTx{err: wantErr}}
	_, err := executeQuery(context.Background(), queryer, defaultQueryTimeout, "SELECT * FROM agent_threads")
	if !errors.Is(err, wantErr) {
		t.Fatalf("executeQuery() error = %v, want %v", err, wantErr)
	}
	if queryer.beginOptions.AccessMode != pgx.ReadOnly {
		t.Fatalf("BeginTx() access mode = %q, want %q", queryer.beginOptions.AccessMode, pgx.ReadOnly)
	}
	if !queryer.tx.rolledBack || queryer.tx.committed {
		t.Fatalf("transaction state commit=%v rollback=%v", queryer.tx.committed, queryer.tx.rolledBack)
	}
}

func TestExecuteQueryRollsBackReadOnlyTransactionOnTimeout(t *testing.T) {
	t.Parallel()

	queryer := &beginTxQueryer{tx: &captureTx{
		queryFn: func(ctx context.Context, _ string, _ ...any) (pgx.Rows, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}}

	_, err := executeQuery(context.Background(), queryer, 50*time.Millisecond, "SELECT * FROM agent_threads")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("executeQuery() error = %v, want %v", err, context.DeadlineExceeded)
	}
	if queryer.beginOptions.AccessMode != pgx.ReadOnly {
		t.Fatalf("BeginTx() access mode = %q, want %q", queryer.beginOptions.AccessMode, pgx.ReadOnly)
	}
	if !queryer.tx.rolledBack || queryer.tx.committed {
		t.Fatalf("transaction state commit=%v rollback=%v", queryer.tx.committed, queryer.tx.rolledBack)
	}
}

func TestQueryRowLimit(t *testing.T) {
	t.Parallel()

	rows := make([][]any, maxQueryRows+1)
	for i := range rows {
		rows[i] = []any{fmt.Sprintf("thread-%d", i)}
	}
	_, err := executeQuery(context.Background(), &captureQueryer{rows: &stubRows{fields: defaultFields(), values: rows}}, defaultQueryTimeout, "SELECT * FROM agent_threads")
	if err == nil || !strings.Contains(err.Error(), "row limit") {
		t.Fatalf("executeQuery() error = %v", err)
	}
}

type timeoutQueryer struct {
	t *testing.T
}

func (q timeoutQueryer) Query(ctx context.Context, _ string, _ ...any) (pgx.Rows, error) {
	q.t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok {
		q.t.Fatal("Query() context missing deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > defaultQueryTimeout+time.Second {
		q.t.Fatalf("Query() deadline = %v, remaining = %v", deadline, remaining)
	}
	return nil, context.DeadlineExceeded
}

func (timeoutQueryer) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("timeoutQueryer: exec not implemented")
}

func (timeoutQueryer) QueryRow(context.Context, string, ...any) pgx.Row { return nil }

type captureQueryer struct {
	args []any
	rows pgx.Rows
	err  error
}

func (*captureQueryer) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("captureQueryer: exec not implemented")
}

func (q *captureQueryer) Query(_ context.Context, _ string, args ...any) (pgx.Rows, error) {
	q.args = append([]any(nil), args...)
	if q.err != nil {
		return nil, q.err
	}
	if q.rows != nil {
		return q.rows, nil
	}
	return emptyRows(), nil
}

func (*captureQueryer) QueryRow(context.Context, string, ...any) pgx.Row { return nil }

type beginTxQueryer struct {
	tx           *captureTx
	beginErr     error
	beginOptions pgx.TxOptions
}

func (*beginTxQueryer) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("beginTxQueryer: exec not implemented")
}

func (*beginTxQueryer) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("beginTxQueryer: direct query should not be used")
}

func (*beginTxQueryer) QueryRow(context.Context, string, ...any) pgx.Row { return nil }

func (q *beginTxQueryer) BeginTx(_ context.Context, txOptions pgx.TxOptions) (pgx.Tx, error) {
	q.beginOptions = txOptions
	if q.beginErr != nil {
		return nil, q.beginErr
	}
	if q.tx == nil {
		q.tx = &captureTx{rows: emptyRows()}
	}
	return q.tx, nil
}

type captureTx struct {
	rows       pgx.Rows
	err        error
	querySQL   string
	queryFn    func(context.Context, string, ...any) (pgx.Rows, error)
	committed  bool
	rolledBack bool
}

func (*captureTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("captureTx: begin not implemented")
}

func (tx *captureTx) Commit(context.Context) error {
	tx.committed = true
	return nil
}

func (tx *captureTx) Rollback(context.Context) error {
	tx.rolledBack = true
	return nil
}

func (*captureTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("captureTx: copyfrom not implemented")
}

func (*captureTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }

func (*captureTx) LargeObjects() pgx.LargeObjects { return pgx.LargeObjects{} }

func (*captureTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("captureTx: prepare not implemented")
}

func (*captureTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("captureTx: exec not implemented")
}

func (tx *captureTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	tx.querySQL = sql
	if tx.queryFn != nil {
		return tx.queryFn(ctx, sql, args...)
	}
	if tx.err != nil {
		return nil, tx.err
	}
	if tx.rows != nil {
		return tx.rows, nil
	}
	return emptyRows(), nil
}

func (*captureTx) QueryRow(context.Context, string, ...any) pgx.Row { return nil }

func (*captureTx) Conn() *pgx.Conn { return nil }

type stubRows struct {
	fields []pgconn.FieldDescription
	values [][]any
	index  int
	err    error
}

func (r *stubRows) Close() {}

func (r *stubRows) Err() error { return r.err }

func (r *stubRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func (r *stubRows) FieldDescriptions() []pgconn.FieldDescription { return r.fields }

func (r *stubRows) Next() bool {
	if r.index >= len(r.values) {
		return false
	}
	r.index++
	return true
}

func (r *stubRows) Scan(...any) error { return errors.New("stubRows: scan not implemented") }

func (r *stubRows) Values() ([]any, error) {
	if r.index == 0 || r.index > len(r.values) {
		return nil, errors.New("stubRows: invalid cursor")
	}
	return append([]any(nil), r.values[r.index-1]...), nil
}

func (r *stubRows) RawValues() [][]byte { return nil }

func (r *stubRows) Conn() *pgx.Conn { return nil }

func emptyRows() pgx.Rows {
	return &stubRows{fields: defaultFields(), values: nil}
}

func defaultFields() []pgconn.FieldDescription {
	return []pgconn.FieldDescription{{Name: "thread_id"}}
}
