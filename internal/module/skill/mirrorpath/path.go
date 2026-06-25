// Package mirrorpath 提供技能 mirror 目录路径的安全解析与校验工具。
// 防止路径逃逸、symlink 劫持等文件系统安全问题。
package mirrorpath

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ResolveValidRootSymlink 尝试将 root 解析为真实路径；若解析结果的最后一段不是 "skills" 则返回原值。
// 用于在 Darwin 等平台处理 /var -> /private/var 类型的系统级 symlink。
func ResolveValidRootSymlink(root string) string {
	if resolved := resolveSymlinkPath(root); filepath.Base(resolved) == "skills" {
		return resolved
	}
	return root
}

// resolveSymlinkPath 逐步解析路径中的 symlink，最多迭代 16 次，失败时返回当前路径。
func resolveSymlinkPath(path string) string {
	if realPath, err := filepath.EvalSymlinks(path); err == nil {
		return realPath
	}
	for i := 0; i < 16; i++ {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			break
		}
		target, err := os.Readlink(path)
		if err != nil {
			break
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = filepath.Clean(target)
	}
	return path
}

// RejectSymlinkAncestors 检查 root 路径的每一层祖先，若存在非系统允许的 symlink 则报错。
// 防止攻击者通过替换父目录 symlink 劫持 mirror 根目录。
func RejectSymlinkAncestors(root string) error {
	path, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("normalize skill mirror root: %w", err)
	}
	for {
		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		info, err := os.Lstat(path)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 && !allowedRootSymlinkAncestor(path) {
				return fmt.Errorf("skill mirror root ancestor is symlink: %s", path)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		path = parent
	}
}

// allowedRootSymlinkAncestor 判断路径是否为 Darwin 系统级允许的 symlink 祖先（/var、/tmp、/etc）。
func allowedRootSymlinkAncestor(path string) bool {
	return runtime.GOOS == "darwin" && (path == "/var" || path == "/tmp" || path == "/etc")
}

// SafeFileInfo 获取目录条目的 FileInfo，若路径为 symlink 或非普通文件则报错，防止通过 symlink 读取越界文件。
func SafeFileInfo(path string, entry fs.DirEntry) (fs.FileInfo, error) {
	info, err := entry.Info()
	if err != nil {
		return nil, fmt.Errorf("stat mirror path %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("unsafe mirror path %s", path)
	}
	return info, nil
}

// SafeRelative 将 path 转换为相对于 root 的 slash 路径，并验证不会逃逸出 root。
func SafeRelative(root, path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("normalize mirror path %s: %w", path, err)
	}
	rel, err := filepath.Rel(root, filepath.Clean(absPath))
	if err != nil {
		return "", fmt.Errorf("rel mirror path %s: %w", path, err)
	}
	rel = filepath.ToSlash(rel)
	if UnsafeRelative(rel) {
		return "", fmt.Errorf("unsafe mirror path %s escapes root", path)
	}
	return rel, nil
}

// ForExistingSkillDirs 对 selected 目录和 roots 下所有已存在的同名目录依次调用 fn。
// selected 目录始终第一个处理，重复路径跳过。
func ForExistingSkillDirs(roots []string, name, selected string, fn func(string) error) error {
	if err := fn(selected); err != nil {
		return err
	}
	for _, root := range roots {
		dir := filepath.Join(root, name)
		if filepath.Clean(dir) == filepath.Clean(selected) {
			continue
		}
		if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if err := fn(dir); err != nil {
			return err
		}
	}
	return nil
}

// UnsafeRelative 判断相对路径是否存在安全风险（空串、绝对路径、含 null 字节、路径逃逸等）。
func UnsafeRelative(rel string) bool {
	if unsafeRelativeValue(rel) {
		return true
	}
	for _, part := range strings.Split(rel, "/") {
		if unsafeRelativePart(part) {
			return true
		}
	}
	return false
}

// unsafeRelativeValue 判断相对路径整体是否为不安全值（空、点、双点、绝对路径、含 null 或以 ../ 开头）。
func unsafeRelativeValue(rel string) bool {
	return rel == "" || rel == "." || rel == ".." ||
		filepath.IsAbs(rel) || strings.Contains(rel, "\x00") ||
		strings.HasPrefix(rel, "../")
}

// unsafeRelativePart 判断路径中单个分段是否不安全（空串、点或双点）。
func unsafeRelativePart(part string) bool {
	return part == "" || part == "." || part == ".."
}
