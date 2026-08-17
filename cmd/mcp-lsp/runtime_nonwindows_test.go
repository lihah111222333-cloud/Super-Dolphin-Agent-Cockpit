//go:build !windows

package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	lspinstaller "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// TestSetupInstallerPrefersNPMGlobalBinaryOverPNPMCommandShim 验证非 Windows
// 的 POSIX npm/pnpm fixture 解析；Windows 生产桥接由锁定 native cache 覆盖。
func TestSetupInstallerPrefersNPMGlobalBinaryOverPNPMCommandShim(t *testing.T) {
	prefix := t.TempDir()
	shadowBin := filepath.Join(prefix, "shadow-bin")
	npmPrefix := filepath.Join(prefix, "npm-prefix")
	shadowBinary := filepath.Join(shadowBin, "vscode-markdown-language-server")
	globalBinary := filepath.Join(npmPrefix, "bin", "vscode-markdown-language-server")
	writeRuntimeExecutable(t, shadowBinary, "#!/bin/sh\nexit 9\n# cmd-shim-target=/invalid/pnpm/markdown-server\n")
	writeRuntimeExecutable(t, globalBinary, "#!/bin/sh\nexit 0\n")
	writeRuntimeExecutable(t, filepath.Join(shadowBin, "npm"), "#!/bin/sh\nprintf '%s\\n' '"+npmPrefix+"'\n")
	t.Setenv("PATH", shadowBin)

	result, err := mustSetupInstaller(t).EnsureInstalledDetailed(
		lspinstaller.WithToolCallInstallCheckOnly(context.Background()),
		"markdown",
	)
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(markdown) error = %v", err)
	}
	if result.Path != globalBinary {
		t.Fatalf("EnsureInstalledDetailed(markdown).Path = %q, want npm global binary %q", result.Path, globalBinary)
	}
}

// TestRuntimeJSTSInitOptionsResolveInstalledTSServerPath 验证非 Windows npm
// symlink fixture 的 TypeScript server 路径解析。
func TestRuntimeJSTSInitOptionsResolveInstalledTSServerPath(t *testing.T) {
	binDir, typeScriptRoot := writeTypeScriptNPMFixture(t)
	t.Setenv("PATH", binDir)
	registry := multilsp.NewDefaultLanguageAdapterRegistry()
	adapter, ok := registry.AdapterForLanguage("typescript")
	if !ok {
		t.Fatal("missing typescript adapter")
	}

	serverBinary := filepath.Join(binDir, "typescript-language-server")
	initOptions := runtimeAdapterInitOptionsWithBinary(adapter, false, serverBinary)
	tsserver, ok := initOptions["tsserver"].(map[string]any)
	if !ok {
		t.Fatalf("typescript init options = %#v, want tsserver map", initOptions)
	}
	if got := runtimeStringOption(tsserver["fallbackPath"]); got != typeScriptRoot {
		t.Fatalf("tsserver fallbackPath = %q, want %q", got, typeScriptRoot)
	}
}

// TestRuntimeTypeScriptModuleRootResolvesPNPMCommandShim 验证非 Windows pnpm
// shell shim 解析到真实 TypeScript module root。
func TestRuntimeTypeScriptModuleRootResolvesPNPMCommandShim(t *testing.T) {
	prefix := t.TempDir()
	binDir := filepath.Join(prefix, "bin")
	typeScriptRoot := filepath.Join(prefix, "store", "node_modules", "typescript")
	target := filepath.Join(typeScriptRoot, "bin", "tsserver")
	writeRuntimeExecutable(t, target, "#!/bin/sh\nexit 0\n")
	writeRuntimeTestFile(t, filepath.Join(typeScriptRoot, "lib", "tsserver.js"), "fixture\n")
	writeRuntimeExecutable(t, filepath.Join(binDir, "tsserver"), "#!/bin/sh\nexit 0\n# cmd-shim-target="+target+"\n")
	t.Setenv("PATH", binDir)

	if got := runtimeTypeScriptModuleRoot(""); got != typeScriptRoot {
		t.Fatalf("runtimeTypeScriptModuleRoot() = %q, want pnpm TypeScript root %q", got, typeScriptRoot)
	}
}

// TestSetupInstallerRegistersBufProtoLanguageServer 验证非 Windows 的
// POSIX PATH fixture；Windows 生产桥接从锁定 native cache 解析 buf。
func TestSetupInstallerRegistersBufProtoLanguageServer(t *testing.T) {
	binDir := t.TempDir()
	writeMcpLSPExecutable(t, binDir, "buf")
	bufBinary := filepath.Join(binDir, mcpLSPExecutableFileName("buf"))
	t.Setenv("PATH", binDir)

	result, err := mustSetupInstaller(t).EnsureInstalledDetailed(lspinstaller.WithToolCallInstallCheckOnly(context.Background()), "proto")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(proto) error = %v", err)
	}
	if result.Binary != "buf" || result.Path != bufBinary {
		t.Fatalf("proto installer result = %#v, want buf at %q", result, bufBinary)
	}
}

// TestSetupInstallerReportsMissingBufBinaryForProto 验证非 Windows 缺少
// POSIX PATH binary 时返回 typed MissingBinaryError，而不是 unsupported。
func TestSetupInstallerReportsMissingBufBinaryForProto(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := mustSetupInstaller(t).EnsureInstalledDetailed(lspinstaller.WithToolCallInstallCheckOnly(context.Background()), "proto")
	if err == nil {
		t.Fatal("EnsureInstalledDetailed(proto) error = nil, want missing binary")
	}
	var missing *lspinstaller.MissingBinaryError
	if !errors.As(err, &missing) {
		t.Fatalf("EnsureInstalledDetailed(proto) error = %T %v, want MissingBinaryError", err, err)
	}
	if languageID, binaryName := missing.MissingLSPBinary(); languageID != "proto" || binaryName != "buf" {
		t.Fatalf("missing proto binary = (%q, %q), want (proto, buf)", languageID, binaryName)
	}
	if errors.Is(err, lspmanager.ErrUnsupportedLanguage) {
		t.Fatalf("missing proto binary error was classified as unsupported language: %v", err)
	}
}
