package skilllibrary

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestReadSection_FindsByAnchor(t *testing.T) {
	cacheDir := t.TempDir()
	skillDir := filepath.Join(cacheDir, "tdd")
	refDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refDir, "01-红绿重构.md"), []byte("body content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refDir, "02-反模式.md"), []byte("anti pattern"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadSection(cacheDir, "tdd", "红绿重构")
	if err != nil {
		t.Fatalf("ReadSection: %v", err)
	}
	if string(got) != "body content" {
		t.Errorf("got %q, want body content", string(got))
	}

	got, err = ReadSection(cacheDir, "tdd", "反模式")
	if err != nil {
		t.Fatalf("ReadSection: %v", err)
	}
	if string(got) != "anti pattern" {
		t.Errorf("got %q, want anti pattern", string(got))
	}
}

func TestReadSection_UnknownSkillReturnsErrNotExist(t *testing.T) {
	cacheDir := t.TempDir()
	_, err := ReadSection(cacheDir, "nope", "any")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("want fs.ErrNotExist, got %v", err)
	}
}

func TestReadSection_UnknownAnchorReturnsErrNotExist(t *testing.T) {
	cacheDir := t.TempDir()
	skillDir := filepath.Join(cacheDir, "x")
	refDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refDir, "01-something.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadSection(cacheDir, "x", "missing-anchor")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("want fs.ErrNotExist, got %v", err)
	}
}

func TestReadSection_EmptyArgsErrors(t *testing.T) {
	if _, err := ReadSection("", "x", "y"); err == nil {
		t.Error("empty cacheDir should error")
	}
	if _, err := ReadSection("/tmp", "", "y"); err == nil {
		t.Error("empty name should error")
	}
	if _, err := ReadSection("/tmp", "x", ""); err == nil {
		t.Error("empty anchor should error")
	}
}
