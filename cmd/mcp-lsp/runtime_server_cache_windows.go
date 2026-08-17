//go:build windows

package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

// runtimeServerProductionNodeVersionResolver 在 Windows 构建中把语言服务器二进制
// 绑定到同一锁定 Node cohort；windows build tag 是唯一的平台选源边界。
func runtimeServerProductionNodeVersionResolver(serverBinary string) runtimeServerNodeVersionResolver {
	return func(overrides []string) (string, bool, error) {
		return runtimeServerWindowsNodeVersionForBinary(overrides, serverBinary)
	}
}

// runtimeServerWindowsNodeVersionForBinary 只读解析 Windows 生产 server 对应的锁定
// Node cohort；优先使用 InstallAction 发布的绝对路径，否则从同一 cache product root
// 调用 Windows resolver。它禁止 PATH、联网、建目录和 cache 写盘。
func runtimeServerWindowsNodeVersionForBinary(overrides []string, serverBinary string) (string, bool, error) {
	nodePath := runtimeServerEnvValue(overrides, runtimeServerWindowsNodeExecutableEnv)
	if strings.TrimSpace(nodePath) == "" {
		productRoot, err := runtimeServerWindowsProductRootFromBinary(serverBinary)
		if err != nil {
			return "", false, err
		}
		nodePath, err = installer.ResolveWindowsNodeRuntimeExecutablePath(productRoot)
		if err != nil {
			return "", false, fmt.Errorf("resolve Windows locked Node executable for %q: %w", serverBinary, err)
		}
	}
	if !filepath.IsAbs(nodePath) {
		return "", false, fmt.Errorf("Windows locked Node executable path must be absolute: %q", nodePath)
	}
	return runtimeServerReadNodeVersion(nodePath, "")
}

// runtimeServerWindowsProductRootFromBinary extracts the product root from a published
// Windows npm cohort path. Ambiguous/non-cache paths fail closed instead of guessing.
func runtimeServerWindowsProductRootFromBinary(serverBinary string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(serverBinary))
	if clean == "." || !filepath.IsAbs(clean) {
		return "", fmt.Errorf("Windows server binary path must be absolute: %q", serverBinary)
	}
	marker := string(filepath.Separator) + "cache" + string(filepath.Separator) + "lsp-assets" + string(filepath.Separator)
	lower := strings.ToLower(clean)
	index := strings.Index(lower, strings.ToLower(marker))
	if index <= 0 {
		return "", fmt.Errorf("Windows server binary is outside locked lsp-assets cache: %q", serverBinary)
	}
	root := filepath.Clean(clean[:index])
	if root == "." || !filepath.IsAbs(root) {
		return "", fmt.Errorf("derive Windows product root from server binary %q", serverBinary)
	}
	return root, nil
}
