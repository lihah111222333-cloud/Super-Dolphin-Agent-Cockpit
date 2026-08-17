//go:build !windows

package main

// mcpLSPExecutableFixtureBody 提供 POSIX shell fixture；公共写入流程保持平台中性。
func mcpLSPExecutableFixtureBody() string {
	return "#!/bin/sh\nexit 0\n"
}

// mcpLSPExecutableFileName 在非 Windows 保留原始 fixture 名称。
func mcpLSPExecutableFileName(name string) string {
	return name
}

// normalizeMcpLSPBundleExecutablePaths 非 Windows 不改 manifest 路径；
// 这是与 Windows .cmd 转换对应的显式 no-op，而非公共 GOOS 分支。
func normalizeMcpLSPBundleExecutablePaths(body string) string {
	return body
}
