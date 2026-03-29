package claudecli

import (
	"context"
	"os"
	"testing"
)

func TestSessionReadHistoryFallsBackToResolvedThreadID(t *testing.T) {
	t.Parallel()

	// Setup: create a JSONL file named by claude session UUID, not agentID.
	dir := t.TempDir()
	projectsDir := dir + "/projects/test-project"
	if err := os.MkdirAll(projectsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeUUID := "882e42ab-6ee2-4d32-86ce-a4fb11027ba6"
	agentID := "agent_123_abc"
	jsonlPath := projectsDir + "/" + claudeUUID + ".jsonl"
	if err := os.WriteFile(jsonlPath, []byte(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Session has threadID = claudeUUID (set by system:init after startup).
	s := &session{
		threadID: claudeUUID,
		history:  &historyBackend{sessionDir: dir},
	}

	// Query with agentID (what the thread module passes) — should fallback to session threadID.
	msgs, err := s.ReadHistory(context.Background(), agentID, 0)
	if err != nil {
		t.Fatalf("ReadHistory(agentID) error = %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("ReadHistory(agentID) returned 0 messages; expected fallback to session threadID to find the JSONL file")
	}
}

func TestReadHistoryReturnsEmptyForNewSession(t *testing.T) {
	t.Parallel()

	// Use a temp dir that has no JSONL files — simulates a brand-new session.
	dir := t.TempDir()
	h := &historyBackend{sessionDir: dir}

	msgs, err := h.ReadHistory(context.Background(), "agent_new_session_123")
	if err != nil {
		t.Fatalf("ReadHistory() error = %v, want nil (new session has no history)", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("ReadHistory() returned %d messages, want 0", len(msgs))
	}
}
