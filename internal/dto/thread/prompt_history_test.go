package thread

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPromptHistoryDTOUsesExactJSONKeys(t *testing.T) {
	t.Parallel()

	result := PromptHistoryResult{
		Entries: []PromptHistoryEntry{{
			ThreadID:  "thread-1",
			MessageID: "42",
			Text:      "repeat",
			CreatedAt: time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC),
		}},
		NextCursor: "opaque-cursor",
		HasMore:    true,
		Nonce:      "opaque-nonce",
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	const want = `{"entries":[{"threadId":"thread-1","messageId":"42","text":"repeat","createdAt":"2026-07-12T01:02:03Z"}],"nextCursor":"opaque-cursor","hasMore":true,"nonce":"opaque-nonce"}`
	if string(raw) != want {
		t.Fatalf("PromptHistoryResult JSON = %s, want %s", raw, want)
	}
}
