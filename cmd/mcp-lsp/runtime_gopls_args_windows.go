//go:build windows

package main

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// runtimeServerArgsPlatform 从 durable record 读取唯一显式 daemon endpoint。
func runtimeServerArgsPlatform(command multilsp.ServerCommand, binary string, env []string, workspaceRoot ...string) ([]string, error) {
	if args, handled, err := runtimeServerWindowsRubyLSPArguments(command, binary); err != nil {
		return nil, err
	} else if handled {
		return args, nil
	}
	if strings.EqualFold(filepath.Base(filepath.Clean(binary)), "sourcekit-lsp.exe") {
		return runtimeServerWindowsSwiftLaunchArgs(binary, command.Args)
	}
	if strings.EqualFold(windowsRuntimeExecutableStem(command.Executable), "lua-language-server") &&
		strings.EqualFold(windowsRuntimeExecutableStem(binary), windowsRuntimeExecutableStem(installer.WindowsEmmyLuaBinaryName)) {
		return installer.WindowsEmmyLuaCommandArguments(), nil
	}
	if strings.EqualFold(filepath.Base(command.Executable), "kotlin-language-server") &&
		strings.EqualFold(filepath.Base(binary), "intellij-server.exe") {
		args := slices.Clone(command.Args)
		if !slices.Contains(args, "--stdio") {
			args = append(args, "--stdio")
		}
		return args, nil
	}
	if strings.EqualFold(filepath.Base(command.Executable), "jdtls") && strings.EqualFold(filepath.Base(binary), "java.exe") {
		if len(workspaceRoot) != 1 || strings.TrimSpace(workspaceRoot[0]) == "" {
			return nil, errors.New("Windows JDTLS launcher requires one workspace root")
		}
		return installer.WindowsJDTLSLaunchArguments(binary, workspaceRoot[0])
	}
	if args, handled, err := runtimeServerWindowsProductSQLSArgs(command, binary); err != nil {
		return nil, err
	} else if handled {
		return args, nil
	}
	if !runtimeServerUsesSharedGoplsDaemon(command) {
		return slices.Clone(command.Args), nil
	}
	if len(workspaceRoot) != 1 || strings.TrimSpace(workspaceRoot[0]) == "" {
		return nil, errors.New("Windows shared gopls daemon requires one workspace root")
	}
	config, err := runtimeServerGoplsRootCohortConfig(command, binary, workspaceRoot[0], env)
	if err != nil {
		return nil, err
	}
	endpoint, err := runtimeServerReadWindowsGoplsDaemonEndpoint(config)
	if err != nil {
		return nil, err
	}
	return []string{"-remote=" + endpoint.Endpoint}, nil
}

// runtimeServerWindowsProductSQLSArgs 只对 product-root identity guard 通过的 sqls.exe
// 去除 sqruff adapter 遗留的 lsp 子命令；v0.2.48 的 sqls 根 action 没有正式 lsp command。
// 外部或同名二进制保持原始参数，避免 Windows 特例改变用户自带语言服务器。
func runtimeServerWindowsProductSQLSArgs(command multilsp.ServerCommand, binary string) ([]string, bool, error) {
	if !strings.EqualFold(filepath.Base(command.Executable), "sqruff") ||
		!strings.EqualFold(filepath.Base(filepath.Clean(binary)), installer.WindowsGoSQLSBinaryName) {
		return nil, false, nil
	}
	if _, owned, err := runtimeServerWindowsOwnedProductRoot(binary); err != nil {
		return nil, false, err
	} else if !owned {
		return nil, false, nil
	}
	if !slices.Equal(command.Args, []string{"lsp"}) {
		return nil, false, errors.New("Windows product-owned SQLS requires exact adapter args [lsp]")
	}
	return []string{}, true, nil
}

func windowsRuntimeExecutableStem(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
