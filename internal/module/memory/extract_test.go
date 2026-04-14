package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemoryExtractorExtractParsesEnvelope(t *testing.T) {
	extractor := NewMemoryExtractor()
	called := 0
	memories, err := extractor.Extract(context.Background(), func(_ context.Context, prompt string) (string, error) {
		called++
		if !strings.Contains(prompt, "Conversation transcript:") {
			t.Fatalf("prompt missing transcript header: %q", prompt)
		}
		if !strings.Contains(prompt, "keep diffs focused") {
			t.Fatalf("prompt missing transcript body: %q", prompt)
		}
		return `{"memories":[{"content":"Always keep diffs focused.","type":"feedback","tags":["review","diff"]},{"content":"Grafana dashboard lives at https://grafana.example.com/team/core.","type":"reference","tags":["grafana"]}]}`, nil
	}, ExtractParams{Transcript: "User: keep diffs focused", MaxItems: 2})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if called != 1 {
		t.Fatalf("extract func called %d times, want 1", called)
	}
	if len(memories) != 2 {
		t.Fatalf("len(memories) = %d, want 2", len(memories))
	}
	if got, want := memories[0].Type, MemoryTypeFeedback; got != want {
		t.Fatalf("memories[0].Type = %q, want %q", got, want)
	}
	if got, want := memories[1].Type, MemoryTypeReference; got != want {
		t.Fatalf("memories[1].Type = %q, want %q", got, want)
	}
}

func TestMemoryExtractorExtractFiltersInvalidItems(t *testing.T) {
	extractor := &MemoryExtractor{MaxItems: 3}
	memories, err := extractor.Extract(context.Background(), func(_ context.Context, _ string) (string, error) {
		return `[{"content":""},{"content":"你偏好简洁直接的回复风格。","tags":["style","style"]},{"content":"你偏好简洁直接的回复风格。","type":"user"}]`, nil
	}, ExtractParams{Transcript: "User: remember my response style"})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(memories) != 1 {
		t.Fatalf("len(memories) = %d, want 1", len(memories))
	}
	if got, want := memories[0].Type, MemoryTypeUser; got != want {
		t.Fatalf("Type = %q, want %q", got, want)
	}
	if got, want := memories[0].Tags, []string{"style"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("Tags = %#v, want %#v", got, want)
	}
}

func TestMemoryExtractorExtractSkipsEmptyTranscript(t *testing.T) {
	extractor := NewMemoryExtractor()
	called := false
	memories, err := extractor.Extract(context.Background(), func(context.Context, string) (string, error) {
		called = true
		return `[]`, nil
	}, ExtractParams{})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if called {
		t.Fatal("extract func should not be called for empty transcript")
	}
	if len(memories) != 0 {
		t.Fatalf("len(memories) = %d, want 0", len(memories))
	}
}

func TestAutoDreamConsolidatorConsolidateRemovesDuplicatesAndRebuildsIndex(t *testing.T) {
	root := newTestMemoryRoot(t)
	olderPath := filepath.Join(root, "feedback", "keep-answers-short.md")
	newerPath := filepath.Join(root, "feedback", "keep-answers-short-dup.md")
	stalePath := filepath.Join(root, "feedback", "empty.md")

	writeExtractFixture(t, olderPath, testMemoryEntry(
		"Keep answers short",
		"legacy",
		MemoryTypeFeedback,
		"Keep answers short\nWhy: older guidance.",
	))
	writeExtractFixture(t, newerPath, testMemoryEntry(
		"Keep answers short",
		"fresh",
		MemoryTypeFeedback,
		"Keep answers short\nWhy: prefer concise bullet points.",
	))
	writeExtractFixture(t, stalePath, testMemoryEntry("Empty note", "stale", MemoryTypeFeedback, "   "))

	now := time.Now()
	if err := os.Chtimes(olderPath, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatalf("Chtimes(%q) error = %v", olderPath, err)
	}
	if err := os.Chtimes(newerPath, now, now); err != nil {
		t.Fatalf("Chtimes(%q) error = %v", newerPath, err)
	}

	consolidator := NewAutoDreamConsolidator(NewMemoryExtractor())
	called := 0
	err := consolidator.Consolidate(context.Background(), root, func(_ context.Context, prompt string) (string, error) {
		called++
		if !strings.Contains(prompt, "Keep answers short") {
			t.Fatalf("prompt missing candidate memory: %q", prompt)
		}
		return `{"memories":[{"content":"Keep answers short\nWhy: default to concise responses.\nHow to apply: answer with compact bullets unless asked otherwise.","type":"feedback","tags":["style","concise"]}]}`, nil
	})
	if err != nil {
		t.Fatalf("Consolidate() error = %v", err)
	}
	if called != 1 {
		t.Fatalf("extract func called %d times, want 1", called)
	}
	if _, err := os.Stat(olderPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %q removed, err=%v", olderPath, err)
	}
	if _, err := os.Stat(stalePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %q removed, err=%v", stalePath, err)
	}
	entry, err := readMemoryEntryFile(newerPath)
	if err != nil {
		t.Fatalf("readMemoryEntryFile(%q) error = %v", newerPath, err)
	}
	if !strings.Contains(entry.Content, "How to apply: answer with compact bullets") {
		t.Fatalf("Content = %q, want consolidated content", entry.Content)
	}
	indexEntries := readIndexEntries(t, root)
	if len(indexEntries) != 1 {
		t.Fatalf("ReadMemoryIndex() entries = %d, want 1", len(indexEntries))
	}
	if got, want := indexEntries[0].Path, "feedback/keep-answers-short-dup.md"; got != want {
		t.Fatalf("indexEntries[0].Path = %q, want %q", got, want)
	}
}

func writeExtractFixture(t *testing.T, path string, entry MemoryEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(formatMemoryEntry(entry)), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
