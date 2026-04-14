package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestServiceReadByNameReturnsEntryContentAndMetadata(t *testing.T) {
	svc := newTestMemoryService(t)
	writeMemoryFixture(t, filepath.Join(userScopeFixtureRoot(t), memoryIndexFileName), "- [Session Profile](user/session-profile.md) — remembers preferences\n")
	writeMemoryFixture(t, filepath.Join(userScopeFixtureRoot(t), "user", "session-profile.md"), `---
name: " Session   Profile "
description: " remembers preferences "
type: "user"
---
prefers dark mode`)

	result, err := svc.Read(context.Background(), contract.MemoryReadRequest{
		Name:  " session  profile ",
		Scope: contract.MemoryScopeUser,
	})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	assertMemoryEntry(t, result, contract.MemoryEntry{
		Name:        "Session Profile",
		Description: "remembers preferences",
		Type:        contract.MemoryTypeUser,
		Content:     "prefers dark mode",
		SourcePath:  "user/session-profile.md",
	})
	if !result.IndexHit {
		t.Fatalf("IndexHit = false, want true")
	}
	if result.Degraded {
		t.Fatalf("Degraded = true, want false")
	}
	if result.Source != "" {
		t.Fatalf("Source = %q, want empty", result.Source)
	}
}

func TestServiceReadByPathRebuildsIndexWhenMemoryIndexMissing(t *testing.T) {
	svc := newTestMemoryService(t)
	writeMemoryFixture(t, filepath.Join(userScopeFixtureRoot(t), "reference", "cli.md"), `---
name: "CLI Notes"
description: "command reference"
type: "reference"
---
use --help`)

	result, err := svc.Read(context.Background(), contract.MemoryReadRequest{
		Path:  " reference/cli.md ",
		Scope: contract.MemoryScopeUser,
		Type:  contract.MemoryTypeReference,
	})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	assertMemoryEntry(t, result, contract.MemoryEntry{
		Name:        "CLI Notes",
		Description: "command reference",
		Type:        contract.MemoryTypeReference,
		Content:     "use --help",
		SourcePath:  "reference/cli.md",
	})
	if !result.IndexHit {
		t.Fatalf("IndexHit = false, want true")
	}
	if !result.Degraded {
		t.Fatalf("Degraded = false, want true")
	}
	if result.Source != "rebuilt_view" {
		t.Fatalf("Source = %q, want rebuilt_view", result.Source)
	}
}

func newTestMemoryService(t *testing.T) contract.MemoryService {
	t.Helper()
	root := t.TempDir()
	t.Setenv(envMemoryRoot, root)
	return NewService(&Config{
		Enabled:     true,
		EnableTools: true,
		RootDir:     root,
		ProjectRoot: filepath.Join(root, "project"),
		MachineID:   "test-machine",
	})
}

func userScopeFixtureRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv(envMemoryRoot)
	if root == "" {
		t.Fatal("envMemoryRoot is not set")
	}
	return filepath.Join(root, memoryUserDir, memoryProjectDirName)
}

func writeMemoryFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func assertMemoryEntry(t *testing.T, result contract.MemoryReadResult, want contract.MemoryEntry) {
	t.Helper()
	if result.Entry == nil {
		t.Fatal("Entry = nil, want value")
	}
	entry := *result.Entry
	if entry.Name != want.Name {
		t.Fatalf("Entry.Name = %q, want %q", entry.Name, want.Name)
	}
	if entry.Description != want.Description {
		t.Fatalf("Entry.Description = %q, want %q", entry.Description, want.Description)
	}
	if entry.Type != want.Type {
		t.Fatalf("Entry.Type = %q, want %q", entry.Type, want.Type)
	}
	if entry.Content != want.Content {
		t.Fatalf("Entry.Content = %q, want %q", entry.Content, want.Content)
	}
	if entry.SourcePath != want.SourcePath {
		t.Fatalf("Entry.SourcePath = %q, want %q", entry.SourcePath, want.SourcePath)
	}
	if result.SourcePath != want.SourcePath {
		t.Fatalf("SourcePath = %q, want %q", result.SourcePath, want.SourcePath)
	}
	if entry.UpdatedAt.IsZero() {
		t.Fatal("Entry.UpdatedAt is zero")
	}
}
