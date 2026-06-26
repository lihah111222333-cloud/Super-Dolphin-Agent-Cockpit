package codemapindex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// indexedSourceExts 是代码地图索引支持的源码文件扩展名集合。
var indexedSourceExts = map[string]bool{
	".go":  true,
	".js":  true,
	".ts":  true,
	".vue": true,
	".sql": true,
	".sh":  true,
}

// indexedSourceSkipDirs 是扫描时应跳过的目录名集合。
var indexedSourceSkipDirs = map[string]bool{
	"node_modules":      true,
	".git":              true,
	".vite-cache":       true,
	"dist":              true,
	".build-cache":      true,
	".workspace":        true,
	"testdata":          true,
	"playwright-report": true,
	"test-results":      true,
}

// ScanSourceFiles 扫描代码地图应纳入索引的源码文件。
// 只扫描固定源码目录和根目录固定文件，避免把构建产物或测试报告写入索引。
func ScanSourceFiles(root string) ([]string, error) {
	var r []string
	for _, dir := range indexedSourceDirs(root) {
		files, err := collectSourceFilesFromDir(root, dir)
		if err != nil {
			return nil, err
		}
		r = append(r, files...)
	}
	return appendRootIndexedFiles(root, r), nil
}

// indexedSourceDirs 返回项目根目录下需要扫描的源码子目录列表。
func indexedSourceDirs(root string) []string {
	var dirs []string
	for _, name := range []string{"cmd", "internal", "pkg", "sql", "migrations", "scripts"} {
		dir := filepath.Join(root, name)
		if dirExists(dir) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// collectSourceFilesFromDir 从单个源码目录收集相对路径。
// Walk 遇到不可读路径会返回错误，调用方据此阻断索引生成。
func collectSourceFilesFromDir(root, dir string) ([]string, error) {
	var files []string
	if err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if shouldSkipIndexedDir(info) {
			return filepath.SkipDir
		}
		if !isIndexedSourceFile(info, p) {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return fmt.Errorf("source file relative path: %w", relErr)
		}
		files = append(files, rel)
		return nil
	}); err != nil {
		return nil, err
	}
	return files, nil
}

// appendRootIndexedFiles 追加根目录中需要入索引的固定入口文件。
func appendRootIndexedFiles(root string, files []string) []string {
	for _, extra := range []string{"run-new-ui-desktop.sh", "run-new-ui-desktop.ps1", "Makefile"} {
		p := filepath.Join(root, extra)
		if fileExists(p) {
			files = append(files, extra)
		}
	}
	return files
}

// shouldSkipIndexedDir 判断目录是否应在 Walk 时剪枝，防止索引生成物和依赖目录。
func shouldSkipIndexedDir(info os.FileInfo) bool {
	return info.IsDir() && indexedSourceSkipDirs[info.Name()]
}

// isIndexedSourceFile 判断文件是否属于代码地图索引范围。
// 测试文件不入索引，避免测试夹具放大 AI 检索入口。
func isIndexedSourceFile(info os.FileInfo, path string) bool {
	if info.IsDir() {
		return false
	}
	if strings.HasSuffix(path, "_test.go") {
		return false
	}
	return indexedSourceExts[filepath.Ext(path)]
}

// dirExists 判断路径是否是存在的目录；Stat 错误统一视为不存在。
func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// fileExists 判断路径是否是存在的普通文件；Stat 错误统一视为不存在。
func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
