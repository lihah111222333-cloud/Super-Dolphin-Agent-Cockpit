package sharedfilefs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// sharedfilefs 提供 sharedfile 磁盘正文的 sandbox 和原子写入原语。
//
// 正文落在 `<CWD>/.agnet/shared/<rel>`，DB 保留索引、作者和时间戳；
// 内容超过 InlineThresholdBytes 后，磁盘成为正文来源。
//
// 本包只负责文件 IO 和 sandbox 边界，不感知 SQL。store 层负责先清理相对路径，
// 写入时先落磁盘再更新 DB，删除时先删 DB 再删磁盘，避免列表索引指向缺失正文。

const (
	// SandboxDir 是 sharedfile 磁盘正文相对 CWD 的固定子目录。
	SandboxDir = ".agnet/shared"
	// DefaultInlineThresholdBytes 是默认的 DB inline 阈值。100KB 足够装下
	// 大多数 handoff / progress 文件，又能挡住整段 LLM transcript 这类
	// 大对象。可由 Config.InlineThresholdBytes 覆盖。
	DefaultInlineThresholdBytes = 100 * 1024
	// tmpSuffix 是原子写入的临时文件后缀；rename 失败后由 defer 清理残留。
	tmpSuffix = ".tmp-"
)

// Config 描述 sharedfile store 落盘行为；所有字段都可零值（CWD=="" 时
// 整个 disk 路径走 no-op，store 退化为 DB-only 模式）。InlineThresholdBytes
// 为 0 时阈值视为无穷大（DB 始终存正文）。
type Config struct {
	// CWD 是磁盘 sandbox 的根目录（通常 = platformconfig.Config.ProjectRoot）。
	// 空字符串关闭磁盘落盘。
	CWD string
	// InlineThresholdBytes 是 DB 内联正文的最大字节数；写入超过时 store
	// 把 DB content 设为空串，磁盘成为唯一正文来源。
	InlineThresholdBytes int
}

// Enabled 判断 Config 是否启用磁盘 source；store 层据此选择 disk-source 或 DB-only 路径。
func (c Config) Enabled() bool { return strings.TrimSpace(c.CWD) != "" }

// ResolvedThreshold 返回有效 DB inline 阈值，0 或负数使用默认阈值。
func (c Config) ResolvedThreshold() int {
	if c.InlineThresholdBytes <= 0 {
		return DefaultInlineThresholdBytes
	}
	return c.InlineThresholdBytes
}

// sharedfilefs sentinel 错误，调用方可用 errors.Is 区分磁盘未启用和 sandbox 逃逸。
var (
	ErrDiskDisabled  = errors.New("sharedfilefs: disk source not configured")
	ErrSandboxEscape = errors.New("sharedfilefs: resolved path escapes sandbox")
)

// SandboxRoot 返回 `<CWD>/.agnet/shared`；未启用磁盘 source 时返回空串。
func (c Config) SandboxRoot() string {
	if !c.Enabled() {
		return ""
	}
	return filepath.Join(c.CWD, SandboxDir)
}

// ResolveAbs 把已清理的相对路径拼到 sandbox 根目录下，并校验结果没有越界。
// 调用方必须先走 sharedfilepath 的读写校验；这里负责 join 后的真实 sandbox 边界。
func (c Config) ResolveAbs(cleanedRel string) (string, error) {
	if !c.Enabled() {
		return "", ErrDiskDisabled
	}
	root := c.SandboxRoot()
	abs := filepath.Clean(filepath.Join(root, cleanedRel))
	rootAbs := filepath.Clean(root)
	// abs 必须等于 sandbox 根或位于其下级；带路径分隔符比较可避免 sharedXX 这类前缀误判。
	rootWithSep := rootAbs + string(filepath.Separator)
	if abs != rootAbs && !strings.HasPrefix(abs, rootWithSep) {
		return "", ErrSandboxEscape
	}
	return abs, nil
}

// ResolveReadAbs 返回可读磁盘路径，并拒绝任何符号链接或 junction 让路径跳出 sandbox。
func (c Config) ResolveReadAbs(cleanedRel string) (string, error) {
	abs, err := c.ResolveAbs(cleanedRel)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(abs); err == nil {
		if err := c.ensureExistingSandboxPath(abs); err != nil {
			return "", err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	return abs, nil
}

// ResolveWriteAbs 返回可写磁盘路径，并在写入前确认父目录没有被重解析到 sandbox 外。
func (c Config) ResolveWriteAbs(cleanedRel string) (string, error) {
	abs, err := c.ResolveAbs(cleanedRel)
	if err != nil {
		return "", err
	}
	if err := c.ensureWritableSandboxPath(abs); err != nil {
		return "", err
	}
	return abs, nil
}

// ResolveDeleteAbs 返回删除路径；目标缺失时允许继续，但已存在目标不能穿过链接边界。
func (c Config) ResolveDeleteAbs(cleanedRel string) (string, error) {
	abs, err := c.ResolveAbs(cleanedRel)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(abs); err == nil {
		if err := c.ensureExistingSandboxPath(abs); err != nil {
			return "", err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	return abs, nil
}

// ensureExistingSandboxPath 校验已存在目标不是 symlink，且真实路径仍在 sandbox 内。
func (c Config) ensureExistingSandboxPath(absPath string) error {
	rootReal, rel, err := c.realSandboxRel(absPath, true)
	if err != nil {
		return err
	}
	info, err := os.Lstat(absPath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrSandboxEscape
	}
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return err
	}
	if !sameFilesystemPath(realPath, filepath.Join(rootReal, rel)) {
		return ErrSandboxEscape
	}
	return nil
}

// ensureWritableSandboxPath 写入前逐级确认父目录不会通过 symlink/junction 跳出 sandbox。
func (c Config) ensureWritableSandboxPath(absPath string) error {
	rootReal, rel, err := c.realSandboxRel(absPath, true)
	if err != nil {
		return err
	}
	dirRel := filepath.Dir(rel)
	currentPath := c.SandboxRoot()
	currentReal := rootReal
	for _, part := range splitCleanRel(dirRel) {
		currentPath = filepath.Join(currentPath, part)
		currentReal = filepath.Join(currentReal, part)
		if err := ensureRealDirectory(currentPath, currentReal); err != nil {
			return err
		}
	}
	if _, err := os.Lstat(absPath); err == nil {
		if err := c.ensureExistingSandboxPath(absPath); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// realSandboxRel 返回 sandbox 真实根目录和目标相对路径，越界时直接拒绝。
func (c Config) realSandboxRel(absPath string, createRoot bool) (string, string, error) {
	if !c.Enabled() {
		return "", "", ErrDiskDisabled
	}
	rel, err := filepath.Rel(filepath.Clean(c.SandboxRoot()), filepath.Clean(absPath))
	if err != nil {
		return "", "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", ErrSandboxEscape
	}
	rootReal, err := c.ensureSandboxRoot(createRoot)
	if err != nil {
		return "", "", err
	}
	return rootReal, rel, nil
}

// ensureSandboxRoot 确认 `<CWD>/.agnet/shared` 每级目录都是真目录并返回真实路径。
func (c Config) ensureSandboxRoot(create bool) (string, error) {
	cwd := filepath.Clean(c.CWD)
	if create {
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			return "", fmt.Errorf("sharedfilefs: mkdir %s: %w", cwd, err)
		}
	}
	cwdReal, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", err
	}
	currentPath := cwd
	currentReal := cwdReal
	for _, part := range splitCleanRel(SandboxDir) {
		currentPath = filepath.Join(currentPath, part)
		currentReal = filepath.Join(currentReal, part)
		if !create {
			if _, err := os.Lstat(currentPath); err != nil {
				return "", err
			}
		}
		if err := ensureRealDirectory(currentPath, currentReal); err != nil {
			return "", err
		}
	}
	return currentReal, nil
}

// ensureRealDirectory 确保路径是目录且解析后的真实路径等于预期路径。
func ensureRealDirectory(path, expectedReal string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		if mkdirErr := os.Mkdir(path, 0o755); mkdirErr != nil && !errors.Is(mkdirErr, fs.ErrExist) {
			return fmt.Errorf("sharedfilefs: mkdir %s: %w", path, mkdirErr)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrSandboxEscape
	}
	stat, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !stat.IsDir() {
		return fmt.Errorf("sharedfilefs: %s is not a directory", path)
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if !sameFilesystemPath(realPath, expectedReal) {
		return ErrSandboxEscape
	}
	return nil
}

// splitCleanRel 把已清理相对路径拆成目录段，`.` 和根路径返回空切片。
func splitCleanRel(rel string) []string {
	cleaned := filepath.Clean(rel)
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return nil
	}
	return strings.Split(cleaned, string(filepath.Separator))
}

// sameFilesystemPath 按平台规则比较文件系统路径，Windows 下忽略大小写。
func sameFilesystemPath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// WriteAtomic 通过临时文件、fsync 和 rename 写入目标文件，避免半写入内容覆盖正式文件。
// absPath 必须已通过 ResolveAbs/ResolveWriteAbs；data 为 nil 时写入空文件。
func WriteAtomic(absPath string, data []byte) error {
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("sharedfilefs: mkdir %s: %w", dir, err)
	}
	tmp, err := makeTempFile(dir, filepath.Base(absPath))
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanedTmp := false
	defer func() {
		if !cleanedTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, writeErr := tmp.Write(data); writeErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("sharedfilefs: write tmp: %w", writeErr)
	}
	if syncErr := tmp.Sync(); syncErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("sharedfilefs: fsync tmp: %w", syncErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return fmt.Errorf("sharedfilefs: close tmp: %w", closeErr)
	}
	if renameErr := os.Rename(tmpPath, absPath); renameErr != nil {
		return fmt.Errorf("sharedfilefs: rename %s → %s: %w", tmpPath, absPath, renameErr)
	}
	cleanedTmp = true
	// 尽力 fsync 父目录，让 rename 元数据也尽量跨断电持久化；失败由调用层日志处理。
	if dirHandle, openErr := os.Open(dir); openErr == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return nil
}

// ReadDisk 读取文件正文和元数据；缺失错误原样返回，便于 store 决定是否回退 DB。
func ReadDisk(absPath string) ([]byte, fs.FileInfo, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		return nil, nil, fmt.Errorf("sharedfilefs: %s is a directory", absPath)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil, err
	}
	return data, info, nil
}

// RemoveDisk 删除磁盘正文；目标缺失视为成功，因为 DB 才是存在性索引。
func RemoveDisk(absPath string) error {
	if err := os.Remove(absPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("sharedfilefs: remove %s: %w", absPath, err)
	}
	return nil
}

// ModTime 返回文件修改时间；文件不可访问时返回零值，供磁盘回退路径填充 UpdatedAt。
func ModTime(absPath string) time.Time {
	info, err := os.Stat(absPath)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// makeTempFile 创建原子写入临时文件；随机后缀可降低并发写同一目标时的命名冲突。
func makeTempFile(dir, base string) (*os.File, error) {
	for attempt := 0; attempt < 5; attempt++ {
		var randomBytes [8]byte
		if _, err := rand.Read(randomBytes[:]); err != nil {
			return nil, fmt.Errorf("sharedfilefs: random suffix: %w", err)
		}
		name := filepath.Join(dir, base+tmpSuffix+hex.EncodeToString(randomBytes[:]))
		// O_EXCL 确保已存在的临时文件不会被复用；命中冲突时下一轮换随机后缀。
		f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("sharedfilefs: open tmp: %w", err)
		}
	}
	return nil, errors.New("sharedfilefs: could not create unique tmp file")
}
