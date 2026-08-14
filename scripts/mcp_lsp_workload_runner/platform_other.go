//go:build !windows

package main

import catalog "github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"

// runnerPlatformWorkload 在非 Windows 平台返回不改变语义的独立 catalog 副本。
func runnerPlatformWorkload(workload catalog.Workload) catalog.Workload {
	return cloneRunnerWorkload(workload)
}
