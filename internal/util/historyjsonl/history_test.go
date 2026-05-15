package historyjsonl

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodexHistoryFallsBackToSessionUUID(t *testing.T) {
	root := t.TempDir()
	sessionUUID := "019e2c22-abae-7e12-9348-1f910b2052e1"
	writeCodexRollout(t, root, sessionUUID, "12345678910已完成。")

	messages, err := ReadProviderMessages(ReadRequest{
		Provider:    "codex",
		ThreadID:    "agent_1778856929920777000",
		SessionUUID: sessionUUID,
		CodexHome:   root,
	})
	if err != nil {
		t.Fatalf("ReadProviderMessages() error = %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("ReadProviderMessages() len = %d, want 1", len(messages))
	}
	if got := messages[0].Content; got != "12345678910已完成。" {
		t.Fatalf("message content = %q, want session UUID rollout", got)
	}
}

func TestCodexHistoryPrefersProviderThreadIDBeforeSessionUUID(t *testing.T) {
	root := t.TempDir()
	writeCodexRollout(t, root, "provider-thread-id", "provider result")
	writeCodexRollout(t, root, "session-uuid", "session result")

	messages, err := ReadProviderMessages(ReadRequest{
		Provider:         "codex",
		ThreadID:         "agent_1",
		ProviderThreadID: "provider-thread-id",
		SessionUUID:      "session-uuid",
		CodexHome:        root,
	})
	if err != nil {
		t.Fatalf("ReadProviderMessages() error = %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("ReadProviderMessages() len = %d, want 1", len(messages))
	}
	if got := messages[0].Content; got != "provider result" {
		t.Fatalf("message content = %q, want provider_thread_id rollout", got)
	}
}

func writeCodexRollout(t *testing.T, root, id, content string) string {
	t.Helper()
	path := filepath.Join(root, "sessions", "2026", "05", "15", "rollout-2026-05-15T22-55-30-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	raw := `{"timestamp":"2026-05-15T22:55:30Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + content + `"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
