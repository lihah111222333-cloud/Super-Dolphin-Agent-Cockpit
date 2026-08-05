package archtest

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const acpModulePath = "github.com/lihah111222333-cloud/super-dolphin-agent"

func TestACPNodeBoundary(t *testing.T) {
	root := repositoryRoot(t)
	allowed := []string{
		filepath.Join(root, "internal", "devtools", "acpnode"),
		filepath.Join(root, "cmd", "acp-node"),
	}
	for _, dir := range allowed {
		if info, err := os.Stat(dir); err != nil {
			t.Fatalf("ACP boundary path %s: %v", dir, err)
		} else if !info.IsDir() {
			t.Fatalf("ACP boundary path is not a directory: %s", dir)
		}
	}
	forbidden := []string{"internal/provider", "internal/auth", "internal/store", "internal/platform", "internal/module", "internal/contract", "internal/dto", "frontend-app"}
	for _, path := range forbidden {
		if _, err := os.Stat(filepath.Join(root, path, "acpnode")); err == nil {
			t.Fatalf("ACP code escaped into forbidden path %s", path)
		}
	}
}

func TestACPNodeStdlibOnlyBoundary(t *testing.T) {
	root := repositoryRoot(t)
	files, err := collectACPGoFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		for _, violation := range acpImportViolations(root, path) {
			t.Error(violation)
		}
	}
}

func collectACPGoFiles(root string) ([]string, error) {
	var files []string
	for _, dir := range []string{filepath.Join(root, "internal", "devtools", "acpnode"), filepath.Join(root, "cmd", "acp-node")} {
		if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			files = append(files, path)
			return nil
		}); err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

func acpImportViolations(root, path string) []string {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return []string{fmt.Sprintf("parse %s: %v", path, err)}
	}
	violations := make([]string, 0)
	for _, spec := range f.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			violations = append(violations, fmt.Sprintf("unquote import in %s: %v", path, err))
			continue
		}
		if strings.HasPrefix(path, filepath.Join(root, "cmd", "acp-node")) {
			if importPath != acpModulePath+"/internal/devtools/acpnode" && !isStdlibImport(importPath) {
				violations = append(violations, fmt.Sprintf("%s imports out-of-scope package %q", path, importPath))
			}
			continue
		}
		if !isStdlibImport(importPath) {
			violations = append(violations, fmt.Sprintf("%s imports non-stdlib package %q", path, importPath))
		}
	}
	return violations
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

func isStdlibImport(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}
