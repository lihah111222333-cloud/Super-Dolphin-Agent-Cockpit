package capcontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSaveLoadManifestRoundTripAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	manifest := &Manifest{
		Version:    "1.0",
		Roots:      []string{"internal/contract"},
		Targets:    canonicalTargets,
		Provenance: targetProvenanceForTests(),
		Packages: []PackageManifest{{
			Path:      "internal/contract",
			Name:      "contract",
			Functions: []FunctionManifest{{Name: "New", Exported: true}},
		}},
	}
	if err := SaveManifest(manifest, path); err != nil {
		t.Fatalf("SaveManifest() error = %v", err)
	}
	loaded, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, manifest) {
		t.Fatalf("loaded = %#v, want %#v", loaded, manifest)
	}

	badPath := filepath.Join(t.TempDir(), "bad.json")
	badManifest := Manifest{
		Version:    "1.0",
		Roots:      []string{"x"},
		Targets:    canonicalTargets,
		Provenance: targetProvenanceForTests(),
		Packages:   []PackageManifest{{Path: "x", Name: "x"}, {Path: " x ", Name: "y"}},
	}
	badData, err := json.Marshal(badManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badPath, badData, 0o644); err != nil {
		t.Fatalf("write bad manifest: %v", err)
	}
	_, err = LoadManifest(badPath)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("LoadManifest() error = %v, want duplicate package error", err)
	}
}

func TestValidateManifestRejectsMissingIdentity(t *testing.T) {
	cases := []struct {
		name     string
		manifest *Manifest
		want     string
	}{
		{name: "nil", want: "nil"},
		{name: "missing version", manifest: &Manifest{Roots: []string{"x"}}, want: "version"},
		{name: "missing roots", manifest: &Manifest{Version: "1.0"}, want: "roots"},
		{name: "missing targets", manifest: &Manifest{Version: "1.0", Roots: []string{"x"}}, want: "targets"},
		{name: "missing provenance", manifest: &Manifest{Version: "1.0", Roots: []string{"x"}, Targets: canonicalTargets}, want: "provenance"},
		{name: "missing package", manifest: &Manifest{Version: "1.0", Roots: []string{"x"}, Targets: canonicalTargets, Provenance: targetProvenanceForTests(), Packages: []PackageManifest{{Path: "x"}}}, want: "package identity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateManifest(tc.manifest)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateManifest() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func targetProvenanceForTests() []TargetProvenance {
	provenance := make([]TargetProvenance, 0, len(canonicalTargets))
	for _, target := range canonicalTargets {
		provenance = append(provenance, TargetProvenance{Target: target})
	}
	return provenance
}

func TestDiffManifestsAddedRemovedChanged(t *testing.T) {
	committed := &Manifest{Packages: []PackageManifest{
		{Path: "pkg", Functions: []FunctionManifest{{Name: "Gone"}, {Name: "Changed", Params: []ParamManifest{{Type: "int"}}}}, Interfaces: []InterfaceManifest{{Name: "Runner", Methods: []InterfaceMethodEntry{{Name: "Run", Returns: []string{"error"}}}}}},
	}}
	live := &Manifest{Packages: []PackageManifest{
		{Path: "pkg", Functions: []FunctionManifest{{Name: "Changed", Params: []ParamManifest{{Type: "string"}}}, {Name: "Added"}}, Interfaces: []InterfaceManifest{{Name: "Runner", Embeds: []string{"io.Closer"}, Methods: []InterfaceMethodEntry{{Name: "Run", Returns: []string{"error"}}}}}},
	}}

	diff := DiffManifests(committed, live)
	if diff.IsClean() {
		t.Fatal("DiffManifests() should not be clean")
	}
	assertStrings(t, diff.Added, []string{"pkg.Added", "pkg.Runner.embed:io.Closer"})
	assertStrings(t, diff.Removed, []string{"pkg.Gone"})
	assertStrings(t, diff.Changed, []string{"pkg.Changed", "pkg.Runner"})
}

func assertStrings(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("strings = %#v, want %#v", got, want)
	}
}
