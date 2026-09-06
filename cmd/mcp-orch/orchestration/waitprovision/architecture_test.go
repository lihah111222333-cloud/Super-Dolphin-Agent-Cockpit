package waitprovision

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCandidateDependenciesStayNarrow prevents the candidate from acquiring
// transport, provider, aggregate-owner, Fx, migration, or public-unified edges.
func TestCandidateDependenciesStayNarrow(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range f.Imports {
			imp := strings.Trim(spec.Path.Value, `"`)
			forbidden := []string{"/provider/", "/waitaggregate", "/unified_v2", "/migration", "go.uber.org/fx", "jrpc2"}
			for _, fragment := range forbidden {
				if strings.Contains(imp, fragment) {
					t.Fatalf("%s imports forbidden candidate dependency %q", path, imp)
				}
			}
		}
	}
}

// TestCandidateIsNotWiredIntoProduction rejects production imports of this
// package. The adapter remains disabled until an independently reviewed seam.
func TestCandidateIsNotWiredIntoProduction(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate candidate package")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "../../../.."))
	candidateImport := "/cmd/mcp-orch/orchestration/waitprovision"
	for _, tree := range []string{"internal", "cmd", "pkg"} {
		err := walkProductionTree(filepath.Join(root, tree), filepath.Dir(current), candidateImport)
		if err != nil {
			if violation, ok := err.(*wiringViolation); ok {
				t.Fatalf("candidate wired into production: %s", violation.path)
			}
			t.Fatal(err)
		}
	}
}

func walkProductionTree(root, currentDir, candidateImport string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || filepath.Dir(path) == currentDir {
			return nil
		}
		return inspectProductionImports(path, candidateImport)
	})
}

func inspectProductionImports(path, candidateImport string) error {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return err
	}
	for _, spec := range f.Imports {
		if strings.HasSuffix(strings.Trim(spec.Path.Value, `"`), candidateImport) {
			return &wiringViolation{path: path}
		}
	}
	return nil
}

type wiringViolation struct{ path string }

func (v *wiringViolation) Error() string { return "candidate wiring: " + v.path }
