package provider

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMessageJSONUsesCreatedAt(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(Message{
		ID:        1,
		AgentID:   "thread-1",
		Role:      "assistant",
		EventType: "agent_message",
		Content:   "done",
		Timestamp: time.Unix(123, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"createdAt":"1970-01-01T00:02:03Z"`) {
		t.Fatalf("marshal output = %s, want createdAt field", text)
	}
	if strings.Contains(text, `"timestamp"`) {
		t.Fatalf("marshal output = %s, want timestamp omitted", text)
	}

	var got Message
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !got.Timestamp.Equal(time.Unix(123, 0).UTC()) {
		t.Fatalf("Timestamp = %v, want %v", got.Timestamp, time.Unix(123, 0).UTC())
	}
}

func TestMessagePageResultCarriesSourceRevision(t *testing.T) {
	t.Parallel()

	const want = "sha256:opaque-source-revision"
	page := MessagePageResult{SourceRevision: want}
	if page.SourceRevision != want {
		t.Fatalf("SourceRevision = %q, want %q", page.SourceRevision, want)
	}
}
