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

// Phase 3.6 / 3C · sharedfile 磁盘 source / DB 索引 的磁盘原语。
//
// Source of truth = 磁盘下 `<CWD>/.agnet/shared/<rel>`；DB 退化为索引（仍存
// updated_by / 时间戳；正文超过 InlineThresholdBytes 时 DB content 为空）。
//
// 本包只做文件 IO + sandbox 边界，不知道 SQL。Caller（store）负责：
//   - 在 ResolveAbs 之前把 rel 通过 sharedfilepath.ValidateRel 清洗
//   - 写完磁盘后再 Upsert DB（顺序保证 read 路径不会在 DB 命中但磁盘读不到）
//   - Delete 时先删 DB 再删磁盘（DB 是 List 的真理来源；磁盘残留比 DB 残留
//     更安全，下次 startup reconciler 可以扫掉）

const (
	// SandboxDir 是磁盘根相对 CWD 的子目录；与 plan §3 协议保持一致。
	SandboxDir = ".agnet/shared"
	// DefaultInlineThresholdBytes 是默认的 DB inline 阈值。100KB 足够装下
	// 大多数 handoff / progress 文件，又能挡住整段 LLM transcript 这类
	// 大对象。可由 Config.InlineThresholdBytes 覆盖。
	DefaultInlineThresholdBytes = 100 * 1024
	// Staging file suffix; rename 失败兜底由 cleanupTmp 处理。
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

// Enabled reports whether this Config has a usable CWD; store layers can use
// this to decide between disk-source mode and legacy DB-only mode.
// Enabled 判断平台sharedfilefs是否启用。
func (c Config) Enabled() bool { return strings.TrimSpace(c.CWD) != "" }

// ResolvedThreshold returns the effective inline-threshold byte count,
// substituting the default when caller passed 0.
// ResolvedThreshold 处理已解析threshold。
func (c Config) ResolvedThreshold() int {
	if c.InlineThresholdBytes <= 0 {
		return DefaultInlineThresholdBytes
	}
	return c.InlineThresholdBytes
}

// Sentinel errors so callers can distinguish disk-disabled / not-found /
// sandbox escape without parsing message strings.
var (
	ErrDiskDisabled  = errors.New("sharedfilefs: disk source not configured")
	ErrSandboxEscape = errors.New("sharedfilefs: resolved path escapes sandbox")
)

// SandboxRoot returns `<CWD>/.agnet/shared` (or "" if CWD is empty).
// SandboxRoot 处理沙箱根目录。
func (c Config) SandboxRoot() string {
	if !c.Enabled() {
		return ""
	}
	return filepath.Join(c.CWD, SandboxDir)
}

// ResolveAbs joins the sandbox root with a pre-cleaned relative path and
// verifies the absolute result still lives under the sandbox. The relative
// path MUST already have been normalized by sharedfilepath.ValidateReadPath
// or ValidateWritePath; this helper only enforces the post-join sandbox
// boundary, not lexical traversal.
// ResolveAbs 解析abs。
func (c Config) ResolveAbs(cleanedRel string) (string, error) {
	if !c.Enabled() {
		return "", ErrDiskDisabled
	}
	root := c.SandboxRoot()
	abs := filepath.Clean(filepath.Join(root, cleanedRel))
	rootAbs := filepath.Clean(root)
	// sandbox check: abs must equal rootAbs or live below it. Use the
	// path-separator form to avoid `<root>/.agnet/sharedXX` matching by
	// prefix-string only.
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

func splitCleanRel(rel string) []string {
	cleaned := filepath.Clean(rel)
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return nil
	}
	return strings.Split(cleaned, string(filepath.Separator))
}

func sameFilesystemPath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// WriteAtomic creates parent directories then writes data through a tmp file
// + rename. Crash-safe: a half-written file never replaces the canonical
// target (rename is atomic on POSIX); a leftover tmp is cleaned up when the
// next attempt fsyncs and renames over the same target.
//
// `data` may be nil (empty file). Caller must have ResolveAbs'd absPath.
// WriteAtomic 写入atomic。
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
	// fsync the parent dir so the rename itself is durable across power loss;
	// best-effort: failure logged at caller level.
	if dirHandle, openErr := os.Open(dir); openErr == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return nil
}

// ReadDisk returns file content along with size and mod time. Missing
// files surface fs.ErrNotExist so callers can fall back to DB.
// ReadDisk 读取disk。
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

// RemoveDisk deletes the file; missing files are treated as success since DB
// is the authoritative existence record.
// RemoveDisk 移除disk。
func RemoveDisk(absPath string) error {
	if err := os.Remove(absPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("sharedfilefs: remove %s: %w", absPath, err)
	}
	return nil
}

// ModTime returns the file's mod time as a regular time.Time, falling back
// to the zero value when the file is missing. Used by store layers to
// populate UpdatedAt during disk-fallback paths.
// ModTime 处理mod时间。
func ModTime(absPath string) time.Time {
	info, err := os.Stat(absPath)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// makeTempFile creates `<dir>/<base>.tmp-<rand>` for atomic writes. The
// random suffix avoids collisions when two writers race on the same target
// (one will fail rename or be cleaned up by the other's defer).
func makeTempFile(dir, base string) (*os.File, error) {
	for attempt := 0; attempt < 5; attempt++ {
		var randomBytes [8]byte
		if _, err := rand.Read(randomBytes[:]); err != nil {
			return nil, fmt.Errorf("sharedfilefs: random suffix: %w", err)
		}
		name := filepath.Join(dir, base+tmpSuffix+hex.EncodeToString(randomBytes[:]))
		// O_EXCL ensures we fail if the path exists (which would imply a
		// race or a stale tmp); next attempt will pick a different suffix.
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
