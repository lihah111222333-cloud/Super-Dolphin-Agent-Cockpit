//go:build windows

package main

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// runtimeServerProductProfile 仅为 resolver 已验证的 product-owned JDTLS cohort 传播精确 profile。
// language id、命令名或任意同名 java.exe 单独均不足以触发能力禁用。
func runtimeServerProductProfile(adapter multilsp.LanguageAdapter, command multilsp.ServerCommand, binary string, args []string) string {
	p := runtimeServerProductProfilePredicates(adapter, command, binary, args)
	if !p.All() {
		return ""
	}
	return multilsp.ServerProfileJDTLS160
}

// runtimeServerProductProfilePredicates 把 JDTLS profile 的每个生产事实分开记录，
// 便于诊断精确 profile 未应用；任何一个事实缺失都 fail-closed，不按模糊名称猜测。
type runtimeServerProductProfilePredicateSet struct {
	ProductOwned          bool
	ProductIDExact        bool
	VersionExact          bool
	AdapterExact          bool
	ExecutableJava        bool
	LauncherArgPresent    bool
	ConfigurationPresent  bool
	DataPresent           bool
	CohortReceiptVerified bool
	ArchExact             bool
}

func (p runtimeServerProductProfilePredicateSet) All() bool {
	return p.ProductOwned && p.ProductIDExact && p.VersionExact && p.AdapterExact &&
		p.ExecutableJava && p.LauncherArgPresent && p.ConfigurationPresent && p.DataPresent &&
		p.CohortReceiptVerified && p.ArchExact
}

func runtimeServerProductProfilePredicates(adapter multilsp.LanguageAdapter, command multilsp.ServerCommand, binary string, args []string) runtimeServerProductProfilePredicateSet {
	p := runtimeServerProductProfilePredicateSet{
		AdapterExact:   adapter != nil && slices.Equal(adapter.LanguageIDs(), []string{"java"}),
		ExecutableJava: strings.EqualFold(filepath.Base(binary), "java.exe"),
	}
	if !strings.EqualFold(filepath.Base(command.Executable), "jdtls") {
		return p
	}
	productRoot, owned, err := runtimeServerWindowsOwnedProductRoot(binary)
	p.ProductOwned = err == nil && owned
	if !p.ProductOwned {
		return p
	}
	resolved, resolveErr := installer.ResolveWindowsRuntimeDependency(
		installer.WindowsRuntimeDependencyProductJDKJDTLS,
		windowsRuntimeDependencyCacheRoot(productRoot),
	)
	p.ProductIDExact = resolveErr == nil && resolved.Product == installer.WindowsRuntimeDependencyProductJDKJDTLS
	p.VersionExact = resolveErr == nil && strings.Contains(strings.ToLower(resolved.Cohort), "jdtls-1.60.0") && filepath.Clean(resolved.ExecutablePath) == filepath.Clean(binary)
	p.CohortReceiptVerified = resolveErr == nil && resolved.Architecture != "" && resolved.Platform.NativeArch == resolved.Architecture && filepath.Clean(resolved.ExecutablePath) == filepath.Clean(binary)
	p.ArchExact = p.CohortReceiptVerified
	p.LauncherArgPresent = resolveErr == nil && slices.ContainsFunc(args, func(arg string) bool {
		return filepath.Clean(arg) == filepath.Clean(resolved.ServerPath)
	})
	p.ConfigurationPresent = slices.Contains(args, "-configuration") && slices.ContainsFunc(args, func(arg string) bool {
		return strings.EqualFold(filepath.Base(filepath.Clean(arg)), "config_win")
	})
	for i, arg := range args {
		if arg == "-data" && i+1 < len(args) && strings.TrimSpace(args[i+1]) != "" {
			p.DataPresent = true
			break
		}
	}
	return p
}
