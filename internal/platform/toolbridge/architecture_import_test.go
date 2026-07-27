package toolbridge

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestPlatformProductionDoesNotImportOuterMCPServerOrProvider(t *testing.T) {
	platformRoot := platformArchitectureTestRoot(t)
	violations, err := platformOuterImportViolations(platformRoot)
	if err != nil {
		t.Fatalf("scan platform production imports: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("platform production imports outer implementation packages:\n%s", strings.Join(violations, "\n"))
	}
}

func platformArchitectureTestRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() did not return architecture test path")
	}
	return filepath.Dir(filepath.Dir(currentFile))
}

func platformOuterImportViolations(platformRoot string) ([]string, error) {
	var violations []string
	err := filepath.WalkDir(platformRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		found, err := platformOuterImportsForPath(platformRoot, path, entry, walkErr)
		if err != nil {
			return err
		}
		violations = append(violations, found...)
		return nil
	})
	return violations, err
}

func platformOuterImportsForPath(
	platformRoot string,
	path string,
	entry fs.DirEntry,
	walkErr error,
) ([]string, error) {
	if walkErr != nil {
		return nil, walkErr
	}
	if entry.IsDir() {
		if entry.Name() == "testdata" {
			return nil, filepath.SkipDir
		}
		return nil, nil
	}
	if !isPlatformProductionGoFile(path) {
		return nil, nil
	}
	return platformOuterImportsInFile(platformRoot, path)
}

func isPlatformProductionGoFile(path string) bool {
	return filepath.Ext(path) == ".go" && !strings.HasSuffix(path, "_test.go")
}

func platformOuterImportsInFile(platformRoot, path string) ([]string, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var violations []string
	for _, spec := range parsed.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, err
		}
		if !isOuterMCPImplementationImport(importPath) {
			continue
		}
		relative, err := filepath.Rel(platformRoot, path)
		if err != nil {
			return nil, err
		}
		violations = append(violations, filepath.ToSlash(relative)+" -> "+importPath)
	}
	return violations, nil
}

func isOuterMCPImplementationImport(importPath string) bool {
	return strings.Contains(importPath, "/internal/mcpserver/") ||
		strings.HasSuffix(importPath, "/internal/mcpserver") ||
		strings.Contains(importPath, "/internal/provider/") ||
		strings.HasSuffix(importPath, "/internal/provider")
}
