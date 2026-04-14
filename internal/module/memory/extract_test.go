package memory

import (
	"context"
	"strings"
	"testing"
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
