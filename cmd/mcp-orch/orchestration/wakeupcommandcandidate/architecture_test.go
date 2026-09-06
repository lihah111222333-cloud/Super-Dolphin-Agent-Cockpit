package wakeupcommandcandidate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestCandidateProductionSurfaceRemainsTransportNeutral keeps the B08 owner
// candidate free of persistence, provider, transport and Fx dependencies.
// The candidate is intentionally an in-memory contract and cannot become a
// second production command/dispatcher owner by adding a convenience import.
func TestCandidateProductionSurfaceRemainsTransportNeutral(t *testing.T) {
	for path, file := range candidateProductionFiles(t) {
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquote %s import: %v", filepath.Base(path), err)
			}
			for _, forbidden := range []string{
				"go.uber.org/fx", "database/sql", "net/http", "os/exec",
				"/provider", "/store", "/taskdag", "recoverycustody",
			} {
				if strings.Contains(imported, forbidden) {
					t.Fatalf("%s imports forbidden candidate dependency %q", filepath.Base(path), imported)
				}
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if !ok {
				return true
			}
			for _, forbidden := range []string{"SQL", "DB", "Provider", "Fx", "Registry", "RecoveryCustody", "HTTP"} {
				if ident.Name == forbidden {
					t.Fatalf("%s contains forbidden implementation identifier %q", filepath.Base(path), ident.Name)
				}
			}
			return true
		})
	}
}

// TestCandidateIsNotWiredIntoProduction scans production Go files and rejects
// any import of this package.  A future OUT/dispatcher integration must use an
// explicit reviewed adapter rather than silently turning this candidate into a
// runtime owner.
func TestCandidateIsNotWiredIntoProduction(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate candidate package")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "../../../.."))
	for _, dir := range []string{"internal", "cmd", "pkg", "scripts"} {
		if err := scanCandidateProductionDir(filepath.Join(root, dir)); err != nil {
			if violation, ok := err.(*candidateWiringViolation); ok {
				t.Fatalf("candidate package is wired into production: %s imports %q", violation.path, violation.importPath)
			}
			t.Fatalf("scan %s: %v", dir, err)
		}
	}
}

func candidateProductionFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate candidate package")
	}
	dir := filepath.Dir(currentFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	set := token.NewFileSet()
	files := make(map[string]*ast.File)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		file, err := parser.ParseFile(set, path, nil, parser.AllErrors)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		files[path] = file
	}
	if len(files) == 0 {
		t.Fatal("candidate production files are empty")
	}
	return files
}

func scanCandidateProductionDir(base string) error {
	return filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasSuffix(imported, "/cmd/mcp-orch/orchestration/wakeupcommandcandidate") {
				return &candidateWiringViolation{path: path, importPath: imported}
			}
		}
		return nil
	})
}

type candidateWiringViolation struct{ path, importPath string }

func (v *candidateWiringViolation) Error() string { return v.path + " imports " + v.importPath }
