package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// makeRefFile creates cacheDir/<name>/references/<prefix>-<anchor>.md with given body.
func makeRefFile(t *testing.T, cacheDir, name, anchor, body string) {
	t.Helper()
	refDir := filepath.Join(cacheDir, name, "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatalf("MkdirAll refDir: %v", err)
	}
	filename := "01-" + anchor + ".md"
	if err := os.WriteFile(filepath.Join(refDir, filename), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

func TestSkillReadSectionTool_ReturnsSectionBody(t *testing.T) {
	cacheDir := t.TempDir()
	makeRefFile(t, cacheDir, "tdd", "red-green-refactor", "## Red Green Refactor\n\nsome content here")

	tool := NewSkillReadSectionTool(cacheDir)
	args := mustMarshal(t, map[string]any{"name": "tdd", "anchor": "red-green-refactor"})
	out, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if string(out) != "## Red Green Refactor\n\nsome content here" {
		t.Fatalf("Call() body = %q, want expected body", string(out))
	}
}

func TestSkillReadSectionTool_MissingSkillReturnsError(t *testing.T) {
	cacheDir := t.TempDir()

	tool := NewSkillReadSectionTool(cacheDir)
	args := mustMarshal(t, map[string]any{"name": "nonexistent", "anchor": "foo"})
	_, err := tool.Call(context.Background(), args)
	if err == nil {
		t.Fatal("Call() expected error for missing skill, got nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Call() error = %v, want wrapping fs.ErrNotExist", err)
	}
	// error must carry skill_read_section: prefix
	if !containsStr(err.Error(), "skill_read_section:") {
		t.Fatalf("error missing skill_read_section: prefix: %v", err)
	}
}

func TestSkillReadSectionTool_MissingAnchorReturnsError(t *testing.T) {
	cacheDir := t.TempDir()
	makeRefFile(t, cacheDir, "tdd", "red-green-refactor", "body")

	tool := NewSkillReadSectionTool(cacheDir)
	args := mustMarshal(t, map[string]any{"name": "tdd", "anchor": "no-such-anchor"})
	_, err := tool.Call(context.Background(), args)
	if err == nil {
		t.Fatal("Call() expected error for missing anchor, got nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Call() error = %v, want wrapping fs.ErrNotExist", err)
	}
	if !containsStr(err.Error(), "skill_read_section:") {
		t.Fatalf("error missing skill_read_section: prefix: %v", err)
	}
}

func TestSkillReadSectionTool_MaxBytesTruncation(t *testing.T) {
	cacheDir := t.TempDir()
	makeRefFile(t, cacheDir, "tdd", "overview", "abcdefghij") // 10 bytes

	tool := NewSkillReadSectionTool(cacheDir)
	args := mustMarshal(t, map[string]any{"name": "tdd", "anchor": "overview", "max_bytes": 4})
	out, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if string(out) != "abcd" {
		t.Fatalf("Call() truncated = %q, want \"abcd\"", string(out))
	}
}

func TestSkillReadSectionTool_MaxBytesZeroNoTruncation(t *testing.T) {
	cacheDir := t.TempDir()
	body := "full body no truncation"
	makeRefFile(t, cacheDir, "skill", "section", body)

	tool := NewSkillReadSectionTool(cacheDir)
	// max_bytes omitted → zero value → no truncation
	args := mustMarshal(t, map[string]any{"name": "skill", "anchor": "section"})
	out, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if string(out) != body {
		t.Fatalf("Call() body = %q, want %q", string(out), body)
	}
}

func TestSkillReadSectionTool_EmptyArgsReturnsError(t *testing.T) {
	cacheDir := t.TempDir()
	tool := NewSkillReadSectionTool(cacheDir)

	// Empty JSON object: name and anchor are empty strings → skilllibrary errors
	args := mustMarshal(t, map[string]any{})
	_, err := tool.Call(context.Background(), args)
	if err == nil {
		t.Fatal("Call() expected error for empty args, got nil")
	}
	if !containsStr(err.Error(), "skill_read_section:") {
		t.Fatalf("error missing skill_read_section: prefix: %v", err)
	}
}

func TestSkillReadSectionTool_InvalidJSONReturnsError(t *testing.T) {
	cacheDir := t.TempDir()
	tool := NewSkillReadSectionTool(cacheDir)

	_, err := tool.Call(context.Background(), json.RawMessage(`{invalid json`))
	if err == nil {
		t.Fatal("Call() expected error for invalid JSON, got nil")
	}
	if !containsStr(err.Error(), "skill_read_section:") {
		t.Fatalf("error missing skill_read_section: prefix: %v", err)
	}
}

func TestNewSkillReadSectionTool_Constructor(t *testing.T) {
	tool := NewSkillReadSectionTool("/some/cache/dir")
	if tool == nil {
		t.Fatal("NewSkillReadSectionTool returned nil")
	}
}

// containsStr is a simple string contains helper to avoid importing strings in test.
func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}
