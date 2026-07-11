package claudecli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestSessionReadHistoryFallsBackToResolvedThreadID(t *testing.T) {
	t.Parallel()

	// Setup: create a JSONL file named by claude session UUID, not agentID.
	dir := t.TempDir()
	projectsDir := filepath.Join(dir, "projects", "test-project")
	if err := os.MkdirAll(projectsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeUUID := "882e42ab-6ee2-4d32-86ce-a4fb11027ba6"
	agentID := "agent_123_abc"
	jsonlPath := filepath.Join(projectsDir, claudeUUID+".jsonl")
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

func TestReadMessagesPageReturnsRecentClaudeHistory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	threadID := "thread-page"
	writeClaudeHistoryMessages(t, dir, threadID, []string{"one", "two", "three"})

	page, err := (&historyBackend{sessionDir: dir}).ReadMessagesPage(context.Background(), threadID, dto.MessagePageRequest{Limit: 2})
	if err != nil {
		t.Fatalf("ReadMessagesPage() error = %v", err)
	}
	if len(page.Items) != 2 || page.Items[0].Content != "two" || page.Items[1].Content != "three" {
		t.Fatalf("page items = %#v, want two/three", page.Items)
	}
	if len(page.Offsets) != 2 || page.Offsets[0] == page.Offsets[1] {
		t.Fatalf("page offsets = %#v, want stable per-record offsets", page.Offsets)
	}
	if !page.HasMore || page.NextBefore == "" {
		t.Fatalf("page metadata = hasMore:%v nextBefore:%q, want more with cursor", page.HasMore, page.NextBefore)
	}
}

func TestSessionReadMessagesPageSetsStableIDsFromOffsets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	threadID := "thread-page"
	writeClaudeHistoryMessages(t, dir, threadID, []string{"one", "two", "three"})
	s := &session{
		threadID: threadID,
		history:  &historyBackend{sessionDir: dir},
	}

	page, err := s.ReadMessagesPage(context.Background(), threadID, dto.MessagePageRequest{Limit: 2})
	if err != nil {
		t.Fatalf("ReadMessagesPage() error = %v", err)
	}
	if len(page.Messages) != 2 || page.Messages[0].ID == 0 || page.Messages[0].ID == page.Messages[1].ID {
		t.Fatalf("page messages = %#v, want non-zero stable IDs", page.Messages)
	}
}

func writeClaudeHistoryMessages(t *testing.T, dir, threadID string, contents []string) {
	t.Helper()
	projectsDir := filepath.Join(dir, "projects", "test-project")
	if err := os.MkdirAll(projectsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := ""
	for _, content := range contents {
		raw += `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + content + `"}]}}` + "\n"
	}
	if err := os.WriteFile(filepath.Join(projectsDir, threadID+".jsonl"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryRootDirPrefersClaudeConfigDir(t *testing.T) {
	configDir := t.TempDir()
	legacyDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	t.Setenv("CLAUDE_HOME", legacyDir)

	h := &historyBackend{}
	got, err := h.rootDir()
	if err != nil {
		t.Fatalf("rootDir() error = %v", err)
	}
	if got != configDir {
		t.Fatalf("rootDir() = %q, want CLAUDE_CONFIG_DIR %q", got, configDir)
	}
}

func TestHistoryRootDirFallsBackToClaudeHome(t *testing.T) {
	legacyDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_HOME", legacyDir)

	h := &historyBackend{}
	got, err := h.rootDir()
	if err != nil {
		t.Fatalf("rootDir() error = %v", err)
	}
	if got != legacyDir {
		t.Fatalf("rootDir() = %q, want CLAUDE_HOME %q", got, legacyDir)
	}
}

func TestSessionRolloutPathReturnsClaudeHistoryFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	projectsDir := filepath.Join(dir, "projects", "test-project")
	if err := os.MkdirAll(projectsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeUUID := "882e42ab-6ee2-4d32-86ce-a4fb11027ba6"
	jsonlPath := filepath.Join(projectsDir, claudeUUID+".jsonl")
	if err := os.WriteFile(jsonlPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &session{
		threadID: claudeUUID,
		history:  &historyBackend{sessionDir: dir},
	}
	if got := s.RolloutPath(); got != jsonlPath {
		t.Fatalf("RolloutPath() = %q, want %q", got, jsonlPath)
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
	var md struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(msg.Metadata, &md); err != nil {
		t.Fatalf("metadata unmarshal: %v", err)
	}
	if len(md.Input) != 1 {
		t.Fatalf("input len = %d, want 1", len(md.Input))
	}
	entry := md.Input[0]
	if entry["type"] != "image" {
		t.Errorf("input.type = %v, want image", entry["type"])
	}
	if entry["url"] != "data:image/png;base64,AAAA" {
		t.Errorf("input.url = %v, want data: URL", entry["url"])
	}
	// sha256(\x00\x00\x00) = 709e80c884... so the recovered metadata.input
	// must carry it, letting the frontend re-correlate dedup placeholders.
	wantHash := "709e80c88487a2411e1ee4dfb9f22a861492d20c4765150c0c794abd70f8147c"
	if entry["sha256"] != wantHash {
		t.Errorf("input.sha256 = %v, want %s", entry["sha256"], wantHash)
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
