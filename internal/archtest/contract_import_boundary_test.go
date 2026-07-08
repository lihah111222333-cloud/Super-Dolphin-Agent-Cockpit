package archtest

import (
	"fmt"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"
)

const repoModulePath = "github.com/anthropic-ai/super-agent-v3/"

func TestContractImportsStayOnDTOWhitelist(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)
	var violations []string
	scanGoFiles(t, root, func(relPath, absPath string) {
		if !strings.HasPrefix(relPath, "internal/contract/") ||
			strings.HasPrefix(relPath, "internal/contract/contracttest/") ||
			strings.HasSuffix(relPath, "_test.go") {
			return
		}
		for _, importPath := range goFileImports(t, absPath) {
			reason := contractImportBoundaryViolation(importPath)
			if reason == "" {
				continue
			}
			violations = append(violations, fmt.Sprintf("%s imports %s: %s", relPath, importPath, reason))
		}
	})
	failContractImportViolations(t, violations)
}

func goFileImports(t *testing.T, absPath string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), absPath, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse imports for %s: %v", absPath, err)
	}
	imports := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		imports = append(imports, strings.Trim(spec.Path.Value, `"`))
	}
	slices.Sort(imports)
	return imports
}

func contractImportBoundaryViolation(importPath string) string {
	if strings.HasPrefix(importPath, "cmd/") || strings.Contains(importPath, "/cmd/") {
		return "contract must not depend on cmd entrypoint packages"
	}
	if strings.Contains(importPath, "frontend-app") {
		return "contract must not depend on frontend application shape"
	}
	if !strings.HasPrefix(importPath, repoModulePath) {
		return ""
	}
	rel := strings.TrimPrefix(importPath, repoModulePath)
	for _, prefix := range forbiddenContractImportPrefixes() {
		if rel == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(rel, prefix) {
			return "contract must not import store/module/provider/cmd or UI implementation packages"
		}
	}
	if contractAllowedImport(rel) {
		return ""
	}
	return "contract imports are limited to standard/external packages plus approved DTO/contract packages"
}

func forbiddenContractImportPrefixes() []string {
	return []string{
		"internal/store/",
		"internal/module/",
		"internal/provider/",
		"internal/ui/",
		"cmd/",
	}
}

func contractAllowedImport(rel string) bool {
	allowed := []string{
		"internal/contract",
		"internal/dto/agent",
		"internal/dto/mcp",
		"internal/dto/provider",
		"internal/dto/shared",
		"internal/dto/thread",
		"internal/dto/turn",
		"internal/dto/ui",
	}
	return slices.Contains(allowed, rel)
}

func failContractImportViolations(t *testing.T, violations []string) {
	t.Helper()
	if len(violations) == 0 {
		return
	}
	t.Fatalf("contract import boundary violations (%d):\n%s", len(violations), strings.Join(violations, "\n"))
}
