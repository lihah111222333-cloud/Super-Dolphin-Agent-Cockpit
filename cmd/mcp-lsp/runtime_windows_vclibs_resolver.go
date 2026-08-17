//go:build windows

package main

import (
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

var (
	runtimeWindowsVCLibsResolverMu sync.RWMutex
	// runtimeWindowsVCLibsProcessPathResolver 是 Windows 产品进程依赖的单一只读
	// 解析边界。生产值始终执行锁定 Appx/SHA/完整 ready-tree 复验；函数间接层仅让
	// 无网络单元测试注入隔离 fixture，不提供环境变量开关，也不放宽生产 ACL。
	runtimeWindowsVCLibsProcessPathResolver = installer.ResolveWindowsVCLibsDesktopAppLocalProcessPath
)

// resolveRuntimeWindowsVCLibsProcessPath 在读取稳定 resolver 快照后执行解析，避免
// 测试依赖注入与并发读取发生数据竞争；生产调用仍 fail-fast 返回原始 typed 错误链。
func resolveRuntimeWindowsVCLibsProcessPath(productRoot string) (string, error) {
	runtimeWindowsVCLibsResolverMu.RLock()
	resolver := runtimeWindowsVCLibsProcessPathResolver
	runtimeWindowsVCLibsResolverMu.RUnlock()
	return resolver(productRoot)
}
