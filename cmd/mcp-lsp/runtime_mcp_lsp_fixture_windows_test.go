//go:build windows

package main

import (
	"path/filepath"
	"strings"
)

// mcpLSPExecutableFixtureBody 提供 Windows cmd fixture；公共写入流程保持平台中性。
func mcpLSPExecutableFixtureBody() string {
	return "@echo off\r\nexit /b 0\r\n"
}

// mcpLSPExecutableFileName 仅在 Windows 为无扩展名 fixture 补 cmd 后缀。
func mcpLSPExecutableFileName(name string) string {
	if filepath.Ext(name) == "" {
		return name + ".cmd"
	}
	return name
}

// normalizeMcpLSPBundleExecutablePaths 将 Windows manifest 中的可执行路径
// 映射到 cmd fixture；JSON 解析和摘要仍由公共 helper 统一完成。
func normalizeMcpLSPBundleExecutablePaths(body string) string {
	for _, path := range []string{
		"bin/gopls", "bin/clangd",
		"bin/typescript-language-server",
		"node_modules/.bin/typescript-language-server",
		"bin/vscode-css-language-server",
		"bin/pyright-langserver",
		"bin/rust-analyzer",
		"bin/bash-language-server",
		"bin/sqruff",
	} {
		body = strings.ReplaceAll(body, `"`+path+`"`, `"`+path+`.cmd"`)
	}
	return body
}
