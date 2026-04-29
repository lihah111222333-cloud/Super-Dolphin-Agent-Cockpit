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

func TestReconcile_RecoversBackupBeforeRemoveOrphans(t *testing.T) {
	// 关键回归：library 没有 "foo"，但 cache 里只有 .foo.bak-*（target 缺失）。
	// 没有 startup recovery，则 .foo.bak-* 会被 removeOrphans 当孤儿删掉，
	// 用户的旧版本被丢。recovery 必须先把 .foo.bak-* 恢复成 foo，再让
	// removeOrphans 把 foo 当孤儿删掉——这是预期行为：library 已删的 skill
	// 应该最终从 cache 消失，但中间不能出现"备份被吃掉"的窗口。
	libRoot := t.TempDir()
	cacheRoot := t.TempDir()
	bak := filepath.Join(cacheRoot, ".foo.bak-1-1")
	if err := os.MkdirAll(bak, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bak, "SKILL.md"), []byte("v-old"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewReconciler(NewStore(libRoot), cacheRoot)
	report, err := r.ReconcileAll()
	if err != nil {
		t.Fatalf("ReconcileAll: %v", err)
	}
	if len(report.Errors) != 0 {
		t.Errorf("unexpected errors: %v", report.Errors)
	}
	// 终态：因为 library 没有 foo，foo 被作为孤儿移除；但 .foo.bak-* 必须先恢复，
	// 否则 removeOrphans 在删 foo 之前就删 .foo.bak-* 而留下不一致状态。
	// 直观断言：恢复+删孤儿后 cache 里既没有 .foo.bak-* 也没有 foo。
	if _, err := os.Stat(bak); !os.IsNotExist(err) {
		t.Errorf("backup still present after reconcile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, "foo")); !os.IsNotExist(err) {
		t.Errorf("restored target should have been removed as orphan: %v", err)
	}
	// Removed 计数包含 "foo"（recovery 恢复出来的）。
	if report.Removed != 1 {
		t.Errorf("Removed = %d, want 1 (foo restored then removed as orphan)", report.Removed)
	}
}

func TestReconcile_DiscardsStaleTmpStaging(t *testing.T) {
	libRoot := t.TempDir()
	cacheRoot := t.TempDir()
	tmp := filepath.Join(cacheRoot, ".x.tmp-1-1")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewReconciler(NewStore(libRoot), cacheRoot)
	if _, err := r.ReconcileAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("stale .tmp- not removed by recovery: %v", err)
	}
}
