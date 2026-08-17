package installer

// 本文件故意不加 windows build tag：下载、摘要、缓存状态机和归档上限使用
// 可移植原语，并需在非 Windows CI 执行契约测试；平台锁与文件类型判定由带标签实现提供。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// WindowsLSPAssetCacheSubdir 是 Windows 产品缓存下保存锁定 LSP 资产的固定子目录名。
	WindowsLSPAssetCacheSubdir = "lsp-assets"
	// windowsAssetHTTPUserAgent 明确标识 Windows EXE 资产请求，避免上游 CDN 拒绝 Go 默认 User-Agent。
	windowsAssetHTTPUserAgent   = "super-dolphin-mcp-lsp-windows-exe/1.0"
	defaultAssetHTTPTimeout     = 10 * time.Minute
	defaultMaxAssetBytes        = 2 << 30
	defaultMaxArchiveBytes      = 8 << 30
	lockedAssetDownloadAttempts = 3
	maxInt64Value               = int64(^uint64(0) >> 1)
)

// WindowsAssetCacheOptions 配置 Windows 锁定资产缓存的 HTTP 客户端、超时和大小上限。
type WindowsAssetCacheOptions struct {
	// HTTPClient 是下载 Windows 锁定资产时使用的 HTTP 客户端；nil 表示使用默认客户端配置。
	HTTPClient *http.Client
	// HTTPTimeout 是单个 Windows 资产请求的最长持续时间；零值使用受控默认值。
	HTTPTimeout time.Duration
	// MaxDownloadBytes 限制单个 Windows 下载载荷的字节数；零值使用受控默认值。
	MaxDownloadBytes int64
	// MaxArchiveBytes 限制 Windows 归档解包输出的字节数；零值使用受控默认值。
	MaxArchiveBytes int64
}

// WindowsAssetCache 管理 Windows 锁定资产的固定缓存、跨进程锁、校验和原子 ready 发布。
type WindowsAssetCache struct {
	root             string
	client           *http.Client
	httpTimeout      time.Duration
	maxDownloadBytes int64
	maxArchiveBytes  int64
}

// assetLock 是 Windows locked asset cache 的 type，联网写盘仅允许由 InstallAction 生命周期触发。
type assetLock struct {
	ready chan struct{}
}

type assetOSLocker interface {
	Close() error
}

type assetLease struct {
	lock   *assetLock
	shared assetOSLocker
}

var processAssetLocks sync.Map

// NewWindowsAssetCache 创建 Windows 锁定资产缓存；会创建并校验绝对缓存目录，但不会下载资产。
func NewWindowsAssetCache(cacheRoot string, client *http.Client) (*WindowsAssetCache, error) {
	return NewWindowsAssetCacheWithOptions(cacheRoot, WindowsAssetCacheOptions{HTTPClient: client})
}

// NewWindowsAssetCacheWithOptions 创建带下载与解包限制的 Windows 锁定资产缓存，不进行网络访问。
func NewWindowsAssetCacheWithOptions(cacheRoot string, options WindowsAssetCacheOptions) (*WindowsAssetCache, error) {
	cacheRoot = strings.TrimSpace(cacheRoot)
	if cacheRoot == "" {
		return nil, errors.New("locked asset cache root is empty")
	}
	absoluteRoot, err := filepath.Abs(cacheRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve locked asset cache root %q: %w", cacheRoot, err)
	}
	absoluteRoot = filepath.Clean(absoluteRoot)
	if err := ensureDirectoryNoSymlink(absoluteRoot); err != nil {
		return nil, fmt.Errorf("create locked asset cache root %q: %w", absoluteRoot, err)
	}
	info, err := os.Stat(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect locked asset cache root %q: %w", absoluteRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("locked asset cache root is not a directory: %q", absoluteRoot)
	}
	if err := validateCacheDirectory(absoluteRoot, absoluteRoot); err != nil {
		return nil, err
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	timeout := options.HTTPTimeout
	if timeout == 0 {
		timeout = defaultAssetHTTPTimeout
	}
	if timeout < 0 {
		return nil, fmt.Errorf("locked asset HTTP timeout cannot be negative: %s", timeout)
	}
	maxDownload := options.MaxDownloadBytes
	if maxDownload == 0 {
		maxDownload = defaultMaxAssetBytes
	}
	if maxDownload < 1 {
		return nil, fmt.Errorf("locked asset download limit must be positive: %d", maxDownload)
	}
	maxArchive := options.MaxArchiveBytes
	if maxArchive == 0 {
		maxArchive = defaultMaxArchiveBytes
	}
	if maxArchive < 1 {
		return nil, fmt.Errorf("locked asset archive limit must be positive: %d", maxArchive)
	}
	return &WindowsAssetCache{
		root: absoluteRoot, client: client, httpTimeout: timeout,
		maxDownloadBytes: maxDownload, maxArchiveBytes: maxArchive,
	}, nil
}

// NewWindowsLSPAssetCache 创建产品专属的 Windows LSP 锁定资产缓存，不会使用 PATH 或系统临时目录。
func NewWindowsLSPAssetCache(productRoot string, client *http.Client) (*WindowsAssetCache, error) {
	productRoot = strings.TrimSpace(productRoot)
	if productRoot == "" {
		return nil, errors.New("product root is empty")
	}
	return NewWindowsAssetCache(filepath.Join(productRoot, "cache", WindowsLSPAssetCacheSubdir), client)
}

// Root 返回 Windows 锁定资产缓存的绝对根路径；nil 缓存返回空字符串且不写盘。
func (c *WindowsAssetCache) Root() string {
	if c == nil {
		return ""
	}
	return c.root
}

// Ensure 按真实 Windows 主机原生架构选择并物化锁定资产，成功返回绝对 ready 路径。
func (c *WindowsAssetCache) Ensure(ctx context.Context, manifest WindowsLockedAssetManifest) (string, error) {
	if c == nil {
		return "", errors.New("locked asset cache is nil")
	}
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return "", fmt.Errorf("detect host platform for locked LSP asset: %w", err)
	}
	return c.EnsureForPlatform(ctx, manifest, platform)
}

// EnsureForPlatform 按给定 Windows 主机事实选择并物化锁定资产；失败不会返回伪 ready 路径。
func (c *WindowsAssetCache) EnsureForPlatform(ctx context.Context, manifest WindowsLockedAssetManifest, platform WindowsHostPlatform) (string, error) {
	asset, err := SelectWindowsLockedAsset(manifest, platform)
	if err != nil {
		return "", err
	}
	return c.ensureAsset(ctx, manifest.Name, asset)
}

// ensureForArchitecture 按指定 Windows 原生架构物化测试或内部调用所需的锁定资产。
func (c *WindowsAssetCache) ensureForArchitecture(ctx context.Context, manifest WindowsLockedAssetManifest, architecture string) (string, error) {
	asset, err := manifest.AssetForArchitecture(architecture)
	if err != nil {
		return "", err
	}
	return c.ensureAsset(ctx, manifest.Name, asset)
}

func (c *WindowsAssetCache) ensureAsset(ctx context.Context, assetName string, asset WindowsLockedAsset) (result string, err error) {
	if ctx == nil {
		return "", errors.New("locked asset context is nil")
	}
	architecture, err := NormalizeWindowsArchitectureAlias(asset.Architecture)
	if err != nil {
		return "", fmt.Errorf("asset architecture is not supported: %w", err)
	}
	if err := asset.validateForArchitecture(architecture); err != nil {
		return "", fmt.Errorf("validate locked asset %q: %w", assetName, err)
	}
	asset.Architecture = architecture
	assetDir := filepath.Join(c.root, cacheSegment(assetName), cacheSegment(asset.Version), architecture, strings.ToLower(asset.SHA256))
	if err := ensureDirectoryNoSymlink(assetDir); err != nil {
		return "", fmt.Errorf("create locked asset directory %q: %w", assetDir, err)
	}
	if err := validateCacheDirectory(c.root, assetDir); err != nil {
		return "", err
	}
	lock, _ := processAssetLocks.LoadOrStore(assetDir, &assetLock{ready: make(chan struct{}, 1)})
	lease, err := acquireAssetLock(ctx, lock.(*assetLock), filepath.Join(assetDir, ".lock"))
	if err != nil {
		return "", err
	}
	defer func() {
		if releaseErr := releaseAssetLock(lease); releaseErr != nil {
			err = joinAssetReleaseError(err, releaseErr, "release Windows locked asset OS lock")
			result = ""
		}
	}()

	payloadPath := filepath.Join(assetDir, "payload"+assetFormatSuffix(asset.Format))
	validPayload, err := verifyAssetPayloadWithinRoot(c.root, payloadPath, asset.SHA256)
	if err != nil {
		return "", err
	}
	if !validPayload {
		if removeErr := removeWindowsInstallerPathChecked(c.root, payloadPath); removeErr != nil && !os.IsNotExist(removeErr) {
			return "", fmt.Errorf("remove invalid locked asset payload %q: %w", payloadPath, removeErr)
		}
		if err := c.downloadPayload(ctx, payloadPath, asset); err != nil {
			return "", err
		}
	}

	readyDir := filepath.Join(assetDir, "ready")
	if binaryPath, ok, verifyErr := c.verifyReadyAsset(ctx, payloadPath, readyDir, asset); verifyErr != nil {
		return "", verifyErr
	} else if ok {
		return binaryPath, nil
	}
	if info, err := os.Lstat(readyDir); err == nil {
		if isUnsafeAssetFile(info) {
			return "", fmt.Errorf("locked asset output is a symlink or reparse point: %q", readyDir)
		}
		if removeErr := removeWindowsInstallerAllChecked(c.root, readyDir); removeErr != nil {
			return "", fmt.Errorf("remove invalid locked asset output %q: %w", readyDir, removeErr)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect locked asset output %q: %w", readyDir, err)
	}

	if err := validateWindowsInstallerPathWithinRoot(c.root, assetDir, false); err != nil {
		return "", fmt.Errorf("validate locked asset directory before temporary output: %w", err)
	}
	temporaryReady, err := os.MkdirTemp(assetDir, ".ready-")
	if err != nil {
		return "", fmt.Errorf("create temporary locked asset output: %w", err)
	}
	keepTemporary := false
	defer func() {
		if !keepTemporary {
			if removeErr := removeWindowsInstallerAllChecked(c.root, temporaryReady); removeErr != nil {
				err = joinWindowsInstallerCleanupError(err, removeErr, fmt.Sprintf("remove temporary locked asset output %q", temporaryReady))
				result = ""
			}
		}
	}()
	if err := validateWindowsInstallerPathWithinRoot(c.root, temporaryReady, false); err != nil {
		return "", fmt.Errorf("validate temporary locked asset output: %w", err)
	}
	if err := validateWindowsInstallerPathWithinRoot(c.root, payloadPath, false); err != nil {
		return "", fmt.Errorf("validate locked asset payload before materialize: %w", err)
	}
	if err := materializeAsset(ctx, payloadPath, temporaryReady, asset, c.maxArchiveBytes); err != nil {
		return "", err
	}
	_, ok := readyBinaryPath(temporaryReady, asset)
	if !ok {
		return "", fmt.Errorf("locked asset %q did not produce a usable locked entry at %q", assetName, asset.BinaryPath)
	}
	if err := renameWindowsInstallerPathChecked(c.root, temporaryReady, readyDir); err != nil {
		return "", fmt.Errorf("atomically publish locked asset output %q: %w", readyDir, err)
	}
	keepTemporary = true
	publishedBinaryPath, ok := readyBinaryPath(readyDir, asset)
	if !ok {
		operationErr := fmt.Errorf("verify published locked asset output at %q", readyDir)
		operationErr = joinWindowsInstallerCleanupError(operationErr, removeWindowsInstallerAllChecked(c.root, readyDir), fmt.Sprintf("remove invalid published locked asset output %q", readyDir))
		result = ""
		return "", operationErr
	}
	relativePath, err := relWindowsInstallerPath(readyDir, publishedBinaryPath)
	if err != nil {
		operationErr := fmt.Errorf("resolve published locked asset path: %w", err)
		operationErr = joinWindowsInstallerCleanupError(operationErr, removeWindowsInstallerAllChecked(c.root, readyDir), fmt.Sprintf("remove invalid published locked asset output %q", readyDir))
		result = ""
		return "", operationErr
	}
	return filepath.Join(readyDir, relativePath), nil
}

// verifyReadyAsset 复验 Windows ready 树与归档展开结果；校验失败时返回 false，调用方随后原子重建。
func (c *WindowsAssetCache) verifyReadyAsset(ctx context.Context, payloadPath, readyDir string, asset WindowsLockedAsset) (binaryPath string, valid bool, err error) {
	if ctx == nil {
		return "", false, errors.New("locked asset verification context is nil")
	}
	binaryPath, ok := readyBinaryPath(readyDir, asset)
	if !ok {
		return "", false, nil
	}
	if asset.Format == WindowsLockedAssetFormatRaw {
		valid, err := verifyAssetPayloadWithinRoot(c.root, binaryPath, asset.SHA256)
		if err != nil {
			return "", false, err
		}
		return binaryPath, valid, nil
	}

	verificationParent := filepath.Dir(readyDir)
	if err := validateWindowsInstallerPathWithinRoot(c.root, verificationParent, false); err != nil {
		return "", false, fmt.Errorf("validate locked asset verification parent: %w", err)
	}
	temporaryRoot, err := os.MkdirTemp(verificationParent, ".verify-")
	if err != nil {
		return "", false, fmt.Errorf("create locked asset verification directory: %w", err)
	}
	defer func() {
		if removeErr := removeWindowsInstallerAllChecked(c.root, temporaryRoot); removeErr != nil {
			err = joinWindowsInstallerCleanupError(err, removeErr, fmt.Sprintf("remove temporary locked asset verification directory %q", temporaryRoot))
			binaryPath = ""
			valid = false
		}
	}()
	if err := validateWindowsInstallerPathWithinRoot(c.root, temporaryRoot, false); err != nil {
		return "", false, fmt.Errorf("validate temporary locked asset verification directory: %w", err)
	}
	if err := validateWindowsInstallerPathWithinRoot(c.root, payloadPath, false); err != nil {
		return "", false, fmt.Errorf("validate locked asset payload before verification: %w", err)
	}
	if err := materializeAsset(ctx, payloadPath, temporaryRoot, asset, c.maxArchiveBytes); err != nil {
		return "", false, fmt.Errorf("verify cached locked asset output: %w", err)
	}
	expectedPath, ok := readyBinaryPath(temporaryRoot, asset)
	if !ok {
		return "", false, fmt.Errorf("verified locked asset payload did not produce %q", asset.BinaryPath)
	}
	if err := validateWindowsInstallerExistingFile(expectedPath); err != nil {
		return "", false, fmt.Errorf("inspect verified locked asset entry: %w", err)
	}
	equal, err := assetTreesEqual(readyDir, temporaryRoot)
	if err != nil {
		return "", false, fmt.Errorf("compare cached locked asset tree: %w", err)
	}
	return binaryPath, equal, nil
}

func acquireAssetLock(ctx context.Context, lock *assetLock, lockPath string) (*assetLease, error) {
	select {
	case lock.ready <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("wait for locked asset cache entry: %w", ctx.Err())
	}
	shared, err := acquireAssetOSLock(ctx, lockPath)
	if err != nil {
		<-lock.ready
		return nil, err
	}
	return &assetLease{lock: lock, shared: shared}, nil
}

// releaseAssetLock 释放 Windows 资产的 OS 锁并归还进程内 token；即使 Close 失败也不能遗留 token 造成后续死锁。
func releaseAssetLock(lease *assetLease) error {
	if lease == nil {
		return nil
	}
	releaseErr := releaseAssetOSLock(lease.shared)
	if lease.lock == nil {
		return releaseErr
	}
	<-lease.lock.ready
	return releaseErr
}

// releaseAssetOSLock 调用可注入的 OS 锁释放接口，保留 Close 错误供调用方 fail-fast 返回。
func releaseAssetOSLock(lock assetOSLocker) error {
	if lock == nil {
		return nil
	}
	return lock.Close()
}

// joinAssetReleaseError 将主操作错误与锁释放错误合并，确保 errors.Is 可分别识别两者。
func joinAssetReleaseError(operationErr, releaseErr error, context string) error {
	if releaseErr == nil {
		return operationErr
	}
	releaseErr = fmt.Errorf("%s: %w", context, releaseErr)
	return errors.Join(operationErr, releaseErr)
}

type windowsLockedAssetTransferError struct {
	operation    string
	statusCode   int
	receivedSize int64
	expectedSize int64
	retryable    bool
	cause        error
}

func (e *windowsLockedAssetTransferError) Error() string {
	return fmt.Sprintf("locked asset transfer phase=%s status=%d received_bytes=%d expected_bytes=%d retryable=%t: %v", e.operation, e.statusCode, e.receivedSize, e.expectedSize, e.retryable, e.cause)
}

func (e *windowsLockedAssetTransferError) Unwrap() error { return e.cause }

type windowsLockedAssetLocalWriteError struct{ cause error }

func (e *windowsLockedAssetLocalWriteError) Error() string {
	return "locked asset local write failed: " + e.cause.Error()
}
func (e *windowsLockedAssetLocalWriteError) Unwrap() error { return e.cause }

func windowsLockedAssetRetryable(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	var transfer *windowsLockedAssetTransferError
	return errors.As(err, &transfer) && transfer.retryable
}

// downloadPayload 是跨平台可复用的锁定资产 HTTP 完整性合同：仅对响应传输截断做有限次重试；
// 本地写盘、权限、路径和校验错误必须立即失败。实际 Windows 产品入口由带 windows 标签的调用方提供，
// 非 Windows 仅复用同一成功路径与 fail-fast 错误语义，不引入平台回退。
func (c *WindowsAssetCache) downloadPayload(ctx context.Context, destination string, asset WindowsLockedAsset) error {
	started := time.Now()
	var lastErr error
	for attempt := 1; attempt <= lockedAssetDownloadAttempts; attempt++ {
		lastErr = c.downloadPayloadOnce(ctx, destination, asset)
		if lastErr == nil {
			return nil
		}
		if !windowsLockedAssetRetryable(ctx, lastErr) || attempt == lockedAssetDownloadAttempts {
			return fmt.Errorf("download locked asset name=%q attempt=%d/%d elapsed=%s: %w", filepath.Base(destination), attempt, lockedAssetDownloadAttempts, time.Since(started).Round(time.Millisecond), lastErr)
		}
		if err := waitLockedAssetRetry(ctx, time.Duration(attempt)*250*time.Millisecond); err != nil {
			return fmt.Errorf("download locked asset name=%q attempt=%d/%d elapsed=%s retry canceled: %w", filepath.Base(destination), attempt, lockedAssetDownloadAttempts, time.Since(started).Round(time.Millisecond), err)
		}
	}
	return fmt.Errorf("download locked asset name=%q exhausted: %w", filepath.Base(destination), lastErr)
}

func waitLockedAssetRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func windowsLockedAssetRetryableRead(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}

func copyLockedAssetPayload(dst, digest io.Writer, src io.Reader, maxBytes int64) (int64, error) {
	buffer := make([]byte, 32*1024)
	var count int64
	for {
		readCount, readErr := src.Read(buffer)
		if readCount > 0 {
			if count+int64(readCount) > maxBytes {
				return count + int64(readCount), fmt.Errorf("locked asset payload exceeds limit %d bytes", maxBytes)
			}
			written, writeErr := dst.Write(buffer[:readCount])
			if writeErr != nil {
				return count, &windowsLockedAssetLocalWriteError{cause: writeErr}
			}
			if written != readCount {
				return count, &windowsLockedAssetLocalWriteError{cause: io.ErrShortWrite}
			}
			written, writeErr = digest.Write(buffer[:readCount])
			if writeErr != nil {
				return count, &windowsLockedAssetLocalWriteError{cause: writeErr}
			}
			if written != readCount {
				return count, &windowsLockedAssetLocalWriteError{cause: io.ErrShortWrite}
			}
			count += int64(readCount)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return count, nil
			}
			return count, readErr
		}
	}
}

func (c *WindowsAssetCache) downloadPayloadOnce(ctx context.Context, destination string, asset WindowsLockedAsset) (err error) {
	if err := validateWindowsInstallerPathWithinRoot(c.root, destination, true); err != nil {
		return fmt.Errorf("validate locked asset payload destination: %w", err)
	}
	requestContext := ctx
	var cancel context.CancelFunc
	if c.httpTimeout > 0 {
		requestContext, cancel = context.WithTimeout(ctx, c.httpTimeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, strings.TrimSpace(asset.URL), nil)
	if err != nil {
		return fmt.Errorf("create locked asset HTTP request: %w", err)
	}
	request.Header.Set("User-Agent", windowsAssetHTTPUserAgent)
	response, err := c.client.Do(request)
	if err != nil {
		if requestContext.Err() != nil {
			return fmt.Errorf("download locked asset: %w", requestContext.Err())
		}
		return &windowsLockedAssetTransferError{operation: "request", expectedSize: -1, retryable: true, cause: err}
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			err = joinWindowsInstallerCleanupError(err, closeErr, "close locked asset HTTP response body")
		}
	}()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		retryable := response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		return &windowsLockedAssetTransferError{operation: "response_status", statusCode: response.StatusCode, expectedSize: response.ContentLength, retryable: retryable, cause: fmt.Errorf("%w: host=%q asset=%q status=%s", ErrWindowsAssetHTTPStatus, request.URL.Host, path.Base(request.URL.Path), response.Status)}
	}
	if response.ContentLength > c.maxDownloadBytes {
		return fmt.Errorf("downloaded locked asset exceeds limit %d bytes", c.maxDownloadBytes)
	}
	if err := validateWindowsInstallerPathWithinRoot(c.root, filepath.Dir(destination), false); err != nil {
		return fmt.Errorf("validate locked asset payload parent before temporary file: %w", err)
	}
	temporary, err := createWindowsInstallerTemp(filepath.Dir(destination), ".payload-")
	if err != nil {
		return fmt.Errorf("create temporary locked asset payload: %w", err)
	}
	temporaryName := temporary.Name()
	removeTemporary := true
	temporaryClosed := false
	defer func() {
		if !temporaryClosed {
			if closeErr := temporary.Close(); closeErr != nil {
				err = joinWindowsInstallerCleanupError(err, closeErr, "close temporary locked asset payload")
			}
		}
		if removeTemporary {
			if removeErr := removeWindowsInstallerPathChecked(c.root, temporaryName); removeErr != nil && !os.IsNotExist(removeErr) {
				err = joinWindowsInstallerCleanupError(err, removeErr, fmt.Sprintf("remove temporary locked asset payload %q", temporaryName))
			}
		}
	}()
	if err := validateWindowsInstallerPathWithinRoot(c.root, temporaryName, false); err != nil {
		return fmt.Errorf("validate temporary locked asset payload: %w", err)
	}
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict temporary locked asset payload: %w", err)
	}
	hasher := sha256.New()
	copyLimit := c.maxDownloadBytes
	if copyLimit < maxInt64Value {
		copyLimit++
	}
	count, err := copyLockedAssetPayload(temporary, hasher, io.LimitReader(response.Body, copyLimit), c.maxDownloadBytes)
	if err != nil {
		var localWrite *windowsLockedAssetLocalWriteError
		if errors.As(err, &localWrite) {
			return err
		}
		return &windowsLockedAssetTransferError{operation: "copy_response_body", statusCode: response.StatusCode, expectedSize: response.ContentLength, receivedSize: count, retryable: windowsLockedAssetRetryableRead(requestContext, err), cause: err}
	}
	if count > c.maxDownloadBytes {
		return fmt.Errorf("downloaded locked asset exceeds limit %d bytes", c.maxDownloadBytes)
	}
	if response.ContentLength >= 0 && count != response.ContentLength {
		return &windowsLockedAssetTransferError{operation: "content_length", statusCode: response.StatusCode, expectedSize: response.ContentLength, receivedSize: count, retryable: true, cause: io.ErrUnexpectedEOF}
	}
	if closeErr := temporary.Close(); closeErr != nil {
		temporaryClosed = true
		return fmt.Errorf("close temporary locked asset payload: %w", closeErr)
	}
	temporaryClosed = true
	got := hex.EncodeToString(hasher.Sum(nil))
	want := strings.ToLower(strings.TrimSpace(asset.SHA256))
	if got != want {
		return fmt.Errorf("%w: want %s, got %s", ErrWindowsAssetChecksumMismatch, want, got)
	}
	if err := renameWindowsInstallerPathChecked(c.root, temporaryName, destination); err != nil {
		return fmt.Errorf("atomically publish locked asset payload %q: %w", destination, err)
	}
	removeTemporary = false
	return nil
}

func verifyAssetPayload(path, expectedSHA256 string) (valid bool, err error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open cached locked asset payload %q: %w", path, err)
	}
	if isUnsafeAssetFile(info) || !info.Mode().IsRegular() {
		return false, fmt.Errorf("cached locked asset payload is not a regular file: %q", path)
	}
	if err := validateWindowsInstallerExistingFile(path); err != nil {
		return false, fmt.Errorf("validate cached locked asset payload before open %q: %w", path, err)
	}
	file, err := openWindowsInstallerInput(path)
	if err != nil {
		return false, fmt.Errorf("open cached locked asset payload %q: %w", path, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = joinWindowsInstallerCleanupError(err, closeErr, fmt.Sprintf("close cached locked asset payload %q", path))
			valid = false
		}
	}()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return false, fmt.Errorf("hash cached locked asset payload %q: %w", path, err)
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	return got == strings.ToLower(strings.TrimSpace(expectedSHA256)), nil
}

func verifyAssetPayloadWithinRoot(root, path, expectedSHA256 string) (bool, error) {
	if err := validateWindowsInstallerPathWithinRoot(root, path, true); err != nil {
		return false, fmt.Errorf("validate cached locked asset payload %q: %w", path, err)
	}
	return verifyAssetPayload(path, expectedSHA256)
}

type assetTreeEntry struct {
	kind string
	size int64
	hash string
}

func assetTreesEqual(left, right string) (bool, error) {
	leftEntries, err := snapshotAssetTree(left)
	if err != nil {
		return false, fmt.Errorf("snapshot cached tree %q: %w", left, err)
	}
	rightEntries, err := snapshotAssetTree(right)
	if err != nil {
		return false, fmt.Errorf("snapshot verified tree %q: %w", right, err)
	}
	if len(leftEntries) != len(rightEntries) {
		return false, nil
	}
	for relative, leftEntry := range leftEntries {
		rightEntry, ok := rightEntries[relative]
		if !ok || leftEntry != rightEntry {
			return false, nil
		}
	}
	return true, nil
}

func snapshotAssetTree(root string) (map[string]assetTreeEntry, error) {
	return snapshotAssetTreeContext(context.Background(), root)
}

// snapshotAssetTreeContext 在保留完整树摘要语义的同时响应调用方取消。
// 安装/发布仍会校验整棵树；check-only 快速路径可在大树上有界退出。
func snapshotAssetTreeContext(ctx context.Context, root string) (map[string]assetTreeEntry, error) {
	if ctx == nil {
		return nil, errors.New("asset tree context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if isUnsafeAssetFile(rootInfo) || !rootInfo.IsDir() {
		return nil, fmt.Errorf("asset tree root is not a real directory: %q", root)
	}
	entries := make(map[string]assetTreeEntry)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if isUnsafeAssetFile(info) {
			return fmt.Errorf("asset tree contains symlink or reparse point %q", path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if info.IsDir() {
			entries[relative] = assetTreeEntry{kind: "directory"}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("asset tree contains unsupported file type %s at %q", info.Mode(), path)
		}
		hash, err := fileSHA256Context(ctx, path)
		if err != nil {
			return err
		}
		entries[relative] = assetTreeEntry{kind: "file", size: info.Size(), hash: hash}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func fileSHA256(filePath string) (hash string, err error) {
	return fileSHA256Context(context.Background(), filePath)
}

// fileSHA256Context 分块读取文件并在每个分块边界检查取消，避免大 DLL
// 的完整性校验在 resolver 的 deadline 之后继续占用 goroutine。
func fileSHA256Context(ctx context.Context, filePath string) (hash string, err error) {
	if ctx == nil {
		return "", errors.New("file hash context is nil")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := validateWindowsInstallerExistingFile(filePath); err != nil {
		return "", fmt.Errorf("validate asset tree file %q: %w", filePath, err)
	}
	file, err := openWindowsInstallerInput(filePath)
	if err != nil {
		return "", fmt.Errorf("open %q: %w", filePath, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = joinWindowsInstallerCleanupError(err, closeErr, fmt.Sprintf("close file %q", filePath))
			hash = ""
		}
	}()
	hasher := sha256.New()
	buffer := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("hash %q: %w", filePath, err)
		}
		readCount, readErr := file.Read(buffer)
		if readCount > 0 {
			if _, writeErr := hasher.Write(buffer[:readCount]); writeErr != nil {
				return "", fmt.Errorf("hash %q: %w", filePath, writeErr)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("hash %q: %w", filePath, readErr)
		}
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func materializeAsset(ctx context.Context, payloadPath, outputRoot string, asset WindowsLockedAsset, maxArchiveBytes int64) error {
	if ctx == nil {
		return errors.New("locked asset materialization context is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("materialize locked asset: %w", err)
	}
	binaryPath, err := assetBinaryPath(asset)
	if err != nil {
		return err
	}
	switch asset.Format {
	case WindowsLockedAssetFormatRaw:
		return materializeRawAsset(payloadPath, outputRoot, binaryPath)
	case WindowsLockedAssetFormatZip:
		return extractZipAsset(payloadPath, outputRoot, binaryPath, maxArchiveBytes)
	case WindowsLockedAssetFormatTarGz:
		return extractTarGzAsset(ctx, payloadPath, outputRoot, binaryPath, maxArchiveBytes)
	case WindowsLockedAssetFormatTarXz:
		return extractTarXzAsset(ctx, payloadPath, outputRoot, binaryPath, maxArchiveBytes)
	default:
		return fmt.Errorf("unsupported locked asset format %q", asset.Format)
	}
}

func materializeRawAsset(payloadPath, outputRoot, binaryPath string) (err error) {
	if err := validateWindowsInstallerExistingFile(payloadPath); err != nil {
		return fmt.Errorf("validate raw locked asset payload: %w", err)
	}
	destination, err := safeOutputPath(outputRoot, binaryPath)
	if err != nil {
		return err
	}
	if err := ensureDirectoryNoSymlink(filepath.Dir(destination)); err != nil {
		return fmt.Errorf("create raw locked asset directory: %w", err)
	}
	if err := validateWindowsInstallerPathWithinRoot(outputRoot, destination, true); err != nil {
		return fmt.Errorf("validate raw locked asset destination: %w", err)
	}
	if err := validateWindowsInstallerExistingFile(payloadPath); err != nil {
		return fmt.Errorf("validate raw locked asset payload before open: %w", err)
	}
	input, err := openWindowsInstallerInput(payloadPath)
	if err != nil {
		return fmt.Errorf("open raw locked asset payload: %w", err)
	}
	defer func() {
		if closeErr := input.Close(); closeErr != nil {
			err = joinWindowsInstallerCleanupError(err, closeErr, fmt.Sprintf("close raw locked asset payload %q", payloadPath))
		}
	}()
	output, err := openWindowsInstallerOutput(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return fmt.Errorf("create raw locked asset executable: %w", err)
	}
	outputClosed := false
	defer func() {
		if !outputClosed {
			if closeErr := output.Close(); closeErr != nil {
				err = joinWindowsInstallerCleanupError(err, closeErr, "close raw locked asset executable")
			}
		}
	}()
	if err := validateWindowsInstallerPathWithinRoot(outputRoot, destination, true); err != nil {
		return fmt.Errorf("validate raw locked asset destination before open: %w", err)
	}
	if _, copyErr := io.Copy(output, input); copyErr != nil {
		closeErr := output.Close()
		outputClosed = true
		return joinWindowsInstallerCleanupError(fmt.Errorf("copy raw locked asset executable: %w", copyErr), closeErr, "close raw locked asset executable")
	}
	if closeErr := output.Close(); closeErr != nil {
		outputClosed = true
		return fmt.Errorf("close raw locked asset executable: %w", closeErr)
	}
	outputClosed = true
	if err := validateWindowsInstallerExistingFile(destination); err != nil {
		return fmt.Errorf("validate raw locked asset executable before chmod: %w", err)
	}
	if err := os.Chmod(destination, 0o700); err != nil {
		return fmt.Errorf("mark raw locked asset executable: %w", err)
	}
	return nil
}

func assetBinaryPath(asset WindowsLockedAsset) (string, error) {
	if strings.TrimSpace(asset.BinaryPath) != "" {
		return normalizeAssetRelativePath(asset.BinaryPath)
	}
	if asset.Format != WindowsLockedAssetFormatRaw {
		return "", fmt.Errorf("archive locked asset binary path is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(asset.URL))
	if err != nil {
		return "", fmt.Errorf("derive raw locked asset binary path: %w", err)
	}
	return normalizeAssetRelativePath(path.Base(parsed.Path))
}

func readyBinaryPath(root string, asset WindowsLockedAsset) (string, bool) {
	binaryPath, err := assetBinaryPath(asset)
	if err != nil {
		return "", false
	}
	path, err := safeOutputPath(root, binaryPath)
	if err != nil {
		return "", false
	}
	if err := validateCacheDirectory(root, filepath.Dir(path)); err != nil {
		return "", false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return "", false
	}
	if isUnsafeAssetFile(info) {
		return "", false
	}
	return path, true
}

func validateCacheDirectory(root, target string) error {
	if err := validateWindowsInstallerPathWithinRoot(root, target, false); err != nil {
		return fmt.Errorf("validate cache directory %q: %w", target, err)
	}
	info, err := os.Lstat(target)
	if err != nil {
		return fmt.Errorf("inspect cache directory %q: %w", target, err)
	}
	if isUnsafeAssetFile(info) || !info.IsDir() {
		return fmt.Errorf("cache path target is not a real directory: %q", target)
	}
	return nil
}

func ensureDirectoryNoSymlink(target string) error {
	target, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve directory %q: %w", target, err)
	}
	target = filepath.Clean(target)
	volume := filepath.VolumeName(target)
	current := volume + string(filepath.Separator)
	relative := strings.TrimPrefix(target, current)
	if relative == target {
		return fmt.Errorf("directory path has an unsupported volume root: %q", target)
	}
	if relative == "" {
		return nil
	}
	for _, segment := range strings.Split(relative, string(filepath.Separator)) {
		if segment == "" || segment == "." {
			continue
		}
		current = filepath.Join(current, segment)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			parent := filepath.Dir(current)
			parentInfo, parentErr := os.Lstat(parent)
			if parentErr != nil {
				return fmt.Errorf("inspect parent directory component %q: %w", parent, parentErr)
			}
			if isUnsafeAssetFile(parentInfo) || !parentInfo.IsDir() {
				return fmt.Errorf("parent directory component is not a real directory: %q", parent)
			}
			if mkdirErr := os.Mkdir(current, 0o700); mkdirErr != nil && !os.IsExist(mkdirErr) {
				return fmt.Errorf("create directory component %q: %w", current, mkdirErr)
			}
			info, statErr = os.Lstat(current)
		}
		if statErr != nil {
			return fmt.Errorf("inspect directory component %q: %w", current, statErr)
		}
		if isUnsafeAssetFile(info) {
			return fmt.Errorf("directory component is a symlink or reparse point: %q", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("directory component is not a directory: %q", current)
		}
	}
	return nil
}

func safeOutputPath(root, relative string) (string, error) {
	normalized, err := normalizeAssetRelativePath(relative)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrWindowsUnsafeAssetArchive, err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve locked asset output root: %w", err)
	}
	destination := filepath.Clean(filepath.Join(root, filepath.FromSlash(normalized)))
	relativeCheck, err := filepath.Rel(root, destination)
	if err != nil || relativeCheck == ".." || strings.HasPrefix(relativeCheck, ".."+string(filepath.Separator)) || filepath.IsAbs(relativeCheck) {
		return "", fmt.Errorf("%w: output path escapes extraction root", ErrWindowsUnsafeAssetArchive)
	}
	return destination, nil
}

func cacheSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unnamed"
	}
	if value == "." || value == ".." {
		return "unnamed"
	}
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('_')
		}
	}
	if builder.Len() == 0 {
		return "unnamed"
	}
	return builder.String()
}

func assetFormatSuffix(format WindowsLockedAssetFormat) string {
	switch format {
	case WindowsLockedAssetFormatZip:
		return ".zip"
	case WindowsLockedAssetFormatTarGz:
		return ".tar.gz"
	case WindowsLockedAssetFormatTarXz:
		return ".tar.xz"
	default:
		return ".raw"
	}
}
