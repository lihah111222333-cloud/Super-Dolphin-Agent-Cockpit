package skillforge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestForge_EndToEnd(t *testing.T) {
	libDir := t.TempDir()
	cacheDir := t.TempDir()
	src := `---
name: tdd
description: write tests first
---

# tdd

## red

write a failing test first.

## green

implement until test passes.
`
	skillRoot := filepath.Join(libDir, "tdd")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Forge(libDir, cacheDir, "tdd", nil); err != nil {
		t.Fatalf("Forge: %v", err)
	}

	want := []string{
		filepath.Join(cacheDir, "tdd", "SKILL.md"),
		filepath.Join(cacheDir, "tdd", "references", "01-red.md"),
		filepath.Join(cacheDir, "tdd", "references", "02-green.md"),
	}
	for _, p := range want {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected file missing: %s (%v)", p, err)
		}
	}
}

func TestForge_MissingSourceReturnsError(t *testing.T) {
	libDir := t.TempDir()
	cacheDir := t.TempDir()
	err := Forge(libDir, cacheDir, "nope", nil)
	if err == nil {
		t.Fatal("Forge with missing source should return error")
	}
	if !os.IsNotExist(err) {
		// 至少 wraps a NotExist; permit either fs.ErrNotExist or a wrapped form
		t.Logf("returned err is not IsNotExist: %v (still acceptable if wrapped meaningfully)", err)
	}
}

func TestForge_InvalidFrontmatterReturnsError(t *testing.T) {
	libDir := t.TempDir()
	cacheDir := t.TempDir()
	skillRoot := filepath.Join(libDir, "bad")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	// 没 frontmatter
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte("# raw\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Forge(libDir, cacheDir, "bad", nil); err == nil {
		t.Fatal("Forge should reject SKILL.md without frontmatter")
	}
}

func TestForge_AppliesSummaryOverride(t *testing.T) {
	libDir := t.TempDir()
	cacheDir := t.TempDir()
	src := `---
name: ovr
description: x
---

# ovr

## A

原文内容
`
	skillRoot := filepath.Join(libDir, "ovr")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	override := map[string]string{"A": "覆盖摘要"}
	if err := Forge(libDir, cacheDir, "ovr", override); err != nil {
		t.Fatalf("Forge: %v", err)
	}
	skillMD := filepath.Join(cacheDir, "ovr", "SKILL.md")
	b, err := os.ReadFile(skillMD)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(b), "覆盖摘要") {
		t.Errorf("SkillMD missing override summary: %s", string(b))
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
