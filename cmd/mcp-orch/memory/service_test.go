package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestServiceWriteCreatesEntryAndUpdatesIndex(t *testing.T) {
	svc := newTestMemoryService(t)
	root := userScopeFixtureRoot(t)

	// Write a feedback memory
	result, err := svc.Write(context.Background(), contract.MemoryWriteRequest{
		Name:    "use-chinese",
		Content: "面向用户的正文一律用中文。commit message 用中文。代码保持英文。",
		Type:    contract.MemoryTypeFeedback,
		Scope:   contract.MemoryScopeUser,
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if result.Path == "" {
		t.Fatal("Write() returned empty path")
	}

	// Verify file exists on disk
	fullPath := filepath.Join(root, result.Path)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", fullPath, err)
	}
	content := string(data)
	// Check frontmatter
	if !containsAll(content, "name:", "type:", "feedback", "source: \"explicit\"") {
		t.Fatalf("file missing expected frontmatter:\n%s", content)
	}
	// Check body content
	if !containsAll(content, "面向用户", "commit message") {
		t.Fatalf("file missing expected body content:\n%s", content)
	}
	// Check auto-added structured sections
	if !containsAll(content, "Why:", "How to apply:") {
		t.Fatalf("file missing auto-added Why/How to apply sections:\n%s", content)
	}

	// Verify MEMORY.md index updated
	indexData, err := os.ReadFile(filepath.Join(root, memoryIndexFileName))
	if err != nil {
		t.Fatalf("ReadFile(MEMORY.md) error = %v", err)
	}
	if !strings.Contains(string(indexData), "use-chinese") {
		t.Fatalf("MEMORY.md index missing entry:\n%s", string(indexData))
	}

	// Verify round-trip: read it back
	readResult, err := svc.Read(context.Background(), contract.MemoryReadRequest{
		Name:  "use-chinese",
		Scope: contract.MemoryScopeUser,
	})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if readResult.Entry == nil {
		t.Fatal("Read() returned nil entry")
	}
	if readResult.Entry.Name != "use-chinese" {
		t.Fatalf("Read().Name = %q, want %q", readResult.Entry.Name, "use-chinese")
	}
}

func TestServiceWriteDeduplicatesSameName(t *testing.T) {
	svc := newTestMemoryService(t)

	// Write first version
	r1, err := svc.Write(context.Background(), contract.MemoryWriteRequest{
		Name:    "test-rule",
		Content: "原始内容",
		Type:    contract.MemoryTypeFeedback,
		Scope:   contract.MemoryScopeUser,
	})
	if err != nil {
		t.Fatalf("Write(1) error = %v", err)
	}

	// Write second version with same name
	r2, err := svc.Write(context.Background(), contract.MemoryWriteRequest{
		Name:    "test-rule",
		Content: "更新后的内容",
		Type:    contract.MemoryTypeFeedback,
		Scope:   contract.MemoryScopeUser,
	})
	if err != nil {
		t.Fatalf("Write(2) error = %v", err)
	}

	// Should write to same path (update, not duplicate)
	if r1.Path != r2.Path {
		t.Fatalf("second Write created new file: %q != %q", r1.Path, r2.Path)
	}

	// Read back should have new content
	root := userScopeFixtureRoot(t)
	data, err := os.ReadFile(filepath.Join(root, r2.Path))
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	if !strings.Contains(string(data), "更新后的内容") {
		t.Fatalf("file has old content:\n%s", string(data))
	}
}

func TestServiceWriteProjectType(t *testing.T) {
	svc := newTestMemoryService(t)

	result, err := svc.Write(context.Background(), contract.MemoryWriteRequest{
		Name:    "tech-stack",
		Content: "我们用的是 PostgreSQL 不是 MySQL。CI 跑在 GitHub Actions。",
		Type:    contract.MemoryTypeProject,
		Scope:   contract.MemoryScopeUser,
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if result.Path == "" {
		t.Fatal("Write() returned empty path")
	}
	// Verify type is project
	root := userScopeFixtureRoot(t)
	data, err := os.ReadFile(filepath.Join(root, result.Path))
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	if !strings.Contains(string(data), "project") {
		t.Fatalf("file missing project type:\n%s", string(data))
	}
}

func TestServiceWriteEmptyNameReturnsError(t *testing.T) {
	svc := newTestMemoryService(t)
	_, err := svc.Write(context.Background(), contract.MemoryWriteRequest{
		Name:    "",
		Content: "some content",
		Type:    contract.MemoryTypeFeedback,
		Scope:   contract.MemoryScopeUser,
	})
	if err == nil {
		t.Fatal("Write() with empty name should return error")
	}
}

func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
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
