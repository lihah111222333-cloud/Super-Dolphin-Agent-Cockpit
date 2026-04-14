package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConsolidationPromptIncludesIndexTopicsAndLogs(t *testing.T) {
	root := newTestMemoryRoot(t)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(root) error = %v", err)
	}
	indexPath := memoryIndexPath(root)
	if err := os.WriteFile(indexPath, []byte("- [Keep answers short](feedback/keep-answers-short.md)\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(MEMORY.md) error = %v", err)
	}
	topicPath := filepath.Join(root, "feedback", "keep-answers-short.md")
	writeExtractFixture(t, topicPath, testMemoryEntry(
		"Keep answers short",
		"concise",
		MemoryTypeFeedback,
		"Keep answers short\nWhy: default to concise bullet points.",
	))
	logPath := filepath.Join(root, "logs", "2026", "04", "2026-04-15.md")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(logs) error = %v", err)
	}
	if err := os.WriteFile(logPath, []byte("- [09:31] prefer absolute dates in incident summaries\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(log) error = %v", err)
	}

	input, err := loadConsolidationPromptInput(root, &Config{Enabled: true, RootDir: root})
	if err != nil {
		t.Fatalf("loadConsolidationPromptInput() error = %v", err)
	}
	input.Limit = 3
	prompt := buildConsolidationPrompt(input)
	for _, snippet := range []string{
		"### Phase 1 — Orient",
		"### Phase 2 — Gather",
		"### Phase 3 — Consolidate",
		"### Phase 4 — Prune",
		"feedback/keep-answers-short.md",
		"logs/2026/04/2026-04-15.md",
		"prefer absolute dates in incident summaries",
		"Keep answers short",
		"MEMORY.md",
	} {
		if !strings.Contains(prompt, snippet) {
			t.Fatalf("prompt missing %q:\n%s", snippet, prompt)
		}
	}
}

func TestConsolidationPromptRejectsAgentMemoryPath(t *testing.T) {
	root := newTestMemoryRoot(t)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(root) error = %v", err)
	}
	cfg := &Config{Enabled: true, RootDir: root}
	manager := NewAgentMemoryManager(cfg)
	agentDir, err := manager.GetAgentMemoryDir("Worker", MemoryScopeUser)
	if err != nil {
		t.Fatalf("GetAgentMemoryDir() error = %v", err)
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(agentDir) error = %v", err)
	}
	if err := os.WriteFile(memoryIndexPath(agentDir), []byte("# agent memory\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(agent MEMORY.md) error = %v", err)
	}

	_, err = loadConsolidationPromptInput(agentDir, cfg)
	if !errors.Is(err, ErrConsolidationAgentMemoryPath) {
		t.Fatalf("loadConsolidationPromptInput(agentDir) error = %v, want %v", err, ErrConsolidationAgentMemoryPath)
	}
}

func TestConsolidationConsolidateUsesMemoryIndexTopicsAndLogs(t *testing.T) {
	root := newTestMemoryRoot(t)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(root) error = %v", err)
	}
	cfg := &Config{Enabled: true, RootDir: root}
	consolidator := NewAutoDreamConsolidator(NewMemoryExtractor())
	consolidator.cfg = cfg
	if err := os.WriteFile(memoryIndexPath(root), []byte("- [Keep answers short](feedback/keep-answers-short.md)\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(MEMORY.md) error = %v", err)
	}
	topicPath := filepath.Join(root, "feedback", "keep-answers-short.md")
	writeExtractFixture(t, topicPath, testMemoryEntry(
		"Keep answers short",
		"legacy",
		MemoryTypeFeedback,
		"Keep answers short\nWhy: older guidance.",
	))
	logPath := filepath.Join(root, "logs", "2026", "04", "2026-04-15.md")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(logs) error = %v", err)
	}
	if err := os.WriteFile(logPath, []byte("- [09:31] prefer absolute dates in incident summaries\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(log) error = %v", err)
	}

	called := 0
	err := consolidator.Consolidate(context.Background(), root, func(_ context.Context, prompt string) (string, error) {
		called++
		for _, snippet := range []string{"MEMORY.md", "feedback/keep-answers-short.md", "logs/2026/04/2026-04-15.md"} {
			if !strings.Contains(prompt, snippet) {
				t.Fatalf("prompt missing %q:\n%s", snippet, prompt)
			}
		}
		return `{"memories":[{"content":"Keep answers short\nWhy: default to concise responses.","type":"feedback","tags":["style"]}]}`, nil
	})
	if err != nil {
		t.Fatalf("Consolidate() error = %v", err)
	}
	if called != 1 {
		t.Fatalf("extract func calls = %d, want 1", called)
	}
	entries, err := scanMemoryEntries(root)
	if err != nil {
		t.Fatalf("scanMemoryEntries() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("scanMemoryEntries() entries = %d, want 1", len(entries))
	}
	if !strings.Contains(entries[0].Content, "default to concise responses") {
		t.Fatalf("consolidated content = %q", entries[0].Content)
	}
	indexEntries := readIndexEntries(t, root)
	if len(indexEntries) != 1 || strings.HasPrefix(indexEntries[0].Path, "logs/") {
		t.Fatalf("index entries = %#v, want one durable topic entry without logs", indexEntries)
	}
}
