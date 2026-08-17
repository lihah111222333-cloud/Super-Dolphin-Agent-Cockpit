//go:build windows

package installer

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// WindowsLSPProduct 标识一个固定版本、按 Windows 原生架构选择的语言工具产品。
type WindowsLSPProduct string

const (
	// WindowsLSPProductClangd 选择固定版本的 Windows 原生 clangd/LLVM 资产。
	WindowsLSPProductClangd WindowsLSPProduct = "clangd"
	// WindowsLSPProductBuf 选择固定版本的 Windows 原生 Buf 资产。
	WindowsLSPProductBuf WindowsLSPProduct = "buf"
	// WindowsLSPProductKotlin 选择固定版本的 Windows Kotlin standalone 服务器资产。
	WindowsLSPProductKotlin WindowsLSPProduct = "kotlin-lsp"
	// WindowsLSPProductDart 选择固定版本的 Windows Dart SDK 资产。
	WindowsLSPProductDart WindowsLSPProduct = "dart"
	// WindowsLSPProductTerraform 选择固定版本的 Windows terraform-ls 资产。
	WindowsLSPProductTerraform WindowsLSPProduct = "terraform-ls"
	// WindowsLSPProductRustAnalyzer 选择固定版本的 Windows rust-analyzer 资产。
	WindowsLSPProductRustAnalyzer WindowsLSPProduct = "rust-analyzer"
	// WindowsLSPProductLuaLanguageLS 标识 LuaLS；其 ARM64 资产刻意缺失，选择时返回带类型的不支持错误。
	WindowsLSPProductLuaLanguageLS WindowsLSPProduct = "lua-language-server"

	// WindowsLSPProductLLVM 是供 LLVM 调用方使用的 clangd 固定产品别名。
	WindowsLSPProductLLVM = WindowsLSPProductClangd
	// WindowsLSPProductClangLLVM 是供 LLVM 调用方使用的 clangd 固定产品别名。
	WindowsLSPProductClangLLVM = WindowsLSPProductClangd
	// WindowsLSPProductKotlinLSP 是固定 Kotlin 产品的别名。
	WindowsLSPProductKotlinLSP = WindowsLSPProductKotlin
	// WindowsLSPProductTerraformLS 是固定 terraform-ls 产品的别名。
	WindowsLSPProductTerraformLS = WindowsLSPProductTerraform
	// WindowsLSPProductRust 是固定 rust-analyzer 产品的别名。
	WindowsLSPProductRust = WindowsLSPProductRustAnalyzer
	// WindowsLSPProductLuaLS 是固定 LuaLS 产品及其 ARM64 类型化缺口的别名。
	WindowsLSPProductLuaLS = WindowsLSPProductLuaLanguageLS
)

const (
	// The catalog deliberately uses one repository-owned install floor. The
	// upstream release pages do not publish a common Windows build floor.
	windowsLSPCatalogMinWindowsVersion = "10.0"
	windowsLSPCatalogMinWindowsBuild   = uint32(19041)
)

var (
	// ErrUnknownWindowsLSPProduct 表示产品 ID 不在 Windows 固定 catalog 中；失败发生在网络、缓存和 PATH 回退之前。
	ErrUnknownWindowsLSPProduct = errors.New("unknown Windows LSP catalog product")
	// ErrUnknownWindowsLSPLanguage 表示语言 ID 没有唯一 Windows catalog 映射；调用方不得猜测其他服务器。
	ErrUnknownWindowsLSPLanguage = errors.New("unknown Windows LSP catalog language")
)

// WindowsLSPCatalogEntry 将产品、主语言 ID 和不可变的 Windows 原生架构发布清单绑定在一起。
type WindowsLSPCatalogEntry struct {
	// Product 是规范的 Windows 固定产品 ID。
	Product WindowsLSPProduct
	// PrimaryLanguages 是 Product 的完整契约语言集合；重复或缺失 ID 会在安装前拒绝。
	PrimaryLanguages []string
	// Manifest 只包含按 Windows 原生架构选择、并在缓存下载和解包前校验的固定 URL、版本与 SHA 资产。
	Manifest WindowsLockedAssetManifest
}

// WindowsLSPCatalog 返回完整固定 Windows catalog 的独立副本；不执行网络或文件系统写入，后续安装在不支持架构或 build 时失败关闭。
func WindowsLSPCatalog() []WindowsLSPCatalogEntry {
	entries := []func() WindowsLSPCatalogEntry{
		catalogClangdEntry, catalogBufEntry, catalogKotlinEntry, catalogDartEntry,
		catalogTerraformEntry, catalogRustAnalyzerEntry, catalogLuaEntry,
	}
	catalog := make([]WindowsLSPCatalogEntry, 0, len(entries))
	for _, entry := range entries {
		catalog = append(catalog, entry())
	}
	return catalog
}

func catalogClangdEntry() WindowsLSPCatalogEntry {
	return WindowsLSPCatalogEntry{Product: WindowsLSPProductClangd, PrimaryLanguages: contract.ClangdLanguageIDs(), Manifest: WindowsLockedAssetManifest{Name: string(WindowsLSPProductClangd), Assets: map[string]WindowsLockedAsset{
		WindowsHostArchARM64: catalogAsset(WindowsHostArchARM64, "22.1.8", "https://github.com/llvm/llvm-project/releases/download/llvmorg-22.1.8/clang%2Bllvm-22.1.8-aarch64-pc-windows-msvc.tar.xz", "de718c58ebbc5f61d58c17b90457fcf42983bc2c4a4aba3e010d108713bfd7f1", WindowsLockedAssetFormatTarXz, "clang+llvm-22.1.8-aarch64-pc-windows-msvc/bin/clangd.exe"),
		WindowsHostArchX64:   catalogAsset(WindowsHostArchX64, "22.1.8", "https://github.com/llvm/llvm-project/releases/download/llvmorg-22.1.8/clang%2Bllvm-22.1.8-x86_64-pc-windows-msvc.tar.xz", "d96c2cc1736f4eb7fa43cb9bbdf56d93551a9ae0a9aadb9c99c3c3b2b712a234", WindowsLockedAssetFormatTarXz, "clang+llvm-22.1.8-x86_64-pc-windows-msvc/bin/clangd.exe"),
	}}}
}

func catalogBufEntry() WindowsLSPCatalogEntry {
	return WindowsLSPCatalogEntry{Product: WindowsLSPProductBuf, PrimaryLanguages: []string{"protobuf", "proto", "proto3"}, Manifest: WindowsLockedAssetManifest{Name: string(WindowsLSPProductBuf), Assets: map[string]WindowsLockedAsset{
		WindowsHostArchARM64: catalogAsset(WindowsHostArchARM64, "1.72.0", "https://github.com/bufbuild/buf/releases/download/v1.72.0/buf-Windows-arm64.exe", "cc06910c1b69715b598fc8d1958538c86b656c05f6dd0a516dfa90c325dcbead", WindowsLockedAssetFormatRaw, "buf-Windows-arm64.exe"),
		WindowsHostArchX64:   catalogAsset(WindowsHostArchX64, "1.72.0", "https://github.com/bufbuild/buf/releases/download/v1.72.0/buf-Windows-x86_64.exe", "6e8f6d043e520bc81cae7b85d4cd6d93e57716a8a9842d5d18200191ee259cb5", WindowsLockedAssetFormatRaw, "buf-Windows-x86_64.exe"),
	}}}
}

func catalogKotlinEntry() WindowsLSPCatalogEntry {
	return WindowsLSPCatalogEntry{Product: WindowsLSPProductKotlin, PrimaryLanguages: []string{"kotlin"}, Manifest: WindowsLockedAssetManifest{Name: string(WindowsLSPProductKotlin), Assets: map[string]WindowsLockedAsset{
		WindowsHostArchARM64: catalogAsset(WindowsHostArchARM64, "262.9593.0", "https://download-cdn.jetbrains.com/language-server/kotlin-server/262.9593.0/kotlin-server-262.9593.0-aarch64.win.zip", "73a552a6a420158622e5ad8d96b53da8aa8ced3f88a24fded01575927a2fd8e7", WindowsLockedAssetFormatZip, "bin/intellij-server.exe"),
		WindowsHostArchX64:   catalogAsset(WindowsHostArchX64, "262.9593.0", "https://download-cdn.jetbrains.com/language-server/kotlin-server/262.9593.0/kotlin-server-262.9593.0.win.zip", "f2daaa476f26d99301b406f76de6d87c437d04dc72f06845154619d8f991c51f", WindowsLockedAssetFormatZip, "bin/intellij-server.exe"),
	}}}
}

func catalogDartEntry() WindowsLSPCatalogEntry {
	return WindowsLSPCatalogEntry{Product: WindowsLSPProductDart, PrimaryLanguages: []string{"dart"}, Manifest: WindowsLockedAssetManifest{Name: string(WindowsLSPProductDart), Assets: map[string]WindowsLockedAsset{
		WindowsHostArchARM64: catalogAsset(WindowsHostArchARM64, "3.13.0", "https://storage.googleapis.com/dart-archive/channels/stable/release/3.13.0/sdk/dartsdk-windows-arm64-release.zip", "3b552053a4c0e95d4e72c20fc20e2e59de34126d2670e2c30161708e7586a1fb", WindowsLockedAssetFormatZip, "dart-sdk/bin/dart.exe"),
		WindowsHostArchX64:   catalogAsset(WindowsHostArchX64, "3.13.0", "https://storage.googleapis.com/dart-archive/channels/stable/release/3.13.0/sdk/dartsdk-windows-x64-release.zip", "8480a527e621e9b06f1121cb4f56204e5c2b07851021cbe63965399de4d7f407", WindowsLockedAssetFormatZip, "dart-sdk/bin/dart.exe"),
	}}}
}

func catalogTerraformEntry() WindowsLSPCatalogEntry {
	return WindowsLSPCatalogEntry{Product: WindowsLSPProductTerraform, PrimaryLanguages: []string{"terraform"}, Manifest: WindowsLockedAssetManifest{Name: string(WindowsLSPProductTerraform), Assets: map[string]WindowsLockedAsset{
		WindowsHostArchARM64: catalogAsset(WindowsHostArchARM64, "0.39.0", "https://releases.hashicorp.com/terraform-ls/0.39.0/terraform-ls_0.39.0_windows_arm64.zip", "4701a880e6cf441a7b24bb9a0bd5f156fdbcd35aee5fa6663c3f08e85f2234fd", WindowsLockedAssetFormatZip, "terraform-ls.exe"),
		WindowsHostArchX64:   catalogAsset(WindowsHostArchX64, "0.39.0", "https://releases.hashicorp.com/terraform-ls/0.39.0/terraform-ls_0.39.0_windows_amd64.zip", "6edc885fe113f6a7fd049622ed0bd255141e68c84acc2fce1bb6a54c1f47bfe1", WindowsLockedAssetFormatZip, "terraform-ls.exe"),
		WindowsHostArchX86:   catalogAsset(WindowsHostArchX86, "0.39.0", "https://releases.hashicorp.com/terraform-ls/0.39.0/terraform-ls_0.39.0_windows_386.zip", "08aa39c907e173a0e941860423c521f92d992e0e26ee9f47413f7546e31eb19a", WindowsLockedAssetFormatZip, "terraform-ls.exe"),
	}}}
}

func catalogRustAnalyzerEntry() WindowsLSPCatalogEntry {
	return WindowsLSPCatalogEntry{Product: WindowsLSPProductRustAnalyzer, PrimaryLanguages: []string{"rust"}, Manifest: WindowsLockedAssetManifest{Name: string(WindowsLSPProductRustAnalyzer), Assets: map[string]WindowsLockedAsset{
		WindowsHostArchARM64: catalogAsset(WindowsHostArchARM64, "2026-08-10.1", "https://github.com/rust-lang/rust-analyzer/releases/download/2026-08-10.1/rust-analyzer-aarch64-pc-windows-msvc.zip", "510ccc383eaeb960f1e1a4b8d3115908d389743383c72f43e4bd17bd1a12b5e5", WindowsLockedAssetFormatZip, "rust-analyzer.exe"),
		WindowsHostArchX64:   catalogAsset(WindowsHostArchX64, "2026-08-10.1", "https://github.com/rust-lang/rust-analyzer/releases/download/2026-08-10.1/rust-analyzer-x86_64-pc-windows-msvc.zip", "f667620d3af202f480faf9e407374509ebddef3b8611922e463aeaa7e6985fc8", WindowsLockedAssetFormatZip, "rust-analyzer.exe"),
		WindowsHostArchX86:   catalogAsset(WindowsHostArchX86, "2026-08-10.1", "https://github.com/rust-lang/rust-analyzer/releases/download/2026-08-10.1/rust-analyzer-i686-pc-windows-msvc.zip", "dab4ed1fbd2545214941f2138bc50280523f66a50edf4971d8c438db04037aab", WindowsLockedAssetFormatZip, "rust-analyzer.exe"),
	}}}
}

func catalogLuaEntry() WindowsLSPCatalogEntry {
	return WindowsLSPCatalogEntry{Product: WindowsLSPProductLuaLanguageLS, PrimaryLanguages: []string{"lua"}, Manifest: WindowsLockedAssetManifest{Name: string(WindowsLSPProductLuaLanguageLS), Assets: map[string]WindowsLockedAsset{
		WindowsHostArchX64: catalogAsset(WindowsHostArchX64, "3.19.1", "https://github.com/LuaLS/lua-language-server/releases/download/3.19.1/lua-language-server-3.19.1-win32-x64.zip", "fdb9a59108cf62517813c97fa5549b0e16d1ef0688306bac728b08434db7e4cd", WindowsLockedAssetFormatZip, "bin/lua-language-server.exe"),
		WindowsHostArchX86: catalogAsset(WindowsHostArchX86, "3.19.1", "https://github.com/LuaLS/lua-language-server/releases/download/3.19.1/lua-language-server-3.19.1-win32-ia32.zip", "27c3fe1ca2c24bbae5370882bb2c4c1cb77025734004f9cb37e79304109232fb", WindowsLockedAssetFormatZip, "bin/lua-language-server.exe"),
	}}}
}

// WindowsLSPCatalogEntries 返回与 WindowsLSPCatalog 相同的独立固定 Windows catalog 副本，不增加回退或副作用。
func WindowsLSPCatalogEntries() []WindowsLSPCatalogEntry { return WindowsLSPCatalog() }

// WindowsLSPCatalogEntryForProduct 按产品 ID 解析固定 Windows 产品，不做 PATH 或跨架构回退；未知 ID 在网络和缓存操作前失败。
func WindowsLSPCatalogEntryForProduct(product WindowsLSPProduct) (WindowsLSPCatalogEntry, error) {
	product = WindowsLSPProduct(strings.ToLower(strings.TrimSpace(string(product))))
	for _, entry := range WindowsLSPCatalog() {
		if entry.Product == product {
			return entry, nil
		}
	}
	return WindowsLSPCatalogEntry{}, fmt.Errorf("%w: %q", ErrUnknownWindowsLSPProduct, product)
}

// WindowsLSPManifestForProduct 返回供 WindowsAssetCache 校验使用的独立固定清单；函数本身不下载也不写盘。
func WindowsLSPManifestForProduct(product WindowsLSPProduct) (WindowsLockedAssetManifest, error) {
	entry, err := WindowsLSPCatalogEntryForProduct(product)
	if err != nil {
		return WindowsLockedAssetManifest{}, err
	}
	return entry.Manifest, nil
}

// WindowsLSPManifest 是 WindowsLSPManifestForProduct 的简洁只读别名，不触发下载或写盘。
func WindowsLSPManifest(product WindowsLSPProduct) (WindowsLockedAssetManifest, error) {
	return WindowsLSPManifestForProduct(product)
}

// WindowsLSPCatalogEntryForLanguage 将规范语言 ID 解析为唯一 Windows 产品；歧义或缺失 ID 在缓存变更前失败。
func WindowsLSPCatalogEntryForLanguage(language string) (WindowsLSPCatalogEntry, error) {
	language = strings.ToLower(strings.TrimSpace(language))
	if language == "" {
		return WindowsLSPCatalogEntry{}, fmt.Errorf("%w: empty language", ErrUnknownWindowsLSPLanguage)
	}
	var match WindowsLSPCatalogEntry
	for _, entry := range WindowsLSPCatalog() {
		for _, candidate := range entry.PrimaryLanguages {
			if candidate != language {
				continue
			}
			if match.Product != "" {
				return WindowsLSPCatalogEntry{}, fmt.Errorf("%w: language %q is mapped more than once", ErrUnknownWindowsLSPLanguage, language)
			}
			match = entry
		}
	}
	if match.Product == "" {
		return WindowsLSPCatalogEntry{}, fmt.Errorf("%w: %q", ErrUnknownWindowsLSPLanguage, language)
	}
	return match, nil
}

// WindowsLSPAssetForArchitecture 只选择请求的 Windows 原生架构，并在不支持时保留类型化错误且不下载资产。
func WindowsLSPAssetForArchitecture(product WindowsLSPProduct, architecture string) (WindowsLockedAsset, error) {
	entry, err := WindowsLSPCatalogEntryForProduct(product)
	if err != nil {
		return WindowsLockedAsset{}, err
	}
	return entry.Manifest.AssetForArchitecture(architecture)
}

// WindowsLSPAssetForPlatform 按 Windows NativeArch 选择资产并执行固定版本/build 门禁；ProcessArch 永不改变资产选择。
func WindowsLSPAssetForPlatform(product WindowsLSPProduct, platform WindowsHostPlatform) (WindowsLockedAsset, error) {
	entry, err := WindowsLSPCatalogEntryForProduct(product)
	if err != nil {
		return WindowsLockedAsset{}, err
	}
	return entry.Manifest.AssetForPlatform(platform)
}

// ValidateWindowsLSPCatalog 只读校验每个 Windows 产品、语言闭包、原生架构资产、官方 HTTPS 来源、SHA 与精确可执行路径，并阻断无效 catalog 安装。
func ValidateWindowsLSPCatalog() error {
	entries := WindowsLSPCatalog()
	wantProducts := map[WindowsLSPProduct]struct{}{
		WindowsLSPProductClangd: {}, WindowsLSPProductBuf: {}, WindowsLSPProductKotlin: {},
		WindowsLSPProductDart: {}, WindowsLSPProductTerraform: {}, WindowsLSPProductRustAnalyzer: {},
		WindowsLSPProductLuaLanguageLS: {},
	}
	if len(entries) != len(wantProducts) {
		return fmt.Errorf("Windows LSP catalog has %d entries, want %d", len(entries), len(wantProducts))
	}
	seenProducts := make(map[WindowsLSPProduct]struct{}, len(entries))
	seenLanguages := make(map[string]WindowsLSPProduct)
	for _, entry := range entries {
		if _, duplicate := seenProducts[entry.Product]; duplicate {
			return fmt.Errorf("Windows LSP catalog repeats product %q", entry.Product)
		}
		seenProducts[entry.Product] = struct{}{}
		if _, expected := wantProducts[entry.Product]; !expected {
			return fmt.Errorf("Windows LSP catalog contains unexpected product %q", entry.Product)
		}
		if err := entry.validate(); err != nil {
			return err
		}
		for _, language := range entry.PrimaryLanguages {
			if previous, duplicate := seenLanguages[language]; duplicate {
				return fmt.Errorf("Windows LSP language %q maps to both %q and %q", language, previous, entry.Product)
			}
			seenLanguages[language] = entry.Product
		}
	}
	for product := range wantProducts {
		if _, present := seenProducts[product]; !present {
			return fmt.Errorf("Windows LSP catalog is missing product %q", product)
		}
	}
	return nil
}

func catalogAsset(architecture, version, rawURL, sha256 string, format WindowsLockedAssetFormat, binaryPath string) WindowsLockedAsset {
	return WindowsLockedAsset{
		Architecture: architecture, Version: version, URL: rawURL, SHA256: sha256,
		Format: format, BinaryPath: binaryPath,
		MinWindowsVersion: windowsLSPCatalogMinWindowsVersion,
		MinWindowsBuild:   windowsLSPCatalogMinWindowsBuild,
	}
}

func (entry WindowsLSPCatalogEntry) validate() error {
	if strings.TrimSpace(string(entry.Product)) == "" {
		return errors.New("Windows LSP catalog product is empty")
	}
	if entry.Manifest.Name != string(entry.Product) {
		return fmt.Errorf("Windows LSP product %q and manifest name %q differ", entry.Product, entry.Manifest.Name)
	}
	if len(entry.PrimaryLanguages) == 0 {
		return fmt.Errorf("Windows LSP product %q has no primary language", entry.Product)
	}
	seenLanguages := make(map[string]struct{}, len(entry.PrimaryLanguages))
	for _, language := range entry.PrimaryLanguages {
		language = strings.ToLower(strings.TrimSpace(language))
		if language == "" {
			return fmt.Errorf("Windows LSP product %q has an empty primary language", entry.Product)
		}
		if _, duplicate := seenLanguages[language]; duplicate {
			return fmt.Errorf("Windows LSP product %q repeats primary language %q", entry.Product, language)
		}
		seenLanguages[language] = struct{}{}
	}
	if err := entry.Manifest.Validate(); err != nil {
		return fmt.Errorf("validate Windows LSP product %q: %w", entry.Product, err)
	}
	expectedVersion, ok := catalogProductVersion(entry.Product)
	if !ok {
		return fmt.Errorf("Windows LSP product %q has no pinned version contract", entry.Product)
	}
	seenArchitectures := make(map[string]struct{}, len(entry.Manifest.Assets))
	for key, asset := range entry.Manifest.Assets {
		architecture, err := NormalizeWindowsArchitectureAlias(key)
		if err != nil {
			return fmt.Errorf("normalize Windows LSP product %q architecture %q: %w", entry.Product, key, err)
		}
		seenArchitectures[architecture] = struct{}{}
		if asset.Version != expectedVersion {
			return fmt.Errorf("Windows LSP product %q architecture %q has version %q, want %q", entry.Product, architecture, asset.Version, expectedVersion)
		}
		if asset.MinWindowsVersion != windowsLSPCatalogMinWindowsVersion || asset.MinWindowsBuild != windowsLSPCatalogMinWindowsBuild {
			return fmt.Errorf("Windows LSP product %q architecture %q does not use catalog Windows floor", entry.Product, architecture)
		}
		if strings.TrimSpace(asset.SHA256) == "" || strings.Trim(strings.ToLower(strings.TrimSpace(asset.SHA256)), "0") == "" {
			return fmt.Errorf("Windows LSP product %q architecture %q has a placeholder SHA256", entry.Product, architecture)
		}
		if err := validateCatalogURL(entry.Product, asset); err != nil {
			return fmt.Errorf("Windows LSP product %q architecture %q: %w", entry.Product, architecture, err)
		}
		if expectedPath, ok := catalogBinaryPath(entry.Product, architecture); !ok || asset.BinaryPath != expectedPath {
			return fmt.Errorf("Windows LSP product %q architecture %q has BinaryPath %q, want %q", entry.Product, architecture, asset.BinaryPath, expectedPath)
		}
	}
	for _, architecture := range []string{WindowsHostArchARM64, WindowsHostArchX64, WindowsHostArchX86} {
		if _, present := seenArchitectures[architecture]; !present {
			continue
		}
		asset, err := entry.Manifest.AssetForArchitecture(architecture)
		if err != nil {
			return fmt.Errorf("select Windows LSP product %q architecture %q: %w", entry.Product, architecture, err)
		}
		if asset.Architecture != architecture {
			return fmt.Errorf("Windows LSP product %q architecture selection returned %q", entry.Product, asset.Architecture)
		}
	}
	return nil
}

func catalogProductVersion(product WindowsLSPProduct) (string, bool) {
	switch product {
	case WindowsLSPProductClangd:
		return "22.1.8", true
	case WindowsLSPProductBuf:
		return "1.72.0", true
	case WindowsLSPProductKotlin:
		return "262.9593.0", true
	case WindowsLSPProductDart:
		return "3.13.0", true
	case WindowsLSPProductTerraform:
		return "0.39.0", true
	case WindowsLSPProductRustAnalyzer:
		return "2026-08-10.1", true
	case WindowsLSPProductLuaLanguageLS:
		return "3.19.1", true
	default:
		return "", false
	}
}

func catalogBinaryPath(product WindowsLSPProduct, architecture string) (string, bool) {
	switch product {
	case WindowsLSPProductClangd:
		switch architecture {
		case WindowsHostArchARM64:
			return "clang+llvm-22.1.8-aarch64-pc-windows-msvc/bin/clangd.exe", true
		case WindowsHostArchX64:
			return "clang+llvm-22.1.8-x86_64-pc-windows-msvc/bin/clangd.exe", true
		}
	case WindowsLSPProductBuf:
		switch architecture {
		case WindowsHostArchARM64:
			return "buf-Windows-arm64.exe", true
		case WindowsHostArchX64:
			return "buf-Windows-x86_64.exe", true
		}
	case WindowsLSPProductKotlin, WindowsLSPProductDart:
		if architecture == WindowsHostArchARM64 || architecture == WindowsHostArchX64 {
			if product == WindowsLSPProductKotlin {
				return "bin/intellij-server.exe", true
			}
			return "dart-sdk/bin/dart.exe", true
		}
	case WindowsLSPProductTerraform:
		if architecture == WindowsHostArchARM64 || architecture == WindowsHostArchX64 || architecture == WindowsHostArchX86 {
			return "terraform-ls.exe", true
		}
	case WindowsLSPProductRustAnalyzer:
		if architecture == WindowsHostArchARM64 || architecture == WindowsHostArchX64 || architecture == WindowsHostArchX86 {
			return "rust-analyzer.exe", true
		}
	case WindowsLSPProductLuaLanguageLS:
		if architecture == WindowsHostArchX64 || architecture == WindowsHostArchX86 {
			return "bin/lua-language-server.exe", true
		}
	}
	return "", false
}

func validateCatalogURL(product WindowsLSPProduct, asset WindowsLockedAsset) error {
	parsed, err := url.Parse(strings.TrimSpace(asset.URL))
	if err != nil {
		return fmt.Errorf("parse URL: %w", err)
	}
	if parsed.Scheme != "https" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("URL must be an https URL without query or fragment: %q", asset.URL)
	}
	expectedHost := ""
	expectedPrefix := ""
	switch product {
	case WindowsLSPProductClangd:
		expectedHost, expectedPrefix = "github.com", "https://github.com/llvm/llvm-project/releases/download/llvmorg-22.1.8/"
	case WindowsLSPProductBuf:
		expectedHost, expectedPrefix = "github.com", "https://github.com/bufbuild/buf/releases/download/v1.72.0/"
	case WindowsLSPProductKotlin:
		expectedHost, expectedPrefix = "download-cdn.jetbrains.com", "https://download-cdn.jetbrains.com/language-server/kotlin-server/262.9593.0/"
	case WindowsLSPProductDart:
		expectedHost, expectedPrefix = "storage.googleapis.com", "https://storage.googleapis.com/dart-archive/channels/stable/release/3.13.0/sdk/"
	case WindowsLSPProductTerraform:
		expectedHost, expectedPrefix = "releases.hashicorp.com", "https://releases.hashicorp.com/terraform-ls/0.39.0/"
	case WindowsLSPProductRustAnalyzer:
		expectedHost, expectedPrefix = "github.com", "https://github.com/rust-lang/rust-analyzer/releases/download/2026-08-10.1/"
	case WindowsLSPProductLuaLanguageLS:
		expectedHost, expectedPrefix = "github.com", "https://github.com/LuaLS/lua-language-server/releases/download/3.19.1/"
	default:
		return fmt.Errorf("no official URL contract for product %q", product)
	}
	if parsed.Host != expectedHost || !strings.HasPrefix(asset.URL, expectedPrefix) {
		return fmt.Errorf("URL is not the official release origin: %q", asset.URL)
	}
	if strings.ContainsAny(asset.URL, "{}<>") || strings.Contains(strings.ToLower(asset.URL), "placeholder") {
		return fmt.Errorf("URL contains a placeholder: %q", asset.URL)
	}
	return nil
}

// WindowsLSPCatalogProducts 返回固定 Windows catalog 的稳定字典序产品 ID，不访问网络、缓存，也不执行回退。
func WindowsLSPCatalogProducts() []WindowsLSPProduct {
	entries := WindowsLSPCatalog()
	products := make([]WindowsLSPProduct, 0, len(entries))
	for _, entry := range entries {
		products = append(products, entry.Product)
	}
	sort.Slice(products, func(i, j int) bool { return products[i] < products[j] })
	return products
}

// WindowsLSPCatalogMinWindows 返回所有 catalog 资产在下载或解包前必须满足的固定 Windows 版本/build 下限。
func WindowsLSPCatalogMinWindows() (string, uint32) {
	return windowsLSPCatalogMinWindowsVersion, windowsLSPCatalogMinWindowsBuild
}

// WindowsLSPAssetCachePath 返回一个 Windows 原生架构的规范 ready 树可执行文件相对路径；缺失资产返回类型化错误且不触发网络或文件系统操作。
func WindowsLSPAssetCachePath(product WindowsLSPProduct, architecture string) (string, error) {
	asset, err := WindowsLSPAssetForArchitecture(product, architecture)
	if err != nil {
		return "", err
	}
	return path.Clean(strings.ReplaceAll(asset.BinaryPath, "\\", "/")), nil
}
