//go:build windows

package installer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// WindowsProvisionRequest 描述 Windows 原生 LSP 物化请求；缓存、下载和写盘均由调用方明确拥有，失败立即返回。
type WindowsProvisionRequest struct {
	// Product 选择 Windows 锁定目录中的产品；只按 Windows NativeArch 选择同架构资产。
	Product WindowsLSPProduct
	// Language 可选地按语言 ID 解析唯一 Windows 产品；与 Product 冲突时在联网前失败。
	Language string
	// Cache 是调用方拥有的 Windows 资产缓存；下载、校验、解包和原子发布都写入此缓存。
	Cache *WindowsAssetCache
	// CacheRoot 是 Cache 为空时使用的显式 Windows 缓存目录；为空会在网络或写盘前失败。
	CacheRoot string
	// CacheOptions 设置 Windows 下载客户端和大小/超时限制；不能启用 PATH 或跨架构回退。
	CacheOptions WindowsAssetCacheOptions
}

// WindowsProvisionResult 描述已校验并发布的 Windows 原生 LSP 启动契约；Executable 必须直接启动，禁止 PATH 回退。
type WindowsProvisionResult struct {
	// Product 是实际物化的 Windows 锁定产品。
	Product WindowsLSPProduct
	// Language 是解析出的规范语言 ID。
	Language string
	// Languages 是该产品可服务语言的独立副本。
	Languages []string
	// Platform 记录 Windows 版本、build、NativeArch 和诊断用 ProcessArch；资产只由 NativeArch 选择。
	Platform WindowsHostPlatform
	// Asset 是下载前已锁定并在发布前验证的 URL、版本、SHA256、格式和路径。
	Asset WindowsLockedAsset
	// Executable 是 ready 树中的绝对 Windows 可执行文件路径；失败时不会退回 PATH、模拟或其他架构。
	Executable string
	// Args 是无需 shell 解析的 Windows 产品专用 LSP/stdio 参数向量。
	Args []string
	// Env 是显式 Windows 环境增量；为空表示继承环境但绝不注入 PATH 回退。
	Env []string
}

// WindowsProvision 检测真实 Windows 主机事实并物化一个锁定的原生 LSP；下载、校验、解包、原子发布和失败均 fail-fast。
func WindowsProvision(ctx context.Context, request WindowsProvisionRequest) (WindowsProvisionResult, error) {
	if err := ensureWindowsCatalogProductionBranch(); err != nil {
		return WindowsProvisionResult{}, err
	}
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return WindowsProvisionResult{}, fmt.Errorf("detect host platform for Windows LSP provisioning: %w", err)
	}
	return WindowsProvisionForPlatform(ctx, request, platform)
}

// WindowsProvisionForPlatform 按给定 Windows 主机事实物化锁定资产；测试适配器可提供事实，生产入口应调用 WindowsProvision。
func WindowsProvisionForPlatform(ctx context.Context, request WindowsProvisionRequest, platform WindowsHostPlatform) (WindowsProvisionResult, error) {
	entry, err := resolveProvisionEntry(request)
	if err != nil {
		return WindowsProvisionResult{}, err
	}
	if platform.OS != WindowsHostOSWindows {
		return WindowsProvisionResult{}, fmt.Errorf("provision Windows LSP %q: %w: got %q", entry.Product, ErrUnsupportedWindowsHostPlatform, platform.OS)
	}
	cache, err := provisionCache(request)
	if err != nil {
		return WindowsProvisionResult{}, err
	}
	asset, err := entry.Manifest.AssetForPlatform(platform)
	if err != nil {
		return WindowsProvisionResult{}, fmt.Errorf("select Windows LSP product %q: %w", entry.Product, err)
	}
	executable, err := cache.ensureAsset(ctx, entry.Manifest.Name, asset)
	if err != nil {
		return WindowsProvisionResult{}, fmt.Errorf("materialize Windows LSP product %q: %w", entry.Product, err)
	}
	if strings.TrimSpace(executable) == "" {
		return WindowsProvisionResult{}, fmt.Errorf("materialize Windows LSP product %q returned an empty executable path", entry.Product)
	}
	args, env, err := windowsLSPCommandMetadata(entry.Product)
	if err != nil {
		return WindowsProvisionResult{}, err
	}
	language := strings.ToLower(strings.TrimSpace(request.Language))
	if language == "" {
		language = entry.PrimaryLanguages[0]
	}
	return WindowsProvisionResult{
		Product:    entry.Product,
		Language:   language,
		Languages:  append([]string(nil), entry.PrimaryLanguages...),
		Platform:   platform,
		Asset:      asset,
		Executable: executable,
		Args:       args,
		Env:        env,
	}, nil
}

// WindowsProvisionProduct 通过显式 WindowsAssetCache 物化一个 Windows 产品，并返回不可替换的 ready 路径和启动参数。
func WindowsProvisionProduct(ctx context.Context, product WindowsLSPProduct, cache *WindowsAssetCache) (WindowsProvisionResult, error) {
	return WindowsProvision(ctx, WindowsProvisionRequest{Product: product, Cache: cache})
}

// Provision 通过该 WindowsAssetCache 物化 Windows 产品；调用方不能覆盖缓存，失败不会 PATH 回退。
func (c *WindowsAssetCache) Provision(ctx context.Context, request WindowsProvisionRequest) (WindowsProvisionResult, error) {
	if c == nil {
		return WindowsProvisionResult{}, errors.New("Windows LSP provision cache is nil")
	}
	if request.Cache != nil || strings.TrimSpace(request.CacheRoot) != "" {
		return WindowsProvisionResult{}, errors.New("cache-bound provision request cannot override its cache")
	}
	request.Cache = c
	return WindowsProvision(ctx, request)
}

// ProvisionForPlatform 通过该 WindowsAssetCache 使用显式 Windows 主机事实物化产品，不按 ProcessArch 或 PATH 回退。
func (c *WindowsAssetCache) ProvisionForPlatform(ctx context.Context, request WindowsProvisionRequest, platform WindowsHostPlatform) (WindowsProvisionResult, error) {
	if c == nil {
		return WindowsProvisionResult{}, errors.New("Windows LSP provision cache is nil")
	}
	if request.Cache != nil || strings.TrimSpace(request.CacheRoot) != "" {
		return WindowsProvisionResult{}, errors.New("cache-bound provision request cannot override its cache")
	}
	request.Cache = c
	return WindowsProvisionForPlatform(ctx, request, platform)
}

// WindowsProvisioner 持有一个显式 WindowsAssetCache；缓存生命周期、下载和原子发布由该拥有者负责。
type WindowsProvisioner struct {
	cache *WindowsAssetCache
}

// NewWindowsProvisioner 创建绑定显式 WindowsAssetCache 的 WindowsProvisioner；nil 缓存立即 fail-fast。
func NewWindowsProvisioner(cache *WindowsAssetCache) (*WindowsProvisioner, error) {
	if cache == nil {
		return nil, errors.New("Windows LSP provisioner cache is nil")
	}
	return &WindowsProvisioner{cache: cache}, nil
}

// Provision 使用 WindowsProvisioner 的显式缓存物化请求，并返回 ready 路径和启动参数。
func (p *WindowsProvisioner) Provision(ctx context.Context, request WindowsProvisionRequest) (WindowsProvisionResult, error) {
	if p == nil || p.cache == nil {
		return WindowsProvisionResult{}, errors.New("Windows LSP provisioner cache is nil")
	}
	if request.Cache != nil || strings.TrimSpace(request.CacheRoot) != "" {
		return WindowsProvisionResult{}, errors.New("provisioner request cannot override its explicit cache")
	}
	request.Cache = p.cache
	return WindowsProvision(ctx, request)
}

// ProvisionForPlatform 使用 WindowsProvisioner 的显式缓存和给定 Windows 主机事实物化请求，不使用 ProcessArch/PATH 回退。
func (p *WindowsProvisioner) ProvisionForPlatform(ctx context.Context, request WindowsProvisionRequest, platform WindowsHostPlatform) (WindowsProvisionResult, error) {
	if p == nil || p.cache == nil {
		return WindowsProvisionResult{}, errors.New("Windows LSP provisioner cache is nil")
	}
	if request.Cache != nil || strings.TrimSpace(request.CacheRoot) != "" {
		return WindowsProvisionResult{}, errors.New("provisioner request cannot override its explicit cache")
	}
	request.Cache = p.cache
	return WindowsProvisionForPlatform(ctx, request, platform)
}

func resolveProvisionEntry(request WindowsProvisionRequest) (WindowsLSPCatalogEntry, error) {
	productID := normalizeProvisionProduct(request.Product)
	language := strings.ToLower(strings.TrimSpace(request.Language))
	if productID == "" && language == "" {
		return WindowsLSPCatalogEntry{}, errors.New("Windows LSP provision request requires Product or Language")
	}
	var entry WindowsLSPCatalogEntry
	var err error
	if productID != "" {
		entry, err = WindowsLSPCatalogEntryForProduct(productID)
	} else {
		entry, err = WindowsLSPCatalogEntryForLanguage(language)
	}
	if err != nil {
		return WindowsLSPCatalogEntry{}, err
	}
	if language != "" {
		matched := false
		for _, candidate := range entry.PrimaryLanguages {
			if strings.EqualFold(candidate, language) {
				matched = true
				break
			}
		}
		if !matched {
			return WindowsLSPCatalogEntry{}, fmt.Errorf("Windows LSP product %q does not serve language %q", entry.Product, language)
		}
	}
	return entry, nil
}

func provisionCache(request WindowsProvisionRequest) (*WindowsAssetCache, error) {
	if request.Cache != nil {
		if strings.TrimSpace(request.CacheRoot) != "" {
			return nil, errors.New("provision request cannot set both Cache and CacheRoot")
		}
		return request.Cache, nil
	}
	cacheRoot := strings.TrimSpace(request.CacheRoot)
	if cacheRoot == "" {
		return nil, errors.New("Windows LSP provision request requires an explicit cache")
	}
	options := request.CacheOptions
	if options.HTTPClient == nil {
		options.HTTPClient = http.DefaultClient
	}
	return NewWindowsAssetCacheWithOptions(cacheRoot, options)
}

func normalizeProvisionProduct(product WindowsLSPProduct) WindowsLSPProduct {
	switch strings.ToLower(strings.TrimSpace(string(product))) {
	case "clang", "clangd", "llvm", "clang-llvm":
		return WindowsLSPProductClangd
	case "buf":
		return WindowsLSPProductBuf
	case "kotlin", "kotlin-lsp", "kotlin-language-server", "kotlin-server":
		return WindowsLSPProductKotlin
	case "dart":
		return WindowsLSPProductDart
	case "terraform", "terraform-ls":
		return WindowsLSPProductTerraform
	case "rust", "rust-analyzer":
		return WindowsLSPProductRustAnalyzer
	case "lua", "luals", "lua-language-server":
		return WindowsLSPProductLuaLanguageLS
	default:
		return WindowsLSPProduct(strings.ToLower(strings.TrimSpace(string(product))))
	}
}

func windowsLSPCommandMetadata(product WindowsLSPProduct) ([]string, []string, error) {
	var args []string
	switch product {
	case WindowsLSPProductClangd, WindowsLSPProductRustAnalyzer, WindowsLSPProductLuaLanguageLS:
		// 这些 Windows 原生服务器使用默认的 framed stdio 通道。
	case WindowsLSPProductKotlin:
		// 官方 Windows Kotlin launcher 要求 --stdio 才能启用 framed 输出。
		args = []string{"--stdio"}
	case WindowsLSPProductBuf:
		args = []string{"lsp", "serve"}
	case WindowsLSPProductDart:
		args = []string{"language-server", "--protocol=lsp"}
	case WindowsLSPProductTerraform:
		args = []string{"serve"}
	default:
		return nil, nil, fmt.Errorf("no Windows LSP command metadata for product %q", product)
	}
	return append([]string(nil), args...), nil, nil
}
