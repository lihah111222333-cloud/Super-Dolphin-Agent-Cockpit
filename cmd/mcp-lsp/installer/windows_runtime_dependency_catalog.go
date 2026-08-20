//go:build windows

package installer

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

// WindowsRuntimeDependencyProduct 标识 Windows 运行时依赖产品；目录只选择 DetectWindowsHostPlatform.NativeArch 的原生资产，失败立即返回。
type WindowsRuntimeDependencyProduct string

const (
	// WindowsRuntimeDependencyProductGoGopls 标识 Windows Go 与 gopls 的固定资产产品；安装联网并写入 cohort，失败不回退。
	WindowsRuntimeDependencyProductGoGopls WindowsRuntimeDependencyProduct = "go-gopls"
	// WindowsRuntimeDependencyProductDotnetCsharpLS 标识 Windows .NET SDK 与 csharp-ls 的固定资产产品；只使用原生架构 SDK。
	WindowsRuntimeDependencyProductDotnetCsharpLS WindowsRuntimeDependencyProduct = "dotnet-csharp-ls"
	// WindowsRuntimeDependencyProductJDKJDTLS 标识 Windows JDK 与 JDTLS 的固定资产产品；启动参数必须指向 cohort 绝对路径。
	WindowsRuntimeDependencyProductJDKJDTLS WindowsRuntimeDependencyProduct = "jdk-jdtls"
	// WindowsRuntimeDependencyProductRubySolargraph 标识 Windows Ruby 与 Solargraph 的固定资产产品；安装后只返回 cohort 内绝对路径。
	WindowsRuntimeDependencyProductRubySolargraph WindowsRuntimeDependencyProduct = "ruby-solargraph"
	// WindowsRuntimeDependencyProductRubyLSP 标识 Windows ARM64 RubyInstaller 与 Ruby LSP 的固定私有闭包；禁止跨架构回退。
	WindowsRuntimeDependencyProductRubyLSP WindowsRuntimeDependencyProduct = "ruby-lsp"
	// WindowsRuntimeDependencyProductSwiftSourceKitLS 标识 Windows Swift/sourcekit-lsp 目录项；证据不足时必须返回 typed gap。
	WindowsRuntimeDependencyProductSwiftSourceKitLS WindowsRuntimeDependencyProduct = "swift-sourcekit-lsp"
	// WindowsRuntimeDependencyProductGoSQLS 标识 Windows SQLS 的固定 Go 源构建 cohort；只按 NativeArch 选择原生 Go 资产。
	WindowsRuntimeDependencyProductGoSQLS WindowsRuntimeDependencyProduct = "go-sqls"

	// WindowsRuntimeDependencyProductSwiftSourceKitLSP 是 WindowsRuntimeDependencyProductSwiftSourceKitLS 的兼容别名，边界仍为 Windows 目录。
	WindowsRuntimeDependencyProductSwiftSourceKitLSP = WindowsRuntimeDependencyProductSwiftSourceKitLS
	// WindowsRuntimeDependencyProductDotnetCSharpLS 是 WindowsRuntimeDependencyProductDotnetCsharpLS 的兼容别名，使用同一原生 SDK 资产。
	WindowsRuntimeDependencyProductDotnetCSharpLS = WindowsRuntimeDependencyProductDotnetCsharpLS
)

// WindowsRuntimeDependencyCatalogStatus 描述 Windows 目录项在指定原生架构上的裁决；非 installable 状态禁止联网和写盘。
type WindowsRuntimeDependencyCatalogStatus string

const (
	// WindowsRuntimeDependencyStatusInstallable 表示 Windows 原生资产、路径和安装生命周期均已锁定，可在校验后写入 cohort。
	WindowsRuntimeDependencyStatusInstallable WindowsRuntimeDependencyCatalogStatus = "installable"
	// WindowsRuntimeDependencyStatusTypedUnsupported 表示 Windows 当前原生架构没有官方可运行资产；调用必须 fail-fast 且不写缓存。
	WindowsRuntimeDependencyStatusTypedUnsupported WindowsRuntimeDependencyCatalogStatus = "typed_unsupported"
	// WindowsRuntimeDependencyStatusEvidenceGap 表示 Windows 官方证据不足以安全物化或启动；调用必须 fail-fast 且不写缓存。
	WindowsRuntimeDependencyStatusEvidenceGap WindowsRuntimeDependencyCatalogStatus = "evidence_gap"

	// WindowsRuntimeDependencyStatusUnsupported 是 WindowsRuntimeDependencyStatusTypedUnsupported 的兼容别名。
	WindowsRuntimeDependencyStatusUnsupported = WindowsRuntimeDependencyStatusTypedUnsupported
	// WindowsRuntimeDependencyStatusGap 是 WindowsRuntimeDependencyStatusEvidenceGap 的兼容别名。
	WindowsRuntimeDependencyStatusGap = WindowsRuntimeDependencyStatusEvidenceGap
)

// WindowsRuntimeDependencyChecksumAlgorithm 指定 Windows 下载资产的摘要算法；摘要不匹配时不发布 ready cohort。
type WindowsRuntimeDependencyChecksumAlgorithm string

const (
	// WindowsRuntimeDependencyChecksumSHA256 表示 Windows 资产使用固定 SHA-256 摘要校验。
	WindowsRuntimeDependencyChecksumSHA256 WindowsRuntimeDependencyChecksumAlgorithm = "sha256"
	// WindowsRuntimeDependencyChecksumSHA512 表示 Windows 资产使用固定 SHA-512 摘要校验。
	WindowsRuntimeDependencyChecksumSHA512 WindowsRuntimeDependencyChecksumAlgorithm = "sha512"
)

// WindowsRuntimeDependencyAssetFormat 指定 Windows 固定资产的受控物化格式；未知格式不得联网写盘。
type WindowsRuntimeDependencyAssetFormat string

const (
	// WindowsRuntimeDependencyAssetFormatEXE 表示 Windows 安装器格式；未具备静默安装和路径证据时必须保持 gap。
	WindowsRuntimeDependencyAssetFormatEXE WindowsRuntimeDependencyAssetFormat = "exe"
	// WindowsRuntimeDependencyAssetFormatZIP 表示 Windows ZIP 资产，由安全解包器写入暂存 cohort。
	WindowsRuntimeDependencyAssetFormatZIP WindowsRuntimeDependencyAssetFormat = "zip"
	// WindowsRuntimeDependencyAssetFormatTarGz 表示 Windows tar.gz 资产，由安全解包器写入暂存 cohort。
	WindowsRuntimeDependencyAssetFormatTarGz WindowsRuntimeDependencyAssetFormat = "tar.gz"
	// WindowsRuntimeDependencyAssetFormatNupkg 表示 Windows .NET 工具包格式，由安全解包器写入暂存 cohort。
	WindowsRuntimeDependencyAssetFormatNupkg WindowsRuntimeDependencyAssetFormat = "nupkg"
	// WindowsRuntimeDependencyAssetFormatGem 表示 Windows Ruby gem 固定包；只传给 cohort 内绝对 gem 命令安装。
	WindowsRuntimeDependencyAssetFormatGem WindowsRuntimeDependencyAssetFormat = "gem"
	// WindowsRuntimeDependencyAssetFormatCrate 表示 Rust crate 格式；当前 Windows 生产路径不接受其自动物化。
	WindowsRuntimeDependencyAssetFormatCrate WindowsRuntimeDependencyAssetFormat = "crate"
	// WindowsRuntimeDependencyAssetFormatSevenZip 表示 Windows portable 7z 格式，由纯 Go 安全解包器写入暂存 cohort。
	WindowsRuntimeDependencyAssetFormatSevenZip WindowsRuntimeDependencyAssetFormat = "7z"
)

// WindowsRuntimeDependencyAsset 描述 Windows 单个固定资产的官方地址、摘要、原生架构和解包路径；任一校验失败均不发布 cohort。
type WindowsRuntimeDependencyAsset struct {
	// Component 是 Windows 资产组件名，用于稳定缓存、receipt 和错误定位。
	Component string
	// Architecture 是资产声明的 Windows 原生架构，必须等于 DetectWindowsHostPlatform.NativeArch。
	Architecture string
	// Version 是资产的精确版本；禁止 latest 或动态范围。
	Version string
	// URL 是资产的固定 HTTPS 官方地址；下载前不允许 query、fragment 或 PATH 回退。
	URL string
	// ChecksumAlgorithm 是下载后使用的固定摘要算法。
	ChecksumAlgorithm WindowsRuntimeDependencyChecksumAlgorithm
	// Checksum 是与 ChecksumAlgorithm 对应的固定十六进制摘要。
	Checksum string
	// Format 是受控下载/解包或安装格式；失败时不得留下伪 ready。
	Format WindowsRuntimeDependencyAssetFormat
	// ArchivePath 是解包后用于验证的安全相对路径，不能越过 cohort 根目录。
	ArchivePath string
	// BinaryPath 是解包后预期二进制的安全相对路径，启动时会解析为绝对路径。
	BinaryPath string
	// MinWindowsVersion 是官方证据支持的最低 Windows 版本；低于此版本立即拒绝。
	MinWindowsVersion string
	// MinWindowsBuild 是已知时使用的最低 Windows build；未知时必须为零。
	MinWindowsBuild uint32
	// MinWindowsBuildKnown 表示 MinWindowsBuild 是否有官方精确证据。
	MinWindowsBuildKnown bool
	// Native 表示资产是否为目标 Windows 原生架构；禁止跨架构或仿真替代。
	Native bool
}

// WindowsRuntimeDependencyInstallSpec 描述 Windows cohort 内的绝对安装器路径、固定参数和 LSP server 相对路径。
type WindowsRuntimeDependencyInstallSpec struct {
	// Command 是审计用的固定安装动作描述，不得暗示 PATH 或 latest 回退。
	Command string
	// RuntimeExecutablePath 是 cohort 内运行时可执行文件的安全相对路径。
	RuntimeExecutablePath string
	// Args 是固定安装参数；生产执行前会以 cohort 绝对可执行路径启动。
	Args []string
	// ServerPath 是 LSP server 的安全相对路径，返回结果中会解析为绝对路径。
	ServerPath string
}

// WindowsRuntimeDependencyCatalogEntry 描述 Windows 产品在 ARM64/x64/x86 上的状态、证据、固定资产和安装合同。
type WindowsRuntimeDependencyCatalogEntry struct {
	// Product 是 Windows 目录产品标识。
	Product WindowsRuntimeDependencyProduct
	// PrimaryLanguages 是映射到该 Windows 产品的语言标识。
	PrimaryLanguages []string
	// StatusByArchitecture 按 Windows 原生架构保存 installable、typed_unsupported 或 evidence_gap 裁决。
	StatusByArchitecture map[string]WindowsRuntimeDependencyCatalogStatus
	// ReasonByArchitecture 保存非 installable 架构的可审计失败原因。
	ReasonByArchitecture map[string]string
	// EvidenceGapsByArchitecture 保存 Windows 官方资料仍缺失的具体证据项。
	EvidenceGapsByArchitecture map[string][]string
	// AssetsByArchitecture 按 Windows 原生架构保存固定 URL、摘要和相对路径资产。
	AssetsByArchitecture map[string][]WindowsRuntimeDependencyAsset
	// Install 是 Windows cohort 的固定安装和 LSP 启动合同。
	Install WindowsRuntimeDependencyInstallSpec
}

var (
	// ErrUnknownWindowsRuntimeDependencyProduct 表示 Windows 目录没有请求的产品；调用立即失败且不联网写盘。
	ErrUnknownWindowsRuntimeDependencyProduct = errors.New("unknown runtime dependency catalog product")
	// ErrUnknownWindowsRuntimeDependencyLanguage 表示 Windows 目录没有请求的语言映射；调用立即失败且不联网写盘。
	ErrUnknownWindowsRuntimeDependencyLanguage = errors.New("unknown runtime dependency catalog language")
	// ErrWindowsRuntimeDependencyUnsupported 表示 Windows 当前原生架构没有官方可运行资产；禁止跨架构回退。
	ErrWindowsRuntimeDependencyUnsupported = errors.New("runtime dependency is unsupported for native architecture")
	// ErrWindowsRuntimeDependencyEvidenceGap 表示 Windows 官方证据不足；调用不得下载、安装或发布 ready。
	ErrWindowsRuntimeDependencyEvidenceGap = errors.New("runtime dependency catalog has an evidence gap")
)

// WindowsRuntimeDependencyUnsupportedError 记录 Windows 产品在某原生架构上没有官方可运行资产的 typed 失败。
type WindowsRuntimeDependencyUnsupportedError struct {
	// Product 是被裁决为 Windows 不支持的产品。
	Product WindowsRuntimeDependencyProduct
	// Architecture 是被检测到的 Windows 原生架构。
	Architecture string
	// Reason 是禁止安装、仿真或跨架构回退的具体原因。
	Reason string
}

// Error 返回 Windows typed unsupported 的稳定错误文本，供 errors.Is 与 receipt 审计使用。
func (e *WindowsRuntimeDependencyUnsupportedError) Error() string {
	if e == nil {
		return ErrWindowsRuntimeDependencyUnsupported.Error()
	}
	return fmt.Sprintf("%s: %s/%s: %s", ErrWindowsRuntimeDependencyUnsupported, e.Product, e.Architecture, e.Reason)
}

// Unwrap 将 Windows typed unsupported 错误映射到 ErrWindowsRuntimeDependencyUnsupported。
func (e *WindowsRuntimeDependencyUnsupportedError) Unwrap() error {
	return ErrWindowsRuntimeDependencyUnsupported
}

// WindowsRuntimeDependencyEvidenceGapError 记录 Windows 产品缺少官方安装或启动证据的 typed 失败。
type WindowsRuntimeDependencyEvidenceGapError struct {
	// Product 是证据不足的 Windows 产品。
	Product WindowsRuntimeDependencyProduct
	// Architecture 是被检测到的 Windows 原生架构。
	Architecture string
	// Reason 是缺失的官方证据或不安全生命周期的具体说明。
	Reason string
}

// Error 返回 Windows evidence gap 的稳定错误文本，供调用方阻断写盘。
func (e *WindowsRuntimeDependencyEvidenceGapError) Error() string {
	if e == nil {
		return ErrWindowsRuntimeDependencyEvidenceGap.Error()
	}
	return fmt.Sprintf("%s: %s/%s: %s", ErrWindowsRuntimeDependencyEvidenceGap, e.Product, e.Architecture, e.Reason)
}

// Unwrap 将 Windows evidence gap 错误映射到 ErrWindowsRuntimeDependencyEvidenceGap。
func (e *WindowsRuntimeDependencyEvidenceGapError) Unwrap() error {
	return ErrWindowsRuntimeDependencyEvidenceGap
}

var runtimeDependencySHA256Pattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
var runtimeDependencySHA512Pattern = regexp.MustCompile(`^[0-9a-fA-F]{128}$`)

// WindowsRuntimeDependencyCatalog 返回 Windows 产品目录的深拷贝；调用只读，不联网、不写盘，修改副本不会影响后续调用。
func WindowsRuntimeDependencyCatalog() []WindowsRuntimeDependencyCatalogEntry {
	constructors := []func() WindowsRuntimeDependencyCatalogEntry{
		runtimeDependencyGoGoplsEntry,
		runtimeDependencyDotnetCsharpLSEntry,
		runtimeDependencyJDKJDTLSEntry,
		runtimeDependencyRubySolargraphEntry,
		runtimeDependencyRubyLSPEntry,
		runtimeDependencySwiftSourceKitLSPEntry,
		runtimeDependencyGoSQLSEntry,
	}
	entries := make([]WindowsRuntimeDependencyCatalogEntry, 0, len(constructors))
	for _, constructor := range constructors {
		entries = append(entries, constructor())
	}
	return entries
}

// WindowsRuntimeDependencyCatalogEntryForProduct 按产品查找 Windows 目录项；未知产品立即失败，不联网、不写盘。
func WindowsRuntimeDependencyCatalogEntryForProduct(product WindowsRuntimeDependencyProduct) (WindowsRuntimeDependencyCatalogEntry, error) {
	product = WindowsRuntimeDependencyProduct(strings.ToLower(strings.TrimSpace(string(product))))
	for _, entry := range WindowsRuntimeDependencyCatalog() {
		if entry.Product == product {
			return entry, nil
		}
	}
	return WindowsRuntimeDependencyCatalogEntry{}, fmt.Errorf("%w: %q", ErrUnknownWindowsRuntimeDependencyProduct, product)
}

// WindowsRuntimeDependencyCatalogEntryForLanguage 按语言查找 Windows 目录项；未知语言立即失败，不联网、不写盘。
func WindowsRuntimeDependencyCatalogEntryForLanguage(language string) (WindowsRuntimeDependencyCatalogEntry, error) {
	language = strings.ToLower(strings.TrimSpace(language))
	if language == "" {
		return WindowsRuntimeDependencyCatalogEntry{}, fmt.Errorf("%w: empty language", ErrUnknownWindowsRuntimeDependencyLanguage)
	}
	for _, entry := range WindowsRuntimeDependencyCatalog() {
		for _, candidate := range entry.PrimaryLanguages {
			if candidate == language {
				return entry, nil
			}
		}
	}
	return WindowsRuntimeDependencyCatalogEntry{}, fmt.Errorf("%w: %q", ErrUnknownWindowsRuntimeDependencyLanguage, language)
}

// WindowsRuntimeDependencyStatusForArchitecture 返回 Windows 产品在指定原生架构上的目录裁决；不下载、不写盘。
func WindowsRuntimeDependencyStatusForArchitecture(product WindowsRuntimeDependencyProduct, architecture string) (WindowsRuntimeDependencyCatalogStatus, error) {
	entry, err := WindowsRuntimeDependencyCatalogEntryForProduct(product)
	if err != nil {
		return "", err
	}
	normalized, err := NormalizeWindowsArchitectureAlias(architecture)
	if err != nil {
		return "", &WindowsRuntimeDependencyUnsupportedError{Product: entry.Product, Architecture: strings.TrimSpace(architecture), Reason: err.Error()}
	}
	status, ok := entry.StatusByArchitecture[normalized]
	if !ok {
		return "", &WindowsRuntimeDependencyUnsupportedError{Product: entry.Product, Architecture: normalized, Reason: "catalog has no architecture entry"}
	}
	return status, nil
}

// WindowsRuntimeDependencyAssetsForArchitecture 返回 Windows 指定原生架构的固定资产副本；unsupported 或 gap 同时返回 typed 错误且不写盘。
func WindowsRuntimeDependencyAssetsForArchitecture(product WindowsRuntimeDependencyProduct, architecture string) ([]WindowsRuntimeDependencyAsset, error) {
	entry, err := WindowsRuntimeDependencyCatalogEntryForProduct(product)
	if err != nil {
		return nil, err
	}
	normalized, err := NormalizeWindowsArchitectureAlias(architecture)
	if err != nil {
		return nil, &WindowsRuntimeDependencyUnsupportedError{Product: entry.Product, Architecture: strings.TrimSpace(architecture), Reason: err.Error()}
	}
	assets := cloneWindowsRuntimeDependencyAssets(entry.AssetsByArchitecture[normalized])
	status, ok := entry.StatusByArchitecture[normalized]
	if !ok {
		return nil, &WindowsRuntimeDependencyUnsupportedError{Product: entry.Product, Architecture: normalized, Reason: "catalog has no architecture entry"}
	}
	switch status {
	case WindowsRuntimeDependencyStatusInstallable:
		return assets, nil
	case WindowsRuntimeDependencyStatusTypedUnsupported:
		return assets, &WindowsRuntimeDependencyUnsupportedError{Product: entry.Product, Architecture: normalized, Reason: entry.ReasonByArchitecture[normalized]}
	case WindowsRuntimeDependencyStatusEvidenceGap:
		return assets, &WindowsRuntimeDependencyEvidenceGapError{Product: entry.Product, Architecture: normalized, Reason: entry.ReasonByArchitecture[normalized]}
	default:
		return assets, fmt.Errorf("%w: unknown status %q", ErrWindowsRuntimeDependencyEvidenceGap, status)
	}
}

// WindowsRuntimeDependencyPlanForArchitecture 返回 Windows 指定原生架构的安装计划；只读校验失败时立即阻断，不联网、不写盘。
func WindowsRuntimeDependencyPlanForArchitecture(product WindowsRuntimeDependencyProduct, architecture string) (WindowsRuntimeDependencyCatalogEntry, error) {
	entry, err := WindowsRuntimeDependencyCatalogEntryForProduct(product)
	if err != nil {
		return WindowsRuntimeDependencyCatalogEntry{}, err
	}
	normalized, err := NormalizeWindowsArchitectureAlias(architecture)
	if err != nil {
		return WindowsRuntimeDependencyCatalogEntry{}, &WindowsRuntimeDependencyUnsupportedError{Product: entry.Product, Architecture: strings.TrimSpace(architecture), Reason: err.Error()}
	}
	status, ok := entry.StatusByArchitecture[normalized]
	if !ok {
		return WindowsRuntimeDependencyCatalogEntry{}, &WindowsRuntimeDependencyUnsupportedError{Product: entry.Product, Architecture: normalized, Reason: "catalog has no architecture entry"}
	}
	switch status {
	case WindowsRuntimeDependencyStatusInstallable:
		return entry, nil
	case WindowsRuntimeDependencyStatusTypedUnsupported:
		return entry, &WindowsRuntimeDependencyUnsupportedError{Product: entry.Product, Architecture: normalized, Reason: entry.ReasonByArchitecture[normalized]}
	case WindowsRuntimeDependencyStatusEvidenceGap:
		return entry, &WindowsRuntimeDependencyEvidenceGapError{Product: entry.Product, Architecture: normalized, Reason: entry.ReasonByArchitecture[normalized]}
	default:
		return entry, fmt.Errorf("%w: unknown status %q", ErrWindowsRuntimeDependencyEvidenceGap, status)
	}
}

// ValidateWindowsRuntimeDependencyCatalog 校验 Windows 目录的产品、架构、固定 URL、摘要和相对路径合同；失败即禁止 provision。
func ValidateWindowsRuntimeDependencyCatalog() error {
	entries := WindowsRuntimeDependencyCatalog()
	want := map[WindowsRuntimeDependencyProduct]struct{}{
		WindowsRuntimeDependencyProductGoGopls: {}, WindowsRuntimeDependencyProductDotnetCsharpLS: {},
		WindowsRuntimeDependencyProductJDKJDTLS: {}, WindowsRuntimeDependencyProductRubySolargraph: {}, WindowsRuntimeDependencyProductRubyLSP: {},
		WindowsRuntimeDependencyProductSwiftSourceKitLS: {}, WindowsRuntimeDependencyProductGoSQLS: {},
	}
	if len(entries) != len(want) {
		return fmt.Errorf("runtime dependency catalog has %d entries, want %d", len(entries), len(want))
	}
	seenProducts := make(map[WindowsRuntimeDependencyProduct]struct{}, len(entries))
	seenLanguages := make(map[string]WindowsRuntimeDependencyProduct)
	for _, entry := range entries {
		if _, ok := want[entry.Product]; !ok {
			return fmt.Errorf("runtime dependency catalog contains unexpected product %q", entry.Product)
		}
		if _, duplicate := seenProducts[entry.Product]; duplicate {
			return fmt.Errorf("runtime dependency catalog repeats product %q", entry.Product)
		}
		seenProducts[entry.Product] = struct{}{}
		if err := entry.validate(); err != nil {
			return err
		}
		for _, language := range entry.PrimaryLanguages {
			if previous, duplicate := seenLanguages[language]; duplicate {
				return fmt.Errorf("runtime dependency language %q maps to both %q and %q", language, previous, entry.Product)
			}
			seenLanguages[language] = entry.Product
		}
	}
	for product := range want {
		if _, ok := seenProducts[product]; !ok {
			return fmt.Errorf("runtime dependency catalog is missing product %q", product)
		}
	}
	return nil
}

func (entry WindowsRuntimeDependencyCatalogEntry) validate() error {
	if entry.Product == "" || len(entry.PrimaryLanguages) == 0 {
		return fmt.Errorf("runtime dependency entry %q has no product or language", entry.Product)
	}
	seenLanguages := make(map[string]struct{}, len(entry.PrimaryLanguages))
	for _, language := range entry.PrimaryLanguages {
		language = strings.ToLower(strings.TrimSpace(language))
		if language == "" {
			return fmt.Errorf("runtime dependency product %q has an empty language", entry.Product)
		}
		if _, duplicate := seenLanguages[language]; duplicate {
			return fmt.Errorf("runtime dependency product %q repeats language %q", entry.Product, language)
		}
		seenLanguages[language] = struct{}{}
	}
	if strings.TrimSpace(entry.Install.Command) == "" || strings.Contains(strings.ToLower(entry.Install.Command), "path") {
		return fmt.Errorf("runtime dependency product %q has a PATH-based install command", entry.Product)
	}
	if entry.Install.RuntimeExecutablePath == "" || entry.Install.ServerPath == "" {
		if entry.Product != WindowsRuntimeDependencyProductSwiftSourceKitLS {
			return fmt.Errorf("runtime dependency product %q has an incomplete explicit path", entry.Product)
		}
	} else {
		if _, err := runtimeDependencyRelativePath(entry.Install.RuntimeExecutablePath); err != nil {
			return fmt.Errorf("runtime dependency product %q runtime path: %w", entry.Product, err)
		}
		if _, err := runtimeDependencyRelativePath(entry.Install.ServerPath); err != nil {
			return fmt.Errorf("runtime dependency product %q server path: %w", entry.Product, err)
		}
	}
	for _, architecture := range []string{WindowsHostArchARM64, WindowsHostArchX64, WindowsHostArchX86} {
		status, ok := entry.StatusByArchitecture[architecture]
		if !ok {
			return fmt.Errorf("runtime dependency product %q is missing %s status", entry.Product, architecture)
		}
		reason := strings.TrimSpace(entry.ReasonByArchitecture[architecture])
		if (status == WindowsRuntimeDependencyStatusTypedUnsupported || status == WindowsRuntimeDependencyStatusEvidenceGap) && reason == "" {
			return fmt.Errorf("runtime dependency product %q/%s has status %q without reason", entry.Product, architecture, status)
		}
		if status != WindowsRuntimeDependencyStatusInstallable && status != WindowsRuntimeDependencyStatusTypedUnsupported && status != WindowsRuntimeDependencyStatusEvidenceGap {
			return fmt.Errorf("runtime dependency product %q/%s has invalid status %q", entry.Product, architecture, status)
		}
		for _, asset := range entry.AssetsByArchitecture[architecture] {
			if err := validateWindowsRuntimeDependencyAsset(asset, architecture, status); err != nil {
				return fmt.Errorf("runtime dependency product %q/%s component %q: %w", entry.Product, architecture, asset.Component, err)
			}
		}
	}
	return nil
}

func validateWindowsRuntimeDependencyAsset(asset WindowsRuntimeDependencyAsset, architecture string, status WindowsRuntimeDependencyCatalogStatus) error {
	if strings.TrimSpace(asset.Component) == "" || strings.TrimSpace(asset.Version) == "" || strings.EqualFold(strings.TrimSpace(asset.Version), "latest") {
		return errors.New("component and fixed version are required")
	}
	declared, err := NormalizeWindowsArchitectureAlias(asset.Architecture)
	if err != nil || declared != architecture {
		return fmt.Errorf("asset architecture %q does not match %q", asset.Architecture, architecture)
	}
	parsed, err := url.Parse(strings.TrimSpace(asset.URL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("asset URL must be an absolute HTTPS URL without query or fragment: %q", asset.URL)
	}
	if strings.Contains(strings.ToLower(asset.URL), "latest") || strings.ContainsAny(asset.URL, "{}<>") {
		return fmt.Errorf("asset URL is not a fixed official URL: %q", asset.URL)
	}
	switch asset.ChecksumAlgorithm {
	case WindowsRuntimeDependencyChecksumSHA256:
		if !runtimeDependencySHA256Pattern.MatchString(asset.Checksum) {
			return errors.New("SHA-256 checksum must contain 64 hexadecimal characters")
		}
	case WindowsRuntimeDependencyChecksumSHA512:
		if !runtimeDependencySHA512Pattern.MatchString(asset.Checksum) {
			return errors.New("SHA-512 checksum must contain 128 hexadecimal characters")
		}
	default:
		return fmt.Errorf("unsupported checksum algorithm %q", asset.ChecksumAlgorithm)
	}
	switch asset.Format {
	case WindowsRuntimeDependencyAssetFormatEXE, WindowsRuntimeDependencyAssetFormatZIP, WindowsRuntimeDependencyAssetFormatTarGz,
		WindowsRuntimeDependencyAssetFormatNupkg, WindowsRuntimeDependencyAssetFormatGem, WindowsRuntimeDependencyAssetFormatCrate, WindowsRuntimeDependencyAssetFormatSevenZip:
	default:
		return fmt.Errorf("unsupported asset format %q", asset.Format)
	}
	if asset.BinaryPath == "" {
		if !(status == WindowsRuntimeDependencyStatusEvidenceGap && asset.Component == "swift-toolchain") {
			return errors.New("binary path is required")
		}
	} else if _, err := runtimeDependencyRelativePath(asset.BinaryPath); err != nil {
		return fmt.Errorf("binary path: %w", err)
	}
	if asset.ArchivePath != "" {
		if _, err := runtimeDependencyRelativePath(asset.ArchivePath); err != nil {
			return fmt.Errorf("archive path: %w", err)
		}
	}
	if asset.MinWindowsVersion != "" {
		if _, _, err := parseWindowsVersion(asset.MinWindowsVersion); err != nil {
			return fmt.Errorf("minimum Windows version: %w", err)
		}
	}
	if asset.MinWindowsBuildKnown && asset.MinWindowsBuild == 0 {
		return errors.New("known minimum Windows build cannot be zero")
	}
	if !asset.MinWindowsBuildKnown && asset.MinWindowsBuild != 0 {
		return errors.New("unknown minimum Windows build must be zero")
	}
	return nil
}

func runtimeDependencyRelativePath(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("relative path is empty")
	}
	clean := path.Clean(strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/"))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || strings.Contains(clean, ":") {
		return "", fmt.Errorf("path is not a safe relative path: %q", raw)
	}
	return clean, nil
}

func runtimeDependencyAsset(architecture, component, version, rawURL string, algorithm WindowsRuntimeDependencyChecksumAlgorithm, checksum string, format WindowsRuntimeDependencyAssetFormat, archivePath, binaryPath string, native bool) WindowsRuntimeDependencyAsset {
	return WindowsRuntimeDependencyAsset{
		Component: component, Architecture: architecture, Version: version, URL: rawURL,
		ChecksumAlgorithm: algorithm, Checksum: checksum, Format: format,
		ArchivePath: archivePath, BinaryPath: binaryPath, MinWindowsVersion: "10.0",
		Native: native,
	}
}

func runtimeDependencyDotnetAsset(architecture, component, version, rawURL string, algorithm WindowsRuntimeDependencyChecksumAlgorithm, checksum string, format WindowsRuntimeDependencyAssetFormat, archivePath, binaryPath string, native bool) WindowsRuntimeDependencyAsset {
	asset := runtimeDependencyAsset(architecture, component, version, rawURL, algorithm, checksum, format, archivePath, binaryPath, native)
	asset.MinWindowsBuild = 14393
	asset.MinWindowsBuildKnown = true
	return asset
}

func runtimeDependencyEntry(product WindowsRuntimeDependencyProduct, languages []string, statuses map[string]WindowsRuntimeDependencyCatalogStatus, reasons map[string]string, gaps map[string][]string, assets map[string][]WindowsRuntimeDependencyAsset, install WindowsRuntimeDependencyInstallSpec) WindowsRuntimeDependencyCatalogEntry {
	return WindowsRuntimeDependencyCatalogEntry{
		Product: product, PrimaryLanguages: append([]string(nil), languages...),
		StatusByArchitecture: cloneWindowsRuntimeDependencyStatuses(statuses), ReasonByArchitecture: cloneWindowsRuntimeDependencyStrings(reasons),
		EvidenceGapsByArchitecture: cloneWindowsRuntimeDependencyStringSlices(gaps), AssetsByArchitecture: cloneWindowsRuntimeDependencyAssetsMap(assets),
		Install: WindowsRuntimeDependencyInstallSpec{Command: install.Command, RuntimeExecutablePath: install.RuntimeExecutablePath, Args: append([]string(nil), install.Args...), ServerPath: install.ServerPath},
	}
}

func runtimeDependencyGoGoplsEntry() WindowsRuntimeDependencyCatalogEntry {
	assets := map[string][]WindowsRuntimeDependencyAsset{}
	for _, architecture := range []string{WindowsHostArchARM64, WindowsHostArchX64, WindowsHostArchX86} {
		goRelease := map[string][2]string{
			WindowsHostArchARM64: {"https://go.dev/dl/go1.26.5.windows-arm64.zip", "f96ee46396d69f1e231c8d981ec6a70216238a646a1f2cd74aea0d0016bbc017"},
			WindowsHostArchX64:   {"https://go.dev/dl/go1.26.5.windows-amd64.zip", "97e6b2a833b6d89f9ff17d25419ac0a7e3b482a044e9ab18cdef834bd834fd38"},
			WindowsHostArchX86:   {"https://go.dev/dl/go1.26.5.windows-386.zip", "cab0f6847c17f4c904c0bacb6ec6b84e730fc797f4ba885f42383d580fc2d399"},
		}[architecture]
		assets[architecture] = []WindowsRuntimeDependencyAsset{
			runtimeDependencyAsset(architecture, "go", "1.26.5", goRelease[0], WindowsRuntimeDependencyChecksumSHA256, goRelease[1], WindowsRuntimeDependencyAssetFormatZIP, "go/bin/go.exe", "go/bin/go.exe", true),
			runtimeDependencyAsset(architecture, "gopls", "0.23.0", "https://proxy.golang.org/golang.org/x/tools/gopls/@v/v0.23.0.zip", WindowsRuntimeDependencyChecksumSHA256, "b3bb593ef163f614e358cdb14a9feede3cad2bfc9087b8e4dca73b2fff858b74", WindowsRuntimeDependencyAssetFormatZIP, "gopls@v0.23.0/go.mod", "gopls@v0.23.0/go.mod", false),
		}
	}
	return runtimeDependencyEntry(WindowsRuntimeDependencyProductGoGopls, []string{"go", "gomod", "gosum", "gowork"},
		map[string]WindowsRuntimeDependencyCatalogStatus{WindowsHostArchARM64: WindowsRuntimeDependencyStatusInstallable, WindowsHostArchX64: WindowsRuntimeDependencyStatusInstallable, WindowsHostArchX86: WindowsRuntimeDependencyStatusInstallable},
		map[string]string{}, map[string][]string{WindowsHostArchARM64: {"Go's official minimum requirements state Windows 10 but do not publish an exact host build"}, WindowsHostArchX64: {"Go's official minimum requirements state Windows 10 but do not publish an exact host build"}, WindowsHostArchX86: {"Go's official minimum requirements state Windows 10 but do not publish an exact host build"}}, assets,
		WindowsRuntimeDependencyInstallSpec{Command: "go install", RuntimeExecutablePath: "go/bin/go.exe", Args: []string{"install", "golang.org/x/tools/gopls@v0.23.0"}, ServerPath: "bin/gopls.exe"})
}

func runtimeDependencyDotnetCsharpLSEntry() WindowsRuntimeDependencyCatalogEntry {
	assets := map[string][]WindowsRuntimeDependencyAsset{
		WindowsHostArchARM64: {
			runtimeDependencyDotnetAsset(WindowsHostArchARM64, "dotnet-sdk", "10.0.400", "https://builds.dotnet.microsoft.com/dotnet/Sdk/10.0.400/dotnet-sdk-10.0.400-win-arm64.zip", WindowsRuntimeDependencyChecksumSHA512, "9d4ecd7439f15c7797d6f46d368cb7aa6513755c5fc3d6de7621bc4878a1805f6b8ffb60ffb9d3e72a049cca87edb252f7c8c03023b643e333544c4606509d7f", WindowsRuntimeDependencyAssetFormatZIP, "dotnet.exe", "dotnet.exe", true),
			runtimeDependencyDotnetAsset(WindowsHostArchARM64, "dotnet-sdk-net8", "8.0.424", "https://builds.dotnet.microsoft.com/dotnet/Sdk/8.0.424/dotnet-sdk-8.0.424-win-arm64.zip", WindowsRuntimeDependencyChecksumSHA512, "fcabd5dfd8587610fce8619827f54ff4d4e8f64b30c161e56e3597e391126621ff1888d83bbfca121b02dab7cc2d9dac68a49b06045320e5a88b5c5ff7bb5eb9", WindowsRuntimeDependencyAssetFormatZIP, "dotnet.exe", "dotnet.exe", true),
			runtimeDependencyDotnetAsset(WindowsHostArchARM64, "csharp-ls", "0.26.0", "https://api.nuget.org/v3-flatcontainer/csharp-ls/0.26.0/csharp-ls.0.26.0.nupkg", WindowsRuntimeDependencyChecksumSHA256, "2b03987aef07bb708bfe56a7bfb370364c7c8203e69aa677a37594bbe21a15b0", WindowsRuntimeDependencyAssetFormatNupkg, "tools/net10.0/any/DotnetToolSettings.xml", "tools/net10.0/any/CSharpLanguageServer.dll", false),
		},
		WindowsHostArchX64: {
			runtimeDependencyDotnetAsset(WindowsHostArchX64, "dotnet-sdk", "10.0.400", "https://builds.dotnet.microsoft.com/dotnet/Sdk/10.0.400/dotnet-sdk-10.0.400-win-x64.zip", WindowsRuntimeDependencyChecksumSHA512, "9b8b88590e4da131bfd0da7aa089d0fc04d5418d5f8607ec13d55dc5a17b4399afd54d496c12657fa05c6c6546dc5eab930f26ac6c50f2d3a7712c0fb378c366", WindowsRuntimeDependencyAssetFormatZIP, "dotnet.exe", "dotnet.exe", true),
			runtimeDependencyDotnetAsset(WindowsHostArchX64, "dotnet-sdk-net8", "8.0.424", "https://builds.dotnet.microsoft.com/dotnet/Sdk/8.0.424/dotnet-sdk-8.0.424-win-x64.zip", WindowsRuntimeDependencyChecksumSHA512, "1787ab90635c2950672ed7c6507b000e1b212ea7d9a22fcef37061344d37c64d4c4eda12b8742601eff5b45c8736485b31c55613892f240c300190e4e88a58b0", WindowsRuntimeDependencyAssetFormatZIP, "dotnet.exe", "dotnet.exe", true),
			runtimeDependencyDotnetAsset(WindowsHostArchX64, "csharp-ls", "0.26.0", "https://api.nuget.org/v3-flatcontainer/csharp-ls/0.26.0/csharp-ls.0.26.0.nupkg", WindowsRuntimeDependencyChecksumSHA256, "2b03987aef07bb708bfe56a7bfb370364c7c8203e69aa677a37594bbe21a15b0", WindowsRuntimeDependencyAssetFormatNupkg, "tools/net10.0/any/DotnetToolSettings.xml", "tools/net10.0/any/CSharpLanguageServer.dll", false),
		},
		WindowsHostArchX86: {
			runtimeDependencyDotnetAsset(WindowsHostArchX86, "dotnet-sdk", "10.0.400", "https://builds.dotnet.microsoft.com/dotnet/Sdk/10.0.400/dotnet-sdk-10.0.400-win-x86.zip", WindowsRuntimeDependencyChecksumSHA512, "d24d81e1fc5a5a0afa3dedad0ba3e44b0d1a6e512399ccd2dbf923d6aca3be28867870d615569ac0b06c32da2a54b27cd86a4ca0cc6ca17c3e1ad2c7f83b82d3", WindowsRuntimeDependencyAssetFormatZIP, "dotnet.exe", "dotnet.exe", true),
			runtimeDependencyDotnetAsset(WindowsHostArchX86, "dotnet-sdk-net8", "8.0.424", "https://builds.dotnet.microsoft.com/dotnet/Sdk/8.0.424/dotnet-sdk-8.0.424-win-x86.zip", WindowsRuntimeDependencyChecksumSHA512, "5621713018be8f3dbded843dc77fc07fd72ef61aef6301a2f6405e964bfbd547701cf003e834985cae2b257110f6c7a226c3e66a805d918beb5cdec2388f2093", WindowsRuntimeDependencyAssetFormatZIP, "dotnet.exe", "dotnet.exe", true),
			runtimeDependencyDotnetAsset(WindowsHostArchX86, "csharp-ls", "0.26.0", "https://api.nuget.org/v3-flatcontainer/csharp-ls/0.26.0/csharp-ls.0.26.0.nupkg", WindowsRuntimeDependencyChecksumSHA256, "2b03987aef07bb708bfe56a7bfb370364c7c8203e69aa677a37594bbe21a15b0", WindowsRuntimeDependencyAssetFormatNupkg, "tools/net10.0/any/DotnetToolSettings.xml", "tools/net10.0/any/CSharpLanguageServer.dll", false),
		},
	}
	return runtimeDependencyEntry(WindowsRuntimeDependencyProductDotnetCsharpLS, []string{"csharp"},
		map[string]WindowsRuntimeDependencyCatalogStatus{WindowsHostArchARM64: WindowsRuntimeDependencyStatusInstallable, WindowsHostArchX64: WindowsRuntimeDependencyStatusInstallable, WindowsHostArchX86: WindowsRuntimeDependencyStatusInstallable},
		map[string]string{}, map[string][]string{}, assets,
		WindowsRuntimeDependencyInstallSpec{Command: "dotnet tool install", RuntimeExecutablePath: "dotnet.exe", Args: []string{"tool", "install", "--tool-path", "tools", "--version", "0.26.0", "csharp-ls"}, ServerPath: "tools/csharp-ls.exe"})
}

func runtimeDependencyJDKJDTLSEntry() WindowsRuntimeDependencyCatalogEntry {
	jdtls := runtimeDependencyAsset(WindowsHostArchX64, "jdtls", "1.60.0", "https://download.eclipse.org/jdtls/milestones/1.60.0/jdt-language-server-1.60.0-202606262232.tar.gz", WindowsRuntimeDependencyChecksumSHA256, "e94c303d8198f977930803582738771fd18c52c5492878410bf222b1aa81ef1d", WindowsRuntimeDependencyAssetFormatTarGz, "plugins/org.eclipse.equinox.launcher_1.7.200.v20260619-2039.jar", "plugins/org.eclipse.equinox.launcher_1.7.200.v20260619-2039.jar", true)
	jdtlsARM64 := runtimeDependencyAsset(WindowsHostArchARM64, "jdtls", "1.60.0", "https://download.eclipse.org/jdtls/milestones/1.60.0/jdt-language-server-1.60.0-202606262232.tar.gz", WindowsRuntimeDependencyChecksumSHA256, "e94c303d8198f977930803582738771fd18c52c5492878410bf222b1aa81ef1d", WindowsRuntimeDependencyAssetFormatTarGz, "plugins/org.eclipse.equinox.launcher_1.7.200.v20260619-2039.jar", "plugins/org.eclipse.equinox.launcher_1.7.200.v20260619-2039.jar", true)
	assets := map[string][]WindowsRuntimeDependencyAsset{
		WindowsHostArchARM64: {
			runtimeDependencyAsset(WindowsHostArchARM64, "jdk", "21.0.12", "https://aka.ms/download-jdk/microsoft-jdk-21.0.12-windows-aarch64.zip", WindowsRuntimeDependencyChecksumSHA256, "2118bb60b19002a0bcc420267518352f10d2be25ce1c79c51701b87b209bbc2a", WindowsRuntimeDependencyAssetFormatZIP, "jdk-21.0.12+8/bin/java.exe", "jdk-21.0.12+8/bin/java.exe", true),
			jdtlsARM64,
		},
		WindowsHostArchX64: {
			runtimeDependencyAsset(WindowsHostArchX64, "jdk", "21.0.12", "https://aka.ms/download-jdk/microsoft-jdk-21.0.12-windows-x64.zip", WindowsRuntimeDependencyChecksumSHA256, "bf27a5d6298c736af8daf5b8c883098e83291446e5766118d8a5ea6a2617195d", WindowsRuntimeDependencyAssetFormatZIP, "jdk-21.0.12+8/bin/java.exe", "jdk-21.0.12+8/bin/java.exe", true),
			jdtls,
		},
	}
	return runtimeDependencyEntry(WindowsRuntimeDependencyProductJDKJDTLS, []string{"java"},
		map[string]WindowsRuntimeDependencyCatalogStatus{WindowsHostArchARM64: WindowsRuntimeDependencyStatusInstallable, WindowsHostArchX64: WindowsRuntimeDependencyStatusInstallable, WindowsHostArchX86: WindowsRuntimeDependencyStatusTypedUnsupported},
		map[string]string{
			WindowsHostArchARM64: "Windows ARM64 E2E passed with Microsoft JDK 21.0.12, JDTLS 1.60.0, absolute -jar/-configuration, mutable -data workspace, and file URI",
			WindowsHostArchX86:   "Microsoft JDK 21.0.12 publishes Windows x64 and AArch64 ZIPs but no native Windows x86 JDK",
		},
		map[string][]string{
			WindowsHostArchX64: {"JDK/JDTLS upstream pages state Windows 10 or later but do not publish an exact host build"},
		}, assets,
		WindowsRuntimeDependencyInstallSpec{Command: "java", RuntimeExecutablePath: "jdk-21.0.12+8/bin/java.exe", Args: []string{"-Declipse.application=org.eclipse.jdt.ls.core.id1", "-Dosgi.bundles.defaultStartLevel=4", "-Declipse.product=org.eclipse.jdt.ls.core.product", "-Dlog.protocol=true", "-Dlog.level=ALL", "-jar", "plugins/org.eclipse.equinox.launcher_1.7.200.v20260619-2039.jar", "-configuration", "config_win"}, ServerPath: "plugins/org.eclipse.equinox.launcher_1.7.200.v20260619-2039.jar"})
}

func runtimeDependencyRubySolargraphEntry() WindowsRuntimeDependencyCatalogEntry {
	solargraph := func(architecture string) WindowsRuntimeDependencyAsset {
		return runtimeDependencyAsset(architecture, "solargraph", "0.60.2", "https://rubygems.org/gems/solargraph-0.60.2.gem", WindowsRuntimeDependencyChecksumSHA256, "35c8fb31fcdbe8ccd0e0e84862a65b8deb319f86210c5966e41e2fc011e52538", WindowsRuntimeDependencyAssetFormatGem, "", "bin/solargraph", false)
	}
	assets := map[string][]WindowsRuntimeDependencyAsset{
		WindowsHostArchARM64: {
			runtimeDependencyAsset(WindowsHostArchARM64, "ruby", "4.0.5-1", "https://github.com/oneclick/rubyinstaller2/releases/download/RubyInstaller-4.0.5-1/rubyinstaller-4.0.5-1-arm.7z", WindowsRuntimeDependencyChecksumSHA256, "c7c6bcd0b070bf7c2e0c03e70fb9754d022b8a216ebc4befab880874c6180b51", WindowsRuntimeDependencyAssetFormatSevenZip, "rubyinstaller-4.0.5-1-arm/bin/ruby.exe", "rubyinstaller-4.0.5-1-arm/bin/ruby.exe", true),
			solargraph(WindowsHostArchARM64),
		},
		WindowsHostArchX64: {
			runtimeDependencyAsset(WindowsHostArchX64, "ruby", "4.0.5-1", "https://github.com/oneclick/rubyinstaller2/releases/download/RubyInstaller-4.0.5-1/rubyinstaller-4.0.5-1-x64.7z", WindowsRuntimeDependencyChecksumSHA256, "74e31613fc71e6e23431dfc4d8b6ec2818a4dc1fd16e0983b074144c16719c8b", WindowsRuntimeDependencyAssetFormatSevenZip, "rubyinstaller-4.0.5-1-x64/bin/ruby.exe", "rubyinstaller-4.0.5-1-x64/bin/ruby.exe", true),
			solargraph(WindowsHostArchX64),
		},
		WindowsHostArchX86: {
			runtimeDependencyAsset(WindowsHostArchX86, "ruby", "3.4.10-1", "https://github.com/oneclick/rubyinstaller2/releases/download/RubyInstaller-3.4.10-1/rubyinstaller-3.4.10-1-x86.7z", WindowsRuntimeDependencyChecksumSHA256, "be323ac7b8342de16edcceb1ee04a90023c39aa7e7a544e628c6360fffb602da", WindowsRuntimeDependencyAssetFormatSevenZip, "rubyinstaller-3.4.10-1-x86/bin/ruby.exe", "rubyinstaller-3.4.10-1-x86/bin/ruby.exe", true),
			solargraph(WindowsHostArchX86),
		},
	}
	return runtimeDependencyEntry(WindowsRuntimeDependencyProductRubySolargraph, []string{"ruby-solargraph"},
		map[string]WindowsRuntimeDependencyCatalogStatus{WindowsHostArchARM64: WindowsRuntimeDependencyStatusEvidenceGap, WindowsHostArchX64: WindowsRuntimeDependencyStatusEvidenceGap, WindowsHostArchX86: WindowsRuntimeDependencyStatusEvidenceGap},
		map[string]string{
			WindowsHostArchARM64: "Solargraph 0.60.2 requires native prism and jaro_winkler gems; the real ARM64 empty-cache install emitted compiler/Devkit-required errors and no fixed ARM64 native gem or Devkit asset is cataloged",
			WindowsHostArchX64:   "Solargraph 0.60.2 requires native prism and jaro_winkler gems; this catalog does not lock fixed x64 native gems or a Devkit/compiler, so safe installation is unproven",
			WindowsHostArchX86:   "Solargraph 0.60.2 requires native prism and jaro_winkler gems; this catalog does not lock fixed x86 native gems or a Devkit/compiler, so safe installation is unproven",
		},
		map[string][]string{
			WindowsHostArchARM64: {"Solargraph 0.60.2 gemspec requires prism and jaro_winkler", "real ARM64 stderr requires native-extension compiler/Devkit", "no fixed ARM64 native dependency assets or Devkit asset"},
			WindowsHostArchX64:   {"Solargraph 0.60.2 gemspec requires prism and jaro_winkler", "no fixed x64 native dependency assets or Devkit asset"},
			WindowsHostArchX86:   {"Solargraph 0.60.2 gemspec requires prism and jaro_winkler", "no fixed x86 native dependency assets or Devkit asset"},
		}, assets,
		WindowsRuntimeDependencyInstallSpec{Command: "gem install --local", RuntimeExecutablePath: "bin/ruby.exe", Args: []string{"install", "--local", "--install-dir", "gems", "--bindir", "bin", "--no-document"}, ServerPath: "bin/solargraph"})
}

func runtimeDependencyRubyLSPEntry() WindowsRuntimeDependencyCatalogEntry {
	assets := map[string][]WindowsRuntimeDependencyAsset{
		WindowsHostArchARM64: {
			runtimeDependencyAsset(WindowsHostArchARM64, "ruby", "4.0.5-1", "https://github.com/oneclick/rubyinstaller2/releases/download/RubyInstaller-4.0.5-1/rubyinstaller-4.0.5-1-arm.7z", WindowsRuntimeDependencyChecksumSHA256, "c7c6bcd0b070bf7c2e0c03e70fb9754d022b8a216ebc4befab880874c6180b51", WindowsRuntimeDependencyAssetFormatSevenZip, "rubyinstaller-4.0.5-1-arm/bin/ruby.exe", "rubyinstaller-4.0.5-1-arm/bin/ruby.exe", true),
			runtimeDependencyAsset(WindowsHostArchARM64, "ruby-lsp", windowsRubyLSPVersion, "https://rubygems.org/gems/ruby-lsp-0.26.10.gem", WindowsRuntimeDependencyChecksumSHA256, "e67284af94423531f6b9a583350596421b5a6a4dd93083f1c2ba03da7c23bbed", WindowsRuntimeDependencyAssetFormatGem, "", "gems/ruby-lsp-0.26.10/exe/ruby-lsp", false),
			runtimeDependencyAsset(WindowsHostArchARM64, "language-server-protocol", windowsRubyLSPProtocolVersion, "https://rubygems.org/gems/language_server-protocol-3.17.0.0.gem", WindowsRuntimeDependencyChecksumSHA256, "eaf5cac33c5f0cc7fff7f1192165c93b0bfee757fd2c81e2f071a3b2afbe9c54", WindowsRuntimeDependencyAssetFormatGem, "", "gems/language_server-protocol-3.17.0.0/lib/language_server/protocol.rb", false),
		},
	}
	return runtimeDependencyEntry(WindowsRuntimeDependencyProductRubyLSP, []string{"ruby"},
		map[string]WindowsRuntimeDependencyCatalogStatus{
			WindowsHostArchARM64: WindowsRuntimeDependencyStatusInstallable,
			WindowsHostArchX64:   WindowsRuntimeDependencyStatusTypedUnsupported,
			WindowsHostArchX86:   WindowsRuntimeDependencyStatusTypedUnsupported,
		},
		map[string]string{
			WindowsHostArchX64: "Ruby LSP is proven only with the native RubyInstaller ARM64 closure; no x64 fallback is allowed",
			WindowsHostArchX86: "Ruby LSP has no locked native RubyInstaller ARM64 closure for Windows x86; no emulation or fallback is allowed",
		},
		map[string][]string{
			WindowsHostArchX64: {"only the ARM64 RubyInstaller and fixed Ruby LSP gems are locked", "cross-architecture Ruby binaries are forbidden"},
			WindowsHostArchX86: {"only the ARM64 RubyInstaller and fixed Ruby LSP gems are locked", "cross-architecture Ruby binaries are forbidden"},
		}, assets,
		WindowsRuntimeDependencyInstallSpec{
			Command:               "RubyInstaller 4.0.5-1 ARM64 + Ruby LSP 0.26.10 offline gem install",
			RuntimeExecutablePath: "rubyinstaller-4.0.5-1-arm/bin/ruby.exe",
			Args:                  []string{"install", "--local", "--ignore-dependencies", "--install-dir", "gems", "--no-document"},
			ServerPath:            "gems/gems/ruby-lsp-0.26.10/exe/ruby-lsp",
		})
}

func runtimeDependencySwiftSourceKitLSPEntry() WindowsRuntimeDependencyCatalogEntry {
	assets := map[string][]WindowsRuntimeDependencyAsset{
		WindowsHostArchARM64: {runtimeDependencyAsset(WindowsHostArchARM64, "swift-toolchain", "6.3.3", "https://download.swift.org/swift-6.3.3-release/windows10-arm64/swift-6.3.3-RELEASE/swift-6.3.3-RELEASE-windows10-arm64.exe", WindowsRuntimeDependencyChecksumSHA256, "09e39c60f0b05d00fbe5f55b2d344752ccbc86e64802a2d896c0d55bc51e243d", WindowsRuntimeDependencyAssetFormatEXE, "", swiftSourceKitLSPServerPath, true)},
		WindowsHostArchX64:   {runtimeDependencyAsset(WindowsHostArchX64, "swift-toolchain", "6.3.3", "https://download.swift.org/swift-6.3.3-release/windows10/swift-6.3.3-RELEASE/swift-6.3.3-RELEASE-windows10.exe", WindowsRuntimeDependencyChecksumSHA256, "235626548f249cd516d3d4d90eee980dccad46f3822dac1f8e3119b0fede94b7", WindowsRuntimeDependencyAssetFormatEXE, "", swiftSourceKitLSPServerPath, true)},
	}
	return runtimeDependencyEntry(WindowsRuntimeDependencyProductSwiftSourceKitLS, []string{"swift"},
		map[string]WindowsRuntimeDependencyCatalogStatus{WindowsHostArchARM64: WindowsRuntimeDependencyStatusInstallable, WindowsHostArchX64: WindowsRuntimeDependencyStatusEvidenceGap, WindowsHostArchX86: WindowsRuntimeDependencyStatusTypedUnsupported},
		map[string]string{WindowsHostArchX64: "Swift 6.3.3 has an official x64 installer, but this recipe is only proven for the ARM64 embedded payload set; no cross-architecture fallback is allowed", WindowsHostArchX86: "Swift's official Windows release page publishes x86_64 and arm64 installers only; no native x86 asset exists"},
		map[string][]string{WindowsHostArchX64: {"the locked embedded MSI/CAB payload checksums in this recipe are ARM64-only", "the x64 installer has not been independently materialized and verified"}}, assets,
		WindowsRuntimeDependencyInstallSpec{Command: "Swift 6.3.3 embedded MSI/CAB extraction", RuntimeExecutablePath: swiftCompilerPath, Args: []string{"bld.asserts.msi", "cli.asserts.msi", "ide.asserts.msi", "rtl.msi", "windows.msi", "ide.asserts.cab", "windows.cab", "a22", "a23", "a24", "a25", "a26", "a27", "a28"}, ServerPath: swiftSourceKitLSPServerPath})
}

func runtimeDependencyGoSQLSEntry() WindowsRuntimeDependencyCatalogEntry {
	const (
		moduleURL      = "https://proxy.golang.org/github.com/sqls-server/sqls/@v/v0.2.48.zip"
		moduleChecksum = "2CEF077F0432DD264E4B8A85348F887DBF508C601B4B0CC7BDB27B4E566DB1F2"
	)
	goRelease := map[string][2]string{
		WindowsHostArchARM64: {"https://go.dev/dl/go1.26.5.windows-arm64.zip", "f96ee46396d69f1e231c8d981ec6a70216238a646a1f2cd74aea0d0016bbc017"},
		WindowsHostArchX64:   {"https://go.dev/dl/go1.26.5.windows-amd64.zip", "97e6b2a833b6d89f9ff17d25419ac0a7e3b482a044e9ab18cdef834bd834fd38"},
		WindowsHostArchX86:   {"https://go.dev/dl/go1.26.5.windows-386.zip", "cab0f6847c17f4c904c0bacb6ec6b84e730fc797f4ba885f42383d580fc2d399"},
	}
	assets := make(map[string][]WindowsRuntimeDependencyAsset, 3)
	for _, architecture := range []string{WindowsHostArchARM64, WindowsHostArchX64, WindowsHostArchX86} {
		goAsset := goRelease[architecture]
		assets[architecture] = []WindowsRuntimeDependencyAsset{
			runtimeDependencyAsset(architecture, "go", "1.26.5", goAsset[0], WindowsRuntimeDependencyChecksumSHA256, goAsset[1], WindowsRuntimeDependencyAssetFormatZIP, "go/bin/go.exe", "go/bin/go.exe", true),
			runtimeDependencyAsset(architecture, "sqls-source", "0.2.48", moduleURL, WindowsRuntimeDependencyChecksumSHA256, moduleChecksum, WindowsRuntimeDependencyAssetFormatZIP, "github.com/sqls-server/sqls@v0.2.48/go.mod", "github.com/sqls-server/sqls@v0.2.48/go.mod", false),
		}
	}
	return runtimeDependencyEntry(WindowsRuntimeDependencyProductGoSQLS, []string{"sql"},
		map[string]WindowsRuntimeDependencyCatalogStatus{WindowsHostArchARM64: WindowsRuntimeDependencyStatusInstallable, WindowsHostArchX64: WindowsRuntimeDependencyStatusInstallable, WindowsHostArchX86: WindowsRuntimeDependencyStatusInstallable},
		map[string]string{},
		map[string][]string{
			WindowsHostArchARM64: {"Go 1.26.5 and SQLS v0.2.48 are source-built with CGO_ENABLED=0 for Windows ARM64; Oracle is excluded by the cgo build tag"},
			WindowsHostArchX64:   {"Go 1.26.5 and SQLS v0.2.48 are source-built with CGO_ENABLED=0 for Windows x64; Oracle is excluded by the cgo build tag"},
			WindowsHostArchX86:   {"Go 1.26.5 and SQLS v0.2.48 are source-built with CGO_ENABLED=0 for Windows x86; Oracle is excluded by the cgo build tag"},
		}, assets,
		WindowsRuntimeDependencyInstallSpec{
			Command:               "Go 1.26.5 source build",
			RuntimeExecutablePath: "bin/sqls.exe",
			Args:                  []string{"build", "-trimpath", "-mod=readonly", "-o", "bin/sqls.exe", "./"},
			ServerPath:            "bin/sqls.exe",
		})
}

func cloneWindowsRuntimeDependencyAssets(assets []WindowsRuntimeDependencyAsset) []WindowsRuntimeDependencyAsset {
	return append([]WindowsRuntimeDependencyAsset(nil), assets...)
}

func cloneWindowsRuntimeDependencyAssetsMap(assets map[string][]WindowsRuntimeDependencyAsset) map[string][]WindowsRuntimeDependencyAsset {
	clone := make(map[string][]WindowsRuntimeDependencyAsset, len(assets))
	for architecture, candidates := range assets {
		clone[architecture] = cloneWindowsRuntimeDependencyAssets(candidates)
	}
	return clone
}

func cloneWindowsRuntimeDependencyStatuses(statuses map[string]WindowsRuntimeDependencyCatalogStatus) map[string]WindowsRuntimeDependencyCatalogStatus {
	clone := make(map[string]WindowsRuntimeDependencyCatalogStatus, len(statuses))
	for architecture, status := range statuses {
		clone[architecture] = status
	}
	return clone
}

func cloneWindowsRuntimeDependencyStrings(values map[string]string) map[string]string {
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func cloneWindowsRuntimeDependencyStringSlices(values map[string][]string) map[string][]string {
	clone := make(map[string][]string, len(values))
	for key, value := range values {
		clone[key] = append([]string(nil), value...)
	}
	return clone
}

// WindowsRuntimeDependencyCatalogProducts 返回目录中按稳定顺序排列的 Windows 产品集合；该读取操作不联网、不写盘。
func WindowsRuntimeDependencyCatalogProducts() []WindowsRuntimeDependencyProduct {
	products := make([]WindowsRuntimeDependencyProduct, 0, len(WindowsRuntimeDependencyCatalog()))
	for _, entry := range WindowsRuntimeDependencyCatalog() {
		products = append(products, entry.Product)
	}
	sort.Slice(products, func(i, j int) bool { return products[i] < products[j] })
	return products
}
