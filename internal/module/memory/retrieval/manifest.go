package retrieval

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const DefaultManifestFileLimit = 200

type ManifestBuilder struct{ MaxFiles int }

// NewManifestBuilder 创建manifest构建器。
func NewManifestBuilder() *ManifestBuilder {
	return &ManifestBuilder{MaxFiles: DefaultManifestFileLimit}
}

// BuildManifest 构建manifest。
func (b *ManifestBuilder) BuildManifest(memoryRoot string) ([]MemoryEntry, error) {
	entries, err := ScanHeadersSafe(memoryRoot)
	if err != nil {
		return nil, err
	}
	if maxFiles := b.maxFiles(); len(entries) > maxFiles {
		entries = entries[:maxFiles]
	}
	return cloneEntries(entries), nil
}

func (b *ManifestBuilder) maxFiles() int {
	if b == nil || b.MaxFiles <= 0 {
		return DefaultManifestFileLimit
	}
	return b.MaxFiles
}

// ScanHeadersSafe 扫描头部safe。
func ScanHeadersSafe(memoryRoot string) ([]MemoryEntry, error) {
	root := strings.TrimSpace(memoryRoot)
	if root == "" {
		return nil, errors.New("memory root dir is empty")
	}
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(normalizedRoot); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	entries := make([]MemoryEntry, 0, 16)
	err = filepath.WalkDir(normalizedRoot, func(path string, d fs.DirEntry, walkErr error) error {
		entry, ok := manifestEntryFromPathSafe(normalizedRoot, path, d, walkErr)
		if ok {
			entries = append(entries, entry)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].UpdatedAt.Equal(entries[j].UpdatedAt) {
			return entries[i].FilePath < entries[j].FilePath
		}
		return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
	})
	return entries, nil
}

// manifestEntryFromPathSafe 从路径safe处理manifest条目。
func manifestEntryFromPathSafe(root, path string, d fs.DirEntry, walkErr error) (MemoryEntry, bool) {
	if walkErr != nil || d == nil || d.IsDir() || filepath.Ext(path) != ".md" || filepath.Base(path) == memoryIndexFileName {
		return MemoryEntry{}, false
	}
	validatedPath, err := validateMemoryReadPath(root, path)
	if err != nil {
		return MemoryEntry{}, false
	}
	entry, err := readMemoryEntryHeader(validatedPath)
	if err != nil {
		return MemoryEntry{}, false
	}
	return entry, true
}
