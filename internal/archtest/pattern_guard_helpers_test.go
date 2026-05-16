package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// findRepoRoot 返回仓库根路径。与 repoRootForGuardTests 相同逻辑但命名更清晰。
func findRepoRoot(t *testing.T) string {
	t.Helper()
	return repoRootForGuardTests(t)
}

// scanGoFiles 遍历仓库内所有 Go 文件，对每个文件调用 fn(relPath, absPath)。
// 跳过 vendor、node_modules、.git 等默认忽略目录。
func scanGoFiles(t *testing.T, repoRoot string, fn func(relPath, absPath string)) {
	t.Helper()
	skip := DefaultSkipDirs()
	scanRoots := DefaultScanRoots()
	for _, root := range scanRoots {
		absRoot := filepath.Join(repoRoot, root)
		if _, err := os.Stat(absRoot); os.IsNotExist(err) {
			continue
		}
		_ = filepath.Walk(absRoot, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if info.IsDir() {
				if skip[info.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			relPath, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return nil
			}
			fn(filepath.ToSlash(relPath), path)
			return nil
		})
	}
}
