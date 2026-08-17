//go:build windows

package installer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	"golang.org/x/sys/windows"
)

// windowsShortProcessPath 把已存在的 Windows 绝对路径转换成同一文件身份的
// 8.3 执行路径。锁定 cache 仍以完整版本、架构和 SHA 路径作为事实源；短路径只
// 绕过 LongPathsEnabled=0 时 cmd.exe/Node 对深层 npm 文件的 MAX_PATH 限制，
// 不改变目标、不走 PATH，也不放宽 ACL。
func windowsShortProcessPath(path string) (string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || !filepath.IsAbs(path) {
		return "", fmt.Errorf("Windows process path must be absolute: %q", path)
	}
	volumeRoot := filepath.Clean(filepath.VolumeName(path) + string(filepath.Separator))
	if volumeRoot == "." || !filepath.IsAbs(volumeRoot) {
		return "", fmt.Errorf("Windows process path has no absolute volume root: %q", path)
	}
	// GetShortPathName 会解析路径组件；在进入 Win32 API 前必须先拒绝完整父链上的
	// junction、符号链接和其他重解析点，避免 8.3 名称把缓存边界悄然导向根外。
	if err := validateWindowsInstallerPathWithinRoot(volumeRoot, path, false); err != nil {
		return "", fmt.Errorf("validate Windows process path %s: %w", securefs.RedactPath(path), securefs.WrapErrorForPath(err, path))
	}
	canonicalInfo, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect Windows process path %s: %w", securefs.RedactPath(path), securefs.WrapErrorForPath(err, path))
	}
	if canonicalInfo.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("Windows process path %s must not be a symlink", securefs.RedactPath(path))
	}
	longPathUnits, err := windows.UTF16FromString(path)
	if err != nil {
		return "", fmt.Errorf("encode Windows process path %s: %w", securefs.RedactPath(path), err)
	}
	longPath := &longPathUnits[0]
	required, err := windows.GetShortPathName(longPath, nil, 0)
	if err != nil {
		return "", fmt.Errorf("resolve short Windows process path %s: %w", securefs.RedactPath(path), securefs.WrapErrorForPath(err, path))
	}
	if required == 0 {
		return "", fmt.Errorf("resolve short Windows process path %s: empty result", securefs.RedactPath(path))
	}
	buffer := make([]uint16, required)
	written, err := windows.GetShortPathName(longPath, &buffer[0], uint32(len(buffer)))
	if err != nil {
		return "", fmt.Errorf("read short Windows process path %s: %w", securefs.RedactPath(path), securefs.WrapErrorForPath(err, path))
	}
	if written == 0 || written >= uint32(len(buffer)) {
		return "", fmt.Errorf("read short Windows process path %s: invalid length %d", securefs.RedactPath(path), written)
	}
	shortPath := filepath.Clean(windows.UTF16ToString(buffer[:written]))
	if shortPath == "." || !filepath.IsAbs(shortPath) {
		return "", errors.New("resolved Windows short process path is not absolute")
	}
	shortPathUnits, err := windows.UTF16FromString(shortPath)
	if err != nil {
		return "", fmt.Errorf("encode resolved Windows short process path: %w", err)
	}
	canonicalLength := len(longPathUnits) - 1
	shortLength := len(shortPathUnits) - 1
	if canonicalLength >= 260 && shortLength >= 260 {
		return "", fmt.Errorf("Windows 8.3 process path is unavailable for a MAX_PATH target: canonical_utf16_length=%d short_utf16_length=%d", canonicalLength, shortLength)
	}
	shortInfo, err := os.Lstat(shortPath)
	if err != nil {
		return "", fmt.Errorf("verify short Windows process path: %w", securefs.WrapErrorForPath(err, path))
	}
	if !os.SameFile(canonicalInfo, shortInfo) {
		return "", errors.New("resolved Windows short process path changed file identity")
	}
	return shortPath, nil
}

// windowsShortProcessPathWithinRoot 先证明路径属于调用方声明的可信根且父链没有
// junction/reparse point，再转换同一文件身份的 8.3 进程路径。它只用于进程边界，
// 不得把短路径写入缓存身份或资产收据。
func windowsShortProcessPathWithinRoot(root, path string) (string, error) {
	if err := validateWindowsInstallerPathWithinRoot(root, path, false); err != nil {
		return "", fmt.Errorf("validate rooted Windows process path %s: %w", securefs.RedactPath(path), securefs.WrapErrorForPath(err, path))
	}
	return windowsShortProcessPath(path)
}

// WindowsShortProcessPath 返回与现有绝对路径保持同一文件身份的 Windows 8.3
// 进程边界路径。完整版本、架构和 SHA 路径仍是缓存事实；调用方不得把短路径
// 持久化为资产身份、回退到 PATH 或借此放宽 ACL。
func WindowsShortProcessPath(path string) (string, error) {
	return windowsShortProcessPath(path)
}

// WindowsShortProcessPathWithinRoot 是公共的受根目录约束版本；只有目标位于真实、
// 无重解析点的 root 父链内时才返回 8.3 路径。权限失败继续保留 Win32 5/1314，
// 由上层分类为 authorization_required 并触发宿主授权提示。
func WindowsShortProcessPathWithinRoot(root, path string) (string, error) {
	return windowsShortProcessPathWithinRoot(root, path)
}
