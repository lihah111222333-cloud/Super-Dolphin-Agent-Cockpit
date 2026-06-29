package thread

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

func TestSessionThreadLookupFailsClosedOnMalformedRuntimeConfig(t *testing.T) {
	t.Parallel()

	lookup := NewSessionThreadLookup(newThreadConfigOverrideStore([]byte(`{"runtime":`)))
	_, err := lookup.GetByThreadID(context.Background(), "thread-1")
	if err == nil || !strings.Contains(err.Error(), "config_override.runtime") {
		t.Fatalf("GetByThreadID() error = %v, want malformed config_override.runtime error", err)
	}
}

func TestSessionThreadLookupCarriesPromptSnapshot(t *testing.T) {
	t.Parallel()

	rawSnapshot, err := json.Marshal(PromptSnapshot{
		DisplayName:      "Thread",
		BaseInstructions: "resume system prompt",
		Provider:         "codex",
		Version:          2,
		Hash:             "snapshot-hash",
	})
	if err != nil {
		t.Fatalf("Marshal snapshot: %v", err)
	}
	store := &store{q: &threadQuerierStub{
		getByIDFn: func(context.Context, string) (sqlc.GetAgentThreadByIDRow, error) {
			return sqlc.GetAgentThreadByIDRow{
				ThreadID:       "thread-1",
				AgentID:        "agent-1",
				Status:         "running",
				ConfigOverride: `{"runtime":{"cwd":"/repo"}}`,
			}, nil
		},
		loadPromptSnapshotFn: func(context.Context, string) ([]byte, error) {
			return rawSnapshot, nil
		},
	}}

	ref, err := NewSessionThreadLookup(store).GetByThreadID(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("GetByThreadID() error = %v", err)
	}
	if ref.PromptSnapshot.BaseInstructions != "resume system prompt" ||
		ref.PromptSnapshot.Version != 2 ||
		ref.PromptSnapshot.Hash != "snapshot-hash" {
		t.Fatalf("PromptSnapshot = %#v, want stored resume snapshot", ref.PromptSnapshot)
	}
}
