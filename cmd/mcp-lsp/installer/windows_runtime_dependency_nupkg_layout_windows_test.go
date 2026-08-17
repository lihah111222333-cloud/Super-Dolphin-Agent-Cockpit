//go:build windows

package installer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWindowsRuntimeDependencyNupkgKeepsLocalSourceExtensionAndIsolatesInspection
// 锁定 csharp-ls 的两层目录语义：固定包必须保留 .nupkg 供 dotnet 本地源消费，
// 包内 tools/net*/any 只能展开到私有检查目录，不能提前占用最终 tool-path。
func TestWindowsRuntimeDependencyNupkgKeepsLocalSourceExtensionAndIsolatesInspection(t *testing.T) {
	stage := t.TempDir()
	asset := WindowsRuntimeDependencyAsset{
		Component: "csharp-ls", Architecture: WindowsHostArchARM64, Version: "0.26.0",
		URL:               "https://example.invalid/csharp-ls.0.26.0.nupkg",
		ChecksumAlgorithm: WindowsRuntimeDependencyChecksumSHA256,
		Checksum:          strings.Repeat("0", 64),
		Format:            WindowsRuntimeDependencyAssetFormatNupkg,
		ArchivePath:       "tools/net10.0/any/DotnetToolSettings.xml",
		BinaryPath:        "tools/net10.0/any/CSharpLanguageServer.dll",
	}
	fetch := func(_ context.Context, _ WindowsRuntimeDependencyAsset, destination string) error {
		if filepath.Ext(destination) != ".nupkg" {
			t.Fatalf("NuGet local-source payload extension=%q, want .nupkg", filepath.Ext(destination))
		}
		return writeWindowsRuntimeDependencyTestZip(destination, map[string]string{
			"tools/net10.0/any/DotnetToolSettings.xml":   `<DotNetCliTool Version="1"><Commands><Command Name="csharp-ls" EntryPoint="CSharpLanguageServer.dll" Runner="dotnet" /></Commands></DotNetCliTool>`,
			"tools/net10.0/any/CSharpLanguageServer.dll": "managed-entrypoint",
		})
	}
	payload, err := materializeWindowsRuntimeDependencyAsset(context.Background(), stage, asset, fetch)
	if err != nil {
		t.Fatalf("materialize fixed csharp-ls nupkg: %v", err)
	}
	if filepath.Ext(payload) != ".nupkg" {
		t.Fatalf("materialized csharp-ls payload=%q, want .nupkg", payload)
	}
	if filepath.Base(payload) != "csharp-ls.0.26.0.nupkg" {
		t.Fatalf("materialized csharp-ls payload basename=%q, want NuGet package-id.version naming", filepath.Base(payload))
	}
	inspection := filepath.Join(stage, ".runtime-assets", "csharp-ls", "expanded", filepath.FromSlash(asset.ArchivePath))
	if info, statErr := os.Stat(inspection); statErr != nil || !info.Mode().IsRegular() {
		t.Fatalf("private csharp-ls inspection entry=%q: info=%#v err=%v", inspection, info, statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(stage, filepath.FromSlash(asset.ArchivePath))); !os.IsNotExist(statErr) {
		t.Fatalf("csharp-ls nupkg occupied final tool-path before dotnet install; stat error=%v", statErr)
	}
}
