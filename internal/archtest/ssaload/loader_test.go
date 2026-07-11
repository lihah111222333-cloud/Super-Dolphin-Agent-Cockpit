package ssaload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func writeLoaderFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":          "module example.com/fixture\n\ngo 1.22\n",
		"fixture.go":      "package fixture\nfunc Value() int { return 1 }\n",
		"fixture_test.go": "package fixture_test\nimport \"testing\"\nfunc TestValue(t *testing.T) {}\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

func TestLoadIncludesForTestMetadataWhenTestsEnabled(t *testing.T) {
	root := writeLoaderFixture(t)
	pkgs, err := Load(Options{
		RepoRoot: root, Patterns: []string{"."}, Tests: true,
		Include: func(pkg *packages.Package) bool {
			return pkg.Name == "fixture_test" && pkg.ForTest == "example.com/fixture"
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("external variants = %d, want 1", len(pkgs))
	}
	pkg := pkgs[0]
	if strings.HasSuffix(pkg.PkgPath, ".test") || strings.HasSuffix(pkg.ID, ".test") {
		t.Fatalf("selected synthetic main %q", pkg.ID)
	}
	found := false
	for _, file := range pkg.Syntax {
		if filepath.Base(pkg.Fset.Position(file.Pos()).Filename) == "fixture_test.go" {
			found = true
		}
	}
	if !found {
		t.Fatal("external Syntax does not contain fixture_test.go")
	}
}

func TestLoadDoesNotRequestForTestMetadataWhenTestsDisabled(t *testing.T) {
	root := writeLoaderFixture(t)
	pkgs, err := Load(Options{RepoRoot: root, Patterns: []string{"."}})
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("production candidates = %d, want 1", len(pkgs))
	}
	if pkgs[0].ForTest != "" {
		t.Fatalf("ForTest = %q", pkgs[0].ForTest)
	}
	for _, file := range pkgs[0].CompiledGoFiles {
		if strings.HasSuffix(file, "_test.go") {
			t.Fatalf("production syntax contains %s", file)
		}
	}
}

func TestLoadRejectsPackageErrors(t *testing.T) {
	root := writeLoaderFixture(t)
	if err := os.WriteFile(filepath.Join(root, "bad.go"), []byte("package fixture\nvar _ = missing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(Options{RepoRoot: root, Patterns: []string{"."}})
	if err == nil || !strings.Contains(err.Error(), "example.com/fixture") || !strings.Contains(err.Error(), "undefined: missing") {
		t.Fatalf("error = %v", err)
	}
}

func TestBuildRejectsEmptySyntax(t *testing.T) {
	_, _, err := Build([]*packages.Package{{ID: "empty"}})
	if err == nil || !strings.Contains(err.Error(), "incomplete package") {
		t.Fatalf("error = %v", err)
	}
}
