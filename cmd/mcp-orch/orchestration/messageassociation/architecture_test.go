package messageassociation

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProductionImportsRemainTransportNeutral(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		imports, parseErr := importsFor(path)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, importPath := range imports {
			if forbiddenImport(importPath) {
				t.Fatalf("%s imports forbidden runtime dependency %q", path, importPath)
			}
		}
	}
}

func importsFor(path string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	imports := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		imports = append(imports, strings.Trim(spec.Path.Value, `"`))
	}
	return imports, nil
}

func forbiddenImport(importPath string) bool {
	return strings.Contains(importPath, "/provider/") || strings.HasSuffix(importPath, "/internal/provider") || strings.Contains(importPath, "/store/") || strings.HasSuffix(importPath, "/internal/store") || strings.Contains(importPath, "/registry") || strings.Contains(importPath, "/unified_v2") || strings.HasSuffix(importPath, "/room") || strings.HasSuffix(importPath, "/eventstore") || importPath == "go.uber.org/fx" || importPath == "github.com/creachadair/jrpc2"
}

// TestCandidateIsNotWiredIntoProduction 扫描仓库生产 Go 文件，阻断候选被 runtime 接线。
func TestCandidateIsNotWiredIntoProduction(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate candidate package")
	}
	packageDir := filepath.Dir(currentFile)
	root := filepath.Clean(filepath.Join(packageDir, "../../../.."))
	for _, dir := range []string{"internal", "cmd", "pkg", "scripts"} {
		err := scanProductionDir(filepath.Join(root, dir))
		if err != nil {
			if violation, ok := err.(*wiringViolation); ok {
				t.Fatalf("candidate package is wired into production: %s imports %q", violation.path, violation.importPath)
			}
			t.Fatalf("scan %s: %v", dir, err)
		}
	}
}

func scanProductionDir(base string) error {
	return filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		imports, err := importsFor(path)
		if err != nil {
			return err
		}
		for _, importPath := range imports {
			if strings.HasSuffix(importPath, "/cmd/mcp-orch/orchestration/messageassociation") {
				return &wiringViolation{path: path, importPath: importPath}
			}
		}
		return nil
	})
}

type wiringViolation struct{ path, importPath string }

func (v *wiringViolation) Error() string { return v.path + " imports " + v.importPath }
