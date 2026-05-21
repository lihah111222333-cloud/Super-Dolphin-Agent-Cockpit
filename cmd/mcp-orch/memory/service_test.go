package memory

import (
	"context"
	"errors"
	"os"
	"os/exec"
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

func TestServiceReadDefaultScopeRemainsProject(t *testing.T) {
	svc := newTestMemoryService(t)
	projectRoot := projectScopeFixtureRoot(t)
	writeMemoryFixture(t, filepath.Join(projectRoot, memoryIndexFileName), "- [Project Alpha](project/project-alpha.md) — project default\n")
	writeMemoryFixture(t, filepath.Join(projectRoot, "project", "project-alpha.md"), `---
name: "Project Alpha"
description: "project default"
type: "project"
---
project memory`)

	result, err := svc.Read(context.Background(), contract.MemoryReadRequest{Name: "Project Alpha"})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	assertMemoryEntry(t, result, contract.MemoryEntry{Name: "Project Alpha", Description: "project default", Type: contract.MemoryTypeProject, Content: "project memory", SourcePath: "project/project-alpha.md"})
}

func TestServiceReadByNameRejectsSymlinkEscape(t *testing.T) {
	svc := newTestMemoryService(t)
	userRoot := userScopeFixtureRoot(t)
	outsideRoot := t.TempDir()
	outsideFile := filepath.Join(outsideRoot, "escape.md")
	writeMemoryFixture(t, outsideFile, `---
name: "Escaped Memory"
description: "outside root"
type: "user"
---
outside content`)

	linkPath := filepath.Join(userRoot, "user", "escape.md")
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(linkPath), err)
	}
	if err := os.Symlink(outsideFile, linkPath); err != nil {
		t.Fatalf("Symlink(%q, %q) error = %v", outsideFile, linkPath, err)
	}

	result, err := svc.Read(context.Background(), contract.MemoryReadRequest{
		Name:  "Escaped Memory",
		Scope: contract.MemoryScopeUser,
	})
	if !errors.Is(err, contract.ErrMemoryInvalidParam) {
		t.Fatalf("Read() error = %v, want ErrMemoryInvalidParam; result=%+v", err, result)
	}
}

func TestServiceReadByNameRejectsTOCTOUSymlinkEscape(t *testing.T) {
	svc := newTestMemoryService(t)
	userRoot := userScopeFixtureRoot(t)
	entryPath := filepath.Join(userRoot, "user", "race.md")
	writeMemoryFixture(t, filepath.Join(userRoot, memoryIndexFileName), "- [Racy Memory](user/race.md) — inside root\n")
	writeMemoryFixture(t, entryPath, `---
name: "Racy Memory"
description: "inside root"
type: "user"
---
inside content`)

	outsideRoot := t.TempDir()
	outsideFile := filepath.Join(outsideRoot, "escape.md")
	writeMemoryFixture(t, outsideFile, `---
name: "Racy Memory"
description: "outside root"
type: "user"
---
outside content`)

	previousHook := afterMemoryEntryPathValidation
	swapped := false
	afterMemoryEntryPathValidation = func(path string) {
		if path != entryPath || swapped {
			return
		}
		swapped = true
		if err := os.Remove(path); err != nil {
			t.Fatalf("Remove(%q) error = %v", path, err)
		}
		if err := os.Symlink(outsideFile, path); err != nil {
			t.Fatalf("Symlink(%q, %q) error = %v", outsideFile, path, err)
		}
	}
	t.Cleanup(func() {
		afterMemoryEntryPathValidation = previousHook
	})

	result, err := svc.Read(context.Background(), contract.MemoryReadRequest{
		Name:  "Racy Memory",
		Scope: contract.MemoryScopeUser,
	})
	if !swapped {
		t.Fatal("test hook did not swap the scanned file")
	}
	if !errors.Is(err, contract.ErrMemoryInvalidParam) {
		t.Fatalf("Read() error = %v, want ErrMemoryInvalidParam; result=%+v", err, result)
	}
}

func TestServiceReadByPathRejectsTOCTOUSymlinkEscape(t *testing.T) {
	svc := newTestMemoryService(t)
	userRoot := userScopeFixtureRoot(t)
	entryPath := filepath.Join(userRoot, "user", "path-race.md")
	writeMemoryFixture(t, filepath.Join(userRoot, memoryIndexFileName), "- [Path Race](user/path-race.md) — inside root\n")
	writeMemoryFixture(t, entryPath, `---
name: "Path Race"
description: "inside root"
type: "user"
---
inside content`)

	outsideRoot := t.TempDir()
	outsideFile := filepath.Join(outsideRoot, "escape.md")
	writeMemoryFixture(t, outsideFile, `---
name: "Path Race"
description: "outside root"
type: "user"
---
outside content`)

	previousHook := afterMemoryEntryPathValidation
	swapped := false
	afterMemoryEntryPathValidation = func(path string) {
		if path != entryPath || swapped {
			return
		}
		swapped = true
		if err := os.Remove(path); err != nil {
			t.Fatalf("Remove(%q) error = %v", path, err)
		}
		if err := os.Symlink(outsideFile, path); err != nil {
			t.Fatalf("Symlink(%q, %q) error = %v", outsideFile, path, err)
		}
	}
	t.Cleanup(func() {
		afterMemoryEntryPathValidation = previousHook
	})

	result, err := svc.Read(context.Background(), contract.MemoryReadRequest{
		Path:  "user/path-race.md",
		Scope: contract.MemoryScopeUser,
	})
	if !swapped {
		t.Fatal("test hook did not swap the direct path file")
	}
	if !errors.Is(err, contract.ErrMemoryInvalidParam) {
		t.Fatalf("Read() error = %v, want ErrMemoryInvalidParam; result=%+v", err, result)
	}
}

func TestServiceReadTeamScopeDoesNotFallThroughToProject(t *testing.T) {
	svc := newTestMemoryService(t)
	projectRoot := projectScopeFixtureRoot(t)
	writeMemoryFixture(t, filepath.Join(projectRoot, memoryIndexFileName), "- [Team Alpha](project/team-alpha.md) — project entry\n")
	writeMemoryFixture(t, filepath.Join(projectRoot, "project", "team-alpha.md"), `---
name: "Team Alpha"
description: "project entry"
type: "project"
---
project memory`)

	result, err := svc.Read(context.Background(), contract.MemoryReadRequest{Name: "Team Alpha", Scope: contract.MemoryScopeTeam})
	if err == nil {
		t.Fatalf("Read(team) error = nil, result=%+v; old mcp-orch memory service must not treat team as project", result)
	}
}

func projectScopeFixtureRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv(envMemoryRoot)
	if root == "" {
		t.Fatal("envMemoryRoot is not set")
	}
	projectRoot, err := findCanonicalGitRoot(context.Background(), filepath.Join(root, "project"))
	if err != nil {
		t.Fatalf("findCanonicalGitRoot() error = %v", err)
	}
	return filepath.Join(root, memoryProjectsDir, sanitizePath(projectRoot), memoryProjectDirName)
}

func newTestMemoryService(t *testing.T) contract.MemoryService {
	t.Helper()
	root := t.TempDir()
	projectRoot := filepath.Join(root, "project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(project root) error = %v", err)
	}
	cmd := exec.Command("git", "init", projectRoot)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init project root: %v\n%s", err, string(output))
	}
	t.Setenv(envMemoryRoot, root)
	return NewService(&Config{
		Enabled:     true,
		EnableTools: true,
		RootDir:     root,
		ProjectRoot: projectRoot,
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
