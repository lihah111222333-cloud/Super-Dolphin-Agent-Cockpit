package wakeupreclaim

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	sqliteruntime "github.com/anthropic-ai/super-agent-v3/internal/platform/db/sqlite"
	_ "modernc.org/sqlite"
)

type wakeupRecoveryTestStore interface {
	taskdag.Store
	taskdag.RunStore
	taskdag.RunNodeReadStore
}

func TestWakeupReclaimerMarksHalfWrittenAssignedRuntimeNode(t *testing.T) {
	ctx := context.Background()
	store := newWakeupRecoveryTestStore(t)
	runID := seedAssignedReadyRuntimeNode(t, ctx, store, "dag-recovery", "run-recovery", "agent-alpha")

	var logs bytes.Buffer
	reclaimer, err := NewWakeupReclaimer(store, slog.New(slog.NewTextHandler(&logs, nil)), WakeupReclaimerConfig{})
	if err != nil {
		t.Fatalf("NewWakeupReclaimer() error = %v", err)
	}
	if _, err := reclaimer.ReclaimOnce(ctx); err != nil {
		t.Fatalf("ReclaimOnce() error = %v", err)
	}

	if got := runtimeNodeStatus(t, ctx, store, "dag-recovery", runID, "root"); got != "dispatch_incomplete" {
		t.Fatalf("runtime node status = %q, want dispatch_incomplete after reclaimer recovery", got)
	}
	if !strings.Contains(logs.String(), "dispatch_incomplete") || !strings.Contains(logs.String(), "dag-recovery") {
		t.Fatalf("reclaimer logs = %q, want diagnostic dispatch_incomplete log", logs.String())
	}
}

func TestWakeupReclaimerKeepsAssignedRuntimeNodeWithActiveWakeup(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		activate    func(*testing.T, context.Context, wakeupRecoveryTestStore, int64)
		wantStatus  string
		description string
	}{
		{
			name:       "pending wakeup is active",
			activate:   enqueueActivePendingWakeup,
			wantStatus: "ready",
		},
		{
			name:       "sent unbound wakeup is active",
			activate:   enqueueActiveSentUnboundWakeup,
			wantStatus: "ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newWakeupRecoveryTestStore(t)
			runID := seedAssignedReadyRuntimeNode(t, ctx, store, "dag-active", "run-active", "agent-alpha")
			tt.activate(t, ctx, store, runID)

			reclaimer, err := NewWakeupReclaimer(store, nil, WakeupReclaimerConfig{})
			if err != nil {
				t.Fatalf("NewWakeupReclaimer() error = %v", err)
			}
			if _, err := reclaimer.ReclaimOnce(ctx); err != nil {
				t.Fatalf("ReclaimOnce() error = %v", err)
			}

			if got := runtimeNodeStatus(t, ctx, store, "dag-active", runID, "root"); got != tt.wantStatus {
				t.Fatalf("runtime node status = %q, want %q while active wakeup exists", got, tt.wantStatus)
			}
		})
	}
}

func newWakeupRecoveryTestStore(t *testing.T) wakeupRecoveryTestStore {
	t.Helper()
	db := openWakeupRecoverySQLiteDB(t)
	store, ok := taskdag.NewStore(db).(wakeupRecoveryTestStore)
	if !ok {
		t.Fatal("taskdag.NewStore does not expose run/node methods required by recovery test")
	}
	return store
}

func openWakeupRecoverySQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "taskdag.sqlite")
	db, err := sql.Open("sqlite", wakeupRecoverySQLiteDSN(path))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(4)
	if err := sqliteruntime.RunMigrations(context.Background(), db, wakeupRecoverySQLiteMigrationsDir(t)); err != nil {
		t.Fatalf("run sqlite migrations: %v", err)
	}
	return db
}

func wakeupRecoverySQLiteDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout=5000")
	q.Add("_pragma", "foreign_keys=ON")
	q.Add("_pragma", "journal_mode=WAL")
	return path + "?" + q.Encode()
}

func wakeupRecoverySQLiteMigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "internal", "platform", "db", "sqlite", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	return dir
}

func seedAssignedReadyRuntimeNode(t *testing.T, ctx context.Context, store wakeupRecoveryTestStore, dagKey, runKey, assignedTo string) int64 {
	t.Helper()
	if _, err := store.UpsertDAG(ctx, taskdag.DAG{
		DagKey:    dagKey,
		Title:     "Recovery DAG",
		Status:    "draft",
		CreatedBy: "test",
		Metadata:  json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("UpsertDAG() error = %v", err)
	}
	if _, err := store.UpsertNode(ctx, taskdag.Node{
		DagKey:     dagKey,
		NodeKey:    "root",
		Title:      "Root",
		NodeType:   "agent",
		AssignedTo: assignedTo,
		DependsOn:  json.RawMessage(`[]`),
		Config:     json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("UpsertNode() error = %v", err)
	}
	run, err := store.CreateRun(ctx, taskdag.CreateRunInput{
		RunKey:             runKey,
		DagKey:             dagKey,
		DagVersionSnapshot: 1,
		TriggerSource:      "manual",
		Metadata:           json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	if rows, err := store.CloneNodesForRun(ctx, dagKey, run.ID); err != nil || rows != 1 {
		t.Fatalf("CloneNodesForRun() rows=%d error=%v, want 1/nil", rows, err)
	}
	if rows, err := store.PromoteRootNodesToReady(ctx, dagKey, run.ID); err != nil || rows != 1 {
		t.Fatalf("PromoteRootNodesToReady() rows=%d error=%v, want 1/nil", rows, err)
	}
	return run.ID
}

func enqueueActivePendingWakeup(t *testing.T, ctx context.Context, store wakeupRecoveryTestStore, runID int64) {
	t.Helper()
	if _, err := store.EnqueueWakeup(ctx, taskdag.EnqueueWakeupInput{
		DagKey:         "dag-active",
		NodeKey:        "root",
		RunID:          runID,
		WakeupKind:     "manual_dispatch",
		TargetAgentID:  "agent-alpha",
		PromptPayload:  json.RawMessage(`{"kind":"manual_dispatch"}`),
		IdempotencyKey: "manual_dispatch:dag-active:run-active:root:agent-alpha:pending",
	}); err != nil {
		t.Fatalf("EnqueueWakeup() error = %v", err)
	}
}

func enqueueActiveSentUnboundWakeup(t *testing.T, ctx context.Context, store wakeupRecoveryTestStore, runID int64) {
	t.Helper()
	enqueueActivePendingWakeup(t, ctx, store, runID)
	claimed, err := store.ClaimDueWakeups(ctx, taskdag.ClaimDueWakeupsInput{
		ClaimedBy:     "recovery-test-worker",
		LeaseInterval: "30s",
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("ClaimDueWakeups() error = %v", err)
	}
	if len(claimed) != 1 || claimed[0].ClaimedAt == nil || claimed[0].LeaseExpiresAt == nil {
		t.Fatalf("ClaimDueWakeups() = %#v, want one claimed wakeup with fence fields", claimed)
	}
	if rows, err := store.MarkWakeupSent(ctx, taskdag.MarkWakeupSentInput{
		ID:             claimed[0].ID,
		ClaimedAt:      *claimed[0].ClaimedAt,
		ClaimedBy:      claimed[0].ClaimedBy,
		LeaseExpiresAt: *claimed[0].LeaseExpiresAt,
	}); err != nil || rows != 1 {
		t.Fatalf("MarkWakeupSent() rows=%d error=%v, want 1/nil", rows, err)
	}
}

func runtimeNodeStatus(t *testing.T, ctx context.Context, store wakeupRecoveryTestStore, dagKey string, runID int64, nodeKey string) string {
	t.Helper()
	nodes, err := store.ListRunNodes(ctx, dagKey, runID)
	if err != nil {
		t.Fatalf("ListRunNodes() error = %v", err)
	}
	for _, node := range nodes {
		if node.NodeKey == nodeKey {
			return node.Status
		}
	}
	t.Fatalf("node %s/%s/%d not found in %#v", dagKey, nodeKey, runID, nodes)
	return ""
}
