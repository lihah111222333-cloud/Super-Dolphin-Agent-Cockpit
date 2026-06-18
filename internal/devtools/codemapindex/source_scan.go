package codemapindex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var indexedSourceExts = map[string]bool{
	".go":  true,
	".js":  true,
	".ts":  true,
	".vue": true,
	".sql": true,
	".sh":  true,
}

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

// ScanSourceFiles 扫描source文件。
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

// collectSourceFilesFromDir 从目录收集source文件。
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

func appendRootIndexedFiles(root string, files []string) []string {
	for _, extra := range []string{"run-new-ui-desktop.sh", "run-new-ui-desktop.ps1", "Makefile"} {
		p := filepath.Join(root, extra)
		if fileExists(p) {
			files = append(files, extra)
		}
	}
	return files
}

func shouldSkipIndexedDir(info os.FileInfo) bool {
	return info.IsDir() && indexedSourceSkipDirs[info.Name()]
}

func isIndexedSourceFile(info os.FileInfo, path string) bool {
	if info.IsDir() {
		return false
	}
	if strings.HasSuffix(path, "_test.go") {
		return false
	}
	return indexedSourceExts[filepath.Ext(path)]
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
