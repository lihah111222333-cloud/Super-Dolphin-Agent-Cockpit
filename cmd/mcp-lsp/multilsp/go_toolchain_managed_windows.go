//go:build windows

package multilsp

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

const (
	managedWindowsSuperDolphinHomeEnv    = "SUPER_DOLPHIN_HOME"
	managedWindowsProjectRootEnv         = "PROJECT_ROOT"
	managedWindowsRuntimeResourcesDirEnv = "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR"
)

// managedGoToolchainCandidates 只读解析已由 Windows 生产安装器物化的 Go 产品缓存。
// 安装动作仍由 manager 的 ensureInstalled 负责；这里不联网、不写盘，也不从宿主 PATH 猜测。
func managedGoToolchainCandidates(env []string) []goToolchainCandidate {
	productRoot, ok := managedWindowsLSPProductRoot(env)
	if !ok {
		return nil
	}

	cacheRoot := filepath.Join(productRoot, "cache", installer.WindowsLSPAssetCacheSubdir)
	result, err := installer.ResolveWindowsRuntimeDependency(installer.WindowsRuntimeDependencyProductGoGopls, cacheRoot)
	if err != nil || !filepath.IsAbs(result.ExecutablePath) || !isExecutableFile(result.ExecutablePath) {
		return nil
	}
	binDir, err := normalizeOptionalPath(filepath.Dir(result.ExecutablePath), "")
	if err != nil {
		return nil
	}
	return []goToolchainCandidate{{binDir: binDir, path: result.ExecutablePath}}
}

// managedWindowsLSPProductRoot 根据与生产 runtimeenv 相同的环境优先级计算产品根。
// 显式空环境不继承宿主配置；相对路径和缺失路径直接让上层继续 fail-fast。
func managedWindowsLSPProductRoot(env []string) (string, bool) {
	for _, marker := range []struct {
		key        string
		appendHome bool
	}{
		{key: managedWindowsSuperDolphinHomeEnv},
		{key: managedWindowsProjectRootEnv, appendHome: true},
		{key: managedWindowsRuntimeResourcesDirEnv, appendHome: true},
	} {
		value, ok := envValue(env, marker.key)
		value = strings.TrimSpace(value)
		if !ok || value == "" {
			continue
		}
		root := filepath.Clean(value)
		if root == "." || !filepath.IsAbs(root) {
			return "", false
		}
		if marker.appendHome {
			root = filepath.Join(root, ".super-dolphin")
		}
		return root, true
	}

	// nil/empty 请求环境必须与宿主隔离；生产启动器在拉起 sidecar 前会提供上述显式标记之一。
	if len(env) == 0 {
		return "", false
	}
	return defaultManagedWindowsLSPProductRoot()
}

// defaultManagedWindowsLSPProductRoot 仅在请求环境非空时复用 runtimeenv 的默认产品根策略。
func defaultManagedWindowsLSPProductRoot() (string, bool) {
	userCacheDir, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(userCacheDir) == "" {
		return "", false
	}
	return filepath.Join(userCacheDir, "super-agent-v3", "mcp-lsp", "language-servers"), true
}
