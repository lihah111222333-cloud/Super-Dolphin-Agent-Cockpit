package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildIndexFailsWhenCodemapDirMissing(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "internal", "example")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "example.go"), []byte("package example\n"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	_, _, err := buildIndex(root, filepath.Join(root, "docs", "doc", "codemap"), "2026-05-20")
	if err == nil || !strings.Contains(err.Error(), "scan codemap markdown") {
		t.Fatalf("buildIndex() error = %v, want codemap markdown scan error", err)
	}
}
