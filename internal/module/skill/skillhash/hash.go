// Package skillhash 提供技能文件内容的哈希计算工具，用于检测技能文件是否发生变化。
package skillhash

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/skill/mirrorpath"
)

const (
	defaultMaxSkillFileBytes  int64 = 1 << 20
	defaultMaxSkillTotalBytes int64 = 32 << 20
	defaultMaxSkillFiles            = 512
)

// SkillContentLimits 限制单个 skill 目录的文件大小、总字节数和文件数量。
type SkillContentLimits struct {
	MaxFileBytes  int64
	MaxTotalBytes int64
	MaxFiles      int
}

// DefaultSkillContentLimits 返回生产 skill 读写路径共用的内容上限。
func DefaultSkillContentLimits() SkillContentLimits {
	return SkillContentLimits{
		MaxFileBytes:  defaultMaxSkillFileBytes,
		MaxTotalBytes: defaultMaxSkillTotalBytes,
		MaxFiles:      defaultMaxSkillFiles,
	}
}

// Validate 校验上限配置必须显式为正数，避免调用方误传零值后失去保护。
func (l SkillContentLimits) Validate() error {
	if l.MaxFileBytes <= 0 || l.MaxTotalBytes <= 0 || l.MaxFiles <= 0 {
		return fmt.Errorf("skill content limits must be positive")
	}
	return nil
}

// CheckFile 校验单个文件大小，调用方应在打开文件前先用 stat 结果调用。
func (l SkillContentLimits) CheckFile(path string, size int64) error {
	if size > l.MaxFileBytes {
		return fmt.Errorf("skill content file too large: %s is %d bytes, limit %d", path, size, l.MaxFileBytes)
	}
	return nil
}

// CheckTotal 校验一个 skill 目录的累计普通文件字节数。
func (l SkillContentLimits) CheckTotal(root string, total int64) error {
	if total > l.MaxTotalBytes {
		return fmt.Errorf("skill content total too large: %s is %d bytes, limit %d", root, total, l.MaxTotalBytes)
	}
	return nil
}

// CheckFileCount 校验一个 skill 目录的普通文件数量。
func (l SkillContentLimits) CheckFileCount(root string, files int) error {
	if files > l.MaxFiles {
		return fmt.Errorf("skill content has too many files: %s has %d files, limit %d", root, files, l.MaxFiles)
	}
	return nil
}

// ContentLimitTracker 累计检查一个 skill 目录内普通文件的数量和总大小。
type ContentLimitTracker struct {
	root   string
	limits SkillContentLimits
	files  int
	total  int64
}

// NewContentLimitTracker 创建使用生产默认上限的目录内容追踪器。
func NewContentLimitTracker(root string) (ContentLimitTracker, error) {
	limits := DefaultSkillContentLimits()
	if err := limits.Validate(); err != nil {
		return ContentLimitTracker{}, err
	}
	return ContentLimitTracker{root: root, limits: limits}, nil
}

// AddFile 在读取或复制文件前累计检查单文件、文件数和总字节数上限。
func (t *ContentLimitTracker) AddFile(path string, size int64) error {
	if err := t.limits.CheckFile(path, size); err != nil {
		return err
	}
	t.files++
	if err := t.limits.CheckFileCount(t.root, t.files); err != nil {
		return err
	}
	t.total += size
	return t.limits.CheckTotal(t.root, t.total)
}

// Limits 返回追踪器正在使用的内容上限，供实际 CopyN/ReadN 阶段复用。
func (t ContentLimitTracker) Limits() SkillContentLimits { return t.limits }

// Content 计算字符串内容的 SHA-256 哈希，返回十六进制字符串。
func Content(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// StableMirrorDirectoryHash 按 mirror 相对路径、权限和文件内容计算稳定 hash，可忽略 manifest 文件。
func StableMirrorDirectoryHash(root, ignoredRel string) (string, error) {
	absRoot, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return "", fmt.Errorf("normalize mirror root: %w", err)
	}
	files, err := collectMirrorHashFiles(filepath.Clean(absRoot), ignoredRel)
	if err != nil {
		return "", err
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	return hashMirrorFiles(files)
}

type mirrorHashFile struct {
	rel  string
	mode fs.FileMode
	path string
	size int64
}

func collectMirrorHashFiles(root, ignoredRel string) ([]mirrorHashFile, error) {
	var files []mirrorHashFile
	tracker, err := NewContentLimitTracker(root)
	if err != nil {
		return nil, err
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		file, err := readMirrorHashFile(root, ignoredRel, path, entry, walkErr, &tracker)
		if err != nil || file == nil {
			return err
		}
		files = append(files, *file)
		return nil
	})
	return files, err
}

// readMirrorHashFile 校验 mirror 文件路径、类型和内容上限，只返回 hash 所需的元数据。
func readMirrorHashFile(root, ignoredRel, path string, entry fs.DirEntry, walkErr error, tracker *ContentLimitTracker) (*mirrorHashFile, error) {
	if walkErr != nil || entry == nil || entry.IsDir() {
		return nil, walkErr
	}
	info, err := mirrorpath.SafeFileInfo(path, entry)
	if err != nil {
		return nil, err
	}
	rel, err := mirrorpath.SafeRelative(root, path)
	if err != nil || rel == ignoredRel {
		return nil, err
	}
	if err := tracker.AddFile(path, info.Size()); err != nil {
		return nil, err
	}
	return &mirrorHashFile{rel: rel, mode: info.Mode(), path: path, size: info.Size()}, nil
}

func hashMirrorFiles(files []mirrorHashFile) (string, error) {
	h := sha256.New()
	for _, file := range files {
		writeLengthPrefixedBytes(h, []byte(file.rel))
		writeHashUint32(h, uint32(file.mode.Perm()))
		if err := WriteLengthPrefixedFile(h, file.path, file.size); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeLengthPrefixedBytes(h hash.Hash, value []byte) {
	var size [8]byte
	writeUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(value)
}

func writeHashUint32(h hash.Hash, value uint32) {
	var data [4]byte
	writeUint32(data[:], value)
	_, _ = h.Write(data[:])
}

// Dir 递归计算目录下所有非 symlink 普通文件的内容哈希，排序后再哈希合并，保证结果与文件顺序无关。
func Dir(root string) (string, error) {
	return DirWithLimits(root, DefaultSkillContentLimits())
}

// DirWithLimits 在指定上限内递归计算目录内容哈希。
// 每个普通文件都以流式读取进入 SHA-256，超限文件会在 stat 阶段先失败。
func DirWithLimits(root string, limits SkillContentLimits) (string, error) {
	if err := limits.Validate(); err != nil {
		return "", err
	}
	state := dirHashState{root: root, limits: limits}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		return state.addFile(path, entry)
	}); err != nil {
		return "", fmt.Errorf("skill dir content hash %q: %w", root, err)
	}
	sort.Strings(state.parts)
	return Content(strings.Join(state.parts, "\x00")), nil
}

type dirHashState struct {
	root   string
	limits SkillContentLimits
	parts  []string
	total  int64
}

// addFile 校验文件大小、数量和累计字节数后，把单文件 hash 加入目录 hash 输入。
func (s *dirHashState) addFile(path string, entry fs.DirEntry) error {
	info, err := entry.Info()
	if err != nil {
		return err
	}
	if err := s.limits.CheckFile(path, info.Size()); err != nil {
		return err
	}
	if err := s.limits.CheckFileCount(s.root, len(s.parts)+1); err != nil {
		return err
	}
	s.total += info.Size()
	if err := s.limits.CheckTotal(s.root, s.total); err != nil {
		return err
	}
	rel, err := filepath.Rel(s.root, path)
	if err != nil {
		return err
	}
	fileHash, err := FileWithLimits(path, s.limits)
	if err != nil {
		return err
	}
	s.parts = append(s.parts, filepath.ToSlash(rel)+"\x00"+fileHash)
	return nil
}

// FileWithLimits 以流式方式计算单个文件的 SHA-256，并限制最多读取 MaxFileBytes+1 字节。
func FileWithLimits(path string, limits SkillContentLimits) (string, error) {
	if err := limits.Validate(); err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = file.Close()
	}()
	h := sha256.New()
	copied, err := io.CopyN(h, file, limits.MaxFileBytes+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if err := limits.CheckFile(path, copied); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CopyFileWithLimits 使用 CopyN 复制单个文件，文件增长超过上限时会删除半成品。
func CopyFileWithLimits(src, dst string, mode os.FileMode, limits SkillContentLimits) (copied int64, err error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer func() {
		if closeErr := in.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return 0, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(dst)
		}
	}()
	copied, err = io.CopyN(out, in, limits.MaxFileBytes+1)
	err = limitedCopyError(src, copied, err, limits)
	if closeErr := out.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return copied, err
	}
	cleanup = false
	return copied, nil
}

// CopyDirWithLimits 以 0644 模式流式复制 skill 目录，并拒绝 symlink 和非常规文件。
func CopyDirWithLimits(source, target string) (int, int64, error) {
	files, total := 0, int64(0)
	tracker, err := NewContentLimitTracker(source)
	if err != nil {
		return 0, 0, err
	}
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return copyDirEntry(source, target, path, entry, &tracker, &files, &total)
	})
	return files, total, err
}

func copyDirEntry(source, target, path string, entry fs.DirEntry, tracker *ContentLimitTracker, files *int, total *int64) error {
	rel, err := filepath.Rel(source, path)
	if err != nil {
		return err
	}
	if rel == "." {
		return os.Mkdir(target, 0o755)
	}
	dst := filepath.Join(target, rel)
	if entry.IsDir() {
		return os.MkdirAll(dst, 0o755)
	}
	return copyDirFileEntry(path, dst, rel, entry, tracker, files, total)
}

// copyDirFileEntry 校验单个来源文件并以有界 CopyN 写入目标文件。
func copyDirFileEntry(path, dst, rel string, entry fs.DirEntry, tracker *ContentLimitTracker, files *int, total *int64) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return fmt.Errorf("symlink is not allowed: %s", rel)
	}
	info, err := entry.Info()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("skill path is not regular: %s", rel)
	}
	if err := tracker.AddFile(path, info.Size()); err != nil {
		return err
	}
	copied, err := CopyFileWithLimits(path, dst, 0o644, tracker.Limits())
	if err != nil {
		return err
	}
	*files, *total = *files+1, *total+copied
	return nil
}

// MirrorFileModeFromSource 根据 provider mirror 规则计算输出文件权限。
func MirrorFileModeFromSource(rel string, mode os.FileMode, src string) (os.FileMode, error) {
	if strings.HasPrefix(filepath.ToSlash(rel), "scripts/") && mode.Perm()&0o111 != 0 {
		shebang, err := FileStartsWith(src, []byte("#!"))
		if err != nil {
			return 0, err
		}
		if shebang {
			return 0o755, nil
		}
	}
	return 0o644, nil
}

func limitedCopyError(src string, copied int64, copyErr error, limits SkillContentLimits) error {
	if errors.Is(copyErr, io.EOF) {
		copyErr = nil
	}
	if copyErr != nil {
		return copyErr
	}
	return limits.CheckFile(src, copied)
}

// ReadFileWithLimits 读取需要改写的小文件内容，最多读取 MaxFileBytes+1 字节。
func ReadFileWithLimits(path string, limits SkillContentLimits) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()
	var buf bytes.Buffer
	copied, err := io.CopyN(&buf, file, limits.MaxFileBytes+1)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	if err := limits.CheckFile(path, copied); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// FileStartsWith 只读取必要前缀字节，用于判断脚本 shebang 而不加载整个资源文件。
func FileStartsWith(path string, prefix []byte) (ok bool, err error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	buf := make([]byte, len(prefix))
	n, err := file.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return n == len(prefix) && bytes.Equal(buf, prefix), nil
}

// WriteLengthPrefixedFile 把文件大小和内容流写入 hash，保持调用方原有的长度前缀格式。
func WriteLengthPrefixedFile(h hash.Hash, path string, size int64) (err error) {
	var prefix [8]byte
	writeUint64(prefix[:], uint64(size))
	if _, err := h.Write(prefix[:]); err != nil {
		return err
	}
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := src.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	copied, err := io.CopyN(h, src, size)
	if err != nil {
		return err
	}
	if copied != size {
		return fmt.Errorf("read file %s: copied %d bytes, want %d", path, copied, size)
	}
	return nil
}

func writeUint64(dst []byte, value uint64) {
	dst[0] = byte(value >> 56)
	dst[1] = byte(value >> 48)
	dst[2] = byte(value >> 40)
	dst[3] = byte(value >> 32)
	dst[4] = byte(value >> 24)
	dst[5] = byte(value >> 16)
	dst[6] = byte(value >> 8)
	dst[7] = byte(value)
}

func writeUint32(dst []byte, value uint32) {
	dst[0] = byte(value >> 24)
	dst[1] = byte(value >> 16)
	dst[2] = byte(value >> 8)
	dst[3] = byte(value)
}

// ExistingDir 与 Dir 相同，但目录不存在时返回空字符串而非错误。
func ExistingDir(root string) (string, error) {
	hash, err := Dir(root)
	switch {
	case err == nil:
		return hash, nil
	case errors.Is(err, os.ErrNotExist):
		return "", nil
	default:
		return "", err
	}
}
