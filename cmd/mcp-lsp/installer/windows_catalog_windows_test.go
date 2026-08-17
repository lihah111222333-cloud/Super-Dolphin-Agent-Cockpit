//go:build windows

package installer

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// TestWindowsLSPCatalogValidates 验证 Windows catalog 的产品、清单和固定资产元数据完整有效。
func TestWindowsLSPCatalogValidates(t *testing.T) {
	if err := ValidateWindowsLSPCatalog(); err != nil {
		t.Fatalf("ValidateWindowsLSPCatalog() error = %v", err)
	}
	entries := WindowsLSPCatalog()
	if len(entries) != 7 {
		t.Fatalf("WindowsLSPCatalog() returned %d entries, want 7", len(entries))
	}
	for _, entry := range entries {
		if err := entry.Manifest.Validate(); err != nil {
			t.Errorf("manifest %q validation error = %v", entry.Product, err)
		}
	}
}

// TestWindowsLSPCatalogSupportMatrixIsClosed 验证每个 Windows 产品的 ARM64、x64 和 x86 支持矩阵及类型化缺口。
func TestWindowsLSPCatalogSupportMatrixIsClosed(t *testing.T) {
	wantSupported := map[WindowsLSPProduct]map[string]bool{
		WindowsLSPProductClangd:        {WindowsHostArchARM64: true, WindowsHostArchX64: true, WindowsHostArchX86: false},
		WindowsLSPProductBuf:           {WindowsHostArchARM64: true, WindowsHostArchX64: true, WindowsHostArchX86: false},
		WindowsLSPProductKotlin:        {WindowsHostArchARM64: true, WindowsHostArchX64: true, WindowsHostArchX86: false},
		WindowsLSPProductDart:          {WindowsHostArchARM64: true, WindowsHostArchX64: true, WindowsHostArchX86: false},
		WindowsLSPProductTerraform:     {WindowsHostArchARM64: true, WindowsHostArchX64: true, WindowsHostArchX86: true},
		WindowsLSPProductRustAnalyzer:  {WindowsHostArchARM64: true, WindowsHostArchX64: true, WindowsHostArchX86: true},
		WindowsLSPProductLuaLanguageLS: {WindowsHostArchARM64: false, WindowsHostArchX64: true, WindowsHostArchX86: true},
	}
	architectures := []string{WindowsHostArchARM64, WindowsHostArchX64, WindowsHostArchX86}
	for product, matrix := range wantSupported {
		for _, architecture := range architectures {
			asset, err := WindowsLSPAssetForArchitecture(product, architecture)
			if matrix[architecture] {
				if err != nil {
					t.Errorf("product %q architecture %q error = %v, want supported", product, architecture, err)
					continue
				}
				if asset.Architecture != architecture {
					t.Errorf("product %q architecture %q selected %q", product, architecture, asset.Architecture)
				}
				continue
			}
			if err == nil {
				t.Errorf("product %q architecture %q selected %q, want typed unsupported error", product, architecture, asset.Architecture)
				continue
			}
			if !errors.Is(err, ErrWindowsUnsupportedAssetArchitecture) {
				t.Errorf("product %q architecture %q error = %v, want ErrWindowsUnsupportedAssetArchitecture", product, architecture, err)
			}
			var unsupported *WindowsUnsupportedAssetArchitectureError
			if !errors.As(err, &unsupported) {
				t.Errorf("product %q architecture %q error = %v, want WindowsUnsupportedAssetArchitectureError", product, architecture, err)
				continue
			}
			if unsupported.AssetName != string(product) || unsupported.Architecture != architecture {
				t.Errorf("typed unsupported error = %#v, want product %q architecture %q", unsupported, product, architecture)
			}
		}
	}
}

// TestWindowsLSPCatalogSelectsNativeArchitecture 验证 catalog 只按 Windows 原生架构选择资产而不跨架构回退。
func TestWindowsLSPCatalogSelectsNativeArchitecture(t *testing.T) {
	for _, entry := range WindowsLSPCatalog() {
		for _, architecture := range []string{WindowsHostArchARM64, WindowsHostArchX64, WindowsHostArchX86} {
			platform := WindowsHostPlatform{
				OS:             WindowsHostOSWindows,
				Arch:           architecture,
				NativeArch:     architecture,
				ProcessArch:    architecture,
				WindowsVersion: "10.0",
				WindowsBuild:   windowsLSPCatalogMinWindowsBuild,
			}
			asset, err := entry.Manifest.AssetForPlatform(platform)
			_, supportedErr := entry.Manifest.AssetForArchitecture(architecture)
			if supportedErr != nil {
				var unsupported *WindowsUnsupportedAssetArchitectureError
				if !errors.As(supportedErr, &unsupported) {
					t.Errorf("product %q architecture %q lookup error = %v, want typed unsupported", entry.Product, architecture, supportedErr)
				}
				if err == nil {
					t.Errorf("product %q architecture %q selected unsupported asset", entry.Product, architecture)
				}
				continue
			}
			if err != nil {
				t.Errorf("product %q architecture %q selection error = %v", entry.Product, architecture, err)
				continue
			}
			if asset.Architecture != architecture {
				t.Errorf("product %q architecture %q selected %q", entry.Product, architecture, asset.Architecture)
			}
		}
	}
}

// TestWindowsLSPCatalogWindowsVersionGate 验证低于固定 Windows build 下限时不下载并返回类型化版本错误。
func TestWindowsLSPCatalogWindowsVersionGate(t *testing.T) {
	entry, err := WindowsLSPCatalogEntryForProduct(WindowsLSPProductDart)
	if err != nil {
		t.Fatal(err)
	}
	tooOld := WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchX64, WindowsVersion: "10.0", WindowsBuild: windowsLSPCatalogMinWindowsBuild - 1}
	if _, err := entry.Manifest.AssetForPlatform(tooOld); err == nil {
		t.Fatal("AssetForPlatform() error = nil for a host below the catalog Windows build floor")
	} else {
		if !errors.Is(err, ErrWindowsUnsupportedWindowsVersion) {
			t.Errorf("AssetForPlatform() error = %v, want ErrWindowsUnsupportedWindowsVersion", err)
		}
		var versionErr *WindowsUnsupportedWindowsVersionError
		if !errors.As(err, &versionErr) {
			t.Errorf("AssetForPlatform() error = %v, want WindowsUnsupportedWindowsVersionError", err)
		} else if versionErr.RequiredBuild != windowsLSPCatalogMinWindowsBuild {
			t.Errorf("RequiredBuild = %d, want %d", versionErr.RequiredBuild, windowsLSPCatalogMinWindowsBuild)
		}
	}
	accepted := tooOld
	accepted.WindowsBuild = windowsLSPCatalogMinWindowsBuild
	if _, err := entry.Manifest.AssetForPlatform(accepted); err != nil {
		t.Fatalf("AssetForPlatform() error at catalog floor = %v", err)
	}
}

// TestWindowsLSPCatalogPrimaryLanguagesAreUnique 验证主语言 ID 唯一且能解析到唯一 Windows 产品。
func TestWindowsLSPCatalogPrimaryLanguagesAreUnique(t *testing.T) {
	seen := map[string]WindowsLSPProduct{}
	for _, entry := range WindowsLSPCatalog() {
		for _, language := range entry.PrimaryLanguages {
			language = strings.ToLower(strings.TrimSpace(language))
			if previous, ok := seen[language]; ok {
				t.Fatalf("language %q maps to both %q and %q", language, previous, entry.Product)
			}
			seen[language] = entry.Product
			resolved, err := WindowsLSPCatalogEntryForLanguage(language)
			if err != nil {
				t.Fatalf("WindowsLSPCatalogEntryForLanguage(%q) error = %v", language, err)
			}
			if resolved.Product != entry.Product {
				t.Errorf("language %q resolved to %q, want %q", language, resolved.Product, entry.Product)
			}
		}
	}
	if _, err := WindowsLSPCatalogEntryForLanguage("not-a-language"); !errors.Is(err, ErrUnknownWindowsLSPLanguage) {
		t.Errorf("unknown language error = %v, want ErrUnknownWindowsLSPLanguage", err)
	}
}

// TestWindowsLSPCatalogClangdLanguagesCloseOverContract 验证 clangd catalog 覆盖契约中的全部语言 ID。
func TestWindowsLSPCatalogClangdLanguagesCloseOverContract(t *testing.T) {
	entry, err := WindowsLSPCatalogEntryForProduct(WindowsLSPProductClangd)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(entry.PrimaryLanguages, contract.ClangdLanguageIDs()) {
		t.Fatalf("clangd catalog languages = %v, want contract %v", entry.PrimaryLanguages, contract.ClangdLanguageIDs())
	}
	for _, languageID := range contract.ClangdLanguageIDs() {
		resolved, err := WindowsLSPCatalogEntryForLanguage(languageID)
		if err != nil {
			t.Fatalf("WindowsLSPCatalogEntryForLanguage(%q) error = %v", languageID, err)
		}
		if resolved.Product != WindowsLSPProductClangd {
			t.Fatalf("language %q resolved to %q, want %q", languageID, resolved.Product, WindowsLSPProductClangd)
		}
	}
}

// TestWindowsLSPCatalogMetadataIsPinnedAndNonPlaceholder 验证所有 Windows 资产版本、URL、SHA 和可执行路径均已固定。
func TestWindowsLSPCatalogMetadataIsPinnedAndNonPlaceholder(t *testing.T) {
	for _, entry := range WindowsLSPCatalog() {
		expectedVersion, ok := catalogProductVersion(entry.Product)
		if !ok {
			t.Fatalf("catalogProductVersion(%q) has no expected version", entry.Product)
		}
		for architecture, asset := range entry.Manifest.Assets {
			if asset.Version != expectedVersion || asset.Version == "latest" {
				t.Errorf("product %q architecture %q version = %q, want pinned %q", entry.Product, architecture, asset.Version, expectedVersion)
			}
			if !strings.HasPrefix(asset.URL, "https://") || strings.ContainsAny(asset.URL, "{}<>") || strings.Contains(strings.ToLower(asset.URL), "placeholder") {
				t.Errorf("product %q architecture %q URL is not a pinned official URL: %q", entry.Product, architecture, asset.URL)
			}
			if len(asset.SHA256) != 64 || strings.Trim(strings.ToLower(asset.SHA256), "0") == "" {
				t.Errorf("product %q architecture %q SHA256 is not a pinned digest: %q", entry.Product, architecture, asset.SHA256)
			}
			if asset.BinaryPath == "" || !strings.HasSuffix(strings.ToLower(asset.BinaryPath), ".exe") {
				t.Errorf("product %q architecture %q BinaryPath = %q, want exact Windows executable path", entry.Product, architecture, asset.BinaryPath)
			}
		}
	}
}

// TestWindowsLSPCatalogKotlinStandaloneWindowsLauncher 固定官方 Kotlin
// standalone Windows 资产的直接可执行入口、架构选择和 --stdio 合同。
func TestWindowsLSPCatalogKotlinStandaloneWindowsLauncher(t *testing.T) {
	entry, err := WindowsLSPCatalogEntryForProduct(WindowsLSPProductKotlin)
	if err != nil {
		t.Fatal(err)
	}
	for _, architecture := range []string{WindowsHostArchARM64, WindowsHostArchX64} {
		asset, assetErr := entry.Manifest.AssetForArchitecture(architecture)
		if assetErr != nil {
			t.Fatalf("Kotlin architecture %q lookup error = %v", architecture, assetErr)
		}
		if asset.Format != WindowsLockedAssetFormatZip {
			t.Errorf("Kotlin architecture %q format = %q, want zip", architecture, asset.Format)
		}
		if asset.BinaryPath != "bin/intellij-server.exe" {
			t.Errorf("Kotlin architecture %q binary path = %q, want direct official launcher", architecture, asset.BinaryPath)
		}
		if !strings.HasPrefix(asset.URL, "https://download-cdn.jetbrains.com/language-server/kotlin-server/") {
			t.Errorf("Kotlin architecture %q URL = %q, want official JetBrains CDN", architecture, asset.URL)
		}
	}
	if _, assetErr := entry.Manifest.AssetForArchitecture(WindowsHostArchX86); !errors.Is(assetErr, ErrWindowsUnsupportedAssetArchitecture) {
		t.Fatalf("Kotlin x86 lookup error = %v, want typed unsupported architecture", assetErr)
	}
	args, env, metadataErr := windowsLSPCommandMetadata(WindowsLSPProductKotlin)
	if metadataErr != nil {
		t.Fatal(metadataErr)
	}
	if !slices.Equal(args, []string{"--stdio"}) {
		t.Errorf("Kotlin launcher args = %q, want [--stdio]", args)
	}
	if len(env) != 0 {
		t.Errorf("Kotlin launcher env = %q, want no extra environment", env)
	}
}

// TestWindowsLSPCatalogReturnsIndependentCopies 验证 catalog 返回的清单和语言切片不会被调用方相互污染。
func TestWindowsLSPCatalogReturnsIndependentCopies(t *testing.T) {
	first := WindowsLSPCatalog()
	first[0].Manifest.Assets[WindowsHostArchX64] = WindowsLockedAsset{}
	first[0].PrimaryLanguages[0] = "mutated"
	second := WindowsLSPCatalog()
	if second[0].Manifest.Assets[WindowsHostArchX64].Version == "" {
		t.Fatal("WindowsLSPCatalog() returned shared manifest map")
	}
	if second[0].PrimaryLanguages[0] == "mutated" {
		t.Fatal("WindowsLSPCatalog() returned shared language slice")
	}
}
