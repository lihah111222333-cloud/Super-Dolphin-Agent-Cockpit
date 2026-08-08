package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// productionSourceFile 是一次生产源码快照中的只读 Go 文件及其 AST。
type productionSourceFile struct {
	relPath string
	data    []byte
	fset    *token.FileSet
	syntax  *ast.File
}

// productionSourceSnapshot 在一个父测试内复用同一候选生产源码视图。
// 快照只读；调用方不得修改 data、fset 或 syntax。
type productionSourceSnapshot struct {
	root  string
	files []productionSourceFile
}

// loadProductionSourceSnapshot 一次读取并解析五个源码守卫共同覆盖的生产树。
func loadProductionSourceSnapshot(t *testing.T, root string) *productionSourceSnapshot {
	t.Helper()
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("resolve production source root %q: %v", root, err)
	}
	skipDirs := DefaultSkipDirs()
	files := make([]productionSourceFile, 0)
	for _, scanRoot := range []string{"cmd", "internal", "pkg"} {
		err := appendProductionSourceRoot(absoluteRoot, scanRoot, skipDirs, &files)
		if err != nil {
			t.Fatalf("snapshot production source root %s: %v", scanRoot, err)
		}
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].relPath < files[j].relPath
	})
	return &productionSourceSnapshot{root: absoluteRoot, files: files}
}

func appendProductionSourceRoot(root, scanRoot string, skipDirs map[string]bool, files *[]productionSourceFile) error {
	absRoot := filepath.Join(root, scanRoot)
	return filepath.Walk(absRoot, func(path string, info os.FileInfo, walkErr error) error {
		return appendProductionSourceEntry(root, path, info, walkErr, skipDirs, files)
	})
}

func appendProductionSourceEntry(root, path string, info os.FileInfo, walkErr error, skipDirs map[string]bool, files *[]productionSourceFile) error {
	if walkErr != nil {
		return walkErr
	}
	if info.IsDir() {
		if skipDirs[info.Name()] {
			return filepath.SkipDir
		}
		return nil
	}
	if !isProductionSourceFile(path) {
		return nil
	}
	relPath, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fset := token.NewFileSet()
	syntax, err := parser.ParseFile(fset, path, data, parser.SkipObjectResolution)
	if err != nil {
		return err
	}
	*files = append(*files, productionSourceFile{
		relPath: filepath.ToSlash(relPath),
		data:    data,
		fset:    fset,
		syntax:  syntax,
	})
	return nil
}

func isProductionSourceFile(path string) bool {
	return strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
}

func productionSourcePathInRoots(relPath string, roots []string) bool {
	relPath = filepath.ToSlash(relPath)
	for _, root := range roots {
		root = strings.Trim(filepath.ToSlash(root), "/")
		if relPath == root || strings.HasPrefix(relPath, root+"/") {
			return true
		}
	}
	return false
}
