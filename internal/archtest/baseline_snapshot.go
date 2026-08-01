package archtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BaselineFileSnapshot 在一次守卫运行中复用全仓 Go 文件枚举。
// 文件度量仍由 BaselineMetricCache 按需读取，快照不跨源码快照复用。
type BaselineFileSnapshot struct {
	production map[string]string
	tests      map[string]string
}

// NewBaselineFileSnapshot 一次收集生产和测试文件。
func NewBaselineFileSnapshot(opts CheckOptions) (*BaselineFileSnapshot, error) {
	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		repoRoot = "."
	}
	snapshot := &BaselineFileSnapshot{
		production: make(map[string]string),
		tests:      make(map[string]string),
	}
	for _, root := range opts.scanRoots() {
		absRoot := filepath.Join(repoRoot, root)
		if err := filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if opts.skipDirs()[info.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			relPath, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return fmt.Errorf("baseline file relative path: %w", err)
			}
			if IsTestFile(path) {
				snapshot.tests[filepath.ToSlash(relPath)] = path
				return nil
			}
			snapshot.production[filepath.ToSlash(relPath)] = path
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return snapshot, nil
}

// Files 返回指定分区的相对路径到绝对路径映射。调用方不得修改返回值。
func (s *BaselineFileSnapshot) Files(testsOnly bool) map[string]string {
	if s == nil {
		return nil
	}
	if testsOnly {
		return s.tests
	}
	return s.production
}

// Shrink 使用同一文件快照和度量缓存执行 baseline 收缩。
func (s *BaselineFileSnapshot) Shrink(bl Baseline, testsOnly bool, cache *BaselineMetricCache) (Baseline, ShrinkStats, error) {
	if s == nil {
		return nil, ShrinkStats{}, fmt.Errorf("baseline file snapshot is required")
	}
	if cache == nil {
		return nil, ShrinkStats{}, fmt.Errorf("baseline metric cache is required")
	}
	files := s.Files(testsOnly)
	fileSet := make(map[string]bool, len(files))
	for relPath := range files {
		fileSet[relPath] = true
	}
	newBL, stats := ShrinkBaseline(bl, fileSet, func(relPath string) FileMetrics {
		return cache.Measure(files[relPath])
	})
	return newBL, stats, nil
}
