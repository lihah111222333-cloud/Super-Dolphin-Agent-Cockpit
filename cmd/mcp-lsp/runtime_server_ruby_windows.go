//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// runtimeServerWindowsRubyLSPArguments 只为产品根内、身份与 RubyLSP resolver
// 完全一致的 ruby.exe 注入固定 -I 与脚本参数；外部 Ruby 保持原参数不变。
func runtimeServerWindowsRubyLSPArguments(command multilsp.ServerCommand, binary string) ([]string, bool, error) {
	if !strings.EqualFold(filepath.Base(filepath.Clean(binary)), "ruby.exe") {
		return nil, false, nil
	}
	productRoot, owned, err := runtimeServerWindowsOwnedProductRoot(binary)
	if err != nil {
		return nil, false, err
	}
	if !owned {
		return nil, false, nil
	}
	resolved, err := installer.ResolveWindowsRuntimeDependency(installer.WindowsRuntimeDependencyProductRubyLSP, windowsRuntimeDependencyCacheRoot(productRoot))
	if err != nil {
		return nil, false, fmt.Errorf("resolve product-owned Ruby LSP cohort: %w", err)
	}
	if !strings.EqualFold(filepath.Clean(resolved.ExecutablePath), filepath.Clean(binary)) {
		return nil, false, fmt.Errorf("product-owned Ruby runtime identity changed: resolved=%s actual=%s", resolved.ExecutablePath, binary)
	}
	args, err := installer.WindowsRubyLSPProcessLaunchArguments(resolved.RootPath)
	if err != nil {
		return nil, false, err
	}
	_ = command
	return args, true, nil
}

// runtimeServerWindowsRubyLSPEnvironment 为产品 Ruby 子进程覆盖 GEM_HOME、GEM_PATH、
// Bundler 和用户目录；环境只来自同一 ready cohort，不能依赖系统 Ruby 或用户配置。
func runtimeServerWindowsRubyLSPEnvironment(serverBinary string, env []string) ([]string, error) {
	if !strings.EqualFold(filepath.Base(filepath.Clean(serverBinary)), "ruby.exe") {
		return append([]string(nil), env...), nil
	}
	productRoot, owned, err := runtimeServerWindowsOwnedProductRoot(serverBinary)
	if err != nil {
		return nil, err
	}
	if !owned {
		return append([]string(nil), env...), nil
	}
	resolved, err := installer.ResolveWindowsRuntimeDependency(installer.WindowsRuntimeDependencyProductRubyLSP, windowsRuntimeDependencyCacheRoot(productRoot))
	if err != nil {
		return nil, fmt.Errorf("resolve product-owned Ruby LSP environment: %w", err)
	}
	if !strings.EqualFold(filepath.Clean(resolved.ExecutablePath), filepath.Clean(serverBinary)) {
		return nil, fmt.Errorf("product-owned Ruby runtime environment identity changed: resolved=%s actual=%s", resolved.ExecutablePath, serverBinary)
	}
	stateRoot := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_SIDECAR_RUNTIME_DIR"))
	var processEnvironment []string
	if stateRoot != "" {
		processEnvironment, err = installer.WindowsRubyLSPProcessEnvironmentWithState(resolved.RootPath, filepath.Join(stateRoot, "ruby-lsp"))
	} else {
		processEnvironment, err = installer.WindowsRubyLSPProcessEnvironment(resolved.RootPath)
	}
	if err != nil {
		return nil, fmt.Errorf("resolve short product-owned Ruby LSP environment: %w", err)
	}
	for _, item := range processEnvironment {
		key, value, ok := strings.Cut(item, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("Ruby LSP environment contains malformed key")
		}
		// 安装边界用空值表示从继承环境删除 RubyGems/Bundler 注入项；不能把
		// 这些标记原样传给子进程，否则用户配置可能污染 product-owned Ruby。
		if value == "" && (strings.EqualFold(key, "RUBYGEMS_GEMDEPS") || strings.EqualFold(key, "RUBYOPT") || strings.EqualFold(key, "RUBYLIB")) {
			env = removeRuntimeServerWindowsEnvironment(env, key)
			continue
		}
		env = replaceRuntimeServerWindowsEnvironment(env, key, value)
	}
	return env, nil
}

// removeRuntimeServerWindowsEnvironment 从继承环境中删除一个不区分大小写的变量。
// RubyGems/Bundler 的受控 unset 变量不能以空值继续传递，否则会保留父进程配置语义。
func removeRuntimeServerWindowsEnvironment(env []string, key string) []string {
	result := make([]string, 0, len(env))
	for _, item := range env {
		name, _, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(name, key) {
			continue
		}
		result = append(result, item)
	}
	return result
}
