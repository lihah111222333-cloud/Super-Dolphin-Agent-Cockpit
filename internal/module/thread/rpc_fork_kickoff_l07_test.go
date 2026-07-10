package thread

import (
	"context"
	"encoding/json"
	"testing"
)

func TestNewThreadHandlersDispatchForkReturnsKickoffState(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{
		forkResult: ForkResult{NewThreadID: "thread-7-fork", ForkedFrom: "thread-7", KickoffState: ForkKickoffCreatedOnly},
	}
	server := newThreadTestServer(stub)
	raw, err := server.Dispatch(context.Background(), "thread/fork", json.RawMessage(`{"threadId":"thread-7"}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/fork) error = %v", err)
	}
	var got struct {
		KickoffState      string `json:"kickoff_state"`
		KickoffStateCamel string `json:"kickoffState"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(thread/fork) error = %v", err)
	}
	if got.KickoffState != "created_only" || got.KickoffStateCamel != "created_only" {
		t.Fatalf("fork kickoff state = %q/%q, want created_only", got.KickoffState, got.KickoffStateCamel)
	}
}
