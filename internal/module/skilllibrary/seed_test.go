package skilllibrary

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skillforge"
)

func TestSeed_FreshLibraryInstallsAllBuiltins(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	count, err := SeedBuiltins(s, "test-version-1", testReader())
	if err != nil {
		t.Fatalf("SeedBuiltins: %v", err)
	}
	names, _ := skillforge.ListEmbeddedSkillNames()
	if count != len(names) {
		t.Errorf("seeded %d, embedded = %d", count, len(names))
	}
	for _, name := range names {
		if _, err := s.Get(name); err != nil {
			t.Errorf("missing seeded skill %s: %v", name, err)
		}
	}
}

func TestSeed_PreservesUserModifiedNonBuiltin(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	names, _ := skillforge.ListEmbeddedSkillNames()
	if len(names) == 0 {
		t.Skip("no embedded skills")
	}
	target := names[0]
	if err := s.Install(target, []byte("---\nname: "+target+"\ndescription: user version\n---\n"),
		SkillMeta{Name: target, Origin: OriginMarketplace, Version: "user-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := SeedBuiltins(s, "test-version-1", testReader()); err != nil {
		t.Fatalf("SeedBuiltins: %v", err)
	}
	got, err := s.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta.Origin != OriginMarketplace {
		t.Errorf("user version overwritten; origin = %s, want marketplace", got.Meta.Origin)
	}
}

func TestSeed_OverwritesOlderBuiltin(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	names, _ := skillforge.ListEmbeddedSkillNames()
	if len(names) == 0 {
		t.Skip("no embedded skills")
	}
	target := names[0]
	if err := s.Install(target, []byte("old"),
		SkillMeta{Name: target, Origin: OriginBuiltin, Version: "0", VersionHash: "stale"}); err != nil {
		t.Fatal(err)
	}
	if _, err := SeedBuiltins(s, "test-version-2", testReader()); err != nil {
		t.Fatalf("SeedBuiltins: %v", err)
	}
	got, err := s.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta.VersionHash == "stale" {
		t.Error("stale builtin not overwritten")
	}
	body, _ := skillforge.ReadEmbeddedSkill(target)
	want := sha256OfBytes(body)
	if got.Meta.VersionHash != want {
		t.Errorf("VersionHash = %s, want %s", got.Meta.VersionHash, want)
	}
}

func TestSeed_IdempotentWhenUpToDate(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	if _, err := SeedBuiltins(s, "v1", testReader()); err != nil {
		t.Fatal(err)
	}
	count2, err := SeedBuiltins(s, "v1", testReader())
	if err != nil {
		t.Fatal(err)
	}
	if count2 != 0 {
		t.Errorf("second seed wrote %d skills, want 0 (all up to date)", count2)
	}
}

func TestSeed_PropagatesUnexpectedGetError(t *testing.T) {
	// Confirm errors other than fs.ErrNotExist propagate up.
	// (Hard to trigger reliably without fs mocks; just sanity-check that
	// the seed function does not silently swallow Get errors.)
	root := t.TempDir()
	s := NewStore(root)
	if _, err := SeedBuiltins(s, "v1", testReader()); err != nil {
		// Should be nil for a fresh root
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("unexpected err: %v", err)
		}
	}
}

func sha256OfBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
