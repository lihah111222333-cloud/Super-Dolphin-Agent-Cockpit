package installer

// 本文件故意不加 windows build tag：清理错误合并和根目录边界是跨平台安全
// 契约，也用于非 Windows 故障注入测试；Win32 ACL 与 reparse 细节由带标签文件实现。

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// windowsInstallerFile 统一 Windows 安装器的读写、权限和关闭能力，便于在清理故障测试中注入 Close 错误。
type windowsInstallerFile interface {
	io.Reader
	io.Writer
	Name() string
	Chmod(os.FileMode) error
	Close() error
}

var (
	openWindowsInstallerInput = func(name string) (windowsInstallerFile, error) {
		return os.Open(name)
	}
	openWindowsInstallerOutput = func(name string, flag int, perm os.FileMode) (windowsInstallerFile, error) {
		return os.OpenFile(name, flag, perm)
	}
	createWindowsInstallerTemp = func(dir, pattern string) (windowsInstallerFile, error) {
		return os.CreateTemp(dir, pattern)
	}
	relWindowsInstallerPath    = filepath.Rel
	removeWindowsInstallerPath = os.Remove
	removeWindowsInstallerAll  = os.RemoveAll
)

// joinWindowsInstallerCleanupError 合并主操作与清理错误；cleanup 失败时上层必须清空成功结果。
func joinWindowsInstallerCleanupError(operationErr, cleanupErr error, context string) error {
	if cleanupErr == nil {
		return operationErr
	}
	cleanupErr = fmt.Errorf("%s: %w", context, cleanupErr)
	return errors.Join(operationErr, cleanupErr)
}

// validateWindowsInstallerPathWithinRoot 在文件操作前重验目标及其父链；任何重解析点都立即阻断。
func validateWindowsInstallerPathWithinRoot(root, target string, targetMayBeMissing bool) error {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve installer path root %q: %w", root, err)
	}
	absoluteTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve installer path target %q: %w", target, err)
	}
	absoluteRoot = filepath.Clean(absoluteRoot)
	absoluteTarget = filepath.Clean(absoluteTarget)
	relative, err := filepath.Rel(absoluteRoot, absoluteTarget)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("installer path target escapes root: %q", absoluteTarget)
	}
	rootInfo, err := os.Lstat(absoluteRoot)
	if err != nil {
		return fmt.Errorf("inspect installer path root %q: %w", absoluteRoot, err)
	}
	if isUnsafeAssetFile(rootInfo) {
		return fmt.Errorf("installer path root is a symlink or reparse point: %q", absoluteRoot)
	}
	if !rootInfo.IsDir() {
		return fmt.Errorf("installer path root is not a real directory: %q", absoluteRoot)
	}
	if relative == "." {
		return nil
	}
	current := absoluteRoot
	segments := strings.Split(relative, string(filepath.Separator))
	for index, segment := range segments {
		if segment == "" || segment == "." {
			continue
		}
		current = filepath.Join(current, segment)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) && targetMayBeMissing && index == len(segments)-1 {
			return nil
		}
		if statErr != nil {
			return fmt.Errorf("inspect installer path component %q: %w", current, statErr)
		}
		if filepath.Clean(current) == absoluteTarget {
			if isUnsafeAssetFile(info) {
				return fmt.Errorf("installer path target is a symlink or reparse point: %q", current)
			}
			return nil
		}
		if isUnsafeAssetFile(info) {
			return fmt.Errorf("installer path component is a symlink or reparse point: %q", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("installer path component is not a real directory: %q", current)
		}
	}
	return nil
}

// removeWindowsInstallerAllChecked 只删除已重验且位于预期根目录中的目标。
func removeWindowsInstallerAllChecked(root, target string) error {
	if err := validateWindowsInstallerPathWithinRoot(root, target, true); err != nil {
		return err
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve installer cleanup root %q: %w", root, err)
	}
	absoluteTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve installer cleanup target %q: %w", target, err)
	}
	if filepath.Clean(absoluteRoot) == filepath.Clean(absoluteTarget) {
		return fmt.Errorf("refuse to remove installer cleanup root: %q", absoluteRoot)
	}
	if err := validateWindowsInstallerTreeWithinRoot(root, target); err != nil {
		return err
	}
	return removeWindowsInstallerAll(target)
}

// removeWindowsInstallerPathChecked 只删除已重验且位于预期根目录中的文件。
func removeWindowsInstallerPathChecked(root, target string) error {
	if err := validateWindowsInstallerPathWithinRoot(root, target, true); err != nil {
		return err
	}
	return removeWindowsInstallerPath(target)
}

// renameWindowsInstallerPathChecked 在原子发布前重验源、目标和两侧父链。
func renameWindowsInstallerPathChecked(root, source, target string) error {
	if err := validateWindowsInstallerPathWithinRoot(root, source, false); err != nil {
		return fmt.Errorf("validate installer rename source %q: %w", source, err)
	}
	if err := validateWindowsInstallerTreeWithinRoot(root, source); err != nil {
		return fmt.Errorf("validate installer rename source tree %q: %w", source, err)
	}
	if err := validateWindowsInstallerPathWithinRoot(root, target, true); err != nil {
		return fmt.Errorf("validate installer rename target %q: %w", target, err)
	}
	return os.Rename(source, target)
}

// validateWindowsInstallerTreeWithinRoot 在递归删除或发布前拒绝树内任意重解析点。
func validateWindowsInstallerTreeWithinRoot(root, target string) error {
	if err := validateWindowsInstallerPathWithinRoot(root, target, true); err != nil {
		return err
	}
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect installer cleanup target %q: %w", target, err)
	}
	if isUnsafeAssetFile(info) {
		return fmt.Errorf("installer cleanup target is a symlink or reparse point: %q", target)
	}
	if !info.IsDir() {
		return nil
	}
	return filepath.WalkDir(target, func(path string, _ os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entryInfo, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if isUnsafeAssetFile(entryInfo) {
			return fmt.Errorf("installer cleanup tree contains a symlink or reparse point: %q", path)
		}
		return nil
	})
}

// validateWindowsInstallerExistingFile 在打开输入文件前拒绝符号链接、junction 和其他重解析点。
func validateWindowsInstallerExistingFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if isUnsafeAssetFile(info) || !info.Mode().IsRegular() {
		return fmt.Errorf("installer input is not a real regular file: %q", path)
	}
	return nil
}
