//go:build windows

package main

import (
	"slices"

	catalog "github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

// runnerPlatformWorkload 构造 Windows 本地 source 视图，不声明 producer 或远程结果权威。
func runnerPlatformWorkload(workload catalog.Workload) catalog.Workload {
	view := cloneRunnerWorkload(workload)
	switch view.ID {
	case "mcp-lsp-idle-quick":
		view.TimeoutSeconds = 120
		view.Command = windowsIdleQuickHostCommand()
		if !slices.Contains(view.Platforms, "windows") {
			view.Platforms = append(view.Platforms, "windows")
		}
	case "mcp-lsp-default-15m":
		view.ImplementationStatus = "implemented"
		view.RunnerTarget = "local-go-test"
		view.Command = windowsDefault15mSourceCommand()
	}
	return view
}

// windowsIdleQuickHostCommand 返回唯一宿主 light 入口的有界精确 selector。
func windowsIdleQuickHostCommand() []string {
	return []string{
		"bash", "./scripts/test_with_guard.sh", "--host-test", "light",
		"./cmd/mcp-lsp/internal/hiddenexec",
		"-run", "^TestProcessTreeProvidesIdentitySnapshotAndBoundedLifecycle$",
		"-timeout=60s", "-count=1",
	}
}

// windowsDefault15mSourceCommand 返回当前共享 daemon 生命周期的本地 source selector。
func windowsDefault15mSourceCommand() []string {
	return []string{
		"go", "test", "./cmd/mcp-lsp", "-tags=e2e",
		"-run", "^TestMcpLSPBinaryWindowsGoplsDefault15mIdleReclaimsSharedDaemonE2E$", "-count=1",
	}
}
