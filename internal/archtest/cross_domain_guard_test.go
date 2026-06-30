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

func TestCrossDomainGuard(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	scanRoots := []string{"cmd", "internal", "pkg"}
	violations := collectCrossDomainViolations(t, root, scanRoots, DefaultSkipDirs())

	if len(violations) > 0 {
		t.Fatalf("Cross-Domain violations (%d):\n  %s", len(violations), strings.Join(violations, "\n  "))
	}
}

func collectCrossDomainViolations(t *testing.T, root string, scanRoots []string, skipDirs map[string]bool) []string {
	t.Helper()

	var violations []string
	for _, scanRoot := range scanRoots {
		got := scanRootForCrossDomainViolations(t, root, scanRoot, skipDirs)
		violations = append(violations, got...)
	}
	return violations
}

func scanRootForCrossDomainViolations(t *testing.T, root, scanRoot string, skipDirs map[string]bool) []string {
	t.Helper()

	var violations []string
	abs := filepath.Join(root, scanRoot)
	err := filepath.Walk(abs, func(path string, info os.FileInfo, walkErr error) error {
		got, err := crossDomainViolationsForPath(root, path, info, walkErr, skipDirs)
		violations = append(violations, got...)
		return err
	})
	if err != nil {
		t.Fatalf("walk %s: %v", scanRoot, err)
	}
	return violations
}

func crossDomainViolationsForPath(root, path string, info os.FileInfo, walkErr error, skipDirs map[string]bool) ([]string, error) {
	if walkErr != nil {
		return nil, walkErr
	}
	if info.IsDir() {
		if _, skip := skipDirs[info.Name()]; skip {
			return nil, filepath.SkipDir
		}
		return nil, nil
	}
	if !isCrossDomainGuardSource(path) {
		return nil, nil
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}
	return crossDomainFileViolations(path, filepath.ToSlash(rel))
}

func isCrossDomainGuardSource(path string) bool {
	return strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
}

func crossDomainFileViolations(path, rel string) ([]string, error) {
	if !strings.Contains(rel, "internal/") {
		return nil, nil
	}

	relWithSlash := "/" + rel
	currentDomain := crossDomainForPath(relWithSlash)
	isCommon := crossDomainPathContains(relWithSlash, "common")
	if currentDomain == "" && !isCommon {
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
		if !strings.HasPrefix(importPath, "github.com/anthropic-ai/super-agent-v3/internal/") {
			continue
		}
		violations = append(violations, violationsForCrossDomainImport(rel, importPath, currentDomain, isCommon)...)
	}
	return violations, nil
}

func crossDomainForPath(relWithSlash string) string {
	for _, domain := range crossDomainTopLevelDomains {
		if crossDomainPathContains(relWithSlash, domain) {
			return domain
		}
	}
	return ""
}

func violationsForCrossDomainImport(rel, importPath, currentDomain string, isCommon bool) []string {
	if isCommon {
		return commonReverseDependencyViolations(rel, importPath)
	}
	return parallelDomainDependencyViolations(rel, importPath, currentDomain)
}

func commonReverseDependencyViolations(rel, importPath string) []string {
	var violations []string
	for _, targetDomain := range crossDomainTopLevelDomains {
		if crossDomainPathContains(importPath, targetDomain) {
			violations = append(violations, fmt.Sprintf("%s: 公共基建层违规: common 禁止逆向导入顶级域 %q", rel, importPath))
		}
	}
	return violations
}

func parallelDomainDependencyViolations(rel, importPath, currentDomain string) []string {
	var violations []string
	for _, targetDomain := range crossDomainTopLevelDomains {
		if targetDomain == currentDomain {
			continue
		}
		if crossDomainPathContains(importPath, targetDomain) {
			violations = append(violations, fmt.Sprintf("%s: 顶级域违规: %s 域禁止直接导入平行域 %s 的依赖 %q", rel, currentDomain, targetDomain, importPath))
		}
	}
	return violations
}

func crossDomainPathContains(path, domain string) bool {
	return strings.Contains(path, "/internal/"+domain+"/") || strings.HasSuffix(path, "/internal/"+domain)
}

var crossDomainTopLevelDomains = []string{
	"admin",
	"user",
	"orchestrator",
	"engine",
	"spider",
}
