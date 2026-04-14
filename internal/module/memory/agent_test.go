package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentMemoryManagerGetAgentMemoryDirScopeIsolation(t *testing.T) {
	manager, userRoot, projectRoot := newTestAgentMemoryManager(t)
	cases := []struct {
		scope MemoryScope
		want  string
	}{
		{scope: MemoryScopeUser, want: filepath.Join(userRoot, "agents", "Writer")},
		{scope: MemoryScopeProject, want: filepath.Join(projectRoot, "memory", "agents", "Writer")},
		{scope: MemoryScopeLocal, want: filepath.Join(projectRoot, "memory", "local", "Writer")},
	}
	for _, tc := range cases {
		got, err := manager.GetAgentMemoryDir("Writer", tc.scope)
		if err != nil {
			t.Fatalf("GetAgentMemoryDir(%q, %q) error = %v", "Writer", tc.scope, err)
		}
		if got != tc.want {
			t.Fatalf("GetAgentMemoryDir(%q, %q) = %q, want %q", "Writer", tc.scope, got, tc.want)
		}
	}
}

func TestAgentMemoryManagerGetAgentMemoryDirFallsBackOnConflict(t *testing.T) {
	manager, _, projectRoot := newTestAgentMemoryManager(t)
	parent := filepath.Join(projectRoot, "memory", "agents")
	if err := os.MkdirAll(filepath.Join(parent, "Writer"), 0o755); err != nil {
		t.Fatalf("MkdirAll(conflict dir) error = %v", err)
	}
	got, err := manager.GetAgentMemoryDir("writer", MemoryScopeProject)
	if err != nil {
		t.Fatalf("GetAgentMemoryDir() error = %v", err)
	}
	base := filepath.Base(got)
	if base == "writer" {
		t.Fatalf("GetAgentMemoryDir() = %q, want hashed fallback under conflict", got)
	}
	if !strings.HasPrefix(base, "writer-") {
		t.Fatalf("GetAgentMemoryDir() base = %q, want readable prefix fallback", base)
	}
}

func TestAgentMemoryManagerLoadAgentMemoryPromptIncludesEntrypoint(t *testing.T) {
	manager, _, projectRoot := newTestAgentMemoryManager(t)
	dir := filepath.Join(projectRoot, "memory", "agents", "Writer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", dir, err)
	}
	const body = "Remember the preferred review style.\nWhy: smaller diffs are faster to review."
	if err := os.WriteFile(memoryIndexPath(dir), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(MEMORY.md) error = %v", err)
	}
	prompt, err := manager.LoadAgentMemoryPrompt("Writer", MemoryScopeProject)
	if err != nil {
		t.Fatalf("LoadAgentMemoryPrompt() error = %v", err)
	}
	for _, snippet := range []string{"### 1. memory system", "## MEMORY.md", body, "project-scoped agent memory"} {
		if !strings.Contains(prompt, snippet) {
			t.Fatalf("LoadAgentMemoryPrompt() missing %q in prompt:\n%s", snippet, prompt)
		}
	}
}

func TestAgentMemoryManagerLoadAgentMemoryPromptStripsUTF8BOM(t *testing.T) {
	manager, _, projectRoot := newTestAgentMemoryManager(t)
	dir := filepath.Join(projectRoot, "memory", "agents", "Writer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", dir, err)
	}
	body := "\uFEFFRemember the preferred review style."
	if err := os.WriteFile(memoryIndexPath(dir), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(MEMORY.md) error = %v", err)
	}
	prompt, err := manager.LoadAgentMemoryPrompt("Writer", MemoryScopeProject)
	if err != nil {
		t.Fatalf("LoadAgentMemoryPrompt() error = %v", err)
	}
	if strings.Contains(prompt, "\uFEFF") {
		t.Fatalf("LoadAgentMemoryPrompt() prompt still contains BOM: %q", prompt)
	}
	if !strings.Contains(prompt, "Remember the preferred review style.") {
		t.Fatalf("LoadAgentMemoryPrompt() missing BOM-stripped entrypoint in prompt: %q", prompt)
	}
}

func TestTruncateAgentMemoryContentWarnsForLineLimit(t *testing.T) {
	lines := make([]string, agentMemoryMaxLines+5)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %03d", i)
	}

	result := truncateAgentMemoryContent(strings.Join(lines, "\n"))
	if !result.wasLineTruncated || result.wasByteTruncated {
		t.Fatalf("truncateAgentMemoryContent() flags = %+v, want line-only truncation", result)
	}
	if !strings.Contains(result.content, "> WARNING: MEMORY.md is "+truncateAgentMemoryReason(result)+".") {
		t.Fatalf("content = %q, want line truncation warning", result.content)
	}
	if !strings.Contains(result.content, fmt.Sprintf("line %03d", agentMemoryMaxLines-1)) {
		t.Fatalf("content missing final retained line: %q", result.content)
	}
	if strings.Contains(result.content, fmt.Sprintf("line %03d", agentMemoryMaxLines)) {
		t.Fatalf("content still contains first discarded line: %q", result.content)
	}
}

func TestTruncateAgentMemoryContentTracksOriginalCodeUnitCount(t *testing.T) {
	line := strings.Repeat("x", 124)
	lines := make([]string, agentMemoryMaxLines+1)
	for i := range lines {
		lines[i] = line
	}
	trimmedByLines := strings.Join(lines[:agentMemoryMaxLines], "\n")
	if jsStringLength(trimmedByLines) > agentMemoryMaxCodeUnits {
		t.Fatalf("test setup invalid: %d > %d", jsStringLength(trimmedByLines), agentMemoryMaxCodeUnits)
	}

	result := truncateAgentMemoryContent(strings.Join(lines, "\n"))
	if !result.wasLineTruncated {
		t.Fatalf("truncateAgentMemoryContent() flags = %+v, want line truncation", result)
	}
	if result.codeUnitCount <= agentMemoryMaxCodeUnits {
		t.Fatalf("truncateAgentMemoryContent() codeUnitCount = %d, want > %d", result.codeUnitCount, agentMemoryMaxCodeUnits)
	}
	wantReason := fmt.Sprintf("%d lines and %s", len(lines), formatEntrypointSize(result.codeUnitCount))
	if !strings.Contains(result.content, wantReason) {
		t.Fatalf("content = %q, want reason %q", result.content, wantReason)
	}
}

func TestTruncateAgentMemoryContentUsesJSCodeUnitSemantics(t *testing.T) {
	content := strings.Repeat("界", agentMemoryMaxCodeUnits)
	result := truncateAgentMemoryContent(content)
	if result.wasByteTruncated {
		t.Fatalf("truncateAgentMemoryContent() unexpectedly byte-truncated CJK content: %+v", result)
	}
	if result.content != content {
		t.Fatalf("truncateAgentMemoryContent() content changed for in-limit CJK text")
	}
}

func TestTruncateAgentMemoryContentTruncatesAtPriorNewlineForCodeUnitLimit(t *testing.T) {
	content := strings.Repeat("a", 10_000) + "\n" + strings.Repeat("b", 16_000)
	result := truncateAgentMemoryContent(content)
	if result.wasLineTruncated || !result.wasByteTruncated {
		t.Fatalf("truncateAgentMemoryContent() flags = %+v, want byte-only truncation", result)
	}
	if !strings.HasPrefix(result.content, strings.Repeat("a", 10_000)) {
		t.Fatalf("content = %q, want retained prefix before newline", result.content)
	}
	if strings.Contains(result.content, strings.Repeat("b", 100)) {
		t.Fatalf("content = %q, want truncation at prior newline before long tail", result.content)
	}
	if !strings.Contains(result.content, "index entries are too long") {
		t.Fatalf("content = %q, want byte truncation guidance", result.content)
	}
}

func newTestAgentMemoryManager(t *testing.T) (*AgentMemoryManager, string, string) {
	t.Helper()
	userRoot := filepath.Join(t.TempDir(), "user-memory-root")
	projectRoot := filepath.Join(t.TempDir(), "project-root")
	manager := NewAgentMemoryManager(&Config{RootDir: userRoot, ProjectRoot: projectRoot})
	return manager, userRoot, projectRoot
}
