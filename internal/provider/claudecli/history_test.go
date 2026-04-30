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

func TestParseHistoryLineExtractsImageContentBlocksMetadata(t *testing.T) {
	// Vision turn shape: claude CLI persists native image content blocks
	// alongside the user prompt. The history parser must surface those blocks
	// as metadata.input so the frontend can render them after a restart.
	raw := []byte(`{"type":"user","timestamp":"2026-04-30T14:00:00Z","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}},{"type":"text","text":"请看这张图"}]}}`)
	msg, ok := parseHistoryLine(raw)
	if !ok {
		t.Fatalf("parseHistoryLine returned !ok for vision turn")
	}
	if msg.Role != "user" {
		t.Errorf("role = %q, want user", msg.Role)
	}
	if msg.Content != "请看这张图" {
		t.Errorf("content = %q, want vision prompt", msg.Content)
	}
	if len(msg.Metadata) == 0 {
		t.Fatalf("metadata empty; want recovered image block input")
	}
	if got := string(msg.Metadata); got != `{"input":[{"type":"image","url":"data:image/png;base64,AAAA"}]}` {
		t.Errorf("metadata mismatch:\n got %s", got)
	}
}

func TestParseHistoryLineHandlesURLImageSource(t *testing.T) {
	raw := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://example.com/x.png"}},{"type":"text","text":"hi"}]}}`)
	msg, ok := parseHistoryLine(raw)
	if !ok {
		t.Fatalf("parseHistoryLine returned !ok for url-source vision turn")
	}
	if got := string(msg.Metadata); got != `{"input":[{"type":"image","url":"https://example.com/x.png"}]}` {
		t.Errorf("metadata mismatch:\n got %s", got)
	}
}

func TestParseHistoryLineLegacyTextHintStillWorks(t *testing.T) {
	// Pre-vision turns persisted attachment hints as plain text. The text-hint
	// parser must still take precedence to avoid double-counting attachments.
	raw := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"The user has attached the following files. Use the Read tool to view them:\n[Image: /tmp/clipboard-old.png]\n\nplease describe"}]}}`)
	msg, ok := parseHistoryLine(raw)
	if !ok {
		t.Fatalf("parseHistoryLine returned !ok for legacy hint")
	}
	if msg.Content != "please describe" {
		t.Errorf("content = %q, want trimmed prompt", msg.Content)
	}
	if len(msg.Metadata) == 0 {
		t.Fatalf("metadata empty for legacy text hint")
	}
}

func TestParseHistoryLinePlainTextNoMetadata(t *testing.T) {
	raw := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`)
	msg, ok := parseHistoryLine(raw)
	if !ok {
		t.Fatalf("parseHistoryLine returned !ok for plain text")
	}
	if len(msg.Metadata) != 0 {
		t.Errorf("plain text turn should have no metadata, got %s", msg.Metadata)
	}
}
