package archtest_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSqlcBoundary(t *testing.T) {
	root := repoRoot(t)
	prefix := internalPrefix("internal/store/sqlc")
	for _, file := range parseImportFiles(t, root, "internal", "cmd") {
		file := file
		for _, imp := range file.Imports {
			if imp != prefix && !strings.HasPrefix(imp, prefix+"/") {
				continue
			}
			// internal/store 及其子包作为 store 层实现，允许直接使用 sqlc 生成代码；
			// store 层之外的包（module、platform、provider 等）不得绕过 store 根包直接引用 sqlc。
			pkgDir := filepath.ToSlash(filepath.Dir(file.RelPath))
			if pkgDir == "internal/store" || strings.HasPrefix(pkgDir, "internal/store/") {
				continue
			}
			pkgName := filepath.Base(pkgDir)
			t.Run(file.RelPath, func(t *testing.T) {
				t.Errorf("package %s (%s) imports %s outside internal/store", pkgName, file.RelPath, imp)
			})
		}
	}
}
