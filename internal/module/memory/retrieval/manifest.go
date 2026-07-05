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

const defaultManifestByteLimit = 8 * 1024 * 1024

// ManifestScanBudget limits memory manifest directory scans before prompt-time retrieval.
type ManifestScanBudget struct {
	MaxFiles      int
	MaxBytes      int64
	MaxReadErrors int
}

// ManifestScanError reports a fail-fast manifest walk, stat, or header read failure.
type ManifestScanError struct {
	Path      string
	Operation string
	Err       error
}

// Error 返回 manifest 扫描失败的可读诊断。
func (e *ManifestScanError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Path) == "" {
		return e.Operation + ": " + e.Err.Error()
	}
	return e.Operation + " " + e.Path + ": " + e.Err.Error()
}

// Unwrap 返回底层文件系统或解析错误，供 errors.Is/As 继续匹配。
func (e *ManifestScanError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ManifestScanTruncatedError reports that a manifest scan stopped at a configured budget.
type ManifestScanTruncatedError struct {
	Reason string
	Budget ManifestScanBudget
	Files  int
	Bytes  int64
}

// Error 返回 manifest 扫描预算截断诊断。
func (e *ManifestScanTruncatedError) Error() string {
	if e == nil {
		return ""
	}
	return "memory manifest scan truncated: " + strings.TrimSpace(e.Reason)
}

type ManifestBuilder struct {
	MaxFiles int
	Budget   ManifestScanBudget
}

// NewManifestBuilder 创建相关记忆检索的 manifest 构建器。
// 默认文件上限保护检索前置扫描，避免大目录一次性进入排序和读取。
func NewManifestBuilder() *ManifestBuilder {
	return &ManifestBuilder{MaxFiles: DefaultManifestFileLimit}
}

// BuildManifest 扫描记忆根目录并返回可检索条目快照。
// 返回值是克隆后的条目，调用方修改不会影响 builder 内部或后续扫描。
func (b *ManifestBuilder) BuildManifest(memoryRoot string) ([]MemoryEntry, error) {
	entries, err := scanHeadersWithBudget(memoryRoot, b.scanBudget())
	if err != nil {
		return cloneEntries(entries), err
	}
	return cloneEntries(entries), nil
}

// scanBudget 合并旧 MaxFiles 字段和新预算结构，保持现有调用兼容。
func (b *ManifestBuilder) scanBudget() ManifestScanBudget {
	budget := defaultManifestScanBudget()
	if b == nil {
		return budget
	}
	if b.Budget.MaxFiles > 0 {
		budget.MaxFiles = b.Budget.MaxFiles
	}
	if b.Budget.MaxBytes > 0 {
		budget.MaxBytes = b.Budget.MaxBytes
	}
	if b.Budget.MaxReadErrors > 0 {
		budget.MaxReadErrors = b.Budget.MaxReadErrors
	}
	if b.MaxFiles > 0 {
		budget.MaxFiles = b.MaxFiles
	}
	return budget
}

func defaultManifestScanBudget() ManifestScanBudget {
	return ManifestScanBudget{
		MaxFiles:      DefaultManifestFileLimit,
		MaxBytes:      defaultManifestByteLimit,
		MaxReadErrors: 0,
	}
}

// ScanHeadersSafe 只读取记忆文件头部并生成 manifest。
// 根目录为空会失败；根目录不存在返回空列表，遍历过程中非法路径会被跳过。
func ScanHeadersSafe(memoryRoot string) ([]MemoryEntry, error) {
	return scanHeadersWithBudget(memoryRoot, defaultManifestScanBudget())
}

// scanHeadersWithBudget 在 WalkDir 内执行预算和读取错误控制。
func scanHeadersWithBudget(memoryRoot string, budget ManifestScanBudget) ([]MemoryEntry, error) {
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
	var bytesScanned int64
	var scanErr error
	err = filepath.WalkDir(normalizedRoot, func(path string, d fs.DirEntry, walkErr error) error {
		entry, ok, err := manifestEntryFromPath(normalizedRoot, path, d, walkErr, budget, len(entries), bytesScanned)
		if err != nil {
			scanErr = err
			return fs.SkipAll
		}
		if ok {
			bytesScanned += memoryEntryFileSize(path, d)
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
	return entries, scanErr
}

// manifestEntryFromPath 把单个 WalkDir 命中转换为 manifest 条目，并在读取前执行预算检查。
func manifestEntryFromPath(
	root string,
	path string,
	d fs.DirEntry,
	walkErr error,
	budget ManifestScanBudget,
	filesScanned int,
	bytesScanned int64,
) (MemoryEntry, bool, error) {
	if walkErr != nil {
		return MemoryEntry{}, false, &ManifestScanError{Path: path, Operation: "walk memory manifest", Err: walkErr}
	}
	if !isMemoryManifestCandidate(path, d) {
		return MemoryEntry{}, false, nil
	}
	validatedPath, ok := validatedManifestReadPath(root, path)
	if !ok {
		return MemoryEntry{}, false, nil
	}
	if err := manifestFileBudgetError(budget, filesScanned, bytesScanned); err != nil {
		return MemoryEntry{}, false, err
	}
	info, err := d.Info()
	if err != nil {
		return MemoryEntry{}, false, &ManifestScanError{Path: path, Operation: "stat memory manifest entry", Err: err}
	}
	if err := manifestByteBudgetError(budget, filesScanned, bytesScanned, info.Size()); err != nil {
		return MemoryEntry{}, false, err
	}
	entry, err := readMemoryEntryHeader(validatedPath)
	if err != nil {
		return MemoryEntry{}, false, &ManifestScanError{Path: path, Operation: "scan memory header", Err: err}
	}
	return entry, true, nil
}

func isMemoryManifestCandidate(path string, d fs.DirEntry) bool {
	return d != nil && !d.IsDir() && filepath.Ext(path) == ".md" && filepath.Base(path) != memoryIndexFileName
}

func validatedManifestReadPath(root, path string) (string, bool) {
	validatedPath, err := validateMemoryReadPath(root, path)
	return validatedPath, err == nil
}

func manifestFileBudgetError(budget ManifestScanBudget, filesScanned int, bytesScanned int64) error {
	if filesScanned < budget.MaxFiles {
		return nil
	}
	return &ManifestScanTruncatedError{
		Reason: "max files",
		Budget: budget,
		Files:  filesScanned,
		Bytes:  bytesScanned,
	}
}

func manifestByteBudgetError(budget ManifestScanBudget, filesScanned int, bytesScanned, nextBytes int64) error {
	if bytesScanned+nextBytes <= budget.MaxBytes {
		return nil
	}
	return &ManifestScanTruncatedError{
		Reason: "max bytes",
		Budget: budget,
		Files:  filesScanned,
		Bytes:  bytesScanned,
	}
}

func memoryEntryFileSize(path string, d fs.DirEntry) int64 {
	if d == nil {
		return 0
	}
	info, err := d.Info()
	if err == nil {
		return info.Size()
	}
	info, err = os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
