package skilllibrary

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore_InstallAndUninstall(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)

	skillSrc := []byte("---\nname: x\ndescription: d\n---\n# x\n")
	meta := SkillMeta{Name: "x", Origin: OriginMarketplace, Version: "0.1.0"}

	if err := s.Install("x", skillSrc, meta); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "x", "SKILL.md")); err != nil {
		t.Errorf("SKILL.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "x", ".skill-meta.json")); err != nil {
		t.Errorf("meta missing: %v", err)
	}

	if err := s.Uninstall("x"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "x")); !os.IsNotExist(err) {
		t.Errorf("dir not removed: %v", err)
	}
}

func TestStore_GetReturnsErrNotFound(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.Get("missing"); !os.IsNotExist(err) {
		t.Errorf("Get(missing): want IsNotExist, got %v", err)
	}
}

func TestStore_InstallOverwritesExisting(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	v1 := []byte("---\nname: x\ndescription: v1\n---\n")
	v2 := []byte("---\nname: x\ndescription: v2\n---\n")
	if err := s.Install("x", v1, SkillMeta{Name: "x", Origin: OriginBuiltin, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Install("x", v2, SkillMeta{Name: "x", Origin: OriginMarketplace, Version: "2"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("x")
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta.Version != "2" {
		t.Errorf("Version = %s, want 2", got.Meta.Version)
	}
	if got.Meta.Origin != OriginMarketplace {
		t.Errorf("Origin = %s, want marketplace", got.Meta.Origin)
	}
}

func TestStore_UninstallMissingIsIdempotent(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Uninstall("never-existed"); err != nil {
		t.Errorf("Uninstall missing: want nil err, got %v", err)
	}
}

func TestStore_EmptyNameRejected(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Install("", []byte("ok"), SkillMeta{Name: "x"}); err == nil {
		t.Error("Install(empty name) should error")
	}
	if err := s.Uninstall(""); err == nil {
		t.Error("Uninstall(empty name) should error")
	}
}

func TestStore_ListDelegatesToScan(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	for _, n := range []string{"alpha", "beta"} {
		if err := s.Install(n, []byte("---\nname: "+n+"\ndescription: x\n---\n"),
			SkillMeta{Name: n, Origin: OriginBuiltin, Version: "1"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("List len = %d, want 2", len(got))
	}
}
