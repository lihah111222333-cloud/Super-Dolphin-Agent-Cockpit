package issuer

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestBindingCandidateRemainsUnwired 扫描非测试生产树，确保 candidate 未进入 Fx/provider/transport。
func TestBindingCandidateRemainsUnwired(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "../../../../../"))
	candidatePath := "/cmd/mcp-orch/standalone/managedruntime/issuer"
	for _, tree := range []string{"cmd", "internal", "pkg"} {
		if err := scanTree(filepath.Join(root, tree), filepath.Dir(current), candidatePath); err != nil {
			t.Fatal(err)
		}
	}
}

// scanTree 检查单个源码树中的非测试 Go 文件导入。
func scanTree(root, candidateDir, candidatePath string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") || strings.HasPrefix(path, candidateDir+string(filepath.Separator)) {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			if strings.Contains(imported.Path.Value, candidatePath) {
				return &wiringError{path: path, imported: imported.Path.Value}
			}
		}
		return nil
	})
}

type wiringError struct {
	path     string
	imported string
}

// Error 返回脱敏 wiring 位置，不携带源码内容。
func (e *wiringError) Error() string {
	return "binding candidate is wired: " + e.path + " imports " + e.imported
}
