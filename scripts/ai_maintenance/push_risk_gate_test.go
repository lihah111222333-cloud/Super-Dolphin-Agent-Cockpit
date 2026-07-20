package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestRaceSensitiveRegistryCoversProductionConcurrencyPrimitives(t *testing.T) {
	t.Parallel()
	root := aiMaintenanceRepoRoot(t)
	var missing []string
	for _, scanRoot := range []string{"cmd", "internal", "pkg", "scripts"} {
		missing = append(missing, missingRaceRegistryFiles(t, root, scanRoot)...)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("production concurrency files missing push race registration:\n%s", strings.Join(missing, "\n"))
	}
}

func TestConcurrencyPrimitiveRecognizesXSyncPackages(t *testing.T) {
	t.Parallel()
	root := aiMaintenanceRepoRoot(t)
	path := filepath.Join(root, "cmd", "mcp-orch", "orchestration", "contextlock", "rwmutex.go")
	if !hasConcurrencyPrimitive(path) {
		t.Fatalf("x/sync concurrency package was not recognized: %s", path)
	}
}

func TestGateExecutorConcurrencyRunsPushRacePackage(t *testing.T) {
	t.Parallel()
	got := affectedRacePackages([]string{"internal/devtools/gate/executor_plan.go"})
	if len(got) != 1 || got[0] != "./internal/devtools/gate" {
		t.Fatalf("gate executor race packages = %v", got)
	}
}

func missingRaceRegistryFiles(t *testing.T, root, scanRoot string) []string {
	t.Helper()
	var missing []string
	err := filepath.WalkDir(filepath.Join(root, scanRoot), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if shouldSkipRaceRegistryDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if hasConcurrencyPrimitive(path) && len(affectedRacePackages([]string{relative})) == 0 {
			missing = append(missing, relative)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan production concurrency under %s: %v", scanRoot, err)
	}
	return missing
}

func shouldSkipRaceRegistryDir(name string) bool {
	switch name {
	case ".git", ".build-cache", "bin", "dist", "node_modules", "testdata", "vendor":
		return true
	default:
		return false
	}
}

func hasConcurrencyPrimitive(path string) bool {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly|parser.SkipObjectResolution)
	if err != nil {
		return true
	}
	for _, importSpec := range file.Imports {
		pathValue := strings.Trim(importSpec.Path.Value, "\"")
		if pathValue == "sync" || pathValue == "sync/atomic" || strings.HasPrefix(pathValue, "golang.org/x/sync/") {
			return true
		}
	}
	file, err = parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return true
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		switch node.(type) {
		case *ast.GoStmt, *ast.ChanType:
			found = true
			return false
		}
		return !found
	})
	return found
}

func aiMaintenanceRepoRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	return filepath.Clean(filepath.Join(workingDirectory, "..", ".."))
}
