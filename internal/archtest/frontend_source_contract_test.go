package archtest

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestCurrentFrontendSourceTreeIsSingular 锁定当前 React/Vite UI 只以 frontend-app 为源码入口。
// 旧 frontend/ 和 cmd/agent-terminal/frontend/ 目录不得重新提交为源码入口。
func TestCurrentFrontendSourceTreeIsSingular(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)
	for _, rel := range []string{
		"frontend",
		"cmd/agent-terminal/frontend",
	} {
		assertNoFrontendLegacyFiles(t, root, rel)
	}
}

// assertNoFrontendLegacyFiles 确认旧前端源码目录没有任何文件残留。
// 空目录允许存在，避免本地删除 tracked 文件后因空目录状态造成误报。
func assertNoFrontendLegacyFiles(t *testing.T, root, rel string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return
	} else if err != nil {
		t.Fatalf("stat legacy frontend tree %s: %v", rel, err)
	}

	var files []string
	walkErr := filepath.WalkDir(path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		got, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		files = append(files, filepath.ToSlash(got))
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk legacy frontend tree %s: %v", rel, walkErr)
	}
	if len(files) > 0 {
		t.Fatalf("legacy frontend tree %s must stay empty; found %v", rel, files)
	}
}
