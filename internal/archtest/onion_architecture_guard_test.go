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

			if !strings.Contains(rel, "internal/") || strings.Contains(rel, "/common/") {
				return nil
			}

			isDomain := strings.Contains(rel, "/domain/") || strings.HasSuffix(rel, "/domain") || strings.Contains(rel, "/entity/")
			isService := strings.Contains(rel, "/service/") || strings.HasSuffix(rel, "/service")

			if !isDomain && !isService {
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
				if !strings.HasPrefix(importPath, "github.com/anthropic-ai/super-agent-v3/") {
					continue
				}

				if isDomain && isExternalLayer(importPath) {
					violations = append(violations, fmt.Sprintf("%s: Domain 层违规: 核心领域模型禁止导入外部层依赖 %q", rel, importPath))
				}
				if isService {
					if strings.Contains(importPath, "common/repo") {
						continue
					}
					if isInfraOrApiLayer(importPath) {
						violations = append(violations, fmt.Sprintf("%s: Service 层违规: 业务逻辑层禁止直接导入基础设施或表示层依赖 %q", rel, importPath))
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
		t.Fatalf("Onion Architecture violations (%d):\n  %s", len(violations), strings.Join(violations, "\n  "))
	}
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
