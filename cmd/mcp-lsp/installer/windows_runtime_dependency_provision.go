//go:build windows

package installer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	runtimeDependencyReadyFile      = ".runtime-ready.json"
	runtimeDependencyInstallTimeout = 45 * time.Minute
	runtimeDependencyMaxAssetBytes  = int64(8 << 30)
	runtimeDependencyMaxTreeBytes   = int64(16 << 30)
	// runtimeDependencyAssetDownloadAttempts 只用于固定 URL、固定摘要的幂等 GET。
	// 每次失败都会丢弃临时文件并从头下载，绝不发布半截资产或绕过摘要校验。
	runtimeDependencyAssetDownloadAttempts = 3
)

var runtimeDependencyPostPublishValidator = runtimeDependencyCacheResult

var (
	// ErrWindowsRuntimeDependencyCacheRoot 表示 Windows 运行时依赖缺少显式缓存根目录，调用会在联网或写盘前失败。
	ErrWindowsRuntimeDependencyCacheRoot = errors.New("runtime dependency cache root is required")
	// ErrWindowsRuntimeDependencyCacheMiss 表示 Windows cohort 的 ready 清单或完整文件树校验失败，禁止伪装命中。
	ErrWindowsRuntimeDependencyCacheMiss = errors.New("runtime dependency cache entry is missing or invalid")
	// ErrWindowsRuntimeDependencyAssetChecksumMismatch 表示 Windows 固定资产下载摘要与目录锁定值不一致。
	ErrWindowsRuntimeDependencyAssetChecksumMismatch = errors.New("runtime dependency asset checksum mismatch")
	// ErrWindowsRuntimeDependencyReadyInvalid 表示 Windows 依赖 ready 树或其原子发布清单损坏。
	ErrWindowsRuntimeDependencyReadyInvalid = errors.New("runtime dependency ready tree is invalid")
)

// WindowsRuntimeDependencyAssetFetcher 在 Windows 上下载固定 HTTPS 资产到暂存目录并校验摘要；下载或校验失败立即返回，调用方负责清理暂存目录。
type WindowsRuntimeDependencyAssetFetcher func(ctx context.Context, asset WindowsRuntimeDependencyAsset, destination string) error

// WindowsRuntimeDependencyCommandRunner 定义 Windows 安装命令执行器；命令使用绝对路径，失败立即返回且不回退 PATH。
type WindowsRuntimeDependencyCommandRunner func(ctx context.Context, executable, workingDir string, args, env []string) error

// WindowsRuntimeDependencyProvisionOptions 配置 Windows 依赖的显式缓存、网络客户端、超时和 check-only 生命周期。
type WindowsRuntimeDependencyProvisionOptions struct {
	// CacheRoot 指定 Windows 依赖 cohort 的绝对或可解析缓存根目录；为空时在联网前失败。
	CacheRoot string
	// CheckOnly 只验证已发布 Windows ready 树并禁止下载、命令执行和写盘。
	CheckOnly bool
	// InstallTimeout 限制 Windows 安装命令的独立生命周期；零值使用受控安装预算。
	InstallTimeout time.Duration
	// HTTPClient 是下载 Windows 固定资产的客户端；联网只发生在非 check-only provision。
	HTTPClient *http.Client
	// Platform 为测试或上层已检测的 Windows 版本/build/NativeArch 事实；生产为空时重新检测。
	Platform *WindowsHostPlatform
	// FetchAsset 是可注入的 Windows 固定资产下载器；失败必须阻止 ready 发布。
	FetchAsset WindowsRuntimeDependencyAssetFetcher
	// RunCommand 是可注入的 Windows 安装命令执行器；失败必须清理暂存并阻止伪 ready。
	RunCommand WindowsRuntimeDependencyCommandRunner
}

// WindowsRuntimeDependencyProvisionResult 返回 Windows 原生架构依赖的已校验 cohort、绝对路径和启动元数据。
type WindowsRuntimeDependencyProvisionResult struct {
	// Product 是实际选择的 Windows 运行时依赖产品。
	Product WindowsRuntimeDependencyProduct
	// Status 是产品在 Windows 当前 NativeArch 上的 installable、unsupported 或 evidence-gap 裁决。
	Status WindowsRuntimeDependencyCatalogStatus
	// Platform 记录用于选择资产的 Windows OS、版本、build、NativeArch 和 ProcessArch。
	Platform WindowsHostPlatform
	// Architecture 是实际使用的 Windows 原生资产架构，绝不表示跨架构或仿真。
	Architecture string
	// Cohort 是由固定版本和架构组成的 Windows 缓存 cohort 标识。
	Cohort string
	// RootPath 是已原子发布并完整复验的 Windows cohort 绝对根路径。
	RootPath string
	// WorkingDirectory 是 Windows LSP 子进程应使用的 cohort 内绝对工作目录。
	WorkingDirectory string
	// ExecutablePath 是 Windows 运行时本身的 cohort 内绝对可执行文件路径。
	ExecutablePath string
	// ServerPath 是 LSP server 的 cohort 内绝对路径，失败时不会返回伪 ready 路径。
	ServerPath string
	// Args 是 Windows LSP 启动参数向量；其中路径由 provision 固定，进程生命周期由调用方管理。
	Args []string
	// InstallArgs 是重放 Windows 安装命令的绝对路径参数向量，不包含 PATH 兜底。
	InstallArgs []string
	// Env 是 Windows LSP 子进程的显式环境增量；空值表示继承但不注入 PATH 回退。
	Env []string
	// CacheHit 表示完整 Windows ready 树校验命中且本次未下载或安装。
	CacheHit bool
}

// ProvisionWindowsRuntimeDependency 从真实 Windows 主机事实选择 NativeArch，并联网物化固定依赖到显式缓存。
func ProvisionWindowsRuntimeDependency(ctx context.Context, product WindowsRuntimeDependencyProduct, cacheRoot string) (WindowsRuntimeDependencyProvisionResult, error) {
	return ProvisionWindowsRuntimeDependencyWithOptions(ctx, product, WindowsRuntimeDependencyProvisionOptions{CacheRoot: cacheRoot})
}

// ProvisionWindowsRuntimeDependencyWithOptions 执行带 Windows 下载器、命令器、超时或平台事实的物化；失败不发布 ready。
func ProvisionWindowsRuntimeDependencyWithOptions(ctx context.Context, product WindowsRuntimeDependencyProduct, options WindowsRuntimeDependencyProvisionOptions) (WindowsRuntimeDependencyProvisionResult, error) {
	if options.Platform != nil {
		return provisionWindowsRuntimeDependencyForPlatform(ctx, product, options, *options.Platform)
	}
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("detect host platform for runtime dependency %q: %w", product, err)
	}
	return provisionWindowsRuntimeDependencyForPlatform(ctx, product, options, platform)
}

// ProvisionWindowsRuntimeDependencyForPlatform 按给定 Windows OS/version/build/NativeArch 事实物化固定依赖，不使用 ProcessArch 回退。
func ProvisionWindowsRuntimeDependencyForPlatform(ctx context.Context, product WindowsRuntimeDependencyProduct, cacheRoot string, platform WindowsHostPlatform) (WindowsRuntimeDependencyProvisionResult, error) {
	return provisionWindowsRuntimeDependencyForPlatform(ctx, product, WindowsRuntimeDependencyProvisionOptions{CacheRoot: cacheRoot, Platform: &platform}, platform)
}

// CheckWindowsRuntimeDependency 只校验 Windows cohort 的完整 ready 树和绝对路径，不下载、不执行命令、不写盘。
func CheckWindowsRuntimeDependency(ctx context.Context, product WindowsRuntimeDependencyProduct, cacheRoot string) (WindowsRuntimeDependencyProvisionResult, error) {
	result, err := ProvisionWindowsRuntimeDependencyWithOptions(ctx, product, WindowsRuntimeDependencyProvisionOptions{CacheRoot: cacheRoot, CheckOnly: true})
	if err == nil {
		result.CacheHit = true
	}
	return result, err
}

// ResolveWindowsRuntimeDependency 解析已存在的 Windows 依赖绝对路径，命中失败即返回 typed cache miss。
func ResolveWindowsRuntimeDependency(product WindowsRuntimeDependencyProduct, cacheRoot string) (WindowsRuntimeDependencyProvisionResult, error) {
	return CheckWindowsRuntimeDependency(context.Background(), product, cacheRoot)
}

// ResolveWindowsRuntimeDependencyForPlatform 按给定 Windows 主机事实只读解析 ready cohort，不下载或写盘。
func ResolveWindowsRuntimeDependencyForPlatform(ctx context.Context, product WindowsRuntimeDependencyProduct, cacheRoot string, platform WindowsHostPlatform) (WindowsRuntimeDependencyProvisionResult, error) {
	result, err := provisionWindowsRuntimeDependencyForPlatform(ctx, product, WindowsRuntimeDependencyProvisionOptions{CacheRoot: cacheRoot, CheckOnly: true, Platform: &platform}, platform)
	if err == nil {
		result.CacheHit = true
	}
	return result, err
}

func provisionWindowsRuntimeDependencyForPlatform(ctx context.Context, product WindowsRuntimeDependencyProduct, options WindowsRuntimeDependencyProvisionOptions, platform WindowsHostPlatform) (result WindowsRuntimeDependencyProvisionResult, err error) {
	if ctx == nil {
		return WindowsRuntimeDependencyProvisionResult{}, errors.New("runtime dependency context is nil")
	}
	cacheRoot, err := runtimeDependencyCacheRoot(options.CacheRoot)
	if err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, err
	}
	entry, err := WindowsRuntimeDependencyCatalogEntryForProduct(product)
	if err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, err
	}
	if err := ValidateWindowsRuntimeDependencyCatalog(); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("validate runtime dependency catalog: %w", err)
	}
	architecture, err := runtimeDependencyNativeArchitecture(entry, platform)
	if err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, err
	}
	status := entry.StatusByArchitecture[architecture]
	if status != WindowsRuntimeDependencyStatusInstallable {
		_, statusErr := WindowsRuntimeDependencyPlanForArchitecture(entry.Product, architecture)
		if statusErr != nil {
			return WindowsRuntimeDependencyProvisionResult{}, statusErr
		}
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("%w: %s/%s", ErrWindowsRuntimeDependencyEvidenceGap, entry.Product, architecture)
	}
	assets := cloneWindowsRuntimeDependencyAssets(entry.AssetsByArchitecture[architecture])
	if len(assets) == 0 {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("%w: installable product %s/%s has no assets", ErrWindowsRuntimeDependencyReadyInvalid, entry.Product, architecture)
	}
	if err := validateWindowsRuntimeDependencyWindows(entry, architecture, platform, assets); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, err
	}
	cohort := runtimeDependencyCohort(entry, architecture)
	finalRoot := runtimeDependencyFinalRoot(cacheRoot, entry.Product, architecture, cohort)
	if options.CheckOnly {
		if entry.Product == WindowsRuntimeDependencyProductSwiftSourceKitLS {
			return runtimeDependencySwiftCacheResult(ctx, entry, platform, architecture, cohort, cacheRoot, finalRoot)
		}
		return runtimeDependencyCacheResultContext(ctx, entry, platform, architecture, cohort, finalRoot)
	}

	timeout := options.InstallTimeout
	if timeout == 0 {
		timeout = runtimeDependencyInstallTimeout
	}
	if timeout < 0 {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("runtime dependency install timeout cannot be negative: %s", timeout)
	}
	installCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := ensureDirectoryNoSymlink(cacheRoot); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("create runtime dependency cache root %q: %w", cacheRoot, err)
	}
	runtimeRoot := filepath.Dir(filepath.Dir(finalRoot))
	if err := ensureDirectoryNoSymlink(runtimeRoot); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("create runtime dependency cache directory %q: %w", runtimeRoot, err)
	}
	if err := ensureDirectoryNoSymlink(filepath.Dir(finalRoot)); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("create runtime dependency cohort parent: %w", err)
	}
	lockDir := filepath.Join(runtimeRoot, ".locks", cacheSegment(string(entry.Product)), architecture)
	if err := ensureDirectoryNoSymlink(lockDir); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("create runtime dependency cohort lock directory: %w", err)
	}
	lockPath := filepath.Join(lockDir, cacheSegment(cohort)+".lock")
	lease, err := acquireAssetOSLock(installCtx, lockPath)
	if err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("lock runtime dependency cohort %q: %w", cohort, err)
	}
	defer func() {
		if releaseErr := releaseAssetOSLock(lease); releaseErr != nil {
			err = joinAssetReleaseError(err, releaseErr, fmt.Sprintf("release Windows runtime dependency cohort OS lock %q", cohort))
			result = WindowsRuntimeDependencyProvisionResult{}
		}
	}()

	if result, cacheErr := runtimeDependencyCacheResultContext(installCtx, entry, platform, architecture, cohort, finalRoot); cacheErr == nil {
		if !options.CheckOnly && entry.Product == WindowsRuntimeDependencyProductJDKJDTLS {
			workspaceRoot := runtimeDependencyJDTLSWorkspaceRoot(finalRoot, architecture, cohort)
			if err := prepareWindowsRuntimeDependencyJDTLSWorkspaceConfiguration(finalRoot, workspaceRoot); err != nil {
				return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("prepare JDTLS mutable workspace: %w", err)
			}
		}
		result.CacheHit = true
		return result, nil
	} else if installCtx.Err() != nil || errors.Is(cacheErr, context.Canceled) || errors.Is(cacheErr, context.DeadlineExceeded) {
		if installCtx.Err() != nil {
			return WindowsRuntimeDependencyProvisionResult{}, installCtx.Err()
		}
		return WindowsRuntimeDependencyProvisionResult{}, cacheErr
	} else if !errors.Is(cacheErr, ErrWindowsRuntimeDependencyCacheMiss) && !errors.Is(cacheErr, ErrWindowsRuntimeDependencyReadyInvalid) {
		return WindowsRuntimeDependencyProvisionResult{}, cacheErr
	}
	if err := removeInvalidWindowsRuntimeDependencyCacheWithinRoot(cacheRoot, finalRoot); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, err
	}
	if err := validateWindowsInstallerPathWithinRoot(cacheRoot, runtimeRoot, false); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("validate runtime dependency staging parent: %w", err)
	}
	stage, err := os.MkdirTemp(runtimeRoot, ".staging-")
	if err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("create runtime dependency staging directory: %w", err)
	}
	removeStage := true
	defer func() {
		if removeStage {
			if removeErr := removeWindowsInstallerAllChecked(cacheRoot, stage); removeErr != nil {
				err = joinWindowsInstallerCleanupError(err, removeErr, fmt.Sprintf("remove runtime dependency staging directory %q", stage))
				result = WindowsRuntimeDependencyProvisionResult{}
			}
		}
	}()
	if err := validateWindowsInstallerPathWithinRoot(cacheRoot, stage, false); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("validate runtime dependency staging directory: %w", err)
	}

	fetch := options.FetchAsset
	if fetch == nil {
		fetch = defaultWindowsRuntimeDependencyAssetFetcher(options.HTTPClient)
	}
	payloads := make(map[string]string, len(assets))
	for _, asset := range assets {
		if entry.Product == WindowsRuntimeDependencyProductGoSQLS && asset.Component == "go" {
			if err := materializeWindowsGoSQLSBuildSDK(stage, architecture, asset); err != nil {
				return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("materialize managed Go build SDK for SQLS: %w", err)
			}
			payloads[asset.Component] = "managed-go-gopls"
			continue
		}
		payload, materializeErr := materializeWindowsRuntimeDependencyAsset(installCtx, stage, asset, fetch)
		if materializeErr != nil {
			return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("materialize runtime dependency asset %s/%s: %w", asset.Component, asset.Version, materializeErr)
		}
		payloads[asset.Component] = payload
	}
	if err := installWindowsRuntimeDependency(installCtx, entry, architecture, stage, payloads, options.RunCommand); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, err
	}
	if entry.Product == WindowsRuntimeDependencyProductSwiftSourceKitLS {
		if err := materializeSwiftWindowsFlatLayout(stage); err != nil {
			return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("materialize Swift short physical layout: %w", err)
		}
	}
	if err := requireWindowsRuntimeDependencyPaths(stage, entry, architecture); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, err
	}
	if err := writeWindowsRuntimeDependencyReady(stage, entry, architecture, cohort); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, err
	}
	if entry.Product == WindowsRuntimeDependencyProductJDKJDTLS {
		workspaceRoot := runtimeDependencyJDTLSWorkspaceRoot(finalRoot, architecture, cohort)
		if err := prepareWindowsRuntimeDependencyJDTLSWorkspaceConfiguration(stage, workspaceRoot); err != nil {
			return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("prepare JDTLS mutable workspace: %w", err)
		}
	}
	if err := renameWindowsInstallerPathChecked(cacheRoot, stage, finalRoot); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("atomically publish runtime dependency cohort %q: %w", cohort, err)
	}
	removeStage = false
	result, err = runtimeDependencyPostPublishValidator(entry, platform, architecture, cohort, finalRoot)
	if err != nil {
		result = WindowsRuntimeDependencyProvisionResult{}
		err = joinWindowsInstallerCleanupError(err, removeWindowsInstallerAllChecked(cacheRoot, finalRoot), fmt.Sprintf("remove invalid published runtime dependency cohort %q", finalRoot))
		return result, err
	}
	return result, nil
}

func runtimeDependencyNativeArchitecture(entry WindowsRuntimeDependencyCatalogEntry, platform WindowsHostPlatform) (string, error) {
	if platform.OS != WindowsHostOSWindows {
		return "", fmt.Errorf("runtime dependency %q requires Windows: %w", entry.Product, ErrUnsupportedWindowsHostPlatform)
	}
	if strings.TrimSpace(platform.NativeArch) == "" {
		return "", &WindowsRuntimeDependencyUnsupportedError{Product: entry.Product, Architecture: "", Reason: "DetectWindowsHostPlatform returned an empty NativeArch"}
	}
	architecture, err := NormalizeWindowsArchitectureAlias(platform.NativeArch)
	if err != nil {
		return "", &WindowsRuntimeDependencyUnsupportedError{Product: entry.Product, Architecture: platform.NativeArch, Reason: err.Error()}
	}
	return architecture, nil
}

func validateWindowsRuntimeDependencyWindows(entry WindowsRuntimeDependencyCatalogEntry, architecture string, platform WindowsHostPlatform, assets []WindowsRuntimeDependencyAsset) error {
	if strings.TrimSpace(platform.WindowsVersion) == "" || platform.WindowsBuild == 0 {
		return &WindowsUnsupportedWindowsVersionError{AssetName: string(entry.Product), WindowsVersion: platform.WindowsVersion, WindowsBuild: platform.WindowsBuild, RequiredVersion: "10.0"}
	}
	hostMajor, hostMinor, err := parseWindowsVersion(platform.WindowsVersion)
	if err != nil {
		return &WindowsUnsupportedWindowsVersionError{AssetName: string(entry.Product), WindowsVersion: platform.WindowsVersion, WindowsBuild: platform.WindowsBuild, RequiredVersion: "10.0"}
	}
	for _, asset := range assets {
		if asset.Architecture != architecture {
			return fmt.Errorf("runtime dependency %q selected non-native asset %q/%s", entry.Product, asset.Component, asset.Architecture)
		}
		minMajor, minMinor := 0, 0
		if strings.TrimSpace(asset.MinWindowsVersion) != "" {
			minMajor, minMinor, err = parseWindowsVersion(asset.MinWindowsVersion)
			if err != nil || hostMajor < minMajor || (hostMajor == minMajor && hostMinor < minMinor) {
				return &WindowsUnsupportedWindowsVersionError{AssetName: asset.Component, WindowsVersion: platform.WindowsVersion, WindowsBuild: platform.WindowsBuild, RequiredVersion: asset.MinWindowsVersion, RequiredBuild: asset.MinWindowsBuild}
			}
		}
		if asset.MinWindowsBuildKnown && asset.MinWindowsBuild != 0 && platform.WindowsBuild < asset.MinWindowsBuild {
			return &WindowsUnsupportedWindowsVersionError{AssetName: asset.Component, WindowsVersion: platform.WindowsVersion, WindowsBuild: platform.WindowsBuild, RequiredVersion: asset.MinWindowsVersion, RequiredBuild: asset.MinWindowsBuild}
		}
	}
	return nil
}

func runtimeDependencyCacheRoot(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", ErrWindowsRuntimeDependencyCacheRoot
	}
	root, err := filepath.Abs(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("resolve runtime dependency cache root: %w", err)
	}
	return filepath.Clean(root), nil
}

func runtimeDependencyCohort(entry WindowsRuntimeDependencyCatalogEntry, architecture string) string {
	parts := make([]string, 0, len(entry.AssetsByArchitecture[architecture])+1)
	for _, asset := range entry.AssetsByArchitecture[architecture] {
		parts = append(parts, cacheSegment(asset.Component)+"-"+cacheSegment(asset.Version))
	}
	sort.Strings(parts)
	return strings.Join(parts, "_")
}

func runtimeDependencyFinalRoot(cacheRoot string, product WindowsRuntimeDependencyProduct, architecture, cohort string) string {
	return filepath.Join(cacheRoot, "runtime-dependencies", cacheSegment(string(product)), architecture, cacheSegment(cohort))
}

// windowsRuntimeDependencyAssetTransferError 保存一次 HTTP 传输失败的可诊断事实。
// 它不进入收据或 wire schema；URL、请求头和本地绝对路径都不会保存在该结构中。
type windowsRuntimeDependencyAssetTransferError struct {
	operation    string
	statusCode   int
	expectedSize int64
	receivedSize int64
	retryable    bool
	cause        error
}

func (e *windowsRuntimeDependencyAssetTransferError) Error() string {
	if e == nil {
		return "Windows runtime dependency asset transfer failed"
	}
	return fmt.Sprintf("Windows runtime dependency asset transfer failed: operation=%s status=%d received_bytes=%d expected_bytes=%d retryable=%t: %v", e.operation, e.statusCode, e.receivedSize, e.expectedSize, e.retryable, e.cause)
}

func (e *windowsRuntimeDependencyAssetTransferError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// defaultWindowsRuntimeDependencyAssetFetcher 为 Windows 固定资产构造有界下载器。
// 只有传输截断、幂等 GET 的 transport failure、校验和不一致和可重试 HTTP 状态会重试；
// 路径、权限、临时文件、解压和发布错误继续立即失败。
func defaultWindowsRuntimeDependencyAssetFetcher(client *http.Client) WindowsRuntimeDependencyAssetFetcher {
	if client == nil {
		client = &http.Client{}
	}
	return func(ctx context.Context, asset WindowsRuntimeDependencyAsset, destination string) (err error) {
		var lastErr error
		for attempt := 1; attempt <= runtimeDependencyAssetDownloadAttempts; attempt++ {
			lastErr = fetchWindowsRuntimeDependencyAssetOnce(ctx, client, asset, destination)
			if lastErr == nil {
				return nil
			}
			if !windowsRuntimeDependencyAssetDownloadRetryable(lastErr) || attempt == runtimeDependencyAssetDownloadAttempts {
				return fmt.Errorf("download fixed asset component=%q version=%q attempts=%d/%d: %w", asset.Component, asset.Version, attempt, runtimeDependencyAssetDownloadAttempts, lastErr)
			}
			if err := waitWindowsRuntimeDependencyAssetRetry(ctx, time.Duration(attempt)*250*time.Millisecond); err != nil {
				return fmt.Errorf("download fixed asset component=%q version=%q retry canceled after attempts=%d/%d: %w", asset.Component, asset.Version, attempt, runtimeDependencyAssetDownloadAttempts, err)
			}
		}
		return fmt.Errorf("download fixed asset component=%q version=%q exhausted without result: %w", asset.Component, asset.Version, lastErr)
	}
}

// fetchWindowsRuntimeDependencyAssetOnce 执行一次从 HTTP 响应到私有临时文件的完整事务。
// 返回 nil 前必须完成大小边界、摘要校验、关闭和同目录原子发布。
func fetchWindowsRuntimeDependencyAssetOnce(ctx context.Context, client *http.Client, asset WindowsRuntimeDependencyAsset, destination string) (err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return fmt.Errorf("create fixed asset request: %w", err)
	}
	request.Header.Set("User-Agent", windowsAssetHTTPUserAgent)
	response, err := client.Do(request)
	if err != nil {
		return &windowsRuntimeDependencyAssetTransferError{
			operation: "request", expectedSize: -1, receivedSize: 0,
			retryable: ctx.Err() == nil, cause: fmt.Errorf("download fixed asset %q: %w", asset.URL, err),
		}
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			err = joinWindowsInstallerCleanupError(err, closeErr, "close fixed asset HTTP response body")
		}
	}()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return &windowsRuntimeDependencyAssetTransferError{
			operation: "response_status", statusCode: response.StatusCode,
			expectedSize: response.ContentLength, receivedSize: 0,
			retryable: windowsRuntimeDependencyAssetHTTPStatusRetryable(response.StatusCode),
			cause:     fmt.Errorf("download fixed asset %q returned %s", asset.URL, response.Status),
		}
	}
	if response.ContentLength > runtimeDependencyMaxAssetBytes {
		return fmt.Errorf("fixed asset %q exceeds %d bytes", asset.URL, runtimeDependencyMaxAssetBytes)
	}
	if err := ensureDirectoryNoSymlink(filepath.Dir(destination)); err != nil {
		return fmt.Errorf("create fixed asset directory: %w", err)
	}
	if err := validateWindowsInstallerPathWithinRoot(filepath.Dir(destination), destination, true); err != nil {
		return fmt.Errorf("validate fixed asset destination: %w", err)
	}
	temporary, err := createWindowsInstallerTemp(filepath.Dir(destination), ".download-")
	if err != nil {
		return fmt.Errorf("create fixed asset temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	keep := false
	temporaryClosed := false
	defer func() {
		if !temporaryClosed {
			if closeErr := temporary.Close(); closeErr != nil {
				err = joinWindowsInstallerCleanupError(err, closeErr, "close fixed asset temporary file")
			}
		}
		if !keep {
			if removeErr := removeWindowsInstallerPathChecked(filepath.Dir(destination), temporaryPath); removeErr != nil && !os.IsNotExist(removeErr) {
				err = joinWindowsInstallerCleanupError(err, removeErr, fmt.Sprintf("remove fixed asset temporary file %q", temporaryPath))
			}
		}
	}()
	if err := validateWindowsInstallerPathWithinRoot(filepath.Dir(destination), temporaryPath, false); err != nil {
		return fmt.Errorf("validate fixed asset temporary file: %w", err)
	}
	hasher := runtimeDependencyHasher(asset.ChecksumAlgorithm)
	if hasher == nil {
		return fmt.Errorf("unsupported checksum algorithm %q", asset.ChecksumAlgorithm)
	}
	limit := io.LimitReader(response.Body, runtimeDependencyMaxAssetBytes+1)
	count, err := io.Copy(io.MultiWriter(temporary, hasher), limit)
	if err != nil {
		return &windowsRuntimeDependencyAssetTransferError{
			operation: "copy_response_body", statusCode: response.StatusCode,
			expectedSize: response.ContentLength, receivedSize: count,
			retryable: windowsRuntimeDependencyAssetCopyRetryable(ctx, err),
			cause:     fmt.Errorf("write fixed asset %q: %w", asset.URL, err),
		}
	}
	if count > runtimeDependencyMaxAssetBytes {
		return fmt.Errorf("fixed asset %q exceeds %d bytes", asset.URL, runtimeDependencyMaxAssetBytes)
	}
	if response.ContentLength >= 0 && count != response.ContentLength {
		return &windowsRuntimeDependencyAssetTransferError{
			operation: "content_length", statusCode: response.StatusCode,
			expectedSize: response.ContentLength, receivedSize: count, retryable: true,
			cause: io.ErrUnexpectedEOF,
		}
	}
	if closeErr := temporary.Close(); closeErr != nil {
		temporaryClosed = true
		return fmt.Errorf("close fixed asset temporary file: %w", closeErr)
	}
	temporaryClosed = true
	got := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(got, strings.TrimSpace(asset.Checksum)) {
		return fmt.Errorf("%w for %s: want %s, got %s", ErrWindowsRuntimeDependencyAssetChecksumMismatch, asset.Component, asset.Checksum, got)
	}
	if err := renameWindowsInstallerPathChecked(filepath.Dir(destination), temporaryPath, destination); err != nil {
		return fmt.Errorf("atomically publish fixed asset payload: %w", err)
	}
	keep = true
	return nil
}

// windowsRuntimeDependencyAssetCopyRetryable 只把可安全从头重放的响应体读取失败标为可重试。
// 固定资产使用幂等 GET；EOF 和 net.Error 表示传输在响应体完成前中断。调用上下文已取消时
// 必须立即停止，普通本地文件写入错误也保持不可重试，避免把磁盘/ACL 故障误判为网络抖动。
func windowsRuntimeDependencyAssetCopyRetryable(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}

// windowsRuntimeDependencyAssetDownloadRetryable 只识别下载层可安全从头重放的失败。
func windowsRuntimeDependencyAssetDownloadRetryable(err error) bool {
	var transferErr *windowsRuntimeDependencyAssetTransferError
	if errors.As(err, &transferErr) {
		return transferErr.retryable
	}
	return errors.Is(err, ErrWindowsRuntimeDependencyAssetChecksumMismatch)
}

// windowsRuntimeDependencyAssetHTTPStatusRetryable 限定幂等 GET 可重试的标准暂态状态。
func windowsRuntimeDependencyAssetHTTPStatusRetryable(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// waitWindowsRuntimeDependencyAssetRetry 在短退避期间响应取消，禁止忽略生命周期截止时间。
func waitWindowsRuntimeDependencyAssetRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func runtimeDependencyHasher(algorithm WindowsRuntimeDependencyChecksumAlgorithm) interface {
	io.Writer
	Sum([]byte) []byte
} {
	switch algorithm {
	case WindowsRuntimeDependencyChecksumSHA256:
		return sha256.New()
	case WindowsRuntimeDependencyChecksumSHA512:
		return sha512.New()
	default:
		return nil
	}
}

const (
	runtimeDependencyDotnetNet8SDKVersion           = "8.0.424"
	runtimeDependencyDotnetNet8ReferencePackVersion = "8.0.30"
	runtimeDependencyDotnetNet8ReferenceFramework   = "net8.0"
	runtimeDependencyDotnetNet8SDKManifestVersion   = "8.0.100"
)

// runtimeDependencySystemDotnetSDKRootResolver 只允许返回已验证的系统 .NET 根；返回空值表示系统没有目标 SDK，
// 调用方随后下载目录锁定的官方 SDK8 归档。测试通过替换该 seam 验证复用与官方下载分支，不读取 PATH。
var runtimeDependencySystemDotnetSDKRootResolver = defaultRuntimeDependencySystemDotnetSDKRootResolver

func defaultRuntimeDependencySystemDotnetSDKRootResolver(architecture, version string) (string, error) {
	normalized, err := NormalizeWindowsArchitectureAlias(architecture)
	if err != nil {
		return "", err
	}
	version = strings.TrimSpace(version)
	if version == "" {
		return "", errors.New("system .NET SDK version is empty")
	}
	for _, root := range runtimeDependencySystemDotnetRootCandidates(normalized) {
		_, statErr := os.Lstat(root)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return "", fmt.Errorf("inspect system .NET root %s: %w", securefs.RedactPath(root), statErr)
		}
		if err := validateWindowsDotnetNet8SDKRoot(root, normalized, version, true); err != nil {
			return "", err
		}
		return filepath.Clean(root), nil
	}
	return "", nil
}

func runtimeDependencySystemDotnetRootCandidates(architecture string) []string {
	values := make([]string, 0, 4)
	seen := make(map[string]struct{})
	appendCandidate := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" || !filepath.IsAbs(raw) {
			return
		}
		candidate := filepath.Clean(raw)
		key := strings.ToLower(candidate)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		values = append(values, candidate)
	}
	switch architecture {
	case WindowsHostArchARM64, WindowsHostArchX64:
		if root := strings.TrimSpace(os.Getenv("ProgramW6432")); root != "" {
			appendCandidate(filepath.Join(root, "dotnet"))
		}
		if root := strings.TrimSpace(os.Getenv("ProgramFiles")); root != "" {
			appendCandidate(filepath.Join(root, "dotnet"))
		}
		appendCandidate(`C:\Program Files\dotnet`)
	case WindowsHostArchX86:
		if root := strings.TrimSpace(os.Getenv("ProgramFiles(x86)")); root != "" {
			appendCandidate(filepath.Join(root, "dotnet"))
		}
		appendCandidate(`C:\Program Files (x86)\dotnet`)
	}
	return values
}

func validateWindowsDotnetNet8SDKRoot(root, architecture, version string, verifyExecutable bool) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return errors.New("system .NET SDK root is empty")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve system .NET SDK root: %w", err)
	}
	root = filepath.Clean(absoluteRoot)
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("inspect system .NET SDK root %s: %w", securefs.RedactPath(root), err)
	}
	if isUnsafeAssetFile(info) || !info.IsDir() {
		return fmt.Errorf("system .NET SDK root is not a real directory: %s", securefs.RedactPath(root))
	}
	architecture, err = NormalizeWindowsArchitectureAlias(architecture)
	if err != nil {
		return err
	}
	if version != runtimeDependencyDotnetNet8SDKVersion {
		return fmt.Errorf("unsupported system .NET SDK8 version %q", version)
	}
	requiredDirectories := []string{
		filepath.Join("sdk", version),
		filepath.Join("packs", "Microsoft.NETCore.App.Ref", runtimeDependencyDotnetNet8ReferencePackVersion, "ref", runtimeDependencyDotnetNet8ReferenceFramework),
		filepath.Join("packs", "Microsoft.AspNetCore.App.Ref", runtimeDependencyDotnetNet8ReferencePackVersion, "ref", runtimeDependencyDotnetNet8ReferenceFramework),
		filepath.Join("packs", "Microsoft.WindowsDesktop.App.Ref", runtimeDependencyDotnetNet8ReferencePackVersion, "ref", runtimeDependencyDotnetNet8ReferenceFramework),
		filepath.Join("shared", "Microsoft.NETCore.App", runtimeDependencyDotnetNet8ReferencePackVersion),
		filepath.Join("sdk-manifests", runtimeDependencyDotnetNet8SDKManifestVersion),
	}
	for _, relative := range requiredDirectories {
		path := filepath.Join(root, relative)
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return fmt.Errorf("system .NET SDK8 is missing %s: %w", filepath.ToSlash(relative), statErr)
		}
		if isUnsafeAssetFile(info) || !info.IsDir() {
			return fmt.Errorf("system .NET SDK8 path is not a real directory: %s", securefs.RedactPath(path))
		}
	}
	requiredFiles := []string{
		"dotnet.exe",
		filepath.Join("sdk", version, "MSBuild.dll"),
		filepath.Join("packs", "Microsoft.NETCore.App.Ref", runtimeDependencyDotnetNet8ReferencePackVersion, "ref", runtimeDependencyDotnetNet8ReferenceFramework, "System.Runtime.dll"),
		filepath.Join("packs", "Microsoft.AspNetCore.App.Ref", runtimeDependencyDotnetNet8ReferencePackVersion, "ref", runtimeDependencyDotnetNet8ReferenceFramework, "Microsoft.AspNetCore.App.Ref.dll"),
		filepath.Join("packs", "Microsoft.WindowsDesktop.App.Ref", runtimeDependencyDotnetNet8ReferencePackVersion, "ref", runtimeDependencyDotnetNet8ReferenceFramework, "Microsoft.WindowsDesktop.App.Ref.dll"),
		filepath.Join("shared", "Microsoft.NETCore.App", runtimeDependencyDotnetNet8ReferencePackVersion, "System.Private.CoreLib.dll"),
	}
	for _, relative := range requiredFiles {
		path := filepath.Join(root, relative)
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return fmt.Errorf("system .NET SDK8 is missing %s: %w", filepath.ToSlash(relative), statErr)
		}
		if isUnsafeAssetFile(info) || !info.Mode().IsRegular() {
			return fmt.Errorf("system .NET SDK8 path is not a real file: %s", securefs.RedactPath(path))
		}
	}
	if verifyExecutable {
		machine, err := windowsDotnetExecutableMachine(filepath.Join(root, "dotnet.exe"))
		if err != nil {
			return fmt.Errorf("verify system .NET SDK8 architecture: %w", err)
		}
		if machine != architecture {
			return fmt.Errorf("system .NET SDK8 architecture %q does not match native architecture %q", machine, architecture)
		}
	}
	return nil
}

func windowsDotnetExecutableMachine(path string) (string, error) {
	image, err := pe.Open(path)
	if err != nil {
		return "", fmt.Errorf("open system dotnet.exe: %w", err)
	}
	machine, err := NormalizeWindowsImageFileMachine(image.FileHeader.Machine)
	closeErr := image.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", fmt.Errorf("close system dotnet.exe: %w", closeErr)
	}
	return machine, nil
}

func materializeWindowsRuntimeDependencyAsset(ctx context.Context, stage string, asset WindowsRuntimeDependencyAsset, fetch WindowsRuntimeDependencyAssetFetcher) (string, error) {
	if asset.Component == "swift-toolchain" && asset.Format == WindowsRuntimeDependencyAssetFormatEXE {
		return materializeSwiftWindowsRuntimeDependencyAsset(ctx, stage, asset, fetch)
	}
	if asset.Component == "dotnet-sdk-net8" {
		systemRoot, err := runtimeDependencySystemDotnetSDKRootResolver(asset.Architecture, asset.Version)
		if err != nil {
			return "", fmt.Errorf("resolve system .NET SDK8 for %s/%s: %w", asset.Architecture, asset.Version, err)
		}
		if strings.TrimSpace(systemRoot) != "" {
			absoluteRoot, absErr := filepath.Abs(strings.TrimSpace(systemRoot))
			if absErr != nil {
				return "", fmt.Errorf("resolve system .NET SDK8 root: %w", absErr)
			}
			systemRoot = filepath.Clean(absoluteRoot)
			if err := validateWindowsDotnetNet8SDKRoot(systemRoot, asset.Architecture, asset.Version, false); err != nil {
				return "", fmt.Errorf("validate system .NET SDK8 root: %w", err)
			}
			if err := mergeWindowsDotnetNet8SDK(stage, systemRoot); err != nil {
				return "", fmt.Errorf("merge system .NET SDK8: %w", err)
			}
			return filepath.Join(systemRoot, "dotnet.exe"), nil
		}
	}
	if asset.Format == WindowsRuntimeDependencyAssetFormatEXE || asset.Format == WindowsRuntimeDependencyAssetFormatCrate {
		return "", fmt.Errorf("%w: asset format %q has no safe archive materializer", ErrWindowsRuntimeDependencyEvidenceGap, asset.Format)
	}
	assetDir := filepath.Join(stage, ".runtime-assets", cacheSegment(asset.Component))
	if err := ensureDirectoryNoSymlink(assetDir); err != nil {
		return "", err
	}
	suffix := ".payload"
	switch asset.Format {
	case WindowsRuntimeDependencyAssetFormatZIP:
		suffix = ".zip"
	case WindowsRuntimeDependencyAssetFormatNupkg:
		// dotnet tool --add-source 只识别 .nupkg；改名为 .zip 会让已校验包在本地源中不可见。
		suffix = ".nupkg"
	case WindowsRuntimeDependencyAssetFormatTarGz:
		suffix = ".tar.gz"
	case WindowsRuntimeDependencyAssetFormatSevenZip:
		suffix = ".7z"
	case WindowsRuntimeDependencyAssetFormatGem:
		suffix = ".gem"
	}
	payloadName := cacheSegment(asset.Component) + "-" + cacheSegment(asset.Version) + suffix
	if asset.Format == WindowsRuntimeDependencyAssetFormatNupkg {
		// NuGet 本地源按 package-id.version.nupkg 识别包；短横线命名会让已校验包不可见。
		payloadName = cacheSegment(asset.Component) + "." + cacheSegment(asset.Version) + suffix
	}
	payload := filepath.Join(assetDir, payloadName)
	if err := validateWindowsInstallerPathWithinRoot(stage, payload, true); err != nil {
		return "", fmt.Errorf("validate runtime dependency payload destination: %w", err)
	}
	if err := fetch(ctx, asset, payload); err != nil {
		return "", err
	}
	if err := validateWindowsInstallerPathWithinRoot(stage, payload, false); err != nil {
		return "", fmt.Errorf("validate runtime dependency payload after fetch: %w", err)
	}
	info, err := os.Lstat(payload)
	if err != nil || isUnsafeAssetFile(info) || !info.Mode().IsRegular() || info.Size() == 0 {
		return "", fmt.Errorf("fixed asset fetcher did not publish a regular payload: %q", payload)
	}
	extractedRoot := stage
	switch asset.Format {
	case WindowsRuntimeDependencyAssetFormatZIP:
		if asset.Component == "dotnet-sdk-net8" {
			extractedRoot = filepath.Join(assetDir, "expanded")
			if err := ensureDirectoryNoSymlink(extractedRoot); err != nil {
				return "", fmt.Errorf("prepare .NET 8 SDK extraction root: %w", err)
			}
			if err := extractZipAsset(payload, extractedRoot, asset.BinaryPath, runtimeDependencyMaxTreeBytes); err != nil {
				return "", err
			}
			if err := mergeWindowsDotnetNet8SDK(stage, extractedRoot); err != nil {
				return "", err
			}
		} else if err := extractZipAsset(payload, stage, asset.BinaryPath, runtimeDependencyMaxTreeBytes); err != nil {
			return "", err
		}
	case WindowsRuntimeDependencyAssetFormatNupkg:
		// NuGet 包只在私有检查目录安全展开；正式 tool-path 由 dotnet tool install 独占，
		// 防止包内 tools/net*/any 与最终 tools/csharp-ls.exe 发生目录语义冲突。
		extractedRoot = filepath.Join(assetDir, "expanded")
		if err := extractZipAsset(payload, extractedRoot, asset.BinaryPath, runtimeDependencyMaxTreeBytes); err != nil {
			return "", err
		}
	case WindowsRuntimeDependencyAssetFormatTarGz:
		if err := extractTarGzAsset(ctx, payload, stage, asset.BinaryPath, runtimeDependencyMaxTreeBytes); err != nil {
			return "", err
		}
	case WindowsRuntimeDependencyAssetFormatSevenZip:
		if err := extractWindowsRuntimeDependencySevenZipAsset(payload, stage, runtimeDependencyMaxTreeBytes); err != nil {
			return "", err
		}
	case WindowsRuntimeDependencyAssetFormatGem:
		// Gem 资产由后续 Ruby cohort 安装步骤处理，当前阶段只保留已校验载荷。
	default:
		return "", fmt.Errorf("unsupported runtime dependency asset format %q", asset.Format)
	}
	checkPath := asset.ArchivePath
	if strings.TrimSpace(checkPath) == "" && asset.Component != "gopls" && asset.Format != WindowsRuntimeDependencyAssetFormatGem {
		checkPath = asset.BinaryPath
	}
	if strings.TrimSpace(checkPath) != "" {
		if _, err := locateWindowsRuntimeDependencyPath(extractedRoot, checkPath); err != nil {
			return "", fmt.Errorf("verify extracted %s path %q: %w", asset.Component, checkPath, err)
		}
	}
	if asset.Component == "dotnet-sdk-net8" {
		if err := removeWindowsInstallerAllChecked(stage, extractedRoot); err != nil {
			return "", fmt.Errorf("remove temporary .NET 8 SDK extraction tree: %w", err)
		}
	}
	return payload, nil
}

// mergeWindowsDotnetNet8SDK 将固定 .NET 8 SDK 中不与产品 .NET 10 host 冲突的 SDK、packs、runtime
// 和 manifest 子树合并到同一 cohort；这样 csharp-ls 仍由 .NET 10 host 启动，但 MSBuild 能解析 net8.0。
func mergeWindowsDotnetNet8SDK(stage, extractedRoot string) error {
	for _, relative := range []string{"sdk", "packs", "shared", "sdk-manifests"} {
		source := filepath.Join(extractedRoot, relative)
		info, err := os.Lstat(source)
		if err != nil {
			return fmt.Errorf(".NET 8 SDK is missing %s: %w", relative, err)
		}
		if isUnsafeAssetFile(info) || !info.IsDir() {
			return fmt.Errorf(".NET 8 SDK %s is not a real directory", relative)
		}
		if err := copyWindowsRuntimeDependencyDirectory(source, filepath.Join(stage, relative)); err != nil {
			return fmt.Errorf("merge .NET 8 SDK %s: %w", relative, err)
		}
	}
	for _, relative := range []string{
		filepath.Join("sdk", runtimeDependencyDotnetNet8SDKVersion),
		filepath.Join("packs", "Microsoft.NETCore.App.Ref", runtimeDependencyDotnetNet8ReferencePackVersion, "ref", runtimeDependencyDotnetNet8ReferenceFramework),
		filepath.Join("packs", "Microsoft.AspNetCore.App.Ref", runtimeDependencyDotnetNet8ReferencePackVersion, "ref", runtimeDependencyDotnetNet8ReferenceFramework),
		filepath.Join("packs", "Microsoft.WindowsDesktop.App.Ref", runtimeDependencyDotnetNet8ReferencePackVersion, "ref", runtimeDependencyDotnetNet8ReferenceFramework),
		filepath.Join("shared", "Microsoft.NETCore.App", runtimeDependencyDotnetNet8ReferencePackVersion),
	} {
		path := filepath.Join(stage, filepath.FromSlash(relative))
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("merged .NET 8 SDK is missing %s: %w", relative, err)
		}
		if isUnsafeAssetFile(info) || !info.IsDir() {
			return fmt.Errorf("merged .NET 8 SDK path %s is not a real directory", relative)
		}
	}
	return nil
}

func installWindowsRuntimeDependency(ctx context.Context, entry WindowsRuntimeDependencyCatalogEntry, architecture, stage string, payloads map[string]string, runner WindowsRuntimeDependencyCommandRunner) error {
	if entry.Product == WindowsRuntimeDependencyProductSwiftSourceKitLS {
		if runner == nil {
			runner = defaultWindowsRuntimeDependencyCommandRunner
		}
		return installSwiftWindowsRuntimeDependency(ctx, entry, architecture, stage, payloads, runner)
	}
	if entry.Product == WindowsRuntimeDependencyProductGoSQLS {
		return installWindowsGoSQLS(ctx, entry, architecture, stage, payloads, runner)
	}
	if runner == nil {
		runner = defaultWindowsRuntimeDependencyCommandRunner
	}
	if entry.Product == WindowsRuntimeDependencyProductRubyLSP {
		return installRubyLSPWindowsRuntimeDependency(ctx, entry, architecture, stage, payloads, runner)
	}
	runtimePath := filepath.Join(stage, filepath.FromSlash(runtimeDependencyRuntimeExecutablePath(entry, architecture)))
	if _, err := requireRegularWindowsRuntimeDependencyPath(runtimePath); err != nil {
		return fmt.Errorf("resolve %s runtime executable: %w", entry.Product, err)
	}
	runCommand := func(args, env []string) error {
		if err := runner(ctx, runtimePath, stage, args, env); err != nil {
			return wrapProcessFailure("runtime-dependency-command", string(entry.Product), securefs.WrapErrorForPath(err, runtimePath), len(args), 0)
		}
		return nil
	}
	switch entry.Product {
	case WindowsRuntimeDependencyProductGoGopls:
		env := runtimeDependencyInstallEnv(entry.Product, stage)
		if err := runCommand(append([]string(nil), entry.Install.Args...), env); err != nil {
			return fmt.Errorf("install gopls with Go 1.26.5: %w", err)
		}
	case WindowsRuntimeDependencyProductDotnetCsharpLS:
		args := append([]string(nil), entry.Install.Args...)
		payload, ok := payloads["csharp-ls"]
		if !ok {
			return errors.New("csharp-ls payload is missing")
		}
		nuGetConfig, err := prepareWindowsCsharpNuGetIsolation(stage, filepath.Dir(payload))
		if err != nil {
			return fmt.Errorf("prepare csharp-ls private NuGet state: %w", err)
		}
		// --source 与仅清空外部源的 configfile 双重锁定已校验 nupkg；禁止 --add-source 继承用户源。
		args = append(args, "--source", filepath.Dir(payload), "--configfile", nuGetConfig)
		if err := runCommand(args, runtimeDependencyInstallEnv(entry.Product, stage)); err != nil {
			return fmt.Errorf("install csharp-ls with .NET SDK 10.0.400: %w", err)
		}
	case WindowsRuntimeDependencyProductRubySolargraph:
		payload, ok := payloads["solargraph"]
		if !ok {
			return errors.New("solargraph gem payload is missing")
		}
		if relative, err := filepath.Rel(stage, payload); err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("solargraph gem payload escapes runtime cohort: %q", payload)
		}
		gemScript := filepath.Join(filepath.Dir(runtimePath), "gem")
		if _, err := requireRegularWindowsRuntimeDependencyPath(gemScript); err != nil {
			return fmt.Errorf("resolve RubyGems script: %w", err)
		}
		args := append([]string{gemScript}, entry.Install.Args...)
		args = append(args, payload)
		if err := runCommand(args, runtimeDependencyInstallEnv(entry.Product, stage)); err != nil {
			return fmt.Errorf("install Solargraph 0.60.2 with Ruby: %w", err)
		}
	case WindowsRuntimeDependencyProductJDKJDTLS:
		// JDTLS 由固定资产直接发布，不经过外部安装器。
	default:
		return fmt.Errorf("runtime dependency product %q has no safe installer", entry.Product)
	}
	return nil
}

type windowsCsharpInstallIsolation struct {
	appData        string
	localAppData   string
	userProfile    string
	nuGetConfig    string
	nuGetPackages  string
	nuGetHTTPCache string
	dotnetCLIHome  string
}

// windowsCsharpInstallIsolationForRoot 统一 C# installer 的用户态路径；所有 NuGet/.NET 状态必须留在当前 stage。
func windowsCsharpInstallIsolationForRoot(root string) windowsCsharpInstallIsolation {
	userRoot := filepath.Join(root, ".dotnet-user")
	return windowsCsharpInstallIsolation{
		appData:        filepath.Join(userRoot, "AppData", "Roaming"),
		localAppData:   filepath.Join(userRoot, "AppData", "Local"),
		userProfile:    userRoot,
		nuGetConfig:    filepath.Join(root, ".nuget", "NuGet.Config"),
		nuGetPackages:  filepath.Join(root, ".nuget-packages"),
		nuGetHTTPCache: filepath.Join(root, ".nuget-http-cache"),
		dotnetCLIHome:  filepath.Join(root, ".dotnet-home"),
	}
}

// prepareWindowsCsharpNuGetIsolation 创建只含当前已校验 nupkg 的 NuGet 配置和隔离用户目录。
func prepareWindowsCsharpNuGetIsolation(stage, sourceDir string) (string, error) {
	if err := validateWindowsInstallerPathWithinRoot(stage, sourceDir, false); err != nil {
		return "", fmt.Errorf("validate private NuGet source: %w", err)
	}
	sourceInfo, err := os.Lstat(sourceDir)
	if err != nil {
		return "", securefs.WrapErrorForPath(err, sourceDir)
	}
	if isUnsafeAssetFile(sourceInfo) || !sourceInfo.IsDir() {
		return "", fmt.Errorf("private NuGet source is not a real directory: %s", securefs.RedactPath(sourceDir))
	}

	isolation := windowsCsharpInstallIsolationForRoot(stage)
	for _, directory := range []string{
		isolation.appData,
		isolation.localAppData,
		isolation.userProfile,
		filepath.Dir(isolation.nuGetConfig),
		isolation.nuGetPackages,
		isolation.nuGetHTTPCache,
		isolation.dotnetCLIHome,
	} {
		if err := ensureDirectoryNoSymlink(directory); err != nil {
			return "", fmt.Errorf("create private C# user-state directory %s: %w", securefs.RedactPath(directory), securefs.WrapErrorForPath(err, directory))
		}
	}
	if err := validateWindowsInstallerPathWithinRoot(stage, isolation.nuGetConfig, true); err != nil {
		return "", fmt.Errorf("validate private NuGet config path: %w", err)
	}

	var escapedSource bytes.Buffer
	if err := xml.EscapeText(&escapedSource, []byte(sourceDir)); err != nil {
		return "", fmt.Errorf("escape private NuGet source: %w", err)
	}
	config := fmt.Sprintf("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<configuration>\n  <packageSources>\n    <clear />\n    <add key=\"locked-csharp-ls\" value=\"%s\" />\n  </packageSources>\n</configuration>\n", escapedSource.String())
	configFile, err := os.OpenFile(isolation.nuGetConfig, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", securefs.WrapErrorForPath(err, isolation.nuGetConfig)
	}
	_, writeErr := io.WriteString(configFile, config)
	closeErr := configFile.Close()
	if writeErr != nil {
		return "", securefs.WrapErrorForPath(writeErr, isolation.nuGetConfig)
	}
	if closeErr != nil {
		return "", securefs.WrapErrorForPath(closeErr, isolation.nuGetConfig)
	}
	if _, err := requireRegularWindowsRuntimeDependencyPath(isolation.nuGetConfig); err != nil {
		return "", fmt.Errorf("validate private NuGet config: %w", err)
	}
	return isolation.nuGetConfig, nil
}

func requireWindowsCsharpInstallIsolation(root string) error {
	isolation := windowsCsharpInstallIsolationForRoot(root)
	for _, directory := range []string{
		isolation.appData,
		isolation.localAppData,
		isolation.userProfile,
		isolation.nuGetPackages,
		isolation.nuGetHTTPCache,
		isolation.dotnetCLIHome,
	} {
		if err := validateWindowsInstallerPathWithinRoot(root, directory, false); err != nil {
			return fmt.Errorf("validate private C# user-state directory: %w", err)
		}
		info, err := os.Lstat(directory)
		if err != nil {
			return securefs.WrapErrorForPath(err, directory)
		}
		if isUnsafeAssetFile(info) || !info.IsDir() {
			return fmt.Errorf("private C# user-state path is not a real directory: %s", securefs.RedactPath(directory))
		}
	}
	if err := validateWindowsInstallerPathWithinRoot(root, isolation.nuGetConfig, false); err != nil {
		return fmt.Errorf("validate private NuGet config: %w", err)
	}
	if _, err := requireRegularWindowsRuntimeDependencyPath(isolation.nuGetConfig); err != nil {
		return fmt.Errorf("private NuGet config is unavailable: %w", err)
	}
	return nil
}

func runtimeDependencyInstallEnv(product WindowsRuntimeDependencyProduct, stage string) []string {
	switch product {
	case WindowsRuntimeDependencyProductGoGopls:
		return []string{
			"GOBIN=" + filepath.Join(stage, "bin"),
			"GOROOT=" + filepath.Join(stage, "go"),
			"GOMODCACHE=" + filepath.Join(stage, ".gomodcache"),
			"GOCACHE=" + filepath.Join(stage, ".gocache"),
			"GOWORK=off",
			"GO111MODULE=on",
			"GOPROXY=https://proxy.golang.org",
		}
	case WindowsRuntimeDependencyProductGoSQLS:
		// SQLS 的 os.UserConfigDir 依赖 APPDATA；绑定到产品 cohort 内的 config，
		// 防止运行时读取或写入系统用户目录。
		return []string{"APPDATA=" + filepath.Join(stage, "config")}
	case WindowsRuntimeDependencyProductDotnetCsharpLS:
		isolation := windowsCsharpInstallIsolationForRoot(stage)
		return []string{
			"APPDATA=" + isolation.appData,
			"LOCALAPPDATA=" + isolation.localAppData,
			"USERPROFILE=" + isolation.userProfile,
			"NUGET_CONFIG=" + isolation.nuGetConfig,
			"NUGET_PACKAGES=" + isolation.nuGetPackages,
			"NUGET_HTTP_CACHE_PATH=" + isolation.nuGetHTTPCache,
			"DOTNET_CLI_HOME=" + isolation.dotnetCLIHome,
			"DOTNET_MULTILEVEL_LOOKUP=0",
			"DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1",
		}
	case WindowsRuntimeDependencyProductJDKJDTLS:
		return []string{"JAVA_HOME=" + filepath.Join(stage, "jdk-21.0.12+8")}
	case WindowsRuntimeDependencyProductRubySolargraph:
		gemRoot := filepath.Join(stage, "gems")
		return []string{"GEM_HOME=" + gemRoot, "GEM_PATH=" + gemRoot}
	case WindowsRuntimeDependencyProductRubyLSP:
		return windowsRubyLSPPrivateEnvironment(stage)
	case WindowsRuntimeDependencyProductSwiftSourceKitLS:
		return swiftWindowsRuntimeEnvironment(stage)
	default:
		return nil
	}
}

func defaultWindowsRuntimeDependencyCommandRunner(ctx context.Context, executable, workingDir string, args, env []string) error {
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = workingDir
	command.Env = runtimeDependencyCommandEnvironment(env)
	output, err := command.CombinedOutput()
	if err != nil {
		return newProcessFailureError(
			"runtime-dependency-command",
			"runtime",
			securefs.WrapErrorForPath(joinProcessFailureCause(ctx.Err(), err), executable),
			output,
			len(args),
			0,
		)
	}
	return nil
}

func runtimeDependencyCommandEnvironment(overrides []string) []string {
	values := make(map[string]string)
	for _, value := range os.Environ() {
		key, _, ok := strings.Cut(value, "=")
		if ok && key != "" {
			values[strings.ToUpper(key)] = value
		}
	}
	for _, value := range overrides {
		key, overrideValue, ok := strings.Cut(value, "=")
		if ok && key != "" {
			upperKey := strings.ToUpper(key)
			// 这些 Ruby/Bundler 注入项的空值是有意的 unset 标记。不能把它们
			// 作为空环境变量传给 Ruby，否则父进程残留配置仍可能污染工作区。
			if (upperKey == "RUBYGEMS_GEMDEPS" || upperKey == "RUBYOPT" || upperKey == "RUBYLIB") && overrideValue == "" {
				delete(values, upperKey)
				continue
			}
			values[upperKey] = value
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, values[key])
	}
	return result
}

func requireWindowsRuntimeDependencyPaths(stage string, entry WindowsRuntimeDependencyCatalogEntry, architecture string) error {
	for _, relative := range []string{runtimeDependencyRuntimeExecutablePath(entry, architecture), entry.Install.ServerPath} {
		path, err := runtimeDependencyExactPath(stage, relative)
		if err != nil {
			return fmt.Errorf("runtime dependency %q did not publish required path %q: %w", entry.Product, relative, err)
		}
		if err := validateWindowsInstallerPathWithinRoot(stage, path, false); err != nil {
			return fmt.Errorf("runtime dependency %q required path %q is unsafe: %w", entry.Product, relative, err)
		}
		if _, err := requireRegularWindowsRuntimeDependencyPath(path); err != nil {
			return fmt.Errorf("runtime dependency %q required path %q: %w", entry.Product, relative, err)
		}
	}
	if entry.Product == WindowsRuntimeDependencyProductDotnetCsharpLS {
		if err := requireWindowsCsharpInstallIsolation(stage); err != nil {
			return fmt.Errorf("csharp-ls user-state isolation is incomplete: %w", err)
		}
	}
	if entry.Product == WindowsRuntimeDependencyProductSwiftSourceKitLS {
		if err := validateSwiftWindowsRuntimeDependencyPayloads(stage); err != nil {
			return err
		}
	}
	if entry.Product == WindowsRuntimeDependencyProductRubyLSP {
		if architecture != WindowsHostArchARM64 {
			return &WindowsRuntimeDependencyUnsupportedError{Product: entry.Product, Architecture: architecture, Reason: "Ruby LSP is locked to the native ARM64 closure"}
		}
		if err := requireWindowsRubyLSPInstalledClosure(stage); err != nil {
			return fmt.Errorf("Ruby LSP private closure is incomplete: %w", err)
		}
	}
	if entry.Product == WindowsRuntimeDependencyProductGoSQLS {
		binaryPath, err := runtimeDependencyExactPath(stage, entry.Install.ServerPath)
		if err != nil {
			return fmt.Errorf("resolve Go SQLS server path: %w", err)
		}
		if err := ValidateWindowsGoSQLSExecutable(binaryPath, architecture); err != nil {
			return err
		}
		configRoot := filepath.Join(stage, "config")
		configInfo, configErr := os.Lstat(configRoot)
		if configErr != nil {
			return fmt.Errorf("SQLS product-owned APPDATA directory is unavailable: %w", configErr)
		}
		if isUnsafeAssetFile(configInfo) || !configInfo.IsDir() {
			return fmt.Errorf("SQLS product-owned APPDATA path is not a real directory: %s", securefs.RedactPath(configRoot))
		}
	}
	return nil
}

func runtimeDependencyRuntimeExecutablePath(entry WindowsRuntimeDependencyCatalogEntry, architecture string) string {
	if entry.Product == WindowsRuntimeDependencyProductRubySolargraph || entry.Product == WindowsRuntimeDependencyProductRubyLSP {
		for _, asset := range entry.AssetsByArchitecture[architecture] {
			if asset.Component != "ruby" {
				continue
			}
			binaryPath, err := runtimeDependencyRelativePath(asset.BinaryPath)
			if err != nil {
				return entry.Install.RuntimeExecutablePath
			}
			return binaryPath
		}
	}
	return entry.Install.RuntimeExecutablePath
}

func runtimeDependencyExactPath(root, relative string) (string, error) {
	normalized, err := runtimeDependencyRelativePath(relative)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.FromSlash(normalized)), nil
}

func requireRegularWindowsRuntimeDependencyPath(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if isUnsafeAssetFile(info) || !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, fmt.Errorf("path is not a non-empty regular file: %q", path)
	}
	return info, nil
}

func locateWindowsRuntimeDependencyPath(root, relative string) (string, error) {
	normalized, err := runtimeDependencyRelativePath(relative)
	if err != nil {
		return "", err
	}
	if err := validateWindowsInstallerPathWithinRoot(filepath.Dir(root), root, false); err != nil {
		return "", fmt.Errorf("validate runtime dependency tree root: %w", err)
	}
	exact := filepath.Join(root, filepath.FromSlash(normalized))
	if _, err := requireRegularWindowsRuntimeDependencyPath(exact); err == nil {
		return exact, nil
	}
	var matches []string
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if isUnsafeAssetFile(info) {
			return fmt.Errorf("runtime dependency tree contains symlink or reparse point %q", path)
		}
		if entry.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("runtime dependency tree contains unsupported file %q", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		forward := filepath.ToSlash(rel)
		if forward == normalized || strings.HasSuffix(forward, "/"+normalized) {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("path %q was not found", normalized)
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("path %q is ambiguous (%d matches)", normalized, len(matches))
	}
	if _, err := requireRegularWindowsRuntimeDependencyPath(matches[0]); err != nil {
		return "", err
	}
	return matches[0], nil
}

type runtimeDependencyReadyManifest struct {
	// Schema 是 ready 清单格式版本，用于拒绝未知序列化合同。
	Schema int `json:"schema"`
	// Product 是清单所属的 Windows 运行时依赖产品。
	Product WindowsRuntimeDependencyProduct `json:"product"`
	// Architecture 是清单所属的 Windows 原生架构。
	Architecture string `json:"architecture"`
	// Cohort 是清单对应的固定版本 cohort 标识。
	Cohort string `json:"cohort"`
	// Assets 是清单记录的固定 URL、版本、摘要和格式集合。
	Assets []runtimeDependencyManifestAsset `json:"assets"`
	// Tree 是 ready 树中每个相对路径的类型、大小和摘要。
	Tree map[string]runtimeDependencyTreeEntry `json:"tree"`
}

type runtimeDependencyManifestAsset struct {
	// Component 是清单内固定资产的产品组件名。
	Component string `json:"component"`
	// Version 是不可使用 latest 替代的固定组件版本。
	Version string `json:"version"`
	// URL 是组件下载所用的绝对 HTTPS 地址。
	URL string `json:"url"`
	// ChecksumAlgorithm 是组件摘要算法。
	ChecksumAlgorithm WindowsRuntimeDependencyChecksumAlgorithm `json:"checksum_algorithm"`
	// Checksum 是组件下载内容的固定摘要。
	Checksum string `json:"checksum"`
	// Format 是组件的固定归档或原始资产格式。
	Format WindowsRuntimeDependencyAssetFormat `json:"format"`
}

type runtimeDependencyTreeEntry struct {
	// Kind 是 ready 树项的 regular-file 或目录类型。
	Kind string `json:"kind"`
	// Size 是 ready 树项的字节大小。
	Size int64 `json:"size,omitempty"`
	// SHA256 是 ready 树项内容的可选摘要。
	SHA256 string `json:"sha256,omitempty"`
}

func writeWindowsRuntimeDependencyReady(stage string, entry WindowsRuntimeDependencyCatalogEntry, architecture, cohort string) (err error) {
	if err := validateWindowsInstallerPathWithinRoot(stage, stage, false); err != nil {
		return fmt.Errorf("validate runtime dependency staging directory before manifest: %w", err)
	}
	tree, err := snapshotAssetTree(stage)
	if err != nil {
		return fmt.Errorf("snapshot runtime dependency tree: %w", err)
	}
	delete(tree, runtimeDependencyReadyFile)
	manifestTree := make(map[string]runtimeDependencyTreeEntry, len(tree))
	for relative, item := range tree {
		manifestTree[relative] = runtimeDependencyTreeEntry{Kind: item.kind, Size: item.size, SHA256: item.hash}
	}
	manifest := runtimeDependencyReadyManifest{Schema: 1, Product: entry.Product, Architecture: architecture, Cohort: cohort, Assets: runtimeDependencyManifestAssets(entry.AssetsByArchitecture[architecture]), Tree: manifestTree}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode runtime dependency ready manifest: %w", err)
	}
	if err := validateWindowsInstallerPathWithinRoot(stage, stage, false); err != nil {
		return fmt.Errorf("validate runtime dependency staging directory before manifest write: %w", err)
	}
	temporary, err := createWindowsInstallerTemp(stage, ".runtime-ready-")
	if err != nil {
		return fmt.Errorf("create runtime dependency ready manifest: %w", err)
	}
	temporaryPath := temporary.Name()
	keep := false
	temporaryClosed := false
	defer func() {
		if !temporaryClosed {
			if closeErr := temporary.Close(); closeErr != nil {
				err = joinWindowsInstallerCleanupError(err, closeErr, "close runtime dependency ready manifest")
			}
		}
		if !keep {
			if removeErr := removeWindowsInstallerPathChecked(stage, temporaryPath); removeErr != nil && !os.IsNotExist(removeErr) {
				err = joinWindowsInstallerCleanupError(err, removeErr, fmt.Sprintf("remove runtime dependency ready manifest temporary file %q", temporaryPath))
			}
		}
	}()
	if err := validateWindowsInstallerPathWithinRoot(stage, temporaryPath, false); err != nil {
		return fmt.Errorf("validate runtime dependency ready manifest temporary file: %w", err)
	}
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict runtime dependency ready manifest: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write runtime dependency ready manifest: %w", err)
	}
	if closeErr := temporary.Close(); closeErr != nil {
		temporaryClosed = true
		return fmt.Errorf("close runtime dependency ready manifest: %w", closeErr)
	}
	temporaryClosed = true
	if err := renameWindowsInstallerPathChecked(stage, temporaryPath, filepath.Join(stage, runtimeDependencyReadyFile)); err != nil {
		return fmt.Errorf("publish runtime dependency ready manifest: %w", err)
	}
	keep = true
	return nil
}

func runtimeDependencyManifestAssets(assets []WindowsRuntimeDependencyAsset) []runtimeDependencyManifestAsset {
	result := make([]runtimeDependencyManifestAsset, 0, len(assets))
	for _, asset := range assets {
		result = append(result, runtimeDependencyManifestAsset{Component: asset.Component, Version: asset.Version, URL: asset.URL, ChecksumAlgorithm: asset.ChecksumAlgorithm, Checksum: asset.Checksum, Format: asset.Format})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Component < result[j].Component })
	return result
}

func runtimeDependencyCacheResult(entry WindowsRuntimeDependencyCatalogEntry, platform WindowsHostPlatform, architecture, cohort, root string) (WindowsRuntimeDependencyProvisionResult, error) {
	return runtimeDependencyCacheResultContext(context.Background(), entry, platform, architecture, cohort, root)
}

// runtimeDependencyCacheResultContext 在保留非 Swift 完整树校验的同时响应取消，避免 resolver deadline 后继续占用 goroutine。
func runtimeDependencyCacheResultContext(ctx context.Context, entry WindowsRuntimeDependencyCatalogEntry, platform WindowsHostPlatform, architecture, cohort, root string) (WindowsRuntimeDependencyProvisionResult, error) {
	if ctx == nil {
		return WindowsRuntimeDependencyProvisionResult{}, errors.New("runtime dependency cache context is nil")
	}
	if err := ctx.Err(); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, err
	}
	cacheRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Clean(root)))))
	pathGuardErr := validateWindowsInstallerPathWithinRoot(cacheRoot, root, true)
	if pathGuardErr != nil && !os.IsNotExist(pathGuardErr) {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("validate runtime dependency cache path: %w", pathGuardErr)
	}
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return WindowsRuntimeDependencyProvisionResult{}, &WindowsRuntimeDependencyCacheMissError{Product: entry.Product, Architecture: architecture, RootPath: root}
	}
	if err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("inspect runtime dependency cache %q: %w", root, err)
	}
	if isUnsafeAssetFile(info) || !info.IsDir() {
		return WindowsRuntimeDependencyProvisionResult{}, runtimeDependencyReadyInvalidError("runtime dependency cache root is not a real directory: %q", root)
	}
	manifestPath := filepath.Join(root, runtimeDependencyReadyFile)
	manifestInfo, err := os.Lstat(manifestPath)
	if os.IsNotExist(err) {
		return WindowsRuntimeDependencyProvisionResult{}, &WindowsRuntimeDependencyCacheMissError{Product: entry.Product, Architecture: architecture, RootPath: root}
	}
	if err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("inspect runtime dependency ready manifest: %w", err)
	}
	if isUnsafeAssetFile(manifestInfo) || !manifestInfo.Mode().IsRegular() {
		return WindowsRuntimeDependencyProvisionResult{}, runtimeDependencyReadyInvalidError("runtime dependency ready manifest is not a real regular file: %q", manifestPath)
	}
	if err := validateWindowsInstallerExistingFile(manifestPath); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("validate runtime dependency ready manifest before read: %w", err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("read runtime dependency ready manifest: %w", err)
	}
	var manifest runtimeDependencyReadyManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, errors.Join(ErrWindowsRuntimeDependencyCacheMiss, fmt.Errorf("%w: decode runtime dependency ready manifest: %w", ErrWindowsRuntimeDependencyReadyInvalid, err))
	}
	if manifest.Schema != 1 {
		return WindowsRuntimeDependencyProvisionResult{}, runtimeDependencyReadyInvalidError("ready manifest schema=%d want=1", manifest.Schema)
	}
	if manifest.Product != entry.Product || manifest.Architecture != architecture || manifest.Cohort != cohort {
		return WindowsRuntimeDependencyProvisionResult{}, runtimeDependencyReadyInvalidError("ready identity product=%q architecture=%q cohort=%q want product=%q architecture=%q cohort=%q", manifest.Product, manifest.Architecture, manifest.Cohort, entry.Product, architecture, cohort)
	}
	expectedAssets := runtimeDependencyManifestAssets(entry.AssetsByArchitecture[architecture])
	if !runtimeDependencyManifestAssetsEqual(manifest.Assets, expectedAssets) {
		return WindowsRuntimeDependencyProvisionResult{}, runtimeDependencyReadyInvalidError("ready manifest assets mismatch actual=%v expected=%v", manifest.Assets, expectedAssets)
	}
	actualTree, err := snapshotAssetTreeContext(ctx, root)
	if err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, errors.Join(ErrWindowsRuntimeDependencyCacheMiss, fmt.Errorf("%w: inspect runtime dependency cache tree: %w", ErrWindowsRuntimeDependencyReadyInvalid, securefs.WrapErrorForPath(err, root)))
	}
	delete(actualTree, runtimeDependencyReadyFile)
	if !runtimeDependencyTreeMatches(manifest.Tree, actualTree) {
		return WindowsRuntimeDependencyProvisionResult{}, runtimeDependencyReadyInvalidError("ready tree mismatch manifest_entries=%d actual_entries=%d first_diff=%s", len(manifest.Tree), len(actualTree), runtimeDependencyTreeFirstDiff(manifest.Tree, actualTree))
	}
	if err := requireWindowsRuntimeDependencyPaths(root, entry, architecture); err != nil {
		return WindowsRuntimeDependencyProvisionResult{}, errors.Join(ErrWindowsRuntimeDependencyCacheMiss, fmt.Errorf("%w: required runtime path check: %w", ErrWindowsRuntimeDependencyReadyInvalid, err))
	}
	return runtimeDependencyResult(entry, platform, architecture, cohort, root, false), nil
}

// WindowsRuntimeDependencyCacheMissError 表示 Windows cohort 的 ready manifest、完整树或必需绝对路径校验失败；调用方必须重新物化，不得把缓存误报为可用。
// runtimeDependencyReadyInvalidError keeps invalid ready trees classified as
// cache misses while retaining the detailed validation reason for callers.
func runtimeDependencyReadyInvalidError(format string, args ...any) error {
	return errors.Join(ErrWindowsRuntimeDependencyCacheMiss, fmt.Errorf("%w: "+format, append([]any{ErrWindowsRuntimeDependencyReadyInvalid}, args...)...))
}

// WindowsRuntimeDependencyCacheMissError 表示 Windows 运行时依赖 ready 校验失败，需要重新物化。
type WindowsRuntimeDependencyCacheMissError struct {
	// Product 是 ready 校验失败的 Windows 运行时依赖产品。
	Product WindowsRuntimeDependencyProduct
	// Architecture 是 ready 校验失败的 Windows 原生架构。
	Architecture string
	// RootPath 是缺失或不一致的 Windows cohort 绝对根路径。
	RootPath string
}

// Error 返回 Windows cohort cache miss 的产品、架构和根路径诊断信息。
func (e *WindowsRuntimeDependencyCacheMissError) Error() string {
	if e == nil {
		return ErrWindowsRuntimeDependencyCacheMiss.Error()
	}
	return fmt.Sprintf("%s: %s/%s at %q", ErrWindowsRuntimeDependencyCacheMiss, e.Product, e.Architecture, e.RootPath)
}

// Unwrap 返回 Windows 运行时依赖 cache miss 哨兵，供 errors.Is 精确分类。
func (e *WindowsRuntimeDependencyCacheMissError) Unwrap() error {
	return ErrWindowsRuntimeDependencyCacheMiss
}

func runtimeDependencyManifestAssetsEqual(left, right []runtimeDependencyManifestAsset) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]runtimeDependencyManifestAsset(nil), left...)
	rightCopy := append([]runtimeDependencyManifestAsset(nil), right...)
	sort.Slice(leftCopy, func(i, j int) bool { return leftCopy[i].Component < leftCopy[j].Component })
	sort.Slice(rightCopy, func(i, j int) bool { return rightCopy[i].Component < rightCopy[j].Component })
	for i := range leftCopy {
		if leftCopy[i] != rightCopy[i] {
			return false
		}
	}
	return true
}

func runtimeDependencyTreeMatches(want map[string]runtimeDependencyTreeEntry, got map[string]assetTreeEntry) bool {
	if len(want) != len(got) {
		return false
	}
	for relative, expected := range want {
		actual, ok := got[relative]
		if !ok || expected.Kind != actual.kind || expected.Size != actual.size || expected.SHA256 != actual.hash {
			return false
		}
	}
	return true
}

func runtimeDependencyTreeFirstDiff(want map[string]runtimeDependencyTreeEntry, got map[string]assetTreeEntry) string {
	keys := make([]string, 0, len(want)+len(got))
	seen := make(map[string]struct{}, len(want)+len(got))
	for key := range want {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range got {
		if _, ok := seen[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		expected, wantOK := want[key]
		actual, gotOK := got[key]
		if !wantOK || !gotOK || expected.Kind != actual.kind || expected.Size != actual.size || expected.SHA256 != actual.hash {
			return fmt.Sprintf("path=%q want_present=%t got_present=%t want_kind=%q got_kind=%q want_size=%d got_size=%d want_sha256=%q got_sha256=%q", key, wantOK, gotOK, expected.Kind, actual.kind, expected.Size, actual.size, expected.SHA256, actual.hash)
		}
	}
	return "<unknown>"
}

func runtimeDependencyResult(entry WindowsRuntimeDependencyCatalogEntry, platform WindowsHostPlatform, architecture, cohort, root string, cacheHit bool) WindowsRuntimeDependencyProvisionResult {
	executable, _ := runtimeDependencyExactPath(root, runtimeDependencyRuntimeExecutablePath(entry, architecture))
	server, _ := runtimeDependencyExactPath(root, entry.Install.ServerPath)
	args := []string(nil)
	if entry.Product == WindowsRuntimeDependencyProductJDKJDTLS {
		args = append([]string(nil), entry.Install.Args...)
		for index := range args {
			switch args[index] {
			case "-jar":
				if index+1 < len(args) {
					args[index+1] = server
				}
			case "-configuration":
				if index+1 < len(args) {
					args[index+1] = filepath.Join(runtimeDependencyJDTLSWorkspaceRoot(root, architecture, cohort), "config_win")
				}
			}
		}
		jdtlsWorkspaceRoot := runtimeDependencyJDTLSWorkspaceRoot(root, architecture, cohort)
		workspaceDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.ToLower(cohort+"\x00"+filepath.Clean(jdtlsWorkspaceRoot)))))
		dataParent := filepath.Join(filepath.Dir(jdtlsWorkspaceRoot), "jdtls-data")
		args = append(args, "-data", filepath.Join(dataParent, workspaceDigest))
	} else if entry.Product == WindowsRuntimeDependencyProductRubySolargraph {
		args = []string{server, "stdio"}
	} else if entry.Product == WindowsRuntimeDependencyProductRubyLSP {
		args, _ = windowsRubyLSPLaunchArguments(root)
	} else if entry.Product == WindowsRuntimeDependencyProductSwiftSourceKitLS {
		executable = filepath.Join(WindowsSwiftSourceKitLSPToolchainBin(root), "swiftc.exe")
		server = filepath.Join(WindowsSwiftSourceKitLSPToolchainBin(root), "sourcekit-lsp.exe")
		args = swiftWindowsSourceKitLSPLaunchArgs(root)
	}
	return WindowsRuntimeDependencyProvisionResult{
		Product: entry.Product, Status: WindowsRuntimeDependencyStatusInstallable, Platform: platform,
		Architecture: architecture, Cohort: cohort, RootPath: root, WorkingDirectory: root,
		ExecutablePath: executable, ServerPath: server, Args: args,
		InstallArgs: append([]string(nil), entry.Install.Args...), Env: runtimeDependencyInstallEnvForResult(entry.Product, root), CacheHit: cacheHit,
	}
}

func runtimeDependencyJDTLSWorkspaceRoot(root, architecture, cohort string) string {
	cacheRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Clean(root)))))
	return filepath.Join(cacheRoot, "runtime-workspaces", string(WindowsRuntimeDependencyProductJDKJDTLS), architecture, cohort)
}

func runtimeDependencyRubySolargraphWorkspaceRoot(root, architecture, cohort string) string {
	cacheRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Clean(root)))))
	return filepath.Join(cacheRoot, "runtime-workspaces", string(WindowsRuntimeDependencyProductRubySolargraph), architecture, cohort)
}

func prepareWindowsRuntimeDependencyJDTLSWorkspaceConfiguration(assetRoot, workspaceRoot string) error {
	source := filepath.Join(assetRoot, "config_win")
	destination := filepath.Join(filepath.Clean(workspaceRoot), "config_win")
	return copyWindowsRuntimeDependencyDirectory(source, destination)
}

func copyWindowsRuntimeDependencyDirectory(source, destination string) error {
	if err := validateWindowsInstallerPathWithinRoot(filepath.Dir(source), source, false); err != nil {
		return fmt.Errorf("validate configuration source: %w", err)
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if isUnsafeAssetFile(info) || !info.IsDir() {
		return fmt.Errorf("source %q is not a real directory", source)
	}
	if err := ensureDirectoryNoSymlink(destination); err != nil {
		return err
	}
	if err := validateWindowsInstallerPathWithinRoot(destination, destination, false); err != nil {
		return fmt.Errorf("validate configuration destination: %w", err)
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entryInfo, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if isUnsafeAssetFile(entryInfo) {
			return fmt.Errorf("configuration tree contains symlink or reparse point %q", path)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		target := filepath.Join(destination, relative)
		if entryInfo.IsDir() {
			return ensureDirectoryNoSymlink(target)
		}
		if !entryInfo.Mode().IsRegular() {
			return fmt.Errorf("configuration tree contains unsupported file %q", path)
		}
		if err := validateWindowsInstallerPathWithinRoot(source, path, false); err != nil {
			return fmt.Errorf("validate configuration input %q: %w", path, err)
		}
		if err := ensureDirectoryNoSymlink(filepath.Dir(target)); err != nil {
			return err
		}
		if err := validateWindowsInstallerPathWithinRoot(destination, target, true); err != nil {
			return fmt.Errorf("validate configuration destination %q: %w", target, err)
		}
		targetInfo, targetErr := os.Lstat(target)
		if targetErr == nil {
			if isUnsafeAssetFile(targetInfo) {
				return fmt.Errorf("configuration destination contains symlink or reparse point %q", target)
			}
		} else if !os.IsNotExist(targetErr) {
			return targetErr
		}
		if err := validateWindowsInstallerExistingFile(path); err != nil {
			return fmt.Errorf("validate configuration input before open %q: %w", path, err)
		}
		if err := validateWindowsInstallerPathWithinRoot(destination, target, true); err != nil {
			return fmt.Errorf("validate configuration destination before open %q: %w", target, err)
		}
		input, err := openWindowsInstallerInput(path)
		if err != nil {
			return err
		}
		if err := validateWindowsInstallerPathWithinRoot(destination, target, true); err != nil {
			return joinWindowsInstallerCleanupError(err, input.Close(), fmt.Sprintf("close runtime dependency input %q", path))
		}
		output, err := openWindowsInstallerOutput(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return joinWindowsInstallerCleanupError(err, input.Close(), fmt.Sprintf("close runtime dependency input %q", path))
		}
		_, copyErr := io.Copy(output, input)
		closeOutputErr := output.Close()
		closeInputErr := input.Close()
		var operationErr error
		if copyErr != nil {
			operationErr = copyErr
		}
		operationErr = joinWindowsInstallerCleanupError(operationErr, closeOutputErr, fmt.Sprintf("close runtime dependency output %q", target))
		operationErr = joinWindowsInstallerCleanupError(operationErr, closeInputErr, fmt.Sprintf("close runtime dependency input %q", path))
		return operationErr
	})
}

func runtimeDependencyInstallEnvForResult(product WindowsRuntimeDependencyProduct, root string) []string {
	return runtimeDependencyInstallEnv(product, root)
}

func removeInvalidWindowsRuntimeDependencyCache(root string) error {
	cacheRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Clean(root)))))
	return removeInvalidWindowsRuntimeDependencyCacheWithinRoot(cacheRoot, root)
}

func removeInvalidWindowsRuntimeDependencyCacheWithinRoot(cacheRoot, root string) error {
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect stale runtime dependency cache %q: %w", root, err)
	}
	if isUnsafeAssetFile(info) {
		return fmt.Errorf("%w: stale runtime dependency cache is a symlink or reparse point: %q", ErrWindowsRuntimeDependencyReadyInvalid, root)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: stale runtime dependency cache is not a directory: %q", ErrWindowsRuntimeDependencyReadyInvalid, root)
	}
	if err := removeWindowsInstallerAllChecked(cacheRoot, root); err != nil {
		return joinWindowsInstallerCleanupError(nil, err, fmt.Sprintf("remove stale runtime dependency cache %q", root))
	}
	return nil
}
