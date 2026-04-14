package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentMemoryManagerGetAgentMemoryDirScopeIsolation(t *testing.T) {
	manager, userRoot, projectRoot := newTestAgentMemoryManager(t)
	cases := []struct {
		scope MemoryScope
		want string
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

func newTestAgentMemoryManager(t *testing.T) (*AgentMemoryManager, string, string) {
	t.Helper()
	userRoot := filepath.Join(t.TempDir(), "user-memory-root")
	projectRoot := filepath.Join(t.TempDir(), "project-root")
	manager := NewAgentMemoryManager(&Config{RootDir: userRoot, ProjectRoot: projectRoot})
	return manager, userRoot, projectRoot
}
