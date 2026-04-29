package skilllibrary

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMeta_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := SkillMeta{
		Name:           "tdd",
		Origin:         OriginBuiltin,
		Version:        "1.0.0",
		VersionHash:    "sha256:abc",
		Pinned:         true,
		ReplacesNative: map[string][]string{"claude": {"feature-dev/feature-dev"}},
	}
	if err := WriteMeta(dir, want); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".skill-meta.json")); err != nil {
		t.Fatalf("meta file missing: %v", err)
	}
	got, err := ReadMeta(dir)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if got.Name != want.Name || got.Origin != want.Origin || got.VersionHash != want.VersionHash {
		t.Errorf("ReadMeta = %+v, want %+v", got, want)
	}
	if !got.Pinned {
		t.Errorf("Pinned not preserved")
	}
	if len(got.ReplacesNative["claude"]) != 1 {
		t.Errorf("ReplacesNative not preserved")
	}
}

func TestMeta_MissingFileReturnsErrNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadMeta(dir)
	if err == nil || !os.IsNotExist(err) {
		t.Errorf("ReadMeta: want IsNotExist, got %v", err)
	}
}

func TestMeta_EmptyNameAfterUnmarshalReturnsError(t *testing.T) {
	dir := t.TempDir()
	bad := []byte(`{"origin":"builtin","version":"1"}`)
	if err := os.WriteFile(filepath.Join(dir, ".skill-meta.json"), bad, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMeta(dir); err == nil {
		t.Fatal("ReadMeta should reject meta with empty name")
	}
}

func TestMeta_OriginConstants(t *testing.T) {
	cases := map[Origin]string{
		OriginBuiltin:     "builtin",
		OriginMarketplace: "marketplace",
		OriginLocal:       "local",
		OriginDevOverride: "dev-override",
	}
	for k, v := range cases {
		if string(k) != v {
			t.Errorf("Origin(%s) = %q, want %q", v, string(k), v)
		}
	}
}
