// Package memshared 提供记忆系统跨模块共享的路径安全工具、类型定义和常量；
// 不依赖任何业务模块，可被 memory 子包自由引用。
package memshared

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"
	"golang.org/x/text/unicode/norm"
)

var (
	ErrInvalidMemoryRoot = errors.New("invalid memory root")

	// SafeReadEntrypoint sentinel errors. Callers can match with errors.Is
	// to distinguish failure modes that previously collapsed into a single
	// boolean. Wraps os.ErrNotExist so legacy `errors.Is(err, os.ErrNotExist)`
	// checks continue to work for the missing-file case.
	ErrSafeReadNotFound    = fmt.Errorf("safe read: not found: %w", os.ErrNotExist)
	ErrSafeReadContainment = errors.New("safe read: path escapes root")
	ErrSafeReadIsDir       = errors.New("safe read: target is a directory")
	ErrSafeReadBrokenLink  = errors.New("safe read: broken symlink or unreadable parent")
)

// ValidateMemoryRoot 校验并规范化记忆根目录路径，拒绝 null byte、UNC、Windows 盘符根、相对路径、过宽路径（/ 或一级子目录）。
// 成功返回末尾带分隔符的绝对路径，输入为空则返回空字符串。
func ValidateMemoryRoot(raw string) (string, error) {
	raw = norm.NFC.String(strings.TrimSpace(raw))
	if raw == "" {
		return "", nil
	}
	if strings.ContainsRune(raw, '\x00') {
		return "", fmt.Errorf("%w: null byte", ErrInvalidMemoryRoot)
	}
	if strings.HasPrefix(raw, `\\`) || strings.HasPrefix(raw, "//") {
		return "", fmt.Errorf("%w: UNC path is not allowed", ErrInvalidMemoryRoot)
	}
	if isWindowsDriveRoot(raw) {
		return "", fmt.Errorf("%w: drive root is not allowed", ErrInvalidMemoryRoot)
	}
	expanded, err := expandHomePath(raw)
	if err != nil {
		return "", err
	}
	if !isAbsoluteMemoryPath(expanded) {
		return "", fmt.Errorf("%w: path must be absolute", ErrInvalidMemoryRoot)
	}
	cleaned, err := CleanAbsolutePath(expanded)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidMemoryRoot, err)
	}
	if isRootOrNearRoot(cleaned) {
		return "", fmt.Errorf("%w: path is too broad", ErrInvalidMemoryRoot)
	}
	return strings.TrimRight(cleaned, string(os.PathSeparator)) + string(os.PathSeparator), nil
}

// CleanAbsolutePath 规范化路径并确保其为绝对路径，输入为空则报错。
func CleanAbsolutePath(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("path is empty")
	}
	cleaned := filepath.Clean(norm.NFC.String(strings.TrimSpace(raw)))
	if !filepath.IsAbs(cleaned) {
		absolute, err := filepath.Abs(cleaned)
		if err != nil {
			return "", err
		}
		cleaned = filepath.Clean(absolute)
	}
	return cleaned, nil
}

// RealPathDeepestExisting 向上逐级查找最深的已存在祖先目录并解析其真实路径，再拼接剩余段；
// 用于校验尚未创建的路径是否会逃逸根目录。
func RealPathDeepestExisting(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if _, err := os.Stat(cleaned); err == nil {
		return filepath.EvalSymlinks(cleaned)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	current := cleaned
	var suffix []string
	for {
		next := filepath.Dir(current)
		if next == current {
			return "", os.ErrNotExist
		}
		suffix = append(suffix, filepath.Base(current))
		current = next
		if _, err := os.Stat(current); err == nil {
			real, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for index := len(suffix) - 1; index >= 0; index-- {
				real = filepath.Join(real, suffix[index])
			}
			return real, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
}

// EnsureResolvablePath 逐级向上检查路径的每个祖先目录是否可解析（符号链接不指向断链）；
// 用于在写盘前确认目标路径可安全创建。
func EnsureResolvablePath(path string) error {
	for probe := filepath.Clean(path); ; probe = filepath.Dir(probe) {
		info, err := os.Lstat(probe)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink != 0 {
				if _, err := filepath.EvalSymlinks(probe); err != nil {
					return err
				}
			}
		case errors.Is(err, os.ErrNotExist):
		default:
			return err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return nil
		}
	}
}

// ShortHash 计算 raw 的 SHA-256 前 4 字节并返回十六进制字符串，用于生成临时文件后缀。
func ShortHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:4])
}

// expandHomePath 将 ~/ 前缀展开为用户主目录；裸 ~ 或 ~user 格式视为不合法路径。
func expandHomePath(raw string) (string, error) {
	switch {
	case raw == "~", raw == "~/", raw == `~\\`:
		return "", fmt.Errorf("%w: home root is not allowed", ErrInvalidMemoryRoot)
	case strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, `~\\`):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrInvalidMemoryRoot, err)
		}
		tail := strings.TrimLeft(raw[1:], `/\`)
		if strings.TrimSpace(tail) == "" || filepath.Clean(tail) == "." {
			return "", fmt.Errorf("%w: home root is not allowed", ErrInvalidMemoryRoot)
		}
		return filepath.Join(home, tail), nil
	case strings.HasPrefix(raw, "~"):
		return "", fmt.Errorf("%w: unsupported home path", ErrInvalidMemoryRoot)
	default:
		return raw, nil
	}
}

// isWindowsDriveRoot 判断路径是否为 Windows 盘符根目录（如 C:\），用于拒绝过宽根目录。
func isWindowsDriveRoot(path string) bool {
	if len(path) < 2 || path[1] != ':' {
		return false
	}
	rest := strings.Trim(path[2:], `/\`)
	return rest == ""
}

// isAbsoluteMemoryPath 判断路径是否为绝对路径（兼容 Unix 和 Windows 格式）。
func isAbsoluteMemoryPath(path string) bool {
	if filepath.IsAbs(path) {
		return true
	}
	return len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/')
}

// isRootOrNearRoot 判断路径是否为根目录或一级子目录，用于拒绝过宽的记忆根目录。
func isRootOrNearRoot(path string) bool {
	volume := filepath.VolumeName(path)
	trimmed := strings.TrimPrefix(path, volume)
	trimmed = strings.TrimRight(trimmed, string(os.PathSeparator))
	if trimmed == "" || trimmed == string(os.PathSeparator) {
		return true
	}
	trimmed = strings.TrimPrefix(trimmed, string(os.PathSeparator))
	return !strings.Contains(trimmed, string(os.PathSeparator))
}

// SafeReadEntrypoint resolves root and the candidate path through symlinks,
// confirms the resolved file lives under the resolved root, then reads its
// bytes and returns os.FileInfo for the resolved file. On failure it
// returns (nil, nil, err) where err is one of the sentinels above
// (matchable via errors.Is) or a wrapped os error preserving Is-chain
// semantics for permission/IO categories.
//
// Failure mapping:
//   - root EvalSymlinks fails       -> ErrSafeReadBrokenLink (wraps cause)
//   - indexPath Lstat ENOENT        -> ErrSafeReadNotFound
//   - indexPath EvalSymlinks fails  -> ErrSafeReadBrokenLink (wraps cause)
//   - parent dir EvalSymlinks fails -> ErrSafeReadBrokenLink (wraps cause)
//   - resolved path escapes root    -> ErrSafeReadContainment
//   - resolved path Stat ENOENT     -> ErrSafeReadNotFound
//   - resolved path is a directory  -> ErrSafeReadIsDir
//   - ReadFile fails                -> wrapped cause (errors.Is for ErrPermission etc still works)
//
// This is the single defense-in-depth read primitive for memory-system
// entrypoints (MEMORY.md and nested CLAUDE.md).
//

// TOCTOU between Lstat/EvalSymlinks and ReadFile is best-effort.
// A future os.OpenRoot-based implementation can close the TOCTOU window.
// Tracked in p25 B-class infrastructure backlog
// (docs/plans/迁移/p25记忆业务能力/p25记忆业务能力对齐.md basic
// infrastructure gap table). Contract is already result-equivalent via
// EvalSymlinks + ContainsPath (mapping §11.2 + §2.3 strong contracts);
// OpenRoot only narrows the implementation race window, not a new
// contract surface.
func SafeReadEntrypoint(root, indexPath string) ([]byte, os.FileInfo, error) {
	rootReal, err := safeReadRoot(root)
	if err != nil {
		return nil, nil, err
	}
	candidate, err := safeReadCandidate(indexPath)
	if err != nil {
		return nil, nil, err
	}
	if !pathutil.ContainsPath(rootReal, candidate) {
		return nil, nil, ErrSafeReadContainment
	}
	return readSafeResolvedFile(candidate)
}

// safeReadRoot 解析记忆根目录的真实路径，失败时返回 ErrSafeReadBrokenLink。
func safeReadRoot(root string) (string, error) {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", safeReadBrokenLinkOrNotFound("root", err)
	}
	return rootReal, nil
}

// safeReadCandidate 通过 Lstat 检查候选路径，区分符号链接与普通文件，返回解析后的真实路径。
func safeReadCandidate(indexPath string) (string, error) {
	info, err := os.Lstat(indexPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrSafeReadNotFound
		}
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return safeReadSymlinkTarget(indexPath)
	}
	return safeReadRegularPath(indexPath)
}

// safeReadSymlinkTarget 解析符号链接目标的真实路径，链接断裂时返回 ErrSafeReadBrokenLink。
func safeReadSymlinkTarget(indexPath string) (string, error) {
	resolved, err := filepath.EvalSymlinks(indexPath)
	if err != nil {
		return "", safeReadBrokenLinkOrNotFound("target", err)
	}
	return resolved, nil
}

// safeReadRegularPath 解析普通文件父目录的真实路径并拼接文件名，用于非符号链接场景。
func safeReadRegularPath(indexPath string) (string, error) {
	resolved, err := filepath.EvalSymlinks(filepath.Dir(indexPath))
	if err != nil {
		return "", safeReadBrokenLinkOrNotFound("parent", err)
	}
	return filepath.Join(resolved, filepath.Base(indexPath)), nil
}

// safeReadBrokenLinkOrNotFound 将 EvalSymlinks 的错误映射为哨兵错误，ENOENT → ErrSafeReadNotFound，其他 → ErrSafeReadBrokenLink。
func safeReadBrokenLinkOrNotFound(label string, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return ErrSafeReadNotFound
	}
	return fmt.Errorf("%w: %s: %w", ErrSafeReadBrokenLink, label, err)
}

// readSafeResolvedFile 对已解析的真实路径执行 Stat + ReadFile，区分目录、不存在、读取失败三种错误。
func readSafeResolvedFile(candidate string) ([]byte, os.FileInfo, error) {
	resolvedInfo, err := os.Stat(candidate)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, ErrSafeReadNotFound
		}
		return nil, nil, err
	}
	if resolvedInfo.IsDir() {
		return nil, nil, ErrSafeReadIsDir
	}
	raw, err := os.ReadFile(candidate)
	if err != nil {
		return nil, nil, err
	}
	return raw, resolvedInfo, nil
}
