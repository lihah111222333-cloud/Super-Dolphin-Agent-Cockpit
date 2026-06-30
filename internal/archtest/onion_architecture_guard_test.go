package archtest

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnionArchitectureGuard(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	scanRoots := []string{"cmd", "internal", "pkg"}
	skipDirs := DefaultSkipDirs()

	var violations []string

	for _, sr := range scanRoots {
		if err := collectOnionArchitectureViolations(root, sr, skipDirs, &violations); err != nil {
			t.Fatalf("walk %s: %v", sr, err)
		}
	}

	if len(violations) > 0 {
		t.Fatalf("Onion Architecture violations (%d):\n  %s", len(violations), strings.Join(violations, "\n  "))
	}
}

func collectOnionArchitectureViolations(root, scanRoot string, skipDirs map[string]bool, violations *[]string) error {
	abs := filepath.Join(root, scanRoot)
	return filepath.Walk(abs, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if shouldSkipOnionWalkEntry(info, skipDirs) {
			return filepath.SkipDir
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		fileViolations, err := onionArchitectureViolationsForFile(path, rel)
		if err != nil {
			return err
		}
		*violations = append(*violations, fileViolations...)
		return nil
	})
}

func shouldSkipOnionWalkEntry(info os.FileInfo, skipDirs map[string]bool) bool {
	if !info.IsDir() {
		return false
	}
	return skipDirs[info.Name()]
}

func onionArchitectureViolationsForFile(path, rel string) ([]string, error) {
	guarded, isDomain, isService := onionLayerFlags(rel)
	if !guarded {
		return nil, nil
	}
	fileNode, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if parseErr != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, parseErr)
	}
	var violations []string
	for _, imp := range fileNode.Imports {
		if imp.Path == nil || imp.Path.Value == "" {
			continue
		}
		importPath := strings.Trim(imp.Path.Value, "\"")
		violations = append(violations, onionImportViolations(rel, importPath, isDomain, isService)...)
	}
	return violations, nil
}

func onionLayerFlags(rel string) (guarded, isDomain, isService bool) {
	if !strings.Contains(rel, "internal/") || strings.Contains(rel, "/common/") {
		return false, false, false
	}
	isDomain = strings.Contains(rel, "/domain/") || strings.HasSuffix(rel, "/domain") || strings.Contains(rel, "/entity/")
	isService = strings.Contains(rel, "/service/") || strings.HasSuffix(rel, "/service")
	return isDomain || isService, isDomain, isService
}

func onionImportViolations(rel, importPath string, isDomain, isService bool) []string {
	if !strings.HasPrefix(importPath, "github.com/anthropic-ai/super-agent-v3/") {
		return nil
	}
	if isDomain && isExternalLayer(importPath) {
		return []string{fmt.Sprintf("%s: Domain 层违规: 核心领域模型禁止导入外部层依赖 %q", rel, importPath)}
	}
	if isService && !strings.Contains(importPath, "common/repo") && isInfraOrApiLayer(importPath) {
		return []string{fmt.Sprintf("%s: Service 层违规: 业务逻辑层禁止直接导入基础设施或表示层依赖 %q", rel, importPath)}
	}
	return nil
}

func isExternalLayer(importPath string) bool {
	return isMatchedLayer(importPath, "/service") ||
		isMatchedLayer(importPath, "/infra") ||
		isMatchedLayer(importPath, "/handler") ||
		isMatchedLayer(importPath, "/router") ||
		isMatchedLayer(importPath, "/api") ||
		isMatchedLayer(importPath, "/adapter")
}

func isInfraOrApiLayer(importPath string) bool {
	return isMatchedLayer(importPath, "/infra") ||
		isMatchedLayer(importPath, "/handler") ||
		isMatchedLayer(importPath, "/router") ||
		isMatchedLayer(importPath, "/api")
}

func isMatchedLayer(importPath, layer string) bool {
	return strings.Contains(importPath, layer+"/") || strings.HasSuffix(importPath, layer)
}
