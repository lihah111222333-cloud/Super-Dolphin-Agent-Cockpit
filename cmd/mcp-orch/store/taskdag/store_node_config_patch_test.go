package taskdag

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

func TestPatchNodeConfigIfUnchangedPatchesOnlyConfig(t *testing.T) {
	store, db, now := newTaskDAGTestStore()
	patcher := store.(NodeConfigPatchStore)
	db.nodes[dagNodeKey("dag-1", "node-1")] = sqlc.TaskDagNode{
		ID:         1,
		DagKey:     "dag-1",
		NodeKey:    "node-1",
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
	key := dagNodeKey("dag-1", "node-1")
	db.nodes[key] = sqlc.TaskDagNode{
		ID:        1,
		DagKey:    "dag-1",
		NodeKey:   "node-1",
		NodeType:  "agent",
		Status:    "ready",
		Config:    json.RawMessage(`{"exec":{"model":"opus"}}`),
		CreatedAt: timestamptzValue(now),
		UpdatedAt: timestamptzValue(now),
	}

	_, err := patcher.PatchNodeConfigIfUnchanged(context.Background(), NodeConfigPatchInput{
		DagKey:         "dag-1",
		NodeKey:        "node-1",
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
