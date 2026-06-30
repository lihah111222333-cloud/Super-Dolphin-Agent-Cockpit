package taskdag

import (
	"context"
	"database/sql"
	"testing"
)

func TestUpdateNodeStatusCASRejectsStaleStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		actualStatus   string
		expectedStatus string
		targetStatus   string
	}{
		{
			name:           "terminal done is not rolled back",
			actualStatus:   "done",
			expectedStatus: "running",
			targetStatus:   "retrying",
		},
		{
			name:           "changed retrying state is not overwritten",
			actualStatus:   "retrying",
			expectedStatus: "running",
			targetStatus:   "ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			db := openTaskDAGSQLiteDB(t)
			store := NewStore(db).(*store)

			seedSQLiteTaskDAGTemplate(t, ctx, store)
			runID := insertSQLiteTaskDAGRun(t, ctx, db, "run-cas", "dag-multi", `{"case":"cas"}`)
			cloneSQLiteRunNodes(t, ctx, store, runID, "run-cas")
			setSQLiteRunNodeStatus(t, ctx, db, "dag-multi", "root", runID, tt.actualStatus)

			_, err := store.UpdateNodeStatus(ctx, NodeStatusUpdate{
				DagKey:         "dag-multi",
				NodeKey:        "root",
				RunID:          runID,
				ExpectedStatus: tt.expectedStatus,
				Status:         tt.targetStatus,
				Result:         []byte(`{"stale":true}`),
			})
			if err == nil {
				t.Fatalf("UpdateNodeStatus stale expected_status error = nil, want CAS miss")
			}
			assertSQLiteRunNodeStatus(t, ctx, store, runID, "run-cas", tt.actualStatus)
		})
	}
}

func setSQLiteRunNodeStatus(t *testing.T, ctx context.Context, db *sql.DB, dagKey, nodeKey string, runID int64, status string) {
	t.Helper()
	res, err := db.ExecContext(ctx, `
UPDATE task_dag_nodes
SET status = ?
WHERE dag_key = ? AND node_key = ? AND run_id = ?`, status, dagKey, nodeKey, runID)
	if err != nil {
		t.Fatalf("set node status %s/%s run_id=%d: %v", dagKey, nodeKey, runID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("set node status rows affected: %v", err)
	}
	if rows != 1 {
		t.Fatalf("set node status rows = %d, want 1", rows)
	}
}
