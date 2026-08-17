//go:build windows

package runtimeenv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveWindowsLSPProductRoot 解析 Windows LSP 自动安装桥的可写产品根目录。
// 显式产品目录优先于系统用户缓存；该函数只返回绝对路径，不创建目录、不联网，
// 真正的缓存写入只能由获得安装能力的 InstallAction 生命周期执行。
func ResolveWindowsLSPProductRoot() (string, error) {
	if home := strings.TrimSpace(os.Getenv(superDolphinHomeEnv)); home != "" {
		return requireAbsoluteWindowsLSPProductRoot(home, superDolphinHomeEnv)
	}
	if projectRoot := strings.TrimSpace(os.Getenv(projectRootEnv)); projectRoot != "" {
		root, err := requireAbsoluteWindowsLSPProductRoot(projectRoot, projectRootEnv)
		if err != nil {
			return "", err
		}
		return filepath.Join(root, ".super-dolphin"), nil
	}
	if resources := strings.TrimSpace(os.Getenv(runtimeResourcesEnv)); resources != "" {
		root, err := requireAbsoluteWindowsLSPProductRoot(resources, runtimeResourcesEnv)
		if err != nil {
			return "", err
		}
		return filepath.Join(root, ".super-dolphin"), nil
	}
	userCacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve Windows user cache directory for LSP products: %w", err)
	}
	if strings.TrimSpace(userCacheDir) == "" {
		return "", errors.New("Windows user cache directory for LSP products is empty")
	}
	return requireAbsoluteWindowsLSPProductRoot(
		filepath.Join(userCacheDir, "super-agent-v3", "mcp-lsp", "language-servers"),
		"Windows user cache",
	)
}

// PrependWindowsRuntimePathEntries 把已验证的 Windows runtime 绝对目录置于 PATH
// 最前方，并在其后保留无关条目；调用方必须先验证目录属于已锁定的产品缓存。
func PrependWindowsRuntimePathEntries(entries ...string) error {
	if len(entries) == 0 {
		return errors.New("Windows runtime PATH entries are required")
	}
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" || !filepath.IsAbs(entry) {
			return fmt.Errorf("Windows runtime PATH entry must be an absolute path: %q", entry)
		}
	}
	all := make([]string, 0, len(entries)+4)
	all = append(all, entries...)
	if current := strings.TrimSpace(os.Getenv("PATH")); current != "" {
		all = append(all, strings.Split(current, string(os.PathListSeparator))...)
	}
	return setControlledEnvPath(os.Setenv, "PATH", all...)
}

func requireAbsoluteWindowsLSPProductRoot(value, key string) (string, error) {
	value = filepath.Clean(strings.TrimSpace(value))
	if value == "." || !filepath.IsAbs(value) {
		return "", fmt.Errorf("%s must be an absolute path: %q", key, value)
	}
	return value, nil
}
