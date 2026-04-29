package skillforge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtomicWriteSkill_CreatesTargetDir(t *testing.T) {
	tmp := t.TempDir()
	res := &RenderResult{
		SkillMD:    "---\nname: x\n---\n",
		References: map[string]string{"01-foo.md": "foo body"},
	}
	if err := AtomicWriteSkill(tmp, "x", res); err != nil {
		t.Fatalf("AtomicWriteSkill: %v", err)
	}
	skillFile := filepath.Join(tmp, "x", "SKILL.md")
	refFile := filepath.Join(tmp, "x", "references", "01-foo.md")
	mustContain(t, skillFile, "name: x")
	mustContain(t, refFile, "foo body")
}

func TestAtomicWriteSkill_OverwritesExisting(t *testing.T) {
	tmp := t.TempDir()
	first := &RenderResult{SkillMD: "v1", References: map[string]string{"01-a.md": "a1"}}
	second := &RenderResult{SkillMD: "v2", References: map[string]string{"01-a.md": "a2"}}
	if err := AtomicWriteSkill(tmp, "x", first); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWriteSkill(tmp, "x", second); err != nil {
		t.Fatal(err)
	}
	mustContain(t, filepath.Join(tmp, "x", "SKILL.md"), "v2")
	mustContain(t, filepath.Join(tmp, "x", "references", "01-a.md"), "a2")
}

func TestAtomicWriteSkill_RemovesStaleTmp(t *testing.T) {
	tmp := t.TempDir()
	stale := filepath.Join(tmp, "x.tmp")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "junk"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := &RenderResult{SkillMD: "ok"}
	if err := AtomicWriteSkill(tmp, "x", res); err != nil {
		t.Fatalf("AtomicWriteSkill: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale .tmp not cleaned: err=%v", err)
	}
}

func TestAtomicWriteSkill_EmptyReferences(t *testing.T) {
	tmp := t.TempDir()
	res := &RenderResult{SkillMD: "intro", References: map[string]string{}}
	if err := AtomicWriteSkill(tmp, "z", res); err != nil {
		t.Fatalf("AtomicWriteSkill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "z", "SKILL.md")); err != nil {
		t.Errorf("SKILL.md missing: %v", err)
	}
	// references/ dir 不应被无谓创建
	if _, err := os.Stat(filepath.Join(tmp, "z", "references")); !os.IsNotExist(err) {
		t.Errorf("references dir should not exist when no references; err=%v", err)
	}
}

func TestAtomicWriteSkill_UnicodeFilename(t *testing.T) {
	tmp := t.TempDir()
	res := &RenderResult{
		SkillMD:    "ok",
		References: map[string]string{"01-红绿重构.md": "测试内容"},
	}
	if err := AtomicWriteSkill(tmp, "测试驱动开发", res); err != nil {
		t.Fatalf("AtomicWriteSkill: %v", err)
	}
	mustContain(t, filepath.Join(tmp, "测试驱动开发", "references", "01-红绿重构.md"), "测试内容")
}

func TestAtomicWriteSkill_NilResultReturnsError(t *testing.T) {
	if err := AtomicWriteSkill(t.TempDir(), "x", nil); err == nil {
		t.Fatal("AtomicWriteSkill(nil) should return error")
	}
}

func mustContain(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(b), want) {
		t.Errorf("file %s: want substring %q, got %q", path, want, string(b))
	}
}
