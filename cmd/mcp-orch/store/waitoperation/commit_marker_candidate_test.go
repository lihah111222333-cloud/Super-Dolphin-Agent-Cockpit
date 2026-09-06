package waitoperation

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
)

func markerPlanRequest(fixture waitOperationFixture, key string) PlanRequest {
	return PlanRequest{OperationKey: key, OperationDigest: strings.Repeat("d", 64), CallerPrincipalRef: "principal", ScopeRef: "scope", Source: fixture.source, DeadlineAt: fixture.now.Add(30 * time.Second), Targets: []TargetPlan{{Key: "target-1", Target: fixture.target, Selector: accesswait.EventSelector{Version: 1, SourceID: "task/src", SourceEpoch: "inc-src", Lane: eventstore.LaneControl, TargetAgentID: "dst", EventTypes: []string{"output"}}}}}
}

func countPendingMarkers(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM orchestration_wait_operation_commit_markers WHERE state = 'pending'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertCommitMarkerCounts(t *testing.T, db *sql.DB, pending, resolved int) {
	t.Helper()
	var gotPending, gotResolved int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FILTER (WHERE state = 'pending'), COUNT(*) FILTER (WHERE state = 'resolved') FROM orchestration_wait_operation_commit_markers`).Scan(&gotPending, &gotResolved); err != nil {
		t.Fatal(err)
	}
	if gotPending != pending || gotResolved != resolved {
		t.Fatalf("marker counts pending=%d resolved=%d, want pending=%d resolved=%d", gotPending, gotResolved, pending, resolved)
	}
}
