//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
)

func TestPatchNodeConfigIfUnchangedPatchesOnlyConfig(t *testing.T) {
	store, db, now := newTaskDAGTestStore()
	patcher := store.(NodeConfigPatchStore)
	runID := db.runs["run-1"].ID
	db.nodes[dagRunNodeKey("dag-1", "node-1", runID)] = sqlc.TaskDagNode{
		ID:         1,
		DagKey:     "dag-1",
		NodeKey:    "node-1",
		RunID:      sqlc.Int8ValuePtr(&runID),
		Title:      "Original title",
		NodeType:   "agent",
		AssignedTo: "agent-A",
		DependsOn:  json.RawMessage(`["upstream"]`),
		Status:     "ready",
		CommandRef: "cmd-1",
		Config:     json.RawMessage(`{"exec":{"model":"sonnet"}}`),
		Result:     json.RawMessage(`{}`),
		CreatedAt:  timestamptzValue(now),
		UpdatedAt:  timestamptzValue(now),
	}

	patched, err := patcher.PatchNodeConfigIfUnchanged(context.Background(), NodeConfigPatchInput{
		DagKey:         "dag-1",
		NodeKey:        "node-1",
		RunID:          runID,
		PreviousConfig: json.RawMessage(`{"exec":{"model":"sonnet"}}`),
		Config:         json.RawMessage(`{"exec":{"model":"opus"}}`),
	})
	if err != nil {
		t.Fatalf("PatchNodeConfigIfUnchanged err = %v", err)
	}
	if string(patched.Config) != `{"exec":{"model":"opus"}}` {
		t.Fatalf("Config = %s, want patched config", patched.Config)
	}
	if patched.Title != "Original title" || patched.AssignedTo != "agent-A" ||
		string(patched.DependsOn) != `["upstream"]` || patched.CommandRef != "cmd-1" {
		t.Fatalf("non-config fields changed: %+v", patched)
	}
}

func TestPatchNodeConfigIfUnchangedRejectsStaleConfig(t *testing.T) {
	store, db, now := newTaskDAGTestStore()
	patcher := store.(NodeConfigPatchStore)
	runID := db.runs["run-1"].ID
	key := dagRunNodeKey("dag-1", "node-1", runID)
	db.nodes[key] = sqlc.TaskDagNode{
		ID:        1,
		DagKey:    "dag-1",
		NodeKey:   "node-1",
		RunID:     sqlc.Int8ValuePtr(&runID),
		NodeType:  "agent",
		Status:    "ready",
		Config:    json.RawMessage(`{"exec":{"model":"opus"}}`),
		CreatedAt: timestamptzValue(now),
		UpdatedAt: timestamptzValue(now),
	}

	_, err := patcher.PatchNodeConfigIfUnchanged(context.Background(), NodeConfigPatchInput{
		DagKey:         "dag-1",
		NodeKey:        "node-1",
		RunID:          runID,
		PreviousConfig: json.RawMessage(`{"exec":{"model":"sonnet"}}`),
		Config:         json.RawMessage(`{"exec":{"model":"haiku"}}`),
	})
	if !errors.Is(err, platformdb.ErrNotFound) {
		t.Fatalf("PatchNodeConfigIfUnchanged err = %v, want ErrNotFound", err)
	}
	if got := string(db.nodes[key].Config); got != `{"exec":{"model":"opus"}}` {
		t.Fatalf("stored config = %s, want unchanged opus config", got)
	}
}

func TestRetryWakeupWithNodeConfigPatchRollsBackRetryOnStaleConfig(t *testing.T) {
	store, db, now := newTaskDAGTestStore()
	patcher := store.(SmartRetryConfigStore)

	runID := db.runs["run-1"].ID
	db.wakeups[7] = newDispatchingWakeup(now, 7, "worker-a", 30*time.Second)
	originalWakeup := db.wakeups[7]
	key := dagRunNodeKey("dag-1", "node-1", runID)
	db.nodes[key] = sqlc.TaskDagNode{
		ID:        1,
		DagKey:    "dag-1",
		NodeKey:   "node-1",
		RunID:     sqlc.Int8ValuePtr(&runID),
		NodeType:  "agent",
		Status:    "ready",
		Config:    json.RawMessage(`{"exec":{"model":"opus"}}`),
		CreatedAt: timestamptzValue(now),
		UpdatedAt: timestamptzValue(now),
	}

	rows, err := patcher.RetryWakeupWithNodeConfigPatch(context.Background(), RetryWakeupWithNodeConfigPatchInput{
		RetryWakeup: RetryWakeupInput{
			ID:             7,
			RetryInterval:  "00:02:00",
			LastError:      "smart retry prepare failed",
			ClaimedAt:      now,
			ClaimedBy:      "worker-a",
			LeaseExpiresAt: now.Add(30 * time.Second),
		},
		NodeConfig: NodeConfigPatchInput{
			DagKey:         "dag-1",
			NodeKey:        "node-1",
			RunID:          runID,
			PreviousConfig: json.RawMessage(`{"exec":{"model":"sonnet"}}`),
			Config:         json.RawMessage(`{"exec":{"model":"haiku"}}`),
		},
	})
	if rows != 0 {
		t.Fatalf("RetryWakeupWithNodeConfigPatch rows = %d, want 0 on patch miss", rows)
	}
	if !errors.Is(err, platformdb.ErrNotFound) {
		t.Fatalf("RetryWakeupWithNodeConfigPatch err = %v, want ErrNotFound", err)
	}

	assertWakeupUnchanged(t, db.wakeups[7], originalWakeup)
	if got := string(db.nodes[key].Config); got != `{"exec":{"model":"opus"}}` {
		t.Fatalf("stored config = %s, want unchanged opus config", got)
	}
}

func assertWakeupUnchanged(t *testing.T, got, want sqlc.TaskDagWakeup) {
	t.Helper()

	if got.Status != want.Status {
		t.Fatalf("wakeup Status = %q, want %q", got.Status, want.Status)
	}
	if got.LastError != want.LastError {
		t.Fatalf("wakeup LastError = %q, want %q", got.LastError, want.LastError)
	}
	if got.ClaimedBy != want.ClaimedBy {
		t.Fatalf("wakeup ClaimedBy = %q, want %q", got.ClaimedBy, want.ClaimedBy)
	}
	if got.AttemptCount != want.AttemptCount {
		t.Fatalf("wakeup AttemptCount = %d, want %d", got.AttemptCount, want.AttemptCount)
	}
	if !sameTimestamp(got.ClaimedAt, want.ClaimedAt) {
		t.Fatalf("wakeup ClaimedAt = %+v, want %+v", got.ClaimedAt, want.ClaimedAt)
	}
	if !sameTimestamp(got.LeaseExpiresAt, want.LeaseExpiresAt) {
		t.Fatalf("wakeup LeaseExpiresAt = %+v, want %+v", got.LeaseExpiresAt, want.LeaseExpiresAt)
	}
	if !sameTimestamp(got.NextRetryAt, want.NextRetryAt) {
		t.Fatalf("wakeup NextRetryAt = %+v, want %+v", got.NextRetryAt, want.NextRetryAt)
	}
}
