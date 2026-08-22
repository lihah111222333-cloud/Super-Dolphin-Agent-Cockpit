package archtest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestRemotePortableCoreRejectsLegacyImports(t *testing.T) {
	root := repositoryRootForPortableGuard(t)
	portableDir := filepath.Join(root, "internal", "devtools", "remoteportable")
	forbidden := []string{"internal/devtools/gate", "gateprivate", "platform/db/sqlite", "scripts/mcp_lsp_workload_catalog", "heterociowner", "heterociexec", "heterocitransport", "heterociworkflow"}
	assertPortableDirectoryImports(t, portableDir, forbidden)
	cliForbidden := []string{"internal/devtools/gate", "gateprivate", "platform/db/sqlite", "scripts/mcp_lsp_workload_catalog"}
	assertPortableDirectoryImports(t, filepath.Join(root, "internal", "devtools", "remotecicli"), cliForbidden, map[string][]string{"automatic_run.go": {"platform/db/sqlite"}})
}

func repositoryRootForPortableGuard(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller is unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}

func assertPortableDirectoryImports(t *testing.T, directory string, forbidden []string, allowedByFile ...map[string][]string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		filePath := filepath.Join(directory, entry.Name())
		var allowed []string
		if len(allowedByFile) > 0 {
			allowed = allowedByFile[0][entry.Name()]
		}
		assertPortableFileImports(t, filePath, forbidden, allowed)
	}
}

func assertPortableFileImports(t *testing.T, filePath string, forbidden, allowed []string) {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), filePath, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range parsed.Imports {
		importPath := strings.Trim(spec.Path.Value, `"`)
		for _, banned := range forbidden {
			if slices.Contains(allowed, banned) {
				continue
			}
			if strings.Contains(importPath, banned) {
				t.Fatalf("%s imports forbidden portable dependency %q", filePath, importPath)
			}
		}
	}
}
