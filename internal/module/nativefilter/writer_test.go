package nativefilter

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteWorkspaceSettings_CreatesFile(t *testing.T) {
	ws := t.TempDir()
	body := []byte(`{"permissions":{"deny":["Read"]}}`)
	if err := WriteWorkspaceSettings(ws, body); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(ws, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("got %s want %s", got, body)
	}
}

func TestWriteWorkspaceSettings_OverwritesExisting(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".claude", "settings.json"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteWorkspaceSettings(ws, []byte("new")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(ws, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("got %s, want \"new\"", got)
	}
}

func TestWriteWorkspaceSettings_CreatesDotClaudeDir(t *testing.T) {
	ws := t.TempDir()
	// 不预创建 .claude/，让 writer 自己 mkdir
	if err := WriteWorkspaceSettings(ws, []byte("body")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".claude")); err != nil {
		t.Errorf("expected .claude dir created: %v", err)
	}
}

func TestWriteWorkspaceSettings_EmptyWorkspaceDirReturnsError(t *testing.T) {
	if err := WriteWorkspaceSettings("", []byte("x")); err == nil {
		t.Fatal("empty workspaceDir should error")
	}
}

func TestWriteWorkspaceSettings_NoStaleTmpAfterSuccess(t *testing.T) {
	ws := t.TempDir()
	if err := WriteWorkspaceSettings(ws, []byte("x")); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(ws, ".claude", "settings.json.tmp")
	if _, err := os.Stat(tmp); err == nil {
		t.Errorf("tmp file should not remain after successful rename")
	}
}
