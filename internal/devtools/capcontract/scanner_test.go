package capcontract

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestScanExtractsCapabilitySurface(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(root, "internal", "contract")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	fixture := `// Package contract fixture package.
package contract

import "context"

type Runner interface {
	context.Context
	Run(ctx context.Context, input []Box[string]) (ok bool, err error)
}

type Box[T any] struct{ Value T }

type service struct{}

func NewRunner(name string, cb func(context.Context) error) (*service, error) { return nil, nil }
func hidden(ch <-chan Box[string]) (out chan<- Box[int]) { return nil }
func (s *service) Start(items map[string]struct{}) error { return nil }
`
	if err := os.WriteFile(filepath.Join(pkgDir, "contract.go"), []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "contract_test.go"), []byte("package contract\nfunc TestIgnored(t *testing.T){}\n"), 0o644); err != nil {
		t.Fatalf("write test fixture: %v", err)
	}

	manifest, err := Scan(ScanOptions{RepoRoot: root, Roots: []string{"internal/contract"}, GeneratedAt: "2026-05-28"})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if got, want := len(manifest.Packages), 1; got != want {
		t.Fatalf("packages = %d, want %d", got, want)
	}
	pkg := manifest.Packages[0]
	if pkg.Path != "internal/contract" || pkg.Name != "contract" {
		t.Fatalf("package identity = %s/%s", pkg.Path, pkg.Name)
	}
	if pkg.Description != "fixture package." {
		t.Fatalf("description = %q", pkg.Description)
	}
	assertFunction(t, pkg.Functions, FunctionManifest{
		Name:     "NewRunner",
		Exported: true,
		Params: []ParamManifest{
			{Name: "name", Type: "string"},
			{Name: "cb", Type: "func(context.Context) error"},
		},
		Returns: []string{"*service", "error"},
	})
	assertFunction(t, pkg.Functions, FunctionManifest{
		Name: "hidden",
		Params: []ParamManifest{
			{Name: "ch", Type: "<-chan Box[string]"},
		},
		Returns: []string{"chan<- Box[int]"},
	})
	assertMethod(t, pkg.Methods, MethodManifest{
		Receiver: "*service",
		Name:     "Start",
		Exported: true,
		Params:   []ParamManifest{{Name: "items", Type: "map[string]struct{}"}},
		Returns:  []string{"error"},
	})
	assertInterface(t, pkg.Interfaces, "Runner", []string{"context.Context"}, []InterfaceMethodEntry{{
		Name:    "Run",
		Params:  []ParamManifest{{Name: "ctx", Type: "context.Context"}, {Name: "input", Type: "[]Box[string]"}},
		Returns: []string{"bool", "error"},
	}})
	assertStruct(t, pkg.Structs, "Box", true)
	assertStruct(t, pkg.Structs, "service", false)
}

func TestScanNormalizesRootsAndOrdersPackages(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"b", "a"} {
		abs := filepath.Join(root, dir)
		if err := os.MkdirAll(abs, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(abs, dir+".go"), []byte("package "+dir+"\nfunc F(){}\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", dir, err)
		}
	}
	manifest, err := Scan(ScanOptions{RepoRoot: root, Roots: []string{" b ", "a", "b"}})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if got, want := manifest.Roots, []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("roots = %#v, want %#v", got, want)
	}
	if got, want := []string{manifest.Packages[0].Path, manifest.Packages[1].Path}, []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("package order = %#v, want %#v", got, want)
	}
}

func TestScanUsesCanonicalTargetsAndBuildTagsDeterministically(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "internal", "contract")
	writeScanFixture(t, pkgDir, filepath.Join(root, "go.mod"), "module fixture\n\ngo 1.25\n")
	writeScanFixture(t, pkgDir, filepath.Join(pkgDir, "contract.go"), "package contract\nfunc Common() {}\n")
	writeScanFixture(t, pkgDir, filepath.Join(pkgDir, "darwin.go"), "//go:build darwin\n\npackage contract\nfunc DarwinOnly() {}\n")
	writeScanFixture(t, pkgDir, filepath.Join(pkgDir, "windows.go"), "//go:build windows\n\npackage contract\nfunc WindowsOnly() {}\n")

	first, err := Scan(ScanOptions{RepoRoot: root, Roots: []string{"internal/contract"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Scan(ScanOptions{RepoRoot: root, Roots: []string{"internal/contract"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("canonical target scan is not deterministic")
	}
	firstBytes, err := MarshalManifest(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := MarshalManifest(second)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstBytes, secondBytes) {
		t.Fatal("canonical target manifest bytes are not deterministic")
	}
	if !reflect.DeepEqual(first.Targets, canonicalTargets()) || len(first.Provenance) != len(canonicalTargets()) {
		t.Fatalf("targets/provenance = %#v/%#v", first.Targets, first.Provenance)
	}
}

func TestCanonicalTargetsCoverReleasePackageMatrix(t *testing.T) {
	want := []string{
		"darwin/amd64",
		"darwin/arm64",
		"linux/amd64",
		"linux/arm64",
		"windows/amd64",
		"windows/arm64",
	}
	if !reflect.DeepEqual(canonicalTargets(), want) {
		t.Fatalf("canonicalTargets() = %#v, want release package matrix %#v", canonicalTargets(), want)
	}
}

func TestCanonicalTargetsAreIsolatedFromCallerMutation(t *testing.T) {
	first := canonicalTargets()
	first[0] = "mutated/target"
	second := canonicalTargets()
	if second[0] != "darwin/amd64" {
		t.Fatalf("canonicalTargets() = %#v, want immutable release package matrix", second)
	}
}

func TestScanDoesNotLoadDependencyGraph(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "internal", "contract")
	writeScanFixture(t, pkgDir, filepath.Join(root, "go.mod"), "module fixture\n\ngo 1.25\n")
	writeScanFixture(t, pkgDir, filepath.Join(pkgDir, "contract.go"), "package contract\nimport _ \"fixture/missing\"\nfunc Common() {}\n")
	manifest, err := Scan(ScanOptions{RepoRoot: root, Roots: []string{"internal/contract"}})
	if err != nil {
		t.Fatalf("Scan loaded dependency graph: %v", err)
	}
	assertFunction(t, manifest.Packages[0].Functions, FunctionManifest{Name: "Common", Exported: true})
}

func TestScanIgnoresCallerRaceFlagsForCrossPlatformSyntaxLoad(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "internal", "contract")
	writeScanFixture(t, pkgDir, filepath.Join(root, "go.mod"), "module fixture\n\ngo 1.25\n")
	writeScanFixture(t, pkgDir, filepath.Join(pkgDir, "contract.go"), "package contract\nfunc Common() {}\n")
	t.Setenv("GOFLAGS", "-race")
	t.Setenv("CGO_ENABLED", "0")
	if _, err := Scan(ScanOptions{RepoRoot: root, Roots: []string{"internal/contract"}}); err != nil {
		t.Fatalf("Scan inherited caller race flags: %v", err)
	}
}

func writeScanFixture(t *testing.T, dir, path, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFunction(t *testing.T, items []FunctionManifest, want FunctionManifest) {
	t.Helper()
	for _, got := range items {
		if got.Name == want.Name {
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("function %s = %#v, want %#v", want.Name, got, want)
			}
			return
		}
	}
	t.Fatalf("function %s not found in %#v", want.Name, items)
}

func assertMethod(t *testing.T, items []MethodManifest, want MethodManifest) {
	t.Helper()
	for _, got := range items {
		if got.Receiver == want.Receiver && got.Name == want.Name {
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("method %s.%s = %#v, want %#v", want.Receiver, want.Name, got, want)
			}
			return
		}
	}
	t.Fatalf("method %s.%s not found in %#v", want.Receiver, want.Name, items)
}

func assertInterface(t *testing.T, items []InterfaceManifest, name string, embeds []string, methods []InterfaceMethodEntry) {
	t.Helper()
	for _, got := range items {
		if got.Name == name {
			if !reflect.DeepEqual(got.Embeds, embeds) || !reflect.DeepEqual(got.Methods, methods) {
				t.Fatalf("interface %s embeds/methods = %#v/%#v, want %#v/%#v", name, got.Embeds, got.Methods, embeds, methods)
			}
			return
		}
	}
	t.Fatalf("interface %s not found in %#v", name, items)
}

func assertStruct(t *testing.T, items []StructManifest, name string, exported bool) {
	t.Helper()
	for _, got := range items {
		if got.Name == name {
			if got.Exported != exported {
				t.Fatalf("struct %s exported = %v, want %v", name, got.Exported, exported)
			}
			return
		}
	}
	t.Fatalf("struct %s not found in %#v", name, items)
}
