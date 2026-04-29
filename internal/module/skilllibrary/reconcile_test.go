package skilllibrary

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skillforge"
)

func TestReconcile_BuildsCacheFromLibrary(t *testing.T) {
	libRoot := t.TempDir()
	cacheRoot := t.TempDir()
	s := NewStore(libRoot)
	if _, err := SeedBuiltins(s, "test-1"); err != nil {
		t.Fatal(err)
	}
	r := NewReconciler(s, cacheRoot)
	report, err := r.ReconcileAll()
	if err != nil {
		t.Fatalf("ReconcileAll: %v", err)
	}
	names, _ := skillforge.ListEmbeddedSkillNames()
	if report.Built != len(names) {
		t.Errorf("Built = %d, want %d", report.Built, len(names))
	}
	// Spot-check: first skill has cache entry with SKILL.md
	for _, n := range names[:1] {
		if _, err := os.Stat(filepath.Join(cacheRoot, n, "SKILL.md")); err != nil {
			t.Errorf("missing cache for %s: %v", n, err)
		}
	}
}

func TestReconcile_RemovesOrphanCacheEntries(t *testing.T) {
	libRoot := t.TempDir()
	cacheRoot := t.TempDir()
	// Pre-populate cache with a skill that's not in library
	orphan := filepath.Join(cacheRoot, "orphan")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewReconciler(NewStore(libRoot), cacheRoot)
	report, err := r.ReconcileAll()
	if err != nil {
		t.Fatal(err)
	}
	if report.Removed != 1 {
		t.Errorf("Removed = %d, want 1", report.Removed)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan not removed: %v", err)
	}
}

func TestReconcile_DisabledSkillIsRemovedFromCache(t *testing.T) {
	libRoot := t.TempDir()
	cacheRoot := t.TempDir()
	s := NewStore(libRoot)
	src := []byte("---\nname: x\ndescription: d\n---\n# x\n## A\nbody\n")
	if err := s.Install("x", src, SkillMeta{Name: "x", Origin: OriginBuiltin, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	r := NewReconciler(s, cacheRoot)
	if _, err := r.ReconcileAll(); err != nil {
		t.Fatal(err)
	}
	// Now disable
	if err := s.Install("x", src, SkillMeta{Name: "x", Origin: OriginBuiltin, Version: "1", Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReconcileAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, "x")); !os.IsNotExist(err) {
		t.Errorf("disabled skill still in cache: %v", err)
	}
}

func TestReconcileOne_RemovesWhenNotInLibrary(t *testing.T) {
	libRoot := t.TempDir()
	cacheRoot := t.TempDir()
	// Cache has an orphan
	orphan := filepath.Join(cacheRoot, "ghost")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewReconciler(NewStore(libRoot), cacheRoot)
	if err := r.ReconcileOne("ghost"); err != nil {
		t.Fatalf("ReconcileOne: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("ghost not removed: %v", err)
	}
}

func TestReconcileOne_BuildsValidSkill(t *testing.T) {
	libRoot := t.TempDir()
	cacheRoot := t.TempDir()
	s := NewStore(libRoot)
	src := []byte("---\nname: y\ndescription: d\n---\n# y\n## sec\nbody\n")
	if err := s.Install("y", src, SkillMeta{Name: "y", Origin: OriginBuiltin, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	r := NewReconciler(s, cacheRoot)
	if err := r.ReconcileOne("y"); err != nil {
		t.Fatalf("ReconcileOne: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, "y", "SKILL.md")); err != nil {
		t.Errorf("cache missing: %v", err)
	}
}
