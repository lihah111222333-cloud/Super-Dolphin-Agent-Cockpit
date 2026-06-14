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

// ResolveValidRootSymlink 解析valid根目录symlink。
func ResolveValidRootSymlink(root string) string {
	if resolved := resolveSymlinkPath(root); filepath.Base(resolved) == "skills" {
		return resolved
	}
	return root
}

// resolveSymlinkPath 解析symlink路径。
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

// RejectSymlinkAncestors 处理rejectsymlinkancestors。
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

func allowedRootSymlinkAncestor(path string) bool {
	return runtime.GOOS == "darwin" && (path == "/var" || path == "/tmp" || path == "/etc")
}

// SafeFileInfo 处理safe文件info。
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

// SafeRelative 处理safe相对。
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

// ForExistingSkillDirs 为existing技能目录处理技能。
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

// UnsafeRelative 处理unsafe相对。
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

// unsafeRelativeValue 处理unsafe相对值。
func unsafeRelativeValue(rel string) bool {
	return rel == "" || rel == "." || rel == ".." ||
		filepath.IsAbs(rel) || strings.Contains(rel, "\x00") ||
		strings.HasPrefix(rel, "../")
}

func unsafeRelativePart(part string) bool {
	return part == "" || part == "." || part == ".."
}
