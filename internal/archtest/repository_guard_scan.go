package archtest

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
)

type repositoryGuardScanEntry struct {
	once       sync.Once
	violations []Violation
}

// RepositoryGuardScanCache 在一个显式调用范围内复用不可变的全仓扫描结果。
type RepositoryGuardScanCache struct {
	entries sync.Map
}

// CheckRepositoryGuardsOnce 使用调用方持有的缓存复用代码尺寸、标识符和死键门禁扫描。
func CheckRepositoryGuardsOnce(cache *RepositoryGuardScanCache, opts CheckOptions) ([]Violation, error) {
	return checkRepositoryGuardsOnce(cache, opts, CheckAll)
}

func checkRepositoryGuardsOnce(
	cache *RepositoryGuardScanCache,
	opts CheckOptions,
	check func(CheckOptions) []Violation,
) ([]Violation, error) {
	if cache == nil {
		return nil, errors.New("repository guard scan cache is required")
	}
	key, err := repositoryGuardScanKey(opts)
	if err != nil {
		return nil, err
	}
	value, _ := cache.entries.LoadOrStore(key, &repositoryGuardScanEntry{})
	entry, ok := value.(*repositoryGuardScanEntry)
	if !ok {
		return nil, fmt.Errorf("repository guard scan cache contains unexpected value %T", value)
	}
	entry.once.Do(func() {
		entry.violations = slices.Clone(check(opts))
	})
	return slices.Clone(entry.violations), nil
}

func repositoryGuardScanKey(opts CheckOptions) (string, error) {
	root := opts.RepoRoot
	if root == "" {
		root = "."
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository guard root %q: %w", root, err)
	}
	roots := slices.Clone(opts.scanRoots())
	slices.Sort(roots)
	skipDirs := make([]string, 0, len(opts.skipDirs()))
	for dir, skip := range opts.skipDirs() {
		skipDirs = append(skipDirs, dir+"="+strconv.FormatBool(skip))
	}
	slices.Sort(skipDirs)
	return strings.Join([]string{
		filepath.Clean(absoluteRoot),
		strings.Join(roots, "\x00"),
		strings.Join(skipDirs, "\x00"),
		strconv.FormatBool(opts.EnforceFuncComments),
		strconv.FormatBool(opts.BaselineTestsOnly),
	}, "\x01"), nil
}
