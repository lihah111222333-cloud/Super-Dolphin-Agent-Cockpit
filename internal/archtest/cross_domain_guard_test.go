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
	skipDirs := DefaultSkipDirs()

	var violations []string
	topLevelDomains := []string{
		"admin",
		"user",
		"orchestrator",
		"engine",
		"spider",
	}

	for _, sr := range scanRoots {
		abs := filepath.Join(root, sr)
		err := filepath.Walk(abs, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				if _, skip := skipDirs[info.Name()]; skip {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)

			if !strings.Contains(rel, "internal/") {
				return nil
			}

			relWithSlash := "/" + rel
			currentDomain := ""
			for _, domain := range topLevelDomains {
				if strings.Contains(relWithSlash, "/internal/"+domain+"/") || strings.HasSuffix(relWithSlash, "/internal/"+domain) {
					currentDomain = domain
					break
				}
			}
			isCommon := strings.Contains(relWithSlash, "/internal/common/") || strings.HasSuffix(relWithSlash, "/internal/common")

			if currentDomain == "" && !isCommon {
				return nil
			}

			fset := token.NewFileSet()
			fileNode, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if parseErr != nil {
				return nil
			}

			for _, imp := range fileNode.Imports {
				if imp.Path == nil || imp.Path.Value == "" {
					continue
				}
				importPath := strings.Trim(imp.Path.Value, "\"")
				if !strings.HasPrefix(importPath, "github.com/anthropic-ai/super-agent-v3/internal/") {
					continue
				}

				// Rule 1: Common reverse dependency
				if isCommon {
					for _, targetDomain := range topLevelDomains {
						if strings.Contains(importPath, "/internal/"+targetDomain+"/") || strings.HasSuffix(importPath, "/internal/"+targetDomain) {
							violations = append(violations, fmt.Sprintf("%s: 公共基建层违规: common 禁止逆向导入顶级域 %q", rel, importPath))
						}
					}
					continue
				}

				// Rule 2: Top-level domain mutual exclusion
				for _, targetDomain := range topLevelDomains {
					if targetDomain == currentDomain {
						continue
					}

					if strings.Contains(importPath, "/internal/"+targetDomain+"/") || strings.HasSuffix(importPath, "/internal/"+targetDomain) {
						violations = append(violations, fmt.Sprintf("%s: 顶级域违规: %s 域禁止直接导入平行域 %s 的依赖 %q", rel, currentDomain, targetDomain, importPath))
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", sr, err)
		}
	}

	if len(violations) > 0 {
		t.Fatalf("Cross-Domain violations (%d):\n  %s", len(violations), strings.Join(violations, "\n  "))
	}
}
