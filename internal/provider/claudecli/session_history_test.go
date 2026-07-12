package claudecli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestTrimClaudeHistoryFiltersNoiseAndInjectedHints(t *testing.T) {
	t.Parallel()

	messages := []Message{
		{
			Role:    "user",
			Content: "<environment_context>\nsecret\n</environment_context>\n",
		},
		{
			Role:    "user",
			Content: "keep me\n已注入 LSP 提示",
		},
		{
			Role:    "user",
			Content: "hello\n[skill: test] 摘要: sample\n使用方式: sample\nmore",
		},
		{
			Role:     "user",
			Content:  "# AGENTS.md\nignored",
			Metadata: json.RawMessage(`{"input":[{"type":"mention","path":"/tmp/file"}]}`),
		},
	}

	got := trimClaudeHistory(messages, 0)
	if len(got) != 3 {
		t.Fatalf("len(trimClaudeHistory()) = %d, want 3", len(got))
	}
	if got[0].Content != "keep me" {
		t.Fatalf("got[0].Content = %q, want %q", got[0].Content, "keep me")
	}
	if got[1].Content != "hello" {
		t.Fatalf("got[1].Content = %q, want %q", got[1].Content, "hello")
	}
	if got[2].Content != "" {
		t.Fatalf("got[2].Content = %q, want empty content for metadata-only message", got[2].Content)
	}
	if string(got[2].Metadata) == "" {
		t.Fatal("metadata-only message was not preserved")
	}
}

func TestSessionMessagePageSourceRevisionStableAcrossPages(t *testing.T) {
	dir, threadID, path, s := setupClaudeSourceRevisionSession(t)
	first := readClaudeMessagePage(t, s, threadID, dto.MessagePageRequest{Limit: 2})
	second := readClaudeMessagePage(t, s, threadID, dto.MessagePageRequest{Limit: 2, Before: first.NextBefore})
	requireClaudeSourceRevisionStable(t, first, second)
	requireClaudeSourceRevisionPrivate(t, first.SourceRevision, dir, path, "claude-prompt-that-must-not-leak")
}

func TestSessionMessagePageSourceRevisionChangesOnSameSecondAppend(t *testing.T) {
	_, threadID, path, s := setupClaudeSourceRevisionSession(t)
	before := readClaudeMessagePage(t, s, threadID, dto.MessagePageRequest{Limit: 2})
	appendClaudeSameSecondDuplicate(t, path)
	after := readClaudeMessagePage(t, s, threadID, dto.MessagePageRequest{Limit: 2})
	if after.SourceRevision == "" {
		t.Fatal("SourceRevision after append is empty")
	}
	if after.SourceRevision == before.SourceRevision {
		t.Fatalf("SourceRevision did not change after same-second duplicate append: %q", after.SourceRevision)
	}
}

func setupClaudeSourceRevisionSession(t *testing.T) (string, string, string, *session) {
	t.Helper()
	dir := t.TempDir()
	threadID := "thread-source-revision"
	writeClaudeHistoryMessages(t, dir, threadID, []string{"one", "two", "claude-prompt-that-must-not-leak", "four"})
	path := filepath.Join(dir, "projects", "test-project", threadID+".jsonl")
	return dir, threadID, path, &session{threadID: threadID, history: &historyBackend{sessionDir: dir}}
}

func readClaudeMessagePage(t *testing.T, s *session, threadID string, req dto.MessagePageRequest) dto.MessagePageResult {
	t.Helper()
	page, err := s.ReadMessagesPage(context.Background(), threadID, req)
	if err != nil {
		t.Fatalf("ReadMessagesPage() error = %v", err)
	}
	return page
}

func requireClaudeSourceRevisionStable(t *testing.T, first, second dto.MessagePageResult) {
	t.Helper()
	if first.SourceRevision == "" {
		t.Fatal("first SourceRevision is empty")
	}
	if second.SourceRevision == "" {
		t.Fatal("second SourceRevision is empty")
	}
	if first.SourceRevision != second.SourceRevision {
		t.Fatalf("SourceRevision changed across one source snapshot: first=%q second=%q", first.SourceRevision, second.SourceRevision)
	}
}

func requireClaudeSourceRevisionPrivate(t *testing.T, revision string, forbidden ...string) {
	t.Helper()
	for _, value := range forbidden {
		if strings.Contains(revision, value) {
			t.Fatalf("SourceRevision exposes source detail %q", value)
		}
	}
}

func appendClaudeSameSecondDuplicate(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(append) error = %v", err)
	}
	const line = `{"type":"user","timestamp":"2026-07-12T01:02:03Z","message":{"role":"user","content":[{"type":"text","text":"claude-prompt-that-must-not-leak"}]}}`
	if _, err := file.WriteString(line + "\n"); err != nil {
		_ = file.Close()
		t.Fatalf("WriteString(append) error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(append) error = %v", err)
	}
}
