//go:build legacy_pg_fake

package workspace

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/jackc/pgx/v5"
)

func TestCreateWorkspaceRun(t *testing.T) {
	finishedAt := timePtr(time.Unix(300, 0).UTC())
	returned := sqlc.WorkspaceRun{
		ID:            7,
		RunKey:        "run-1",
		DagKey:        "dag-1",
		SourceRoot:    "/src",
		WorkspacePath: "/tmp/run-1",
		Status:        "active",
		CreatedBy:     "agent-1",
		UpdatedBy:     "agent-1",
		Metadata:      []byte(`{"kind":"workspace"}`),
		CreatedAt:     timestamptz(time.Unix(301, 0).UTC()),
		UpdatedAt:     timestamptz(time.Unix(302, 0).UTC()),
		FinishedAt:    timestamptz(time.Unix(303, 0).UTC()),
	}
	db := stubWorkspaceDB{
		queryRowFunc: func(_ context.Context, sql string, args ...any) pgx.Row {
			if !strings.Contains(sql, "INSERT INTO workspace_runs") {
				t.Fatalf("QueryRow() sql = %q", sql)
			}
			if len(args) != 9 {
				t.Fatalf("QueryRow() args len = %d, want 9", len(args))
			}
			if got := args[0]; got != "run-1" {
				t.Fatalf("run_key arg = %#v, want run-1", got)
			}
			if got := args[7]; !reflect.DeepEqual(got, []byte(`{"kind":"workspace"}`)) {
				t.Fatalf("metadata arg = %#v", got)
			}
			if got := args[8]; !reflect.DeepEqual(got, sqlc.TimeValuePtr(finishedAt)) {
				t.Fatalf("finished_at arg = %#v, want %#v", got, sqlc.TimeValuePtr(finishedAt))
			}
			return stubWorkspaceRow{values: workspaceRunValues(returned)}
		},
	}

	store := NewStore(db)
	run, err := store.UpsertRun(context.Background(), WorkspaceRun{
		RunKey:        "run-1",
		DagKey:        "dag-1",
		SourceRoot:    "/src",
		WorkspacePath: "/tmp/run-1",
		Status:        "active",
		CreatedBy:     "agent-1",
		UpdatedBy:     "agent-1",
		Metadata:      []byte(`{"kind":"workspace"}`),
		FinishedAt:    finishedAt,
	})
	if err != nil {
		t.Fatalf("UpsertRun() error = %v", err)
	}
	if run.RunKey != "run-1" {
		t.Fatalf("UpsertRun().RunKey = %q, want run-1", run.RunKey)
	}
	if run.FinishedAt == nil || !run.FinishedAt.Equal(returned.FinishedAt.Time) {
		t.Fatalf("UpsertRun().FinishedAt = %#v, want %v", run.FinishedAt, returned.FinishedAt.Time)
	}
}

func TestGetWorkspaceRun(t *testing.T) {
	returned := sqlc.WorkspaceRun{
		ID:            8,
		RunKey:        "run-get",
		DagKey:        "dag-get",
		SourceRoot:    "/repo",
		WorkspacePath: "/tmp/run-get",
		Status:        "merged",
		CreatedBy:     "agent-2",
		UpdatedBy:     "agent-3",
		Metadata:      []byte(`{"done":true}`),
		CreatedAt:     timestamptz(time.Unix(400, 0).UTC()),
		UpdatedAt:     timestamptz(time.Unix(401, 0).UTC()),
	}
	db := stubWorkspaceDB{
		queryRowFunc: func(_ context.Context, sql string, args ...any) pgx.Row {
			if !strings.Contains(sql, "FROM workspace_runs") {
				t.Fatalf("QueryRow() sql = %q", sql)
			}
			if len(args) != 1 || args[0] != "run-get" {
				t.Fatalf("QueryRow() args = %#v, want [run-get]", args)
			}
			return stubWorkspaceRow{values: workspaceRunValues(returned)}
		},
	}

	store := NewStore(db)
	run, err := store.GetRun(context.Background(), "run-get")
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if run.Status != "merged" {
		t.Fatalf("GetRun().Status = %q, want merged", run.Status)
	}
	if string(run.Metadata) != `{"done":true}` {
		t.Fatalf("GetRun().Metadata = %s", run.Metadata)
	}
}

func TestListWorkspaceRuns(t *testing.T) {
	db := stubWorkspaceDB{
		queryFunc: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			if !strings.Contains(sql, "FROM workspace_runs") {
				t.Fatalf("Query() sql = %q", sql)
			}
			if len(args) != 3 {
				t.Fatalf("Query() args len = %d, want 3", len(args))
			}
			if got := args[0]; got != "active" {
				t.Fatalf("status arg = %#v, want active", got)
			}
			if got := args[1]; got != "dag-list" {
				t.Fatalf("dag_key arg = %#v, want dag-list", got)
			}
			if got := args[2]; got != int32(2) {
				t.Fatalf("limit arg = %#v, want 2", got)
			}
			rows := [][]any{
				workspaceRunValues(sqlc.WorkspaceRun{
					ID:            1,
					RunKey:        "run-a",
					DagKey:        "dag-list",
					SourceRoot:    "/src/a",
					WorkspacePath: "/tmp/a",
					Status:        "active",
					CreatedBy:     "agent-a",
					UpdatedBy:     "agent-a",
					CreatedAt:     timestamptz(time.Unix(500, 0).UTC()),
					UpdatedAt:     timestamptz(time.Unix(501, 0).UTC()),
				}),
				workspaceRunValues(sqlc.WorkspaceRun{
					ID:            2,
					RunKey:        "run-b",
					DagKey:        "dag-list",
					SourceRoot:    "/src/b",
					WorkspacePath: "/tmp/b",
					Status:        "active",
					CreatedBy:     "agent-b",
					UpdatedBy:     "agent-b",
					CreatedAt:     timestamptz(time.Unix(502, 0).UTC()),
					UpdatedAt:     timestamptz(time.Unix(503, 0).UTC()),
				}),
			}
			return &stubWorkspaceRows{rows: rows}, nil
		},
	}

	store := NewStore(db)
	runs, err := store.ListRuns(context.Background(), ListRunsFilter{
		Status: "active",
		DagKey: "dag-list",
		Limit:  2,
	})
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("ListRuns() len = %d, want 2", len(runs))
	}
	if runs[0].RunKey != "run-a" || runs[1].RunKey != "run-b" {
		t.Fatalf("ListRuns() keys = [%s %s], want [run-a run-b]", runs[0].RunKey, runs[1].RunKey)
	}
}
