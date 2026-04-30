package skilllibrary

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

func TestReadSection_UnknownAnchorListsAvailable(t *testing.T) {
	cacheDir := t.TempDir()
	refDir := filepath.Join(cacheDir, "tdd", "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"01-red-green.md", "02-anti-patterns.md", "03-pre-commit.md"} {
		if err := os.WriteFile(filepath.Join(refDir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, err := ReadSection(cacheDir, "tdd", "missing")
	if err == nil {
		t.Fatal("expected error for missing anchor")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("must still satisfy errors.Is(fs.ErrNotExist), got %v", err)
	}
	var uae *UnknownAnchorError
	if !errors.As(err, &uae) {
		t.Fatalf("must be *UnknownAnchorError, got %T", err)
	}
	if uae.Name != "tdd" || uae.Anchor != "missing" {
		t.Errorf("Name/Anchor wrong: %+v", uae)
	}
	want := []string{"anti-patterns", "pre-commit", "red-green"}
	if len(uae.Available) != len(want) {
		t.Fatalf("Available = %v, want %v", uae.Available, want)
	}
	for i, w := range want {
		if uae.Available[i] != w {
			t.Errorf("Available[%d] = %q, want %q", i, uae.Available[i], w)
		}
	}
	msg := uae.Error()
	for _, w := range want {
		if !strings.Contains(msg, w) {
			t.Errorf("Error() = %q, must mention %q", msg, w)
		}
	}
}

func TestReadSection_NoSectionsReportsEmptyAvailable(t *testing.T) {
	cacheDir := t.TempDir()
	refDir := filepath.Join(cacheDir, "empty", "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := ReadSection(cacheDir, "empty", "anything")
	var uae *UnknownAnchorError
	if !errors.As(err, &uae) {
		t.Fatalf("must be *UnknownAnchorError, got %T", err)
	}
	if len(uae.Available) != 0 {
		t.Errorf("Available should be empty, got %v", uae.Available)
	}
	if !strings.Contains(uae.Error(), "no sections") {
		t.Errorf("error should hint empty: %q", uae.Error())
	}
}
