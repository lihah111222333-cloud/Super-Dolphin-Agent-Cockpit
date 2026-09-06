package archtest_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestTaskTransportAuthorizationCandidateHasNoEffectOwnerImports keeps the
// grant/parent/cursor candidate read-only. It may depend on common attribution
// and the standard library, but never on a persistence, network, process,
// provider, or registry owner.
func TestTaskTransportAuthorizationCandidateHasNoEffectOwnerImports(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	files := parseImportFiles(t, root, taskTransportCandidatePackage)
	var violations []string
	seen := map[string]bool{}
	for _, file := range files {
		if !strings.HasPrefix(filepath.Base(file.RelPath), "authorization") {
			continue
		}
		seen[filepath.Base(file.RelPath)] = true
		for _, imported := range file.Imports {
			if imported == taskTransportCommonImport || isTaskTransportStdlibImport(imported) && !isTaskAuthorizationEffectOwnerImport(imported) {
				continue
			}
			violations = append(violations, file.RelPath+": authorization candidate imports effect owner "+imported)
		}
	}
	for _, required := range []string{"authorization.go", "authorization_validation.go"} {
		if !seen[required] {
			violations = append(violations, taskTransportCandidatePackage+": missing authorization candidate file "+required)
		}
	}
	failIfViolations(t, violations)
}

// TestTaskTransportAuthorizationBoundaryGuardRejectsStdlibEffectImports keeps
// the guard fail-closed even when a future candidate uses a standard-library
// package that can allocate files, sockets, processes, or database handles.
func TestTaskTransportAuthorizationBoundaryGuardRejectsStdlibEffectImports(t *testing.T) {
	t.Parallel()
	for _, imported := range []string{"database/sql", "net/http", "os", "os/exec", "os/signal", "syscall"} {
		if !isTaskAuthorizationEffectOwnerImport(imported) {
			t.Fatalf("effectful standard-library import %q was not classified", imported)
		}
	}
}

func isTaskAuthorizationEffectOwnerImport(imported string) bool {
	for _, denied := range []string{
		"database/sql", "net", "net/http", "os", "os/exec", "os/signal", "syscall",
		modulePath + "/cmd/mcp-orch/store", modulePath + "/internal/provider", "go.uber.org/fx",
	} {
		if imported == denied || strings.HasPrefix(imported, denied+"/") {
			return true
		}
	}
	return false
}
