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

// NewManifestBuilder 创建相关记忆检索的 manifest 构建器。
// 默认文件上限保护检索前置扫描，避免大目录一次性进入排序和读取。
func NewManifestBuilder() *ManifestBuilder {
	return &ManifestBuilder{MaxFiles: DefaultManifestFileLimit}
}

// BuildManifest 扫描记忆根目录并返回可检索条目快照。
// 返回值是克隆后的条目，调用方修改不会影响 builder 内部或后续扫描。
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

// maxFiles 返回 manifest 扫描的文件上限。
// 未配置或非法值回到默认上限，避免 nil builder 造成无限扫描。
func (b *ManifestBuilder) maxFiles() int {
	if b == nil || b.MaxFiles <= 0 {
		return DefaultManifestFileLimit
	}
	return b.MaxFiles
}

// ScanHeadersSafe 只读取记忆文件头部并生成 manifest。
// 根目录为空会失败；根目录不存在返回空列表，遍历过程中非法路径会被跳过。
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

// manifestEntryFromPathSafe 将 WalkDir 命中的 markdown 文件转成 manifest 条目。
// 它跳过目录、MEMORY.md 和无法通过读路径校验的文件，避免检索越界内容。
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
