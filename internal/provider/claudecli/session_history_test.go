package claudecli

import (
	"encoding/json"
	"testing"
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
