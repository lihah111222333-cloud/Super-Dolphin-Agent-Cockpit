package skilllibrary

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScan_FindsValidSkillsOnly(t *testing.T) {
	root := t.TempDir()
	mkSkill(t, root, "alpha", true)
	mkSkill(t, root, "beta", false) // missing meta -> skipped
	if err := os.MkdirAll(filepath.Join(root, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "loose.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Meta.Name != "alpha" {
		t.Errorf("got[0].Meta.Name = %s, want alpha", got[0].Meta.Name)
	}
}

func TestScan_NonexistentRootReturnsNilNil(t *testing.T) {
	got, err := Scan(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("got = %v, want nil", got)
	}
}

func TestScan_SortsByName(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"charlie", "alpha", "bravo"} {
		mkSkill(t, root, n, true)
	}
	got, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	want := []string{"alpha", "bravo", "charlie"}
	for i, e := range got {
		if e.Meta.Name != want[i] {
			t.Errorf("got[%d].Name = %q, want %q", i, e.Meta.Name, want[i])
		}
	}
}

func TestScan_MissingSKILLMDSkipped(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Only meta, no SKILL.md
	if err := WriteMeta(dir, SkillMeta{Name: "x", Origin: OriginBuiltin, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	got, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0 (no SKILL.md)", len(got))
	}
}

func mkSkill(t *testing.T, root, name string, withMeta bool) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: "+name+"\ndescription: x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if withMeta {
		if err := WriteMeta(dir, SkillMeta{Name: name, Origin: OriginBuiltin, Version: "1"}); err != nil {
			t.Fatal(err)
		}
	}
}
