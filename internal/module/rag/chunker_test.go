package rag

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSplitTextSplitsAtBoundaryNearDefaultTarget(t *testing.T) {
	text := numberedText(700, map[int]string{500: "。"})

	chunks, err := SplitText(text, DefaultChunkOptions())
	if err != nil {
		t.Fatalf("SplitText() error = %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("SplitText() returned %d chunks, want 2", len(chunks))
	}
	if got := len(strings.Fields(chunks[0].Text)); got != 500 {
		t.Fatalf("first chunk token count = %d, want 500", got)
	}
	if !strings.HasSuffix(strings.TrimSpace(chunks[0].Text), "。") {
		t.Fatalf("first chunk = %q, want sentence boundary suffix", chunks[0].Text)
	}
	if chunks[0].EndToken != 500 {
		t.Fatalf("first chunk EndToken = %d, want 500", chunks[0].EndToken)
	}
	if got := joinChunkText(chunks); got != text {
		t.Fatalf("joined chunk text changed input:\ngot  %q\nwant %q", got, text)
	}
}

func TestSplitTextPrefersExactTargetBoundaryBeforeStrongerDistantBoundary(t *testing.T) {
	text := numberedText(12, map[int]string{
		5: "，",
		6: "。",
	})
	opts := ChunkOptions{
		TargetTokens: 5,
		MinTokens:    4,
		MaxTokens:    7,
	}

	chunks, err := SplitText(text, opts)
	if err != nil {
		t.Fatalf("SplitText() error = %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("SplitText() returned %d chunks, want 2", len(chunks))
	}
	if chunks[0].EndToken != 5 {
		t.Fatalf("first chunk EndToken = %d, want exact target 5", chunks[0].EndToken)
	}
	if !strings.HasSuffix(strings.TrimSpace(chunks[0].Text), "，") {
		t.Fatalf("first chunk = %q, want comma boundary suffix", chunks[0].Text)
	}
}

func TestSplitTextFallsBackToTargetWhenNoBoundaryExistsInWindow(t *testing.T) {
	text := numberedText(12, nil)
	opts := ChunkOptions{
		TargetTokens: 5,
		MinTokens:    4,
		MaxTokens:    7,
	}

	chunks, err := SplitText(text, opts)
	if err != nil {
		t.Fatalf("SplitText() error = %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("SplitText() returned %d chunks, want 2", len(chunks))
	}
	if chunks[0].EndToken != 5 {
		t.Fatalf("first chunk EndToken = %d, want target token 5", chunks[0].EndToken)
	}
	if got := len(strings.Fields(chunks[0].Text)); got != 5 {
		t.Fatalf("first chunk token count = %d, want 5", got)
	}
	if got := joinChunkText(chunks); got != text {
		t.Fatalf("joined chunk text changed input:\ngot  %q\nwant %q", got, text)
	}
}

func TestSplitTextRejectsInvalidOptions(t *testing.T) {
	_, err := SplitText("hello。", ChunkOptions{})
	if !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("SplitText() error = %v, want ErrInvalidOptions", err)
	}
}

func numberedText(count int, suffixByToken map[int]string) string {
	parts := make([]string, count)
	for i := 1; i <= count; i++ {
		parts[i-1] = fmt.Sprintf("tok%03d%s", i, suffixByToken[i])
	}
	return strings.Join(parts, " ")
}

func joinChunkText(chunks []Chunk) string {
	var b strings.Builder
	for _, chunk := range chunks {
		b.WriteString(chunk.Text)
	}
	return b.String()
}
