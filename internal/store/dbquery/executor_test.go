package dbquery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

type validateQueryCase struct {
	name        string
	query       string
	argCount    int
	wantErrText string
}

func TestIsAllowedTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want bool
	}{
		{name: "agent_interactions", want: true},
		{name: "agent_provider_binding", want: true},
		{name: "agent_status", want: true},
		{name: "agent_threads", want: true},
		{name: "audit_events", want: true},
		{name: "bus_exception_logs", want: true},
		{name: "command_card_runs", want: true},
		{name: "command_card_versions", want: true},
		{name: "command_cards", want: true},
		{name: "cwd_instance_locks", want: true},
		{name: "prompt_templates", want: true},
		{name: "prompt_versions", want: true},
		{name: "shared_files", want: true},
		{name: "system_logs", want: true},
		{name: "task_dag_nodes", want: true},
		{name: "task_dag_runs", want: true},
		{name: "task_dags", want: true},
		{name: "topology_approvals", want: true},
		{name: "ui_preferences", want: true},
		{name: "workspace_run_files", want: true},
		{name: "workspace_runs", want: true},
		{name: "sqlite_master", want: false},
		{name: "task_traces", want: false},
		{name: "", want: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isAllowedTable(tc.name); got != tc.want {
				t.Fatalf("isAllowedTable(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestValidateQueryAllowsWhitelistedSelects(t *testing.T) {
	t.Parallel()

	cases := []validateQueryCase{
		{
			name:     "allows whitelisted cte query",
			query:    "WITH running AS (SELECT thread_id FROM agent_threads WHERE status = ?) SELECT thread_id FROM running",
			argCount: 1,
		},
		{
			name:  "allows whitelisted table query",
			query: "SELECT * FROM agent_threads",
		},
		{
			name:  "allows whitelisted comma join",
			query: "SELECT agent_threads.thread_id FROM agent_threads, agent_status",
		},
		{
			name:  "allows quoted whitelisted table query",
			query: `SELECT * FROM "agent_threads"`,
		},
		{
			name:  "allows whitelisted subquery",
			query: "SELECT * FROM (SELECT thread_id FROM agent_threads) AS threads",
		},
		{
			name:     "allows sqlite positional placeholders",
			query:    "SELECT * FROM agent_threads WHERE thread_id = ? AND score > ?",
			argCount: 2,
		},
	}
	runValidateQueryCases(t, cases)
}

func TestValidateQueryRejectsUnauthorizedTables(t *testing.T) {
	t.Parallel()

	cases := []validateQueryCase{
		{
			name:        "direct disallowed table",
			query:       "SELECT * FROM sqlite_master",
			wantErrText: "disallowed",
		},
		{
			name:        "disallowed comma join table",
			query:       "SELECT sqlite_master.sql FROM agent_threads, sqlite_master",
			wantErrText: "disallowed",
		},
		{
			name:        "schema qualified table",
			query:       "SELECT * FROM main.agent_threads",
			wantErrText: "disallowed",
		},
		{
			name:        "quoted schema qualified table",
			query:       `SELECT * FROM "main"."agent_threads"`,
			wantErrText: "disallowed",
		},
		{
			name:        "subquery over disallowed table",
			query:       "SELECT * FROM (SELECT sql FROM sqlite_master) AS leaked",
			wantErrText: "disallowed",
		},
		{
			name:        "table valued function",
			query:       "SELECT * FROM json_each('[1,2]')",
			wantErrText: "disallowed",
		},
		{
			name:        "table valued function comma join",
			query:       "SELECT agent_threads.thread_id FROM agent_threads, json_each('[1,2]')",
			wantErrText: "disallowed",
		},
		{
			name:        "cte over disallowed comma join table",
			query:       "WITH leaked AS (SELECT sqlite_master.sql FROM agent_threads, sqlite_master) SELECT sql FROM leaked",
			wantErrText: "disallowed",
		},
		{
			name:        "removed task trace page table",
			query:       "SELECT * FROM task_traces",
			wantErrText: "disallowed",
		},
	}
	runValidateQueryCases(t, cases)
}

func TestValidateQueryRejectsInvalidStatementsAndPlaceholders(t *testing.T) {
	t.Parallel()

	cases := []validateQueryCase{
		{
			name:        "postgres placeholder style",
			query:       "SELECT * FROM agent_threads WHERE thread_id = $1 AND score > ?",
			argCount:    2,
			wantErrText: "only supports SQLite ? placeholders",
		},
		{
			name:        "mutation statement",
			query:       "UPDATE agent_threads SET status = ? WHERE thread_id = ?",
			argCount:    2,
			wantErrText: "only supports SELECT",
		},
		{
			name:        "dangerous function without table",
			query:       "SELECT load_extension('anything')",
			wantErrText: "disallowed function",
		},
		{
			name:        "tableless constant query",
			query:       "SELECT 1",
			wantErrText: "must reference at least one allowed table",
		},
		{
			name:        "placeholder mismatch",
			query:       "SELECT * FROM agent_threads WHERE status = ? AND score > ?",
			argCount:    1,
			wantErrText: "expected 2 args",
		},
	}
	runValidateQueryCases(t, cases)
}

func runValidateQueryCases(t *testing.T, cases []validateQueryCase) {
	t.Helper()
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

func TestValidateQueryRejectsSQLiteDangerousStatements(t *testing.T) {
	t.Parallel()

	queries := []string{
		" pRaGmA table_info(agent_threads)",
		"ATTACH DATABASE 'x.db' AS extra",
		"DETACH DATABASE extra",
		"VACUUM INTO 'copy.db'",
		"REINDEX agent_threads",
		"ALTER TABLE agent_threads ADD COLUMN owned TEXT",
		"CREATE TEMP TABLE temp_owned(id INTEGER)",
		"DROP TABLE agent_threads",
		"INSERT INTO agent_threads(thread_id) VALUES ('x')",
		"UPDATE agent_threads SET status = 'done'",
		"DELETE FROM agent_threads",
		"REPLACE INTO agent_threads(thread_id) VALUES ('x')",
		"WITH deleted AS (DELETE FROM agent_threads RETURNING thread_id) SELECT thread_id FROM deleted",
		"INSERT INTO agent_threads(thread_id) VALUES ('x') RETURNING thread_id",
		"SELECT * FROM agent_threads; SELECT * FROM agent_status",
		"SELECT * FROM agent_threads -- comment smuggling",
		"SELECT load_extension('x') FROM agent_threads",
	}
	for _, query := range queries {
		query := query
		t.Run(query, func(t *testing.T) {
			t.Parallel()

			if err := validateQuery(query, strings.Count(query, "$")); err == nil {
				t.Fatalf("validateQuery(%q) error = nil, want rejection", query)
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

func TestValidateQueryAllowsDAGSnapshotTables(t *testing.T) {
	t.Parallel()

	queries := []string{
		"SELECT * FROM task_dags",
		"SELECT version FROM task_dags",
		"SELECT * FROM task_dag_runs WHERE dag_key = ?",
		"SELECT * FROM task_dag_nodes WHERE dag_key = ?",
	}
	for _, query := range queries {
		query := query
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			argCount := strings.Count(query, "?")
			if err := validateQuery(query, argCount); err != nil {
				t.Fatalf("validateQuery(%q) error = %v", query, err)
			}
		})
	}
}

func TestValidateQueryRejectsExpandedDangerousFunctions(t *testing.T) {
	t.Parallel()

	queries := []string{
		"SELECT load_extension('x') FROM agent_threads",
		"SELECT writefile('/tmp/x','x') FROM agent_threads",
		"SELECT current_user FROM agent_threads",
		"SELECT last_insert_rowid() FROM agent_threads",
		"SELECT changes() FROM agent_threads",
		"SELECT total_changes() FROM agent_threads",
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

func TestExecuteQueryReturnsRowsFromSQLiteReadOnlyConnection(t *testing.T) {
	t.Parallel()

	db := newDBQuerySQLiteDB(t)
	rows, err := executeQuery(context.Background(), db, defaultQueryTimeout, "SELECT thread_id, status FROM agent_threads WHERE thread_id = ?", "thread-1")
	if err != nil {
		t.Fatalf("executeQuery() error = %v", err)
	}
	if len(rows) != 1 || rows[0]["thread_id"] != "thread-1" || rows[0]["status"] != "running" {
		t.Fatalf("executeQuery() rows = %#v", rows)
	}
}

func TestExecuteQueryBindsParameters(t *testing.T) {
	t.Parallel()

	db := newDBQuerySQLiteDB(t)
	rows, err := executeQuery(context.Background(), db, defaultQueryTimeout, "SELECT thread_id FROM agent_threads WHERE thread_id = ?", "thread-1' OR 1=1 --")
	if err != nil {
		t.Fatalf("executeQuery() error = %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("executeQuery() rows = %#v, want no SQL injection match", rows)
	}
}

func TestExecuteQueryRejectsQueryableWithoutDedicatedConnection(t *testing.T) {
	t.Parallel()

	queryer := &unsupportedQueryable{}
	_, err := executeQuery(context.Background(), queryer, defaultQueryTimeout, "SELECT * FROM agent_threads")
	if err == nil || !strings.Contains(err.Error(), "dedicated SQLite connection") {
		t.Fatalf("executeQuery() error = %v, want fail-closed dedicated connection error", err)
	}
	if queryer.called {
		t.Fatal("executeQuery() called QueryContext on unsupported queryer")
	}
}

func TestExecuteQueryRejectsParserErrorBeforeRows(t *testing.T) {
	t.Parallel()

	db := newDBQuerySQLiteDB(t)
	_, err := executeQuery(context.Background(), db, defaultQueryTimeout, "SELECT * FROM agent_threads WHERE")
	if err == nil || !strings.Contains(err.Error(), "parse dbquery SQL") {
		t.Fatalf("executeQuery() error = %v, want parser error before rows", err)
	}
}

func TestOpenReadOnlyRowsEnforcesSQLiteQueryOnly(t *testing.T) {
	t.Parallel()

	db := newDBQuerySQLiteDB(t)
	rows, finish, err := openSQLiteReadOnlyRows(context.Background(), db, "INSERT INTO agent_threads(thread_id, status, score) VALUES ('owned', 'running', 1)")
	if err == nil {
		_ = finish(false)
		rows.Close()
		t.Fatal("openSQLiteReadOnlyRows() error = nil, want query_only write rejection")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "readonly") && !strings.Contains(strings.ToLower(err.Error()), "read-only") {
		t.Fatalf("openSQLiteReadOnlyRows() error = %v, want read-only rejection", err)
	}
	assertCanWriteAfterDBQuery(t, db)
}

func TestQueryRowLimit(t *testing.T) {
	t.Parallel()

	db := newDBQuerySQLiteDB(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	for i := 0; i < maxQueryRows+1; i++ {
		if _, err := tx.Exec("INSERT INTO agent_threads(thread_id, status, score) VALUES (?, 'bulk', 0)", fmt.Sprintf("bulk-%05d", i)); err != nil {
			_ = tx.Rollback()
			t.Fatalf("insert bulk row %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	_, err = executeQuery(context.Background(), db, defaultQueryTimeout, fmt.Sprintf("SELECT thread_id FROM agent_threads LIMIT %d", maxQueryRows+1))
	if err == nil || !strings.Contains(err.Error(), "row limit") {
		t.Fatalf("executeQuery() error = %v, want row limit", err)
	}
}

func TestExecuteQueryUsesInjectedLimitForExecution(t *testing.T) {
	t.Parallel()

	db := newDBQuerySQLiteDB(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	for i := 0; i < maxQueryRows+1; i++ {
		if _, err := tx.Exec("INSERT INTO agent_threads(thread_id, status, score) VALUES (?, 'bulk', 0)", fmt.Sprintf("implicit-%05d", i)); err != nil {
			_ = tx.Rollback()
			t.Fatalf("insert implicit limit row %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	rows, err := executeQuery(context.Background(), db, defaultQueryTimeout, "SELECT thread_id FROM agent_threads ORDER BY thread_id")
	if err != nil {
		t.Fatalf("executeQuery() error = %v, want injected SQL limit to bound the result", err)
	}
	if len(rows) != maxQueryRows {
		t.Fatalf("executeQuery() rows = %d, want injected limit %d", len(rows), maxQueryRows)
	}
}

func TestInjectLimitIfMissingDetectsExistingLimitAfterNewline(t *testing.T) {
	t.Parallel()

	query := "SELECT thread_id FROM agent_threads\nLIMIT ?"
	got := injectLimitIfMissing(query, maxQueryRows)
	if got != query {
		t.Fatalf("injectLimitIfMissing() = %q, want existing LIMIT unchanged", got)
	}
}

func newDBQuerySQLiteDB(t *testing.T) *sql.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dbquery.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close sqlite: %v", err)
		}
	})
	if _, err := db.Exec(`
CREATE TABLE agent_threads (
	thread_id TEXT PRIMARY KEY,
	status TEXT NOT NULL,
	score REAL NOT NULL DEFAULT 0
);
INSERT INTO agent_threads(thread_id, status, score) VALUES
	('thread-1', 'running', 7),
	('thread-2', 'idle', 1);
`); err != nil {
		t.Fatalf("seed sqlite: %v", err)
	}
	return db
}

func assertCanWriteAfterDBQuery(t *testing.T, db *sql.DB) {
	t.Helper()

	if _, err := db.Exec("INSERT INTO agent_threads(thread_id, status, score) VALUES ('after-query-only', 'running', 1)"); err != nil {
		t.Fatalf("write after dbquery query_only cleanup error = %v", err)
	}
}

type unsupportedQueryable struct {
	called bool
}

func (*unsupportedQueryable) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("unsupportedQueryable: exec not implemented")
}

func (q *unsupportedQueryable) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	q.called = true
	return nil, errors.New("unsupportedQueryable: query should not be called")
}

func (*unsupportedQueryable) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}
