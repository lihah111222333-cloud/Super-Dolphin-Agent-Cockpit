//go:build windows && e2e

package installer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	windowsLSPCatalogE2EEnv         = "MCP_LSP_WINDOWS_CATALOG_E2E"
	windowsLSPCatalogE2EProductsEnv = "MCP_LSP_WINDOWS_CATALOG_E2E_PRODUCTS"
	windowsLSPCatalogE2EReceiptEnv  = "MCP_LSP_WINDOWS_CATALOG_E2E_RECEIPT"
	// The official clangd archive is large and pure-Go xz extraction plus
	// cache-hit tree verification are intentionally bounded by this Windows-only
	// E2E budget.
	windowsLSPCatalogE2ETimeout = 90 * time.Minute
	// LSP exit is a protocol event; EOF is only a bounded recovery for a server
	// that does not terminate after receiving the exit notification.
	windowsLSPCatalogE2EExitGrace = 10 * time.Second
)

var defaultWindowsLSPCatalogE2EProducts = []WindowsLSPProduct{
	WindowsLSPProductBuf,
	WindowsLSPProductTerraform,
	WindowsLSPProductRustAnalyzer,
	WindowsLSPProductClangd,
	WindowsLSPProductKotlin,
	WindowsLSPProductDart,
	WindowsLSPProductLuaLanguageLS,
}

type windowsLSPCatalogE2EReceipt struct {
	// Product 是本次 Windows 原生 catalog E2E 产品名。
	Product string `json:"product"`
	// Status 是本次 Provision 与真实进程验证的最终状态。
	Status string `json:"status"`
	// Duration 是从子测试开始到 receipt 生成前的耗时。
	Duration string `json:"duration"`
	// Provision 是下载、校验和解包官方资产的耗时。
	Provision string `json:"provision_duration,omitempty"`
	// HandshakeTime 是真实 LSP 进程握手与退出检查的耗时。
	HandshakeTime string `json:"handshake_duration,omitempty"`
	// PayloadBytes 是锁定官方归档的实际字节数。
	PayloadBytes int64 `json:"payload_bytes,omitempty"`
	// ReadyFiles 是发布 ready 树中的常规文件数量。
	ReadyFiles int `json:"ready_files,omitempty"`
	// ReadyBytes 是发布 ready 树中的常规文件总字节数。
	ReadyBytes int64 `json:"ready_bytes,omitempty"`
	// StagingClean 表示本次检查时没有遗留临时 staging 目录。
	StagingClean bool `json:"staging_clean"`
	// CacheHit 表示第二次 Provision 复用了缓存并完成了完整校验。
	CacheHit bool `json:"cache_hit"`
	// CacheHitTime 是第二次无下载 Provision 的耗时。
	CacheHitTime string `json:"cache_hit_duration,omitempty"`
	// CacheHitHTTP 是 cache-hit 阶段累计 HTTP 请求数，用于证明未联网。
	CacheHitHTTP int64 `json:"cache_hit_http_requests,omitempty"`
	// CacheRoot 是本次 Windows 临时缓存根路径，生命周期到 receipt 返回前结束。
	CacheRoot string `json:"cache_root,omitempty"`
	// Executable 是经过架构和 PE 校验的绝对 Windows 可执行文件路径。
	Executable string `json:"executable,omitempty"`
	// AssetVersion 是实际选中的锁定资产版本。
	AssetVersion string `json:"asset_version,omitempty"`
	// AssetSHA256 是实际选中的锁定资产 SHA-256。
	AssetSHA256 string `json:"asset_sha256,omitempty"`
	// AssetURL 是实际选中的官方 HTTPS 资产地址。
	AssetURL string `json:"asset_url,omitempty"`
	// HostOS 是原生 Windows 宿主系统标识。
	HostOS string `json:"host_os"`
	// WindowsVersion 是 Windows 原生版本字符串。
	WindowsVersion string `json:"windows_version"`
	// WindowsBuild 是 Windows 原生 build 号。
	WindowsBuild uint32 `json:"windows_build"`
	// NativeArch 是 Windows 原生架构，决定 catalog 资产选择。
	NativeArch string `json:"native_arch"`
	// ProcessArch 是运行 E2E 的 Windows 进程架构，仅用于证明。
	ProcessArch string `json:"process_arch"`
	// PEMachine 是可执行文件 PE 头中的 IMAGE_FILE_MACHINE 值。
	PEMachine string `json:"pe_machine,omitempty"`
	// ArgsCount/ArgsSHA256 证明启动参数稳定，但不把本机路径或参数原文写入收据。
	ArgsCount  int    `json:"args_count,omitempty"`
	ArgsSHA256 string `json:"args_sha256,omitempty"`
	// EnvKeyCount/EnvKeysSHA256 只记录排序后的环境变量键；变量值禁止进入收据。
	EnvKeyCount   int    `json:"env_key_count,omitempty"`
	EnvKeysSHA256 string `json:"env_keys_sha256,omitempty"`
	// PID 是真实 LSP 子进程的进程号。
	PID int `json:"pid"`
	// ExitCode 是真实 LSP 子进程的退出码，强制回收不会伪装成零。
	ExitCode int `json:"exit_code"`
	// ExitMode 是 graceful、stdin-eof-reclaim 或 process-kill-reclaim 生命周期模式。
	ExitMode string `json:"exit_mode,omitempty"`
	// Handshake 是 initialize/shutdown 响应摘要。
	Handshake string `json:"handshake,omitempty"`
	// Semantic 是 clangd 或 Kotlin hover 非空语义证明摘要。
	Semantic string `json:"semantic,omitempty"`
	// StderrBytes/StderrSHA256 记录真实 stderr 的大小与摘要，不保存输出原文。
	StderrBytes  int    `json:"stderr_bytes,omitempty"`
	StderrSHA256 string `json:"stderr_sha256,omitempty"`
	// Error 是未通过的真实错误；不会吞掉进程或协议错误。
	Error string `json:"error,omitempty"`
	// ExpectedError 是被明确记账的产品生命周期预期错误。
	ExpectedError string `json:"expected_error,omitempty"`
}

type windowsLSPMessage struct {
	// JSONRPC 是严格 LSP framed message 的 JSON-RPC 版本字段。
	JSONRPC string `json:"jsonrpc"`
	// ID 是请求或响应的 JSON-RPC 标识。
	ID json.RawMessage `json:"id"`
	// Result 是成功响应的原始 JSON 结果。
	Result json.RawMessage `json:"result"`
	// Error 是失败响应的原始 JSON 错误。
	Error json.RawMessage `json:"error"`
}

type windowsLSPCatalogE2EHTTPGate struct {
	delegate http.RoundTripper
	reject   atomic.Bool
	requests atomic.Int64
}

func (gate *windowsLSPCatalogE2EHTTPGate) RoundTrip(request *http.Request) (*http.Response, error) {
	gate.requests.Add(1)
	if gate.reject.Load() {
		return nil, errors.New("Windows LSP cache-hit network gate rejected an HTTP request")
	}
	return gate.delegate.RoundTrip(request)
}

// TestWindowsLSPCatalogNativeProvisionE2E 自动检测 Windows 版本、原生架构和进程架构，
// 下载并校验匹配的锁定官方资产，以真实进程完成 LSP 握手、语义请求、退出和残留检查。
func TestWindowsLSPCatalogNativeProvisionE2E(t *testing.T) {
	if os.Getenv(windowsLSPCatalogE2EEnv) != "1" {
		t.Skip("set MCP_LSP_WINDOWS_CATALOG_E2E=1 to run the real native Windows catalog E2E")
	}
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("DetectWindowsHostPlatform() error = %v", err)
	}
	if platform.OS != WindowsHostOSWindows {
		t.Fatalf("real catalog E2E requires Windows, got os=%q native=%q process=%q", platform.OS, platform.NativeArch, platform.ProcessArch)
	}
	products, err := windowsLSPCatalogE2EProducts()
	if err != nil {
		t.Fatal(err)
	}
	for _, product := range products {
		product := product
		caseName, nameErr := windowsE2EPlatformCaseName(platform, string(product))
		if nameErr != nil {
			t.Fatal(nameErr)
		}
		t.Run(caseName, func(t *testing.T) {
			receipt := runWindowsLSPCatalogProductE2E(t, product, platform)
			if receiptErr := appendWindowsLSPCatalogE2EReceipt(receipt); receiptErr != nil {
				t.Fatalf("save product receipt: %v", receiptErr)
			}
			t.Logf("catalog product=%s status=%s duration=%s native_arch=%s process_arch=%s args_count=%d handshake=%s exit_code=%d stderr_bytes=%d stderr_sha256=%s", receipt.Product, receipt.Status, receipt.Duration, receipt.NativeArch, receipt.ProcessArch, receipt.ArgsCount, receipt.Handshake, receipt.ExitCode, receipt.StderrBytes, receipt.StderrSHA256)
			if receipt.Error != "" {
				t.Fatalf("catalog product %s: %s", receipt.Product, receipt.Error)
			}
		})
	}
}

func windowsLSPCatalogE2EProducts() ([]WindowsLSPProduct, error) {
	raw := strings.TrimSpace(os.Getenv(windowsLSPCatalogE2EProductsEnv))
	if raw == "" {
		return append([]WindowsLSPProduct(nil), defaultWindowsLSPCatalogE2EProducts...), nil
	}
	seen := make(map[WindowsLSPProduct]struct{})
	products := make([]WindowsLSPProduct, 0)
	for _, token := range strings.Split(raw, ",") {
		product := normalizeProvisionProduct(WindowsLSPProduct(strings.TrimSpace(token)))
		if product == "" {
			return nil, errors.New("Windows LSP catalog E2E product filter contains an empty product")
		}
		if _, duplicate := seen[product]; duplicate {
			return nil, fmt.Errorf("Windows LSP catalog E2E product filter repeats %q", product)
		}
		if _, err := WindowsLSPCatalogEntryForProduct(product); err != nil {
			return nil, fmt.Errorf("Windows LSP catalog E2E product filter %q: %w", product, err)
		}
		seen[product] = struct{}{}
		products = append(products, product)
	}
	return products, nil
}

func runWindowsLSPCatalogProductE2E(t *testing.T, product WindowsLSPProduct, platform WindowsHostPlatform) (receipt windowsLSPCatalogE2EReceipt) {
	t.Helper()
	started := time.Now()
	receipt = windowsLSPCatalogE2EReceipt{Product: string(product), Status: "blocker", PID: -1, ExitCode: -1}
	recordWindowsLSPCatalogE2EPlatform(&receipt, platform)
	defer func() { receipt.Duration = time.Since(started).String() }()

	cacheRoot, cacheCleanup, cacheRootErr := windowsLSPCatalogE2EShortTempDir("win-" + platform.NativeArch + "-" + string(product) + "-cache-")
	if cacheRootErr != nil {
		receipt.Error = fmt.Sprintf("create short Windows cache root: %v", cacheRootErr)
		return receipt
	}
	defer func() {
		if cleanupErr := cacheCleanup(); cleanupErr != nil && receipt.Error == "" {
			receipt.Error = fmt.Sprintf("remove Windows cache root %q: %v", cacheRoot, cleanupErr)
		}
	}()
	receipt.CacheRoot = cacheRoot
	cacheOptions := WindowsAssetCacheOptions{HTTPTimeout: windowsLSPCatalogE2ETimeout}
	var cacheGate *windowsLSPCatalogE2EHTTPGate
	if product == WindowsLSPProductClangd || product == WindowsLSPProductKotlin {
		cacheGate = &windowsLSPCatalogE2EHTTPGate{delegate: http.DefaultTransport}
		cacheOptions.HTTPClient = &http.Client{Transport: cacheGate}
	}
	cache, err := NewWindowsAssetCacheWithOptions(cacheRoot, cacheOptions)
	if err != nil {
		receipt.Error = fmt.Sprintf("NewWindowsAssetCacheWithOptions: %v", err)
		return receipt
	}
	if entries, readErr := os.ReadDir(cache.Root()); readErr != nil {
		receipt.Error = fmt.Sprintf("inspect empty cache: %v", readErr)
		return receipt
	} else if len(entries) != 0 {
		receipt.Error = fmt.Sprintf("cache is not empty before Provision: %d entries", len(entries))
		return receipt
	}

	ctx, cancel := context.WithTimeout(context.Background(), windowsLSPCatalogE2ETimeout)
	defer cancel()
	provisionStarted := time.Now()
	result, err := WindowsProvision(ctx, WindowsProvisionRequest{Product: product, Cache: cache})
	receipt.Provision = time.Since(provisionStarted).String()
	if err != nil && errors.Is(err, ErrWindowsUnsupportedAssetArchitecture) {
		receipt.ExpectedError = ErrWindowsUnsupportedAssetArchitecture.Error()
		var unsupported *WindowsUnsupportedAssetArchitectureError
		if !errors.As(err, &unsupported) {
			receipt.Error = fmt.Sprintf("%s %s error is not typed unsupported architecture: %v", product, platform.NativeArch, err)
			return receipt
		}
		if entries, readErr := os.ReadDir(cache.Root()); readErr != nil {
			receipt.Error = fmt.Sprintf("inspect %s cache after typed unsupported: %v", product, readErr)
			return receipt
		} else if len(entries) != 0 {
			receipt.Error = fmt.Sprintf("%s typed unsupported created cache entries: %d", product, len(entries))
			return receipt
		}
		receipt.Status = "typed-unsupported"
		recordWindowsLSPCatalogE2EStderrEvidence(&receipt, err.Error())
		return receipt
	}
	if err != nil {
		receipt.Error = fmt.Sprintf("Provision: %v", err)
		return receipt
	}
	receipt.Executable = result.Executable
	receipt.AssetVersion = result.Asset.Version
	receipt.AssetSHA256 = result.Asset.SHA256
	receipt.AssetURL = result.Asset.URL
	recordWindowsLSPCatalogE2EPlatform(&receipt, result.Platform)
	receipt.ArgsCount = len(result.Args)
	receipt.ArgsSHA256 = windowsLSPCatalogE2EStringListSHA256(result.Args)
	envKeys := windowsLSPCatalogE2EEnvKeys(result.Env)
	receipt.EnvKeyCount = len(envKeys)
	receipt.EnvKeysSHA256 = windowsLSPCatalogE2EStringListSHA256(envKeys)
	if machine, machineErr := windowsLSPCatalogE2EPEMachine(result.Executable); machineErr != nil {
		receipt.Error = machineErr.Error()
		return receipt
	} else {
		receipt.PEMachine = fmt.Sprintf("0x%04x", machine)
	}
	if err := validateWindowsLSPCatalogE2EResult(cache, product, platform, result); err != nil {
		receipt.Error = err.Error()
		return receipt
	}
	if product == WindowsLSPProductClangd || product == WindowsLSPProductKotlin {
		payloadBytes, readyFiles, readyBytes, statsErr := windowsLSPCatalogE2EAssetStats(cache, result)
		if statsErr != nil {
			receipt.Error = fmt.Sprintf("inspect %s ready asset: %v", product, statsErr)
			return receipt
		}
		receipt.PayloadBytes = payloadBytes
		receipt.ReadyFiles = readyFiles
		receipt.ReadyBytes = readyBytes
		if cleanErr := windowsLSPCatalogE2EAssertStagingClean(cache.Root()); cleanErr != nil {
			receipt.Error = cleanErr.Error()
			return receipt
		}
		receipt.StagingClean = true
	}
	workspace, workspaceCleanup, workspaceErr := windowsLSPCatalogE2EShortTempDir("win-" + platform.NativeArch + "-" + string(product) + "-workspace-")
	if workspaceErr != nil {
		receipt.Error = fmt.Sprintf("create short Windows LSP workspace: %v", workspaceErr)
		return receipt
	}
	defer func() {
		if cleanupErr := workspaceCleanup(); cleanupErr != nil && receipt.Error == "" {
			receipt.Error = fmt.Sprintf("remove Windows LSP workspace %q: %v", workspace, cleanupErr)
		}
	}()
	handshakeStarted := time.Now()
	handshake, semantic, stderr, pid, exitCode, exitMode, err := runWindowsLSPCatalogE2EHandshake(ctx, result, workspace)
	receipt.HandshakeTime = time.Since(handshakeStarted).String()
	receipt.Handshake = handshake
	receipt.Semantic = semantic
	recordWindowsLSPCatalogE2EStderrEvidence(&receipt, stderr)
	receipt.PID = pid
	receipt.ExitCode = exitCode
	receipt.ExitMode = exitMode
	if err != nil {
		if product == WindowsLSPProductBuf && exitMode == "stdin-eof-reclaim" && exitCode == 1 && strings.Contains(stderr, "failed reading header line: EOF") {
			receipt.Status = "forced-reclaim"
			receipt.ExpectedError = err.Error()
			return receipt
		}
		receipt.Error = err.Error()
		return receipt
	}
	if product == WindowsLSPProductClangd || product == WindowsLSPProductKotlin {
		if cacheGate == nil {
			receipt.Error = fmt.Sprintf("%s cache-hit HTTP gate was not initialized", product)
			return receipt
		}
		requestsBeforeHit := cacheGate.requests.Load()
		cacheGate.reject.Store(true)
		cacheHitStarted := time.Now()
		cacheHitResult, cacheHitErr := WindowsProvision(ctx, WindowsProvisionRequest{Product: product, Cache: cache})
		receipt.CacheHitTime = time.Since(cacheHitStarted).String()
		receipt.CacheHitHTTP = cacheGate.requests.Load()
		if cacheHitErr != nil {
			receipt.Error = fmt.Sprintf("%s cache-hit Provision: %v", product, cacheHitErr)
			return receipt
		}
		if cacheHitResult.Executable != result.Executable || cacheHitResult.Asset.SHA256 != result.Asset.SHA256 {
			receipt.Error = fmt.Sprintf("%s cache-hit result changed: executable=%q sha=%q", product, cacheHitResult.Executable, cacheHitResult.Asset.SHA256)
			return receipt
		}
		if cacheGate.requests.Load() != requestsBeforeHit {
			receipt.Error = fmt.Sprintf("%s cache-hit performed HTTP requests: before=%d after=%d", product, requestsBeforeHit, cacheGate.requests.Load())
			return receipt
		}
		if cleanErr := windowsLSPCatalogE2EAssertStagingClean(cache.Root()); cleanErr != nil {
			receipt.Error = cleanErr.Error()
			return receipt
		}
		if _, readyFiles, readyBytes, statsErr := windowsLSPCatalogE2EAssetStats(cache, cacheHitResult); statsErr != nil {
			receipt.Error = fmt.Sprintf("inspect %s cache-hit ready asset: %v", product, statsErr)
			return receipt
		} else {
			receipt.ReadyFiles = readyFiles
			receipt.ReadyBytes = readyBytes
		}
		receipt.CacheHit = true
		receipt.StagingClean = true
	}
	receipt.Status = "pass"
	return receipt
}

func windowsLSPCatalogE2EShortTempDir(prefix string) (string, func() error, error) {
	tempRoot := strings.TrimSpace(os.Getenv("TEMP"))
	if tempRoot == "" {
		return "", func() error { return nil }, errors.New("TEMP is empty; cannot create short Windows E2E path")
	}
	root, err := os.MkdirTemp(tempRoot, prefix)
	if err != nil {
		return "", func() error { return nil }, fmt.Errorf("create temporary directory under %q: %w", tempRoot, err)
	}
	return root, func() error { return os.RemoveAll(root) }, nil
}

func windowsLSPCatalogE2EAssetStats(cache *WindowsAssetCache, result WindowsProvisionResult) (payloadBytes int64, readyFiles int, readyBytes int64, returnErr error) {
	if cache == nil {
		return 0, 0, 0, errors.New("Windows LSP asset stats: cache is nil")
	}
	assetRoot := filepath.Join(
		cache.Root(),
		cacheSegment(string(result.Product)),
		cacheSegment(result.Asset.Version),
		result.Asset.Architecture,
		strings.ToLower(result.Asset.SHA256),
	)
	payloadPath := filepath.Join(assetRoot, "payload"+assetFormatSuffix(result.Asset.Format))
	payloadInfo, err := os.Lstat(payloadPath)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("inspect downloaded payload %q: %w", payloadPath, err)
	}
	if payloadInfo.Mode()&os.ModeSymlink != 0 || !payloadInfo.Mode().IsRegular() || payloadInfo.Size() == 0 {
		return 0, 0, 0, fmt.Errorf("downloaded payload is not a non-empty regular file: %q", payloadPath)
	}
	readyRoot := filepath.Join(assetRoot, "ready")
	readyInfo, err := os.Lstat(readyRoot)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("inspect published ready tree %q: %w", readyRoot, err)
	}
	if readyInfo.Mode()&os.ModeSymlink != 0 || !readyInfo.IsDir() {
		return 0, 0, 0, fmt.Errorf("published ready tree is not a real directory: %q", readyRoot)
	}
	var visit func(string) error
	visit = func(root string) error {
		entries, readErr := os.ReadDir(root)
		if readErr != nil {
			return readErr
		}
		for _, entry := range entries {
			fullPath := filepath.Join(root, entry.Name())
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("published ready tree contains a symlink: %q", fullPath)
			}
			if entry.IsDir() {
				if err := visit(fullPath); err != nil {
					return err
				}
				continue
			}
			info, infoErr := entry.Info()
			if infoErr != nil {
				return infoErr
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("published ready tree contains non-regular file %q", fullPath)
			}
			readyFiles++
			readyBytes += info.Size()
		}
		return nil
	}
	if err := visit(readyRoot); err != nil {
		return 0, 0, 0, fmt.Errorf("walk published ready tree: %w", err)
	}
	return payloadInfo.Size(), readyFiles, readyBytes, nil
}

func windowsLSPCatalogE2EAssertStagingClean(root string) error {
	var visit func(string) error
	visit = func(directory string) error {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return fmt.Errorf("inspect staging directory %q: %w", directory, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".ready-") ||
				strings.HasPrefix(name, ".verify-") ||
				strings.HasPrefix(name, ".payload-") ||
				strings.HasPrefix(name, ".download-") ||
				strings.HasPrefix(name, ".staging-") {
				return fmt.Errorf("Windows LSP staging residue remains at %q", filepath.Join(directory, name))
			}
			if entry.IsDir() {
				if err := visit(filepath.Join(directory, name)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(root)
}

func recordWindowsLSPCatalogE2EPlatform(receipt *windowsLSPCatalogE2EReceipt, platform WindowsHostPlatform) {
	if receipt == nil {
		return
	}
	receipt.HostOS = string(platform.OS)
	receipt.WindowsVersion = platform.WindowsVersion
	receipt.WindowsBuild = platform.WindowsBuild
	receipt.NativeArch = string(platform.NativeArch)
	receipt.ProcessArch = string(platform.ProcessArch)
}

func validateWindowsLSPCatalogE2EResult(cache *WindowsAssetCache, product WindowsLSPProduct, platform WindowsHostPlatform, result WindowsProvisionResult) error {
	if cache == nil {
		return errors.New("validate result: cache is nil")
	}
	entry, err := WindowsLSPCatalogEntryForProduct(product)
	if err != nil {
		return err
	}
	if result.Product != product {
		return fmt.Errorf("Provision returned product %q, want %q", result.Product, product)
	}
	if result.Platform.OS != platform.OS || result.Platform.NativeArch != platform.NativeArch || result.Platform.ProcessArch != platform.ProcessArch {
		return fmt.Errorf("Provision returned platform os=%q native=%q process=%q, want os=%q native=%q process=%q", result.Platform.OS, result.Platform.NativeArch, result.Platform.ProcessArch, platform.OS, platform.NativeArch, platform.ProcessArch)
	}
	if result.Asset.Architecture != platform.NativeArch {
		return fmt.Errorf("Provision selected asset architecture %q, want native architecture %q", result.Asset.Architecture, platform.NativeArch)
	}
	wantAsset, err := entry.Manifest.AssetForArchitecture(platform.NativeArch)
	if err != nil {
		return err
	}
	if result.Asset.Version != wantAsset.Version || result.Asset.SHA256 != wantAsset.SHA256 || result.Asset.URL != wantAsset.URL || result.Asset.BinaryPath != wantAsset.BinaryPath {
		return fmt.Errorf("Provision asset differs from catalog: got version=%q sha=%q url=%q path=%q, want version=%q sha=%q url=%q path=%q", result.Asset.Version, result.Asset.SHA256, result.Asset.URL, result.Asset.BinaryPath, wantAsset.Version, wantAsset.SHA256, wantAsset.URL, wantAsset.BinaryPath)
	}
	parsedURL, err := url.Parse(result.Asset.URL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" {
		return fmt.Errorf("Provision asset URL is not an official HTTPS URL: %q", result.Asset.URL)
	}
	expectedExecutable := filepath.Join(cache.Root(), cacheSegment(string(product)), cacheSegment(result.Asset.Version), platform.NativeArch, strings.ToLower(result.Asset.SHA256), "ready", filepath.FromSlash(result.Asset.BinaryPath))
	if filepath.Clean(result.Executable) != filepath.Clean(expectedExecutable) {
		return fmt.Errorf("Provision executable = %q, want exact cache path %q", result.Executable, expectedExecutable)
	}
	if !filepath.IsAbs(result.Executable) {
		return fmt.Errorf("Provision executable is not absolute: %q", result.Executable)
	}
	info, err := os.Lstat(result.Executable)
	if err != nil {
		return fmt.Errorf("inspect Provision executable: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("Provision executable is not a non-empty regular file: mode=%s size=%d", info.Mode(), info.Size())
	}
	machine, err := windowsLSPCatalogE2EPEMachine(result.Executable)
	if err != nil {
		return err
	}
	machineArchitecture, err := NormalizeWindowsImageFileMachine(machine)
	if err != nil {
		return fmt.Errorf("normalize Provision executable PE machine 0x%04x: %w", machine, err)
	}
	if machineArchitecture != platform.NativeArch {
		return fmt.Errorf("Provision executable PE machine = 0x%04x (%s), want native architecture %s", machine, machineArchitecture, platform.NativeArch)
	}
	wantArgs, wantEnv, err := windowsLSPCommandMetadata(product)
	if err != nil {
		return err
	}
	if !equalWindowsLSPCatalogE2ESlices(result.Args, wantArgs) || !equalWindowsLSPCatalogE2ESlices(result.Env, wantEnv) {
		return fmt.Errorf("Provision launch metadata mismatch: args_count=%d args_sha256=%s env_key_count=%d env_keys_sha256=%s", len(result.Args), windowsLSPCatalogE2EStringListSHA256(result.Args), len(windowsLSPCatalogE2EEnvKeys(result.Env)), windowsLSPCatalogE2EStringListSHA256(windowsLSPCatalogE2EEnvKeys(result.Env)))
	}
	return nil
}

func windowsLSPCatalogE2EPEMachine(path string) (uint16, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open Provision executable for PE inspection: %w", err)
	}
	defer file.Close()
	var mz [2]byte
	if _, err := io.ReadFull(file, mz[:]); err != nil {
		return 0, fmt.Errorf("read PE DOS signature: %w", err)
	}
	if string(mz[:]) != "MZ" {
		return 0, fmt.Errorf("Provision executable is not a Windows PE file: signature %q", mz)
	}
	if _, err := file.Seek(0x3c, io.SeekStart); err != nil {
		return 0, fmt.Errorf("seek PE header pointer: %w", err)
	}
	var headerOffset uint32
	if err := binary.Read(file, binary.LittleEndian, &headerOffset); err != nil {
		return 0, fmt.Errorf("read PE header pointer: %w", err)
	}
	if _, err := file.Seek(int64(headerOffset), io.SeekStart); err != nil {
		return 0, fmt.Errorf("seek PE header: %w", err)
	}
	var signature [4]byte
	if _, err := io.ReadFull(file, signature[:]); err != nil {
		return 0, fmt.Errorf("read PE signature: %w", err)
	}
	if string(signature[:]) != "PE\x00\x00" {
		return 0, fmt.Errorf("invalid PE signature %q", signature)
	}
	var machine uint16
	if err := binary.Read(file, binary.LittleEndian, &machine); err != nil {
		return 0, fmt.Errorf("read PE machine: %w", err)
	}
	return machine, nil
}

func runWindowsLSPCatalogE2EHandshake(ctx context.Context, result WindowsProvisionResult, workspace string) (handshake, semanticOutput, stderrOutput string, pid, exitCode int, exitMode string, returnErr error) {
	exitCode = -1
	if ctx == nil {
		return "", "", "", 0, exitCode, "", errors.New("LSP handshake context is nil")
	}
	cmd := exec.CommandContext(ctx, result.Executable, result.Args...)
	cmd.Dir = workspace
	cmd.Env = append([]string(nil), os.Environ()...)
	if len(result.Env) > 0 {
		cmd.Env = append(cmd.Env, result.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", "", "", 0, exitCode, "", fmt.Errorf("create LSP stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", "", 0, exitCode, "", fmt.Errorf("create LSP stdout: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", "", stderr.String(), 0, exitCode, "", fmt.Errorf("start exact executable basename=%q args_count=%d args_sha256=%s: %w", filepath.Base(result.Executable), len(result.Args), windowsLSPCatalogE2EStringListSHA256(result.Args), err)
	}
	pid = cmd.Process.Pid
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			if cmd.ProcessState != nil {
				exitCode = cmd.ProcessState.ExitCode()
			}
		}
		stderrOutput = stderr.String()
	}()

	reader := bufio.NewReader(stdout)
	workspaceURI := windowsLSPCatalogE2EFileURI(workspace)
	initializeParams := map[string]any{
		"processId":    os.Getpid(),
		"clientInfo":   map[string]any{"name": "super-dolphin-native-catalog-e2e", "version": "1"},
		"rootUri":      workspaceURI,
		"capabilities": map[string]any{},
		"workspaceFolders": []map[string]any{{
			"uri":  workspaceURI,
			"name": "native-catalog-e2e",
		}},
	}
	if err := writeWindowsLSPCatalogE2EMessage(stdin, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": initializeParams}); err != nil {
		return "", "", stderr.String(), pid, 0, "", fmt.Errorf("send initialize: %w", err)
	}
	initializeResponse, err := waitWindowsLSPCatalogE2EResponse(ctx, reader, 1)
	if err != nil {
		return "", "", stderr.String(), pid, 0, "", fmt.Errorf("initialize handshake: %w", err)
	}
	if len(initializeResponse.Result) == 0 || string(initializeResponse.Result) == "null" {
		return "", "", stderr.String(), pid, 0, "", errors.New("initialize response has no result")
	}
	if err := writeWindowsLSPCatalogE2EMessage(stdin, map[string]any{"jsonrpc": "2.0", "method": "initialized", "params": map[string]any{}}); err != nil {
		return "", "", stderr.String(), pid, 0, "", fmt.Errorf("send initialized: %w", err)
	}
	switch result.Product {
	case WindowsLSPProductClangd:
		semanticOutput, err = runWindowsLSPCatalogE2EClangdHover(ctx, stdin, reader, workspace)
		if err != nil {
			return "", "", stderr.String(), pid, 0, "", fmt.Errorf("clangd semantic proof: %w", err)
		}
	case WindowsLSPProductKotlin:
		semanticOutput, err = runWindowsLSPCatalogE2EKotlinHover(ctx, stdin, reader, workspace)
		if err != nil {
			return "", "", stderr.String(), pid, 0, "", fmt.Errorf("Kotlin semantic proof: %w", err)
		}
	}
	if err := writeWindowsLSPCatalogE2EMessage(stdin, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "shutdown", "params": nil}); err != nil {
		return "", semanticOutput, stderr.String(), pid, 0, "", fmt.Errorf("send shutdown: %w", err)
	}
	shutdownResponse, err := waitWindowsLSPCatalogE2EResponse(ctx, reader, 2)
	if err != nil {
		return "", semanticOutput, stderr.String(), pid, 0, "", fmt.Errorf("shutdown handshake: %w", err)
	}
	// The LSP exit notification has no params member.  Give the server a short
	// opportunity to honor exit before closing stdin; servers that only stop at
	// EOF still get the close below.
	if err := writeWindowsLSPCatalogE2EMessage(stdin, map[string]any{"jsonrpc": "2.0", "method": "exit"}); err != nil {
		return "", semanticOutput, stderr.String(), pid, 0, "", fmt.Errorf("send exit: %w", err)
	}
	waitResult := waitWindowsLSPCatalogE2EProcess(ctx, cmd, stdin)
	exitMode = waitResult.Mode
	waited = true
	exitCode = -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	handshake = fmt.Sprintf("initialize result bytes=%d; shutdown result bytes=%d", len(initializeResponse.Result), len(shutdownResponse.Result))
	if err := assertWindowsLSPCatalogE2EPIDGone(pid); err != nil {
		return handshake, semanticOutput, stderr.String(), pid, exitCode, exitMode, err
	}
	if waitResult.Err != nil {
		return handshake, semanticOutput, stderr.String(), pid, exitCode, exitMode, fmt.Errorf("wait after exit: %w; %s", waitResult.Err, windowsLSPCatalogE2EByteEvidence(stderr.String()))
	}
	if exitCode != 0 {
		return handshake, semanticOutput, stderr.String(), pid, exitCode, exitMode, fmt.Errorf("LSP process exited with code %d; %s", exitCode, windowsLSPCatalogE2EByteEvidence(stderr.String()))
	}
	return handshake, semanticOutput, stderr.String(), pid, exitCode, exitMode, nil
}

func runWindowsLSPCatalogE2EClangdHover(ctx context.Context, stdin io.Writer, reader *bufio.Reader, workspace string) (string, error) {
	sourcePath := filepath.Join(workspace, "main.cpp")
	source := []byte("int meaning = 42;\nint main() { return meaning; }\n")
	if err := os.WriteFile(sourcePath, source, 0o600); err != nil {
		return "", fmt.Errorf("write clangd semantic source %q: %w", sourcePath, err)
	}
	uri := windowsLSPCatalogE2EFileURI(sourcePath)
	if err := writeWindowsLSPCatalogE2EMessage(stdin, map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didOpen",
		"params": map[string]any{
			"textDocument": map[string]any{
				"uri":        uri,
				"languageId": "cpp",
				"version":    1,
				"text":       string(source),
			},
		},
	}); err != nil {
		return "", fmt.Errorf("send clangd textDocument/didOpen: %w", err)
	}
	if err := writeWindowsLSPCatalogE2EMessage(stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "textDocument/hover",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": uri},
			"position":     map[string]any{"line": 1, "character": 4},
		},
	}); err != nil {
		return "", fmt.Errorf("send clangd textDocument/hover: %w", err)
	}
	hoverResponse, err := waitWindowsLSPCatalogE2EResponse(ctx, reader, 3)
	if err != nil {
		return "", fmt.Errorf("clangd textDocument/hover: %w", err)
	}
	if len(hoverResponse.Result) == 0 || string(hoverResponse.Result) == "null" {
		return "", errors.New("clangd textDocument/hover returned an empty result")
	}
	var hover struct {
		Contents json.RawMessage `json:"contents"`
	}
	if err := json.Unmarshal(hoverResponse.Result, &hover); err != nil {
		return "", fmt.Errorf("decode clangd textDocument/hover result: %w", err)
	}
	contents := bytes.TrimSpace(hover.Contents)
	if len(contents) == 0 || bytes.Equal(contents, []byte("null")) || bytes.Equal(contents, []byte("{}")) || bytes.Equal(contents, []byte("[]")) {
		return "", fmt.Errorf("clangd textDocument/hover returned empty contents: %s", windowsLSPCatalogE2EByteEvidence(string(hoverResponse.Result)))
	}
	return fmt.Sprintf("clangd hover result bytes=%d contents bytes=%d", len(hoverResponse.Result), len(contents)), nil
}

// runWindowsLSPCatalogE2EKotlinHover proves that the official Windows Kotlin
// standalone launcher handles didOpen and returns non-empty hover semantics.
func runWindowsLSPCatalogE2EKotlinHover(ctx context.Context, stdin io.Writer, reader *bufio.Reader, workspace string) (string, error) {
	sourcePath := filepath.Join(workspace, "Main.kt")
	source := []byte("package sample\n\nfun answer(): Int = 42\nfun main() {\n    answer()\n}\n")
	if err := os.WriteFile(sourcePath, source, 0o600); err != nil {
		return "", fmt.Errorf("write Kotlin semantic source %q: %w", sourcePath, err)
	}
	uri := windowsLSPCatalogE2EFileURI(sourcePath)
	if err := writeWindowsLSPCatalogE2EMessage(stdin, map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didOpen",
		"params": map[string]any{
			"textDocument": map[string]any{
				"uri":        uri,
				"languageId": "kotlin",
				"version":    1,
				"text":       string(source),
			},
		},
	}); err != nil {
		return "", fmt.Errorf("send Kotlin textDocument/didOpen: %w", err)
	}
	if err := writeWindowsLSPCatalogE2EMessage(stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "textDocument/hover",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": uri},
			"position":     map[string]any{"line": 4, "character": 5},
		},
	}); err != nil {
		return "", fmt.Errorf("send Kotlin textDocument/hover: %w", err)
	}
	hoverResponse, err := waitWindowsLSPCatalogE2EResponse(ctx, reader, 3)
	if err != nil {
		return "", fmt.Errorf("Kotlin textDocument/hover: %w", err)
	}
	if len(hoverResponse.Result) == 0 || string(hoverResponse.Result) == "null" {
		return "", errors.New("Kotlin textDocument/hover returned an empty result")
	}
	var hover struct {
		Contents json.RawMessage `json:"contents"`
	}
	if err := json.Unmarshal(hoverResponse.Result, &hover); err != nil {
		return "", fmt.Errorf("decode Kotlin textDocument/hover result: %w", err)
	}
	contents := bytes.TrimSpace(hover.Contents)
	if len(contents) == 0 || bytes.Equal(contents, []byte("null")) || bytes.Equal(contents, []byte("{}")) || bytes.Equal(contents, []byte("[]")) {
		return "", fmt.Errorf("Kotlin textDocument/hover returned empty contents: %s", windowsLSPCatalogE2EByteEvidence(string(hoverResponse.Result)))
	}
	return fmt.Sprintf("Kotlin hover result bytes=%d contents bytes=%d", len(hoverResponse.Result), len(contents)), nil
}

func writeWindowsLSPCatalogE2EMessage(writer io.Writer, message any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err = writer.Write(payload)
	return err
}

func waitWindowsLSPCatalogE2EResponse(ctx context.Context, reader *bufio.Reader, id int64) (windowsLSPMessage, error) {
	for {
		message, err := readWindowsLSPCatalogE2EMessage(ctx, reader)
		if err != nil {
			return windowsLSPMessage{}, err
		}
		if len(message.ID) == 0 || string(message.ID) == "null" {
			continue
		}
		var gotID int64
		if err := json.Unmarshal(message.ID, &gotID); err != nil {
			return windowsLSPMessage{}, fmt.Errorf("decode response id: %w", err)
		}
		if gotID != id {
			continue
		}
		if len(message.Error) != 0 && string(message.Error) != "null" {
			return windowsLSPMessage{}, fmt.Errorf("JSON-RPC response error: %s", message.Error)
		}
		return message, nil
	}
}

func readWindowsLSPCatalogE2EMessage(ctx context.Context, reader *bufio.Reader) (windowsLSPMessage, error) {
	type readResult struct {
		message windowsLSPMessage
		err     error
	}
	resultCh := make(chan readResult, 1)
	go func() {
		var contentLength int
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				resultCh <- readResult{err: err}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				resultCh <- readResult{err: fmt.Errorf("invalid LSP header %q", line)}
				return
			}
			if strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
				parsed, parseErr := strconv.Atoi(strings.TrimSpace(parts[1]))
				if parseErr != nil || parsed < 0 {
					resultCh <- readResult{err: fmt.Errorf("invalid LSP Content-Length %q", parts[1])}
					return
				}
				contentLength = parsed
			}
		}
		if contentLength == 0 {
			resultCh <- readResult{err: errors.New("LSP response has no positive Content-Length")}
			return
		}
		payload := make([]byte, contentLength)
		if _, err := io.ReadFull(reader, payload); err != nil {
			resultCh <- readResult{err: err}
			return
		}
		var message windowsLSPMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			resultCh <- readResult{err: fmt.Errorf("decode LSP JSON: %w", err)}
			return
		}
		resultCh <- readResult{message: message}
	}()
	select {
	case result := <-resultCh:
		return result.message, result.err
	case <-ctx.Done():
		return windowsLSPMessage{}, fmt.Errorf("read LSP response: %w", ctx.Err())
	}
}

type windowsLSPCatalogE2EProcessWait struct {
	// Err 是等待真实 Windows LSP 进程退出的结果，不会把强制回收伪装成成功。
	Err error
	// Mode 是 graceful、stdin-eof-reclaim 或 process-kill-reclaim 生命周期模式。
	Mode string
}

func waitWindowsLSPCatalogE2EProcess(ctx context.Context, cmd *exec.Cmd, stdin io.Closer) windowsLSPCatalogE2EProcessWait {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	grace := time.NewTimer(windowsLSPCatalogE2EExitGrace)
	defer grace.Stop()
	select {
	case err := <-done:
		return windowsLSPCatalogE2EProcessWait{Err: err, Mode: "graceful"}
	case <-grace.C:
		var closeErr error
		if stdin != nil {
			closeErr = stdin.Close()
		}
		select {
		case err := <-done:
			if closeErr != nil {
				return windowsLSPCatalogE2EProcessWait{Err: fmt.Errorf("close LSP stdin: %w (process wait: %v)", closeErr, err), Mode: "stdin-eof-reclaim"}
			}
			return windowsLSPCatalogE2EProcessWait{Err: err, Mode: "stdin-eof-reclaim"}
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			waitErr := <-done
			return windowsLSPCatalogE2EProcessWait{Err: fmt.Errorf("process wait timeout: %w (kill wait: %v)", ctx.Err(), waitErr), Mode: "process-kill-reclaim"}
		}
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		waitErr := <-done
		return windowsLSPCatalogE2EProcessWait{Err: fmt.Errorf("process wait timeout: %w (kill wait: %v)", ctx.Err(), waitErr), Mode: "process-kill-reclaim"}
	}
}

func assertWindowsLSPCatalogE2EPIDGone(pid int) error {
	systemRoot := strings.TrimSpace(os.Getenv("SystemRoot"))
	if systemRoot == "" {
		return errors.New("SystemRoot is empty; cannot verify native process residue")
	}
	tasklist := filepath.Join(systemRoot, "System32", "tasklist.exe")
	deadline := time.Now().Add(3 * time.Second)
	filter := "PID eq " + strconv.Itoa(pid)
	for time.Now().Before(deadline) {
		output, err := exec.Command(tasklist, "/FI", filter, "/FO", "CSV", "/NH").CombinedOutput()
		if err != nil {
			return fmt.Errorf("tasklist PID residue check: %w; %s", err, windowsLSPCatalogE2EByteEvidence(string(output)))
		}
		if !bytes.Contains(output, []byte(`"`+strconv.Itoa(pid)+`"`)) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("native LSP process PID %d remains after exit", pid)
}

func windowsLSPCatalogE2EFileURI(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	return (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(absolute)}).String()
}

// TestWindowsLSPCatalogE2EFileURI 验证 Windows E2E 工作区 URI 使用规范的 file:/// 形式。
func TestWindowsLSPCatalogE2EFileURI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace")
	uri := windowsLSPCatalogE2EFileURI(path)
	if !strings.HasPrefix(uri, "file:///") {
		t.Fatalf("Windows LSP E2E file URI = %q, want file:/// prefix", uri)
	}
	if strings.HasPrefix(uri, "file://C:/") {
		t.Fatalf("Windows LSP E2E file URI retained malformed authority form: %q", uri)
	}
}

func equalWindowsLSPCatalogE2ESlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// recordWindowsLSPCatalogE2EStderrEvidence 保存 stderr 的大小和 SHA-256，禁止把语言服务器输出原文写入公共收据。
func recordWindowsLSPCatalogE2EStderrEvidence(receipt *windowsLSPCatalogE2EReceipt, output string) {
	if receipt == nil {
		return
	}
	receipt.StderrBytes = len(output)
	digest := sha256.Sum256([]byte(output))
	receipt.StderrSHA256 = fmt.Sprintf("%x", digest[:])
}

func windowsLSPCatalogE2EByteEvidence(output string) string {
	digest := sha256.Sum256([]byte(output))
	return fmt.Sprintf("output_bytes=%d output_sha256=%x", len(output), digest[:])
}

// windowsLSPCatalogE2EEnvKeys 返回排序后的环境变量键，变量值可能包含凭据，不得参与日志或收据。
func windowsLSPCatalogE2EEnvKeys(values []string) []string {
	keys := make([]string, 0, len(values))
	for _, value := range values {
		key, _, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		keys = append(keys, strings.ToUpper(strings.TrimSpace(key)))
	}
	sort.Strings(keys)
	return keys
}

func windowsLSPCatalogE2EStringListSHA256(values []string) string {
	payload, _ := json.Marshal(values)
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("%x", digest[:])
}

func appendWindowsLSPCatalogE2EReceipt(receipt windowsLSPCatalogE2EReceipt) error {
	path := strings.TrimSpace(os.Getenv(windowsLSPCatalogE2EReceiptEnv))
	platformToken := "windows-" + strings.ToLower(strings.TrimSpace(receipt.NativeArch)) + "-process-" + strings.ToLower(strings.TrimSpace(receipt.ProcessArch))
	if path == "" {
		path = filepath.Join(os.TempDir(), "mcp-lsp-"+platformToken+"-catalog-e2e-receipt.jsonl")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve receipt path: %w", err)
	}
	if receipt.NativeArch == "" || receipt.ProcessArch == "" || !strings.Contains(strings.ToLower(filepath.Base(absolute)), platformToken) {
		return fmt.Errorf("receipt filename %q must visibly contain platform token %q", filepath.Base(absolute), platformToken)
	}
	if info, statErr := os.Lstat(absolute); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("receipt path is a symlink: %q", absolute)
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("inspect receipt path: %w", statErr)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return fmt.Errorf("create receipt directory: %w", err)
	}
	payload, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode receipt: %w", err)
	}
	file, err := os.OpenFile(absolute, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open receipt path %q: %w", absolute, err)
	}
	if _, err := file.Write(append(payload, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("write receipt: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close receipt: %w", err)
	}
	return nil
}

// TestWindowsLSPCatalogReceiptFilenameRequiresPlatformTokenE2E 证明 Windows 收据文件名必须直接暴露原生架构与进程架构，避免 native/WOW64 证据混淆。
func TestWindowsLSPCatalogReceiptFilenameRequiresPlatformTokenE2E(t *testing.T) {
	receipt := windowsLSPCatalogE2EReceipt{Product: string(WindowsLSPProductClangd), NativeArch: WindowsHostArchARM64, ProcessArch: WindowsHostArchX86, Status: "pass"}
	t.Setenv(windowsLSPCatalogE2EReceiptEnv, filepath.Join(t.TempDir(), "catalog-receipt.jsonl"))
	if err := appendWindowsLSPCatalogE2EReceipt(receipt); err == nil || !strings.Contains(err.Error(), "windows-arm64-process-x86") {
		t.Fatalf("platform-hidden receipt filename error = %v, want windows-arm64-process-x86 requirement", err)
	}

	path := filepath.Join(t.TempDir(), "mcp-lsp-windows-arm64-process-x86-catalog-e2e-receipt.jsonl")
	t.Setenv(windowsLSPCatalogE2EReceiptEnv, path)
	if err := appendWindowsLSPCatalogE2EReceipt(receipt); err != nil {
		t.Fatalf("append platform-visible Windows receipt: %v", err)
	}
	payload, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(payload, []byte(`"native_arch":"arm64"`)) {
		t.Fatalf("platform-visible Windows receipt payload = %q err=%v", payload, err)
	}
}

// TestWindowsLSPCatalogReceiptRedactsProcessInputsE2E 证明 catalog 收据不会保存参数、环境变量值或 stderr 原文。
func TestWindowsLSPCatalogReceiptRedactsProcessInputsE2E(t *testing.T) {
	const secret = "catalog-secret-must-not-appear"
	args := []string{"--config", `C:\\private\\` + secret}
	env := []string{"TOKEN=" + secret, "SystemRoot=C:\\Windows"}
	receipt := windowsLSPCatalogE2EReceipt{
		Product:       string(WindowsLSPProductClangd),
		NativeArch:    WindowsHostArchX64,
		ProcessArch:   WindowsHostArchX64,
		ArgsCount:     len(args),
		ArgsSHA256:    windowsLSPCatalogE2EStringListSHA256(args),
		EnvKeyCount:   len(env),
		EnvKeysSHA256: windowsLSPCatalogE2EStringListSHA256(windowsLSPCatalogE2EEnvKeys(env)),
	}
	recordWindowsLSPCatalogE2EStderrEvidence(&receipt, secret)
	payload, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(secret)) || bytes.Contains(payload, []byte(`C:\\private`)) {
		t.Fatalf("catalog receipt leaked process input: %s", payload)
	}
}
