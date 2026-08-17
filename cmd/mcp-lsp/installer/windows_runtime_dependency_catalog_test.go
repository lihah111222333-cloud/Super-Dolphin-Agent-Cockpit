//go:build windows

package installer

import (
	"errors"
	"strings"
	"testing"
)

func TestWindowsRuntimeDependencyCatalogIsComplete(t *testing.T) {
	if err := ValidateWindowsRuntimeDependencyCatalog(); err != nil {
		t.Fatalf("validate runtime dependency catalog: %v", err)
	}
	wantLanguages := map[string]WindowsRuntimeDependencyProduct{
		"go":     WindowsRuntimeDependencyProductGoGopls,
		"gomod":  WindowsRuntimeDependencyProductGoGopls,
		"gosum":  WindowsRuntimeDependencyProductGoGopls,
		"gowork": WindowsRuntimeDependencyProductGoGopls,
		"csharp": WindowsRuntimeDependencyProductDotnetCsharpLS,
		"java":   WindowsRuntimeDependencyProductJDKJDTLS,
		"ruby":   WindowsRuntimeDependencyProductRubyLSP,
		"swift":  WindowsRuntimeDependencyProductSwiftSourceKitLS,
		"sql":    WindowsRuntimeDependencyProductGoSQLS,
	}
	for language, wantProduct := range wantLanguages {
		entry, err := WindowsRuntimeDependencyCatalogEntryForLanguage(language)
		if err != nil {
			t.Fatalf("language %q: %v", language, err)
		}
		if entry.Product != wantProduct {
			t.Fatalf("language %q resolved to %q, want %q", language, entry.Product, wantProduct)
		}
	}
	if _, err := WindowsRuntimeDependencyCatalogEntryForLanguage("typescript"); !errors.Is(err, ErrUnknownWindowsRuntimeDependencyLanguage) {
		t.Fatalf("unknown language error = %v, want ErrUnknownWindowsRuntimeDependencyLanguage", err)
	}
	wantCommands := map[WindowsRuntimeDependencyProduct]struct {
		command string
		args    []string
	}{
		WindowsRuntimeDependencyProductGoGopls:          {"go install", []string{"install", "golang.org/x/tools/gopls@v0.23.0"}},
		WindowsRuntimeDependencyProductDotnetCsharpLS:   {"dotnet tool install", []string{"tool", "install", "--tool-path", "tools", "--version", "0.26.0", "csharp-ls"}},
		WindowsRuntimeDependencyProductJDKJDTLS:         {"java", []string{"-Declipse.application=org.eclipse.jdt.ls.core.id1", "-Dosgi.bundles.defaultStartLevel=4", "-Declipse.product=org.eclipse.jdt.ls.core.product", "-Dlog.protocol=true", "-Dlog.level=ALL", "-jar", "plugins/org.eclipse.equinox.launcher_1.7.200.v20260619-2039.jar", "-configuration", "config_win"}},
		WindowsRuntimeDependencyProductRubySolargraph:   {"gem install --local", []string{"install", "--local", "--install-dir", "gems", "--bindir", "bin", "--no-document"}},
		WindowsRuntimeDependencyProductRubyLSP:          {"RubyInstaller 4.0.5-1 ARM64 + Ruby LSP 0.26.10 offline gem install", []string{"install", "--local", "--ignore-dependencies", "--install-dir", "gems", "--no-document"}},
		WindowsRuntimeDependencyProductSwiftSourceKitLS: {"Swift 6.3.3 embedded MSI/CAB extraction", []string{"bld.asserts.msi", "cli.asserts.msi", "ide.asserts.msi", "rtl.msi", "windows.msi", "ide.asserts.cab", "windows.cab", "a22", "a23", "a24", "a25", "a26", "a27", "a28"}},
		WindowsRuntimeDependencyProductGoSQLS:           {"Go 1.26.5 source build", []string{"build", "-trimpath", "-mod=readonly", "-o", "bin/sqls.exe", "./"}},
	}
	for product, expected := range wantCommands {
		entry, err := WindowsRuntimeDependencyCatalogEntryForProduct(product)
		if err != nil {
			t.Fatal(err)
		}
		if entry.Install.Command != expected.command || strings.Join(entry.Install.Args, "\x00") != strings.Join(expected.args, "\x00") {
			t.Fatalf("%q install command drifted: command=%q args=%q", product, entry.Install.Command, entry.Install.Args)
		}
		if strings.Contains(strings.ToLower(strings.Join(entry.Install.Args, " ")), "latest") {
			t.Fatalf("%q install command uses latest", product)
		}
	}
}

func TestWindowsRuntimeDependencyCatalogArchitectureVerdicts(t *testing.T) {
	want := map[WindowsRuntimeDependencyProduct]map[string]WindowsRuntimeDependencyCatalogStatus{
		WindowsRuntimeDependencyProductGoGopls: {
			WindowsHostArchARM64: WindowsRuntimeDependencyStatusInstallable,
			WindowsHostArchX64:   WindowsRuntimeDependencyStatusInstallable,
			WindowsHostArchX86:   WindowsRuntimeDependencyStatusInstallable,
		},
		WindowsRuntimeDependencyProductDotnetCsharpLS: {
			WindowsHostArchARM64: WindowsRuntimeDependencyStatusInstallable,
			WindowsHostArchX64:   WindowsRuntimeDependencyStatusInstallable,
			WindowsHostArchX86:   WindowsRuntimeDependencyStatusInstallable,
		},
		WindowsRuntimeDependencyProductJDKJDTLS: {
			WindowsHostArchARM64: WindowsRuntimeDependencyStatusInstallable,
			WindowsHostArchX64:   WindowsRuntimeDependencyStatusInstallable,
			WindowsHostArchX86:   WindowsRuntimeDependencyStatusTypedUnsupported,
		},
		WindowsRuntimeDependencyProductRubySolargraph: {
			WindowsHostArchARM64: WindowsRuntimeDependencyStatusEvidenceGap,
			WindowsHostArchX64:   WindowsRuntimeDependencyStatusEvidenceGap,
			WindowsHostArchX86:   WindowsRuntimeDependencyStatusEvidenceGap,
		},
		WindowsRuntimeDependencyProductRubyLSP: {
			WindowsHostArchARM64: WindowsRuntimeDependencyStatusInstallable,
			WindowsHostArchX64:   WindowsRuntimeDependencyStatusTypedUnsupported,
			WindowsHostArchX86:   WindowsRuntimeDependencyStatusTypedUnsupported,
		},
		WindowsRuntimeDependencyProductSwiftSourceKitLS: {
			WindowsHostArchARM64: WindowsRuntimeDependencyStatusInstallable,
			WindowsHostArchX64:   WindowsRuntimeDependencyStatusEvidenceGap,
			WindowsHostArchX86:   WindowsRuntimeDependencyStatusTypedUnsupported,
		},
		WindowsRuntimeDependencyProductGoSQLS: {
			WindowsHostArchARM64: WindowsRuntimeDependencyStatusInstallable,
			WindowsHostArchX64:   WindowsRuntimeDependencyStatusInstallable,
			WindowsHostArchX86:   WindowsRuntimeDependencyStatusInstallable,
		},
	}
	for product, byArchitecture := range want {
		entry, err := WindowsRuntimeDependencyCatalogEntryForProduct(product)
		if err != nil {
			t.Fatalf("product %q: %v", product, err)
		}
		for architecture, wantStatus := range byArchitecture {
			status, err := WindowsRuntimeDependencyStatusForArchitecture(product, architecture)
			if err != nil {
				t.Fatalf("status %q/%q: %v", product, architecture, err)
			}
			if status != wantStatus {
				t.Fatalf("status %q/%q = %q, want %q", product, architecture, status, wantStatus)
			}
			assets, planErr := WindowsRuntimeDependencyAssetsForArchitecture(product, architecture)
			switch wantStatus {
			case WindowsRuntimeDependencyStatusInstallable:
				if planErr != nil {
					t.Fatalf("installable plan %q/%q: %v", product, architecture, planErr)
				}
			case WindowsRuntimeDependencyStatusTypedUnsupported:
				var typed *WindowsRuntimeDependencyUnsupportedError
				if !errors.As(planErr, &typed) {
					t.Fatalf("unsupported plan %q/%q error = %v, want typed unsupported", product, architecture, planErr)
				}
			case WindowsRuntimeDependencyStatusEvidenceGap:
				var typed *WindowsRuntimeDependencyEvidenceGapError
				if !errors.As(planErr, &typed) {
					t.Fatalf("evidence-gap plan %q/%q error = %v, want typed evidence gap", product, architecture, planErr)
				}
			}
			if wantStatus == WindowsRuntimeDependencyStatusInstallable && len(assets) == 0 {
				t.Fatalf("installable plan %q/%q has no assets", product, architecture)
			}
			if entry.StatusByArchitecture[architecture] != wantStatus {
				t.Fatalf("entry status %q/%q drifted", product, architecture)
			}
		}
	}
	if _, err := WindowsRuntimeDependencyPlanForArchitecture(WindowsRuntimeDependencyProductGoGopls, "mips64"); !errors.Is(err, ErrWindowsRuntimeDependencyUnsupported) {
		t.Fatalf("unknown architecture error = %v, want ErrWindowsRuntimeDependencyUnsupported", err)
	}
}

func TestWindowsRuntimeDependencyCatalogOfficialPins(t *testing.T) {
	type expectedAsset struct {
		product      WindowsRuntimeDependencyProduct
		architecture string
		component    string
		version      string
		url          string
		checksum     string
		format       WindowsRuntimeDependencyAssetFormat
		binaryPath   string
	}
	want := []expectedAsset{
		{WindowsRuntimeDependencyProductGoGopls, WindowsHostArchARM64, "go", "1.26.5", "https://go.dev/dl/go1.26.5.windows-arm64.zip", "f96ee46396d69f1e231c8d981ec6a70216238a646a1f2cd74aea0d0016bbc017", WindowsRuntimeDependencyAssetFormatZIP, "go/bin/go.exe"},
		{WindowsRuntimeDependencyProductGoGopls, WindowsHostArchX64, "go", "1.26.5", "https://go.dev/dl/go1.26.5.windows-amd64.zip", "97e6b2a833b6d89f9ff17d25419ac0a7e3b482a044e9ab18cdef834bd834fd38", WindowsRuntimeDependencyAssetFormatZIP, "go/bin/go.exe"},
		{WindowsRuntimeDependencyProductGoGopls, WindowsHostArchX86, "go", "1.26.5", "https://go.dev/dl/go1.26.5.windows-386.zip", "cab0f6847c17f4c904c0bacb6ec6b84e730fc797f4ba885f42383d580fc2d399", WindowsRuntimeDependencyAssetFormatZIP, "go/bin/go.exe"},
		{WindowsRuntimeDependencyProductGoSQLS, WindowsHostArchARM64, "go", "1.26.5", "https://go.dev/dl/go1.26.5.windows-arm64.zip", "f96ee46396d69f1e231c8d981ec6a70216238a646a1f2cd74aea0d0016bbc017", WindowsRuntimeDependencyAssetFormatZIP, "go/bin/go.exe"},
		{WindowsRuntimeDependencyProductGoSQLS, WindowsHostArchX64, "go", "1.26.5", "https://go.dev/dl/go1.26.5.windows-amd64.zip", "97e6b2a833b6d89f9ff17d25419ac0a7e3b482a044e9ab18cdef834bd834fd38", WindowsRuntimeDependencyAssetFormatZIP, "go/bin/go.exe"},
		{WindowsRuntimeDependencyProductGoSQLS, WindowsHostArchX86, "go", "1.26.5", "https://go.dev/dl/go1.26.5.windows-386.zip", "cab0f6847c17f4c904c0bacb6ec6b84e730fc797f4ba885f42383d580fc2d399", WindowsRuntimeDependencyAssetFormatZIP, "go/bin/go.exe"},
		{WindowsRuntimeDependencyProductGoSQLS, WindowsHostArchARM64, "sqls-source", "0.2.48", WindowsGoSQLSModuleZipURL, WindowsGoSQLSModuleZipSHA256, WindowsRuntimeDependencyAssetFormatZIP, "github.com/sqls-server/sqls@v0.2.48/go.mod"},
		{WindowsRuntimeDependencyProductGoSQLS, WindowsHostArchX64, "sqls-source", "0.2.48", WindowsGoSQLSModuleZipURL, WindowsGoSQLSModuleZipSHA256, WindowsRuntimeDependencyAssetFormatZIP, "github.com/sqls-server/sqls@v0.2.48/go.mod"},
		{WindowsRuntimeDependencyProductGoSQLS, WindowsHostArchX86, "sqls-source", "0.2.48", WindowsGoSQLSModuleZipURL, WindowsGoSQLSModuleZipSHA256, WindowsRuntimeDependencyAssetFormatZIP, "github.com/sqls-server/sqls@v0.2.48/go.mod"},
		{WindowsRuntimeDependencyProductDotnetCsharpLS, WindowsHostArchARM64, "dotnet-sdk", "10.0.400", "https://builds.dotnet.microsoft.com/dotnet/Sdk/10.0.400/dotnet-sdk-10.0.400-win-arm64.zip", "9d4ecd7439f15c7797d6f46d368cb7aa6513755c5fc3d6de7621bc4878a1805f6b8ffb60ffb9d3e72a049cca87edb252f7c8c03023b643e333544c4606509d7f", WindowsRuntimeDependencyAssetFormatZIP, "dotnet.exe"},
		{WindowsRuntimeDependencyProductDotnetCsharpLS, WindowsHostArchX64, "dotnet-sdk", "10.0.400", "https://builds.dotnet.microsoft.com/dotnet/Sdk/10.0.400/dotnet-sdk-10.0.400-win-x64.zip", "9b8b88590e4da131bfd0da7aa089d0fc04d5418d5f8607ec13d55dc5a17b4399afd54d496c12657fa05c6c6546dc5eab930f26ac6c50f2d3a7712c0fb378c366", WindowsRuntimeDependencyAssetFormatZIP, "dotnet.exe"},
		{WindowsRuntimeDependencyProductDotnetCsharpLS, WindowsHostArchX86, "dotnet-sdk", "10.0.400", "https://builds.dotnet.microsoft.com/dotnet/Sdk/10.0.400/dotnet-sdk-10.0.400-win-x86.zip", "d24d81e1fc5a5a0afa3dedad0ba3e44b0d1a6e512399ccd2dbf923d6aca3be28867870d615569ac0b06c32da2a54b27cd86a4ca0cc6ca17c3e1ad2c7f83b82d3", WindowsRuntimeDependencyAssetFormatZIP, "dotnet.exe"},
		{WindowsRuntimeDependencyProductDotnetCsharpLS, WindowsHostArchX64, "csharp-ls", "0.26.0", "https://api.nuget.org/v3-flatcontainer/csharp-ls/0.26.0/csharp-ls.0.26.0.nupkg", "2b03987aef07bb708bfe56a7bfb370364c7c8203e69aa677a37594bbe21a15b0", WindowsRuntimeDependencyAssetFormatNupkg, "tools/net10.0/any/CSharpLanguageServer.dll"},
		{WindowsRuntimeDependencyProductJDKJDTLS, WindowsHostArchX64, "jdk", "21.0.12", "https://aka.ms/download-jdk/microsoft-jdk-21.0.12-windows-x64.zip", "bf27a5d6298c736af8daf5b8c883098e83291446e5766118d8a5ea6a2617195d", WindowsRuntimeDependencyAssetFormatZIP, "jdk-21.0.12+8/bin/java.exe"},
		{WindowsRuntimeDependencyProductJDKJDTLS, WindowsHostArchX64, "jdtls", "1.60.0", "https://download.eclipse.org/jdtls/milestones/1.60.0/jdt-language-server-1.60.0-202606262232.tar.gz", "e94c303d8198f977930803582738771fd18c52c5492878410bf222b1aa81ef1d", WindowsRuntimeDependencyAssetFormatTarGz, "plugins/org.eclipse.equinox.launcher_1.7.200.v20260619-2039.jar"},
		{WindowsRuntimeDependencyProductJDKJDTLS, WindowsHostArchARM64, "jdk", "21.0.12", "https://aka.ms/download-jdk/microsoft-jdk-21.0.12-windows-aarch64.zip", "2118bb60b19002a0bcc420267518352f10d2be25ce1c79c51701b87b209bbc2a", WindowsRuntimeDependencyAssetFormatZIP, "jdk-21.0.12+8/bin/java.exe"},
		{WindowsRuntimeDependencyProductJDKJDTLS, WindowsHostArchARM64, "jdtls", "1.60.0", "https://download.eclipse.org/jdtls/milestones/1.60.0/jdt-language-server-1.60.0-202606262232.tar.gz", "e94c303d8198f977930803582738771fd18c52c5492878410bf222b1aa81ef1d", WindowsRuntimeDependencyAssetFormatTarGz, "plugins/org.eclipse.equinox.launcher_1.7.200.v20260619-2039.jar"},
		{WindowsRuntimeDependencyProductRubySolargraph, WindowsHostArchARM64, "ruby", "4.0.5-1", "https://github.com/oneclick/rubyinstaller2/releases/download/RubyInstaller-4.0.5-1/rubyinstaller-4.0.5-1-arm.7z", "c7c6bcd0b070bf7c2e0c03e70fb9754d022b8a216ebc4befab880874c6180b51", WindowsRuntimeDependencyAssetFormatSevenZip, "rubyinstaller-4.0.5-1-arm/bin/ruby.exe"},
		{WindowsRuntimeDependencyProductRubySolargraph, WindowsHostArchX64, "ruby", "4.0.5-1", "https://github.com/oneclick/rubyinstaller2/releases/download/RubyInstaller-4.0.5-1/rubyinstaller-4.0.5-1-x64.7z", "74e31613fc71e6e23431dfc4d8b6ec2818a4dc1fd16e0983b074144c16719c8b", WindowsRuntimeDependencyAssetFormatSevenZip, "rubyinstaller-4.0.5-1-x64/bin/ruby.exe"},
		{WindowsRuntimeDependencyProductRubySolargraph, WindowsHostArchX86, "ruby", "3.4.10-1", "https://github.com/oneclick/rubyinstaller2/releases/download/RubyInstaller-3.4.10-1/rubyinstaller-3.4.10-1-x86.7z", "be323ac7b8342de16edcceb1ee04a90023c39aa7e7a544e628c6360fffb602da", WindowsRuntimeDependencyAssetFormatSevenZip, "rubyinstaller-3.4.10-1-x86/bin/ruby.exe"},
		{WindowsRuntimeDependencyProductRubySolargraph, WindowsHostArchARM64, "solargraph", "0.60.2", "https://rubygems.org/gems/solargraph-0.60.2.gem", "35c8fb31fcdbe8ccd0e0e84862a65b8deb319f86210c5966e41e2fc011e52538", WindowsRuntimeDependencyAssetFormatGem, "bin/solargraph"},
		{WindowsRuntimeDependencyProductRubyLSP, WindowsHostArchARM64, "ruby", "4.0.5-1", "https://github.com/oneclick/rubyinstaller2/releases/download/RubyInstaller-4.0.5-1/rubyinstaller-4.0.5-1-arm.7z", "c7c6bcd0b070bf7c2e0c03e70fb9754d022b8a216ebc4befab880874c6180b51", WindowsRuntimeDependencyAssetFormatSevenZip, "rubyinstaller-4.0.5-1-arm/bin/ruby.exe"},
		{WindowsRuntimeDependencyProductRubyLSP, WindowsHostArchARM64, "ruby-lsp", "0.26.10", "https://rubygems.org/gems/ruby-lsp-0.26.10.gem", "e67284af94423531f6b9a583350596421b5a6a4dd93083f1c2ba03da7c23bbed", WindowsRuntimeDependencyAssetFormatGem, "gems/ruby-lsp-0.26.10/exe/ruby-lsp"},
		{WindowsRuntimeDependencyProductRubyLSP, WindowsHostArchARM64, "language-server-protocol", "3.17.0.0", "https://rubygems.org/gems/language_server-protocol-3.17.0.0.gem", "eaf5cac33c5f0cc7fff7f1192165c93b0bfee757fd2c81e2f071a3b2afbe9c54", WindowsRuntimeDependencyAssetFormatGem, "gems/language_server-protocol-3.17.0.0/lib/language_server/protocol.rb"},
		{WindowsRuntimeDependencyProductSwiftSourceKitLS, WindowsHostArchX64, "swift-toolchain", "6.3.3", "https://download.swift.org/swift-6.3.3-release/windows10/swift-6.3.3-RELEASE/swift-6.3.3-RELEASE-windows10.exe", "235626548f249cd516d3d4d90eee980dccad46f3822dac1f8e3119b0fede94b7", WindowsRuntimeDependencyAssetFormatEXE, swiftSourceKitLSPServerPath},
		{WindowsRuntimeDependencyProductSwiftSourceKitLS, WindowsHostArchARM64, "swift-toolchain", "6.3.3", "https://download.swift.org/swift-6.3.3-release/windows10-arm64/swift-6.3.3-RELEASE/swift-6.3.3-RELEASE-windows10-arm64.exe", "09e39c60f0b05d00fbe5f55b2d344752ccbc86e64802a2d896c0d55bc51e243d", WindowsRuntimeDependencyAssetFormatEXE, swiftSourceKitLSPServerPath},
	}
	for _, expected := range want {
		entry, err := WindowsRuntimeDependencyCatalogEntryForProduct(expected.product)
		if err != nil {
			t.Fatalf("asset product %q: %v", expected.product, err)
		}
		var found *WindowsRuntimeDependencyAsset
		for index := range entry.AssetsByArchitecture[expected.architecture] {
			candidate := &entry.AssetsByArchitecture[expected.architecture][index]
			if candidate.Component == expected.component {
				found = candidate
				break
			}
		}
		if found == nil {
			t.Fatalf("missing asset %q/%q/%q", expected.product, expected.architecture, expected.component)
		}
		if found.Version != expected.version || found.URL != expected.url || found.Checksum != expected.checksum || found.Format != expected.format || found.BinaryPath != expected.binaryPath {
			t.Fatalf("asset %q/%q/%q drifted: %#v", expected.product, expected.architecture, expected.component, *found)
		}
	}
}

func TestWindowsRuntimeDependencyCatalogRejectsFallbackAndCopies(t *testing.T) {
	entries := WindowsRuntimeDependencyCatalog()
	if len(entries) == 0 {
		t.Fatal("catalog is empty")
	}
	entries[0].PrimaryLanguages[0] = "mutated"
	entries[0].AssetsByArchitecture[WindowsHostArchARM64][0].URL = "https://example.invalid/fallback"
	entries[0].Install.Args[0] = "latest"
	fresh, err := WindowsRuntimeDependencyCatalogEntryForProduct(WindowsRuntimeDependencyProductGoGopls)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.PrimaryLanguages[0] == "mutated" || strings.Contains(fresh.AssetsByArchitecture[WindowsHostArchARM64][0].URL, "example.invalid") || fresh.Install.Args[0] == "latest" {
		t.Fatal("catalog returned mutable shared state")
	}
	for _, entry := range WindowsRuntimeDependencyCatalog() {
		if strings.Contains(strings.ToLower(entry.Install.Command), "path") {
			t.Fatalf("%q uses PATH fallback in install command %q", entry.Product, entry.Install.Command)
		}
		for architecture, assets := range entry.AssetsByArchitecture {
			for _, asset := range assets {
				if strings.Contains(strings.ToLower(asset.URL), "latest") || strings.ContainsAny(asset.URL, "{}<>") {
					t.Fatalf("%q/%q has non-fixed URL %q", entry.Product, architecture, asset.URL)
				}
			}
		}
	}
}

func TestWindowsRuntimeDependencyCatalogWindowsFloors(t *testing.T) {
	dotnet, err := WindowsRuntimeDependencyCatalogEntryForProduct(WindowsRuntimeDependencyProductDotnetCsharpLS)
	if err != nil {
		t.Fatal(err)
	}
	for architecture, assets := range dotnet.AssetsByArchitecture {
		for _, asset := range assets {
			if asset.MinWindowsVersion != "10.0" || !asset.MinWindowsBuildKnown || asset.MinWindowsBuild != 14393 {
				t.Fatalf(".NET asset %q/%q has floor %q build %d known=%t", architecture, asset.Component, asset.MinWindowsVersion, asset.MinWindowsBuild, asset.MinWindowsBuildKnown)
			}
		}
	}
	goEntry, err := WindowsRuntimeDependencyCatalogEntryForProduct(WindowsRuntimeDependencyProductGoGopls)
	if err != nil {
		t.Fatal(err)
	}
	for architecture, assets := range goEntry.AssetsByArchitecture {
		for _, asset := range assets {
			if asset.MinWindowsVersion != "10.0" || asset.MinWindowsBuildKnown || asset.MinWindowsBuild != 0 {
				t.Fatalf("Go asset %q/%q invented a Windows build floor", architecture, asset.Component)
			}
		}
	}
}
