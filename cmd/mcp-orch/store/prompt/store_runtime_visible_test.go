package prompt

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestListRuntimeVisibleRequiresCWD(t *testing.T) {
	t.Parallel()

	db := &capturePromptListDB{}
	store := NewStore(sqlc.New(db))

	_, err := store.List(context.Background(), ListFilter{RuntimeVisible: true, Limit: 10})
	if err == nil || !strings.Contains(err.Error(), "cwd is required") {
		t.Fatalf("List() error = %v, want cwd required", err)
	}
	if db.query != "" {
		t.Fatalf("query executed despite missing cwd:\n%s", db.query)
	}
}

func TestListRuntimeVisibleUsesScopedEnabledQuery(t *testing.T) {
	t.Parallel()

	ts := pgtype.Timestamptz{Time: time.Unix(1, 0).UTC(), Valid: true}
	db := &capturePromptListDB{rows: &stubPromptSectionRows{rows: [][]any{{
		int64(8), "repo-a/prompt", "Repo Prompt", "main", "",
		"body", []byte(`{}`), []byte(`["scope.cwd:/repo/a"]`),
		"desc", "Use in repo A", true, true, []byte(`{}`), int32(10),
		"tester", "tester", ts, ts,
	}}}}
	store := NewStore(sqlc.New(db))

	got, err := store.List(context.Background(), ListFilter{
		Keyword:        " repo ",
		CWD:            " /repo/a ",
		RuntimeVisible: true,
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 1 || got[0].PromptKey != "repo-a/prompt" {
		t.Fatalf("List() = %+v, want scoped prompt", got)
	}
	for _, want := range []string{
		"enabled = TRUE",
		"(tags ? ('scope.cwd:' || $4::text) OR tags ? 'scope.global')",
		"LIMIT $5",
	} {
		if !strings.Contains(db.query, want) {
			t.Fatalf("list SQL missing %q:\n%s", want, db.query)
		}
	}
	wantArgs := []any{"", " repo ", true, "/repo/a", int32(10)}
	if !promptArgsEqual(db.args, wantArgs) {
		t.Fatalf("List() args = %#v, want %#v", db.args, wantArgs)
	}
}

type capturePromptListDB struct {
	query string
	args  []any
	rows  pgx.Rows
	err   error
}

func (*capturePromptListDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec call")
}

func (db *capturePromptListDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	db.query = sql
	db.args = append([]any(nil), args...)
	if db.err != nil {
		return nil, db.err
	}
	if db.rows == nil {
		return &stubPromptSectionRows{}, nil
	}
	return db.rows, nil
}

func (*capturePromptListDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return captureRecallSectionRow{err: fmt.Errorf("unexpected QueryRow call")}
}

func promptArgsEqual(left, right []any) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
