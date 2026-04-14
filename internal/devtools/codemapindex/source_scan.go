package codemapindex

import (
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

func ScanSourceFiles(root string) (r []string) {
	for _, dir := range indexedSourceDirs(root) {
		r = append(r, collectSourceFilesFromDir(root, dir)...)
	}
	return appendRootIndexedFiles(root, r)
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

func collectSourceFilesFromDir(root, dir string) (files []string) {
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if shouldSkipIndexedDir(info) {
			return filepath.SkipDir
		}
		if !isIndexedSourceFile(info, p) {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr == nil {
			files = append(files, rel)
		}
		return nil
	})
	return files
}

func appendRootIndexedFiles(root string, files []string) []string {
	for _, extra := range []string{"run-debug.sh", "Makefile"} {
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
