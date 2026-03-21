package dbquery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
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
	_, err := executeQuery(context.Background(), queryer, "SELECT * FROM agent_threads WHERE thread_id = $1 AND score > $2", float64(7), float64(1.5))
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

	_, err := executeQuery(context.Background(), timeoutQueryer{t: t}, "SELECT * FROM agent_threads")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("executeQuery() error = %v, want %v", err, context.DeadlineExceeded)
	}
}

func TestQueryRowLimit(t *testing.T) {
	t.Parallel()

	rows := make([][]any, maxQueryRows+1)
	for i := range rows {
		rows[i] = []any{fmt.Sprintf("thread-%d", i)}
	}
	_, err := executeQuery(context.Background(), &captureQueryer{rows: &stubRows{fields: defaultFields(), values: rows}}, "SELECT * FROM agent_threads")
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
	if remaining <= 0 || remaining > platformconfig.DBQueryTimeout+time.Second {
		q.t.Fatalf("Query() deadline = %v, remaining = %v", deadline, remaining)
	}
	return nil, context.DeadlineExceeded
}

type captureQueryer struct {
	args []any
	rows pgx.Rows
	err  error
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
