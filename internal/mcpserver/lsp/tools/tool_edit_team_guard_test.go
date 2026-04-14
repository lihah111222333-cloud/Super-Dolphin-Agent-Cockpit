package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	memorymod "github.com/anthropic-ai/super-agent-v3/internal/module/memory"
)

func TestGuardTeamMemoryWriteRejectsSecretOnTeamPath(t *testing.T) {
	t.Setenv("ENABLE_MEMORY_SYSTEM", "1")
	t.Setenv("MULTI_AGENT_MEMORY_FEATURE_TEAMMEM", "1")
	repoRoot := t.TempDir()
	path := filepath.Join(repoRoot, "team", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := guardTeamMemoryWrite(path, "token = \"ghp_abcdefghijklmnopqrstuvwxyz1234567890AB\"\n"); !errors.Is(err, memorymod.ErrTeamMemSecretDetected) {
		t.Fatalf("guardTeamMemoryWrite(secret) error = %v, want %v", err, memorymod.ErrTeamMemSecretDetected)
	}
}

func TestGuardTeamMemoryWriteIgnoresNonTeamMarkdown(t *testing.T) {
	t.Setenv("ENABLE_MEMORY_SYSTEM", "1")
	t.Setenv("MULTI_AGENT_MEMORY_FEATURE_TEAMMEM", "1")
	path := filepath.Join(t.TempDir(), "docs", "guide.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := guardTeamMemoryWrite(path, "plain text\n"); err != nil {
		t.Fatalf("guardTeamMemoryWrite(non-team) error = %v", err)
	}
}

func TestFilterTeamMemoryBatchWritesSkipsOnlySecretTeamFiles(t *testing.T) {
	t.Setenv("ENABLE_MEMORY_SYSTEM", "1")
	t.Setenv("MULTI_AGENT_MEMORY_FEATURE_TEAMMEM", "1")
	repoRoot := t.TempDir()
	safePath := filepath.Join(repoRoot, "team", "safe.md")
	secretPath := filepath.Join(repoRoot, "team", "secret.md")
	nonTeamPath := filepath.Join(repoRoot, "docs", "readme.md")
	allowed, warning, err := filterTeamMemoryBatchWrites(map[string]string{
		safePath:    "# Safe\n- roadmap only\n",
		secretPath:  "api_key = \"sk-proj-abcdefghijklmnopqrstuvwxyz1234567890\"\n",
		nonTeamPath: "# Docs\n",
	})
	if err != nil {
		t.Fatalf("filterTeamMemoryBatchWrites() error = %v", err)
	}
	if len(allowed) != 2 {
		t.Fatalf("len(allowed) = %d, want 2", len(allowed))
	}
	if _, ok := allowed[safePath]; !ok {
		t.Fatalf("allowed missing safe team path: %#v", allowed)
	}
	if _, ok := allowed[nonTeamPath]; !ok {
		t.Fatalf("allowed missing non-team path: %#v", allowed)
	}
	if _, ok := allowed[secretPath]; ok {
		t.Fatalf("allowed unexpectedly contains secret path: %#v", allowed)
	}
	if !strings.Contains(warning, secretPath) {
		t.Fatalf("warning = %q, want to mention %q", warning, secretPath)
	}
}
