//go:build !windows

package main

import "context"

// runtimePlatformSemanticLSPToolsAvailable 保持非 Windows 的既有契约：语义工具
// 只由显式 bundle 或 PATH 服务器启用，不读取也不启用 Windows 自动安装策略。
func runtimePlatformSemanticLSPToolsAvailable(context.Context) (bool, error) {
	return false, nil
}
