package installer

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	// NativeArtifactFormatZip 表示 ZIP artifact。
	NativeArtifactFormatZip = "zip"
	// NativeArtifactFormatGzip 表示单文件 gzip artifact。
	NativeArtifactFormatGzip = "gzip"
	// NativeArtifactFormatGz 是 NativeArtifactFormatGzip 的兼容名称。
	NativeArtifactFormatGz = NativeArtifactFormatGzip
	// NativeArtifactFormatTar 表示未压缩的 tar artifact。
	NativeArtifactFormatTar = "tar"
	// NativeArtifactFormatTarGz 表示 gzip 压缩的 tar artifact。
	NativeArtifactFormatTarGz = "tar.gz"
	// NativeArtifactFormatDeb 表示 Debian binary package；仅提取其中的 data.tar.*。
	NativeArtifactFormatDeb = "deb"
	// nativeArtifactDefaultMaxBytes 限制下载和解压的单个 artifact 大小。
	nativeArtifactDefaultMaxBytes int64 = 512 << 20
)

// NativeArtifactInstallerConfig 描述受控 native artifact 安装器的运行边界。
type NativeArtifactInstallerConfig struct {
	// InstallRoot 必须是绝对路径，安装器不会从环境变量或当前目录推断它。
	InstallRoot string
	// HTTPClient 可注入带测试 TLS 根证书的客户端；nil 使用默认客户端。
	HTTPClient *http.Client
	// MaxArtifactBytes 限制下载和解压总字节数，零值使用安全上限。
	MaxArtifactBytes int64
}

// NativeInstallerConfig 是 NativeArtifactInstallerConfig 的兼容别名。
type NativeInstallerConfig = NativeArtifactInstallerConfig

// NativeArtifactSpec 描述一个待安装的、已由上游 manifest 固定的 artifact。
type NativeArtifactSpec struct {
	// Name 是 install root 下的单层组件目录名。
	Name string
	// Version 是 Name 下的单层版本目录名。
	Version string
	// URL 必须使用 HTTPS；安装器不会把 HTTP 升级或降级为其他协议。
	URL string
	// SHA256 是下载字节的 64 位十六进制 SHA-256 摘要。
	SHA256 string
	// Format 必须是 zip、gzip、tar、tar.gz 或 deb。
	Format string
	// ArchiveFormat 是 Format 的兼容字段；同时填写时必须一致。
	ArchiveFormat string
	// BinaryPath 是 artifact 内的相对可执行文件路径。
	BinaryPath string
	// LauncherName 是受管 launcher 文件名；空值使用 BinaryPath 的 basename。
	LauncherName string
	// AllowSymlinks 允许受信归档中的内部相对符号链接；默认拒绝。
	// 链接目标必须保持在 payload 根内，且只用于官方工具链等需要保留链接布局的归档。
	AllowSymlinks bool
}

// ArtifactSpec 是 NativeArtifactSpec 的短名称。
type ArtifactSpec = NativeArtifactSpec

// NativeInstallResult 描述一次成功安装后可直接交给 LSP client 的绝对路径。
type NativeInstallResult struct {
	Name         string
	Version      string
	InstallDir   string
	BinaryPath   string
	LauncherPath string
	SHA256       string
}

// NativeArtifactInstaller 下载、校验并原子安装受管 native artifact。
type NativeArtifactInstaller struct {
	mu              sync.Mutex
	installRoot     string
	httpClient      *http.Client
	maxArtifactSize int64
}

// NewNativeArtifactInstaller 创建一个要求显式绝对 install root 的安装器。
func NewNativeArtifactInstaller(cfg NativeArtifactInstallerConfig) (*NativeArtifactInstaller, error) {
	root, err := validateInstallRoot(cfg.InstallRoot)
	if err != nil {
		return nil, err
	}
	maxBytes := cfg.MaxArtifactBytes
	if maxBytes == 0 {
		maxBytes = nativeArtifactDefaultMaxBytes
	}
	if maxBytes < 1 {
		return nil, errors.New("native artifact max bytes must be positive")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	return &NativeArtifactInstaller{
		installRoot:     root,
		httpClient:      client,
		maxArtifactSize: maxBytes,
	}, nil
}

// NewNativeInstaller 是 NewNativeArtifactInstaller 的短名称。
func NewNativeInstaller(cfg NativeArtifactInstallerConfig) (*NativeArtifactInstaller, error) {
	return NewNativeArtifactInstaller(cfg)
}

// Install 下载、验证并以原子 rename 发布一个 native artifact。
func (i *NativeArtifactInstaller) Install(ctx context.Context, spec NativeArtifactSpec) (NativeInstallResult, error) {
	return i.InstallArtifact(ctx, spec)
}

// InstallArtifact 将 artifact 安装到 installRoot/name/version 并返回绝对 launcher。
func (i *NativeArtifactInstaller) InstallArtifact(ctx context.Context, spec NativeArtifactSpec) (NativeInstallResult, error) {
	if i == nil {
		return NativeInstallResult{}, errors.New("native artifact installer is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	i.mu.Lock()
	defer i.mu.Unlock()

	normalized, stageDir, finalDir, err := i.prepareNativeInstallTarget(spec)
	if err != nil {
		return NativeInstallResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(stageDir)
		}
	}()

	result, err := i.installNativeArtifactStage(ctx, stageDir, finalDir, normalized)
	if err != nil {
		return NativeInstallResult{}, err
	}
	if err := os.Rename(stageDir, finalDir); err != nil {
		return NativeInstallResult{}, fmt.Errorf("atomically publish native artifact %s: %w", finalDir, err)
	}
	committed = true
	result.InstallDir = finalDir
	result.BinaryPath = filepath.Join(finalDir, filepath.FromSlash(result.BinaryPath))
	result.LauncherPath = filepath.Join(finalDir, filepath.FromSlash(result.LauncherPath))
	return result, nil
}

// prepareNativeInstallTarget 校验 spec，创建 owner 目录并拒绝覆盖既有版本。
func (i *NativeArtifactInstaller) prepareNativeInstallTarget(spec NativeArtifactSpec) (normalizedNativeArtifactSpec, string, string, error) {
	normalized, err := normalizeNativeArtifactSpec(spec)
	if err != nil {
		return normalizedNativeArtifactSpec{}, "", "", err
	}
	if err := ensureInstallDirectory(i.installRoot); err != nil {
		return normalizedNativeArtifactSpec{}, "", "", err
	}
	componentRoot := filepath.Join(i.installRoot, normalized.name)
	if err := ensureInstallDirectory(componentRoot); err != nil {
		return normalizedNativeArtifactSpec{}, "", "", err
	}
	finalDir := filepath.Join(componentRoot, normalized.version)
	if _, err := os.Lstat(finalDir); err == nil {
		return normalizedNativeArtifactSpec{}, "", "", fmt.Errorf("native artifact install already exists: %s", finalDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return normalizedNativeArtifactSpec{}, "", "", fmt.Errorf("inspect native artifact install target %s: %w", finalDir, err)
	}
	stageDir, err := os.MkdirTemp(componentRoot, ".native-artifact-")
	if err != nil {
		return normalizedNativeArtifactSpec{}, "", "", fmt.Errorf("create native artifact staging directory: %w", err)
	}
	return normalized, stageDir, finalDir, nil
}

// installNativeArtifactStage 下载并解压 artifact，在临时目录内生成最终 launcher。
func (i *NativeArtifactInstaller) installNativeArtifactStage(ctx context.Context, stageDir, finalDir string, normalized normalizedNativeArtifactSpec) (NativeInstallResult, error) {
	archivePath := filepath.Join(stageDir, "artifact.download")
	if err := i.download(ctx, normalized.url, archivePath, normalized.sha256); err != nil {
		return NativeInstallResult{}, err
	}
	payloadDir := filepath.Join(stageDir, "payload")
	if err := os.Mkdir(payloadDir, 0o700); err != nil {
		return NativeInstallResult{}, fmt.Errorf("create native artifact payload directory: %w", err)
	}
	if err := extractNativeArchive(archivePath, payloadDir, normalized.format, normalized.binaryPath, i.maxArtifactSize, normalized.allowSymlinks); err != nil {
		return NativeInstallResult{}, err
	}
	if err := os.Remove(archivePath); err != nil {
		return NativeInstallResult{}, fmt.Errorf("remove native artifact staging archive: %w", err)
	}
	result, err := prepareNativeLauncher(stageDir, finalDir, normalized)
	if err != nil {
		return NativeInstallResult{}, err
	}
	return result, nil
}

type normalizedNativeArtifactSpec struct {
	name          string
	version       string
	url           string
	sha256        string
	format        string
	binaryPath    string
	launcherName  string
	allowSymlinks bool
}

// normalizeNativeArtifactSpec 校验并规范化下载、归档和 launcher 的全部外部输入。
func normalizeNativeArtifactSpec(spec NativeArtifactSpec) (normalizedNativeArtifactSpec, error) {
	if err := validateSinglePathComponent("artifact name", spec.Name); err != nil {
		return normalizedNativeArtifactSpec{}, err
	}
	if err := validateSinglePathComponent("artifact version", spec.Version); err != nil {
		return normalizedNativeArtifactSpec{}, err
	}
	artifactURL, err := validateHTTPSURL(spec.URL)
	if err != nil {
		return normalizedNativeArtifactSpec{}, err
	}
	digest, err := normalizeSHA256(spec.SHA256)
	if err != nil {
		return normalizedNativeArtifactSpec{}, err
	}
	format, err := normalizeArtifactFormat(spec.Format, spec.ArchiveFormat)
	if err != nil {
		return normalizedNativeArtifactSpec{}, err
	}
	binaryPath, err := cleanArchivePath(spec.BinaryPath)
	if err != nil {
		return normalizedNativeArtifactSpec{}, fmt.Errorf("invalid artifact binary path: %w", err)
	}
	launcherName := strings.TrimSpace(spec.LauncherName)
	if launcherName == "" {
		launcherName = path.Base(binaryPath)
	}
	if err := validateSinglePathComponent("launcher name", launcherName); err != nil {
		return normalizedNativeArtifactSpec{}, err
	}
	return normalizedNativeArtifactSpec{
		name:          spec.Name,
		version:       spec.Version,
		url:           artifactURL,
		sha256:        digest,
		format:        format,
		binaryPath:    binaryPath,
		launcherName:  launcherName,
		allowSymlinks: spec.AllowSymlinks,
	}, nil
}

// validateInstallRoot 要求安装根为绝对路径且既有组件不含符号链接。
func validateInstallRoot(value string) (string, error) {
	root := strings.TrimSpace(value)
	if root == "" {
		return "", errors.New("native artifact install root is required")
	}
	if strings.ContainsRune(root, '\x00') || !filepath.IsAbs(root) {
		return "", fmt.Errorf("native artifact install root must be absolute: %q", value)
	}
	if hasParentPathComponent(root) {
		return "", fmt.Errorf("native artifact install root must not contain parent traversal: %q", value)
	}
	clean := filepath.Clean(root)
	if clean == string(filepath.Separator) || clean == "." {
		return "", fmt.Errorf("native artifact install root is too broad: %q", value)
	}
	if err := rejectExistingSymlinkComponents(clean); err != nil {
		return "", err
	}
	return clean, nil
}

// ensureInstallDirectory 创建私有目录，并拒绝既有符号链接或非目录目标。
func ensureInstallDirectory(directory string) error {
	if err := rejectExistingSymlinkComponents(directory); err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create native artifact directory %s: %w", directory, err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect native artifact directory %s: %w", directory, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("native artifact install path is not a real directory: %s", directory)
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return fmt.Errorf("resolve native artifact directory %s: %w", directory, err)
	}
	if filepath.Clean(resolved) != filepath.Clean(directory) {
		return fmt.Errorf("native artifact install directory resolves outside managed path: %s", directory)
	}
	return nil
}

// rejectExistingSymlinkComponents 从目标向上拒绝任一既有符号链接组件。
func rejectExistingSymlinkComponents(value string) error {
	clean := filepath.Clean(value)
	current := filepath.VolumeName(clean)
	if current != "" {
		current += string(filepath.Separator)
	} else if filepath.IsAbs(clean) {
		current = string(filepath.Separator)
	}
	rest := strings.TrimPrefix(clean, current)
	for _, component := range strings.FieldsFunc(rest, func(r rune) bool { return r == '\\' || r == '/' }) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect native artifact install path %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("native artifact install path contains symlink: %s", current)
		}
	}
	return nil
}

// validateSinglePathComponent 校验 artifact 名称等字段只能是单一安全路径组件。
func validateSinglePathComponent(label, value string) error {
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%s is required and must not contain whitespace edges or NUL", label)
	}
	if value == "." || value == ".." || filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return fmt.Errorf("%s must be one relative path component: %q", label, value)
	}
	if strings.ContainsAny(value, `/\\`) {
		return fmt.Errorf("%s must not contain path separators: %q", label, value)
	}
	return nil
}

func hasParentPathComponent(value string) bool {
	for _, component := range strings.FieldsFunc(value, func(r rune) bool { return r == '\\' || r == '/' }) {
		if component == ".." {
			return true
		}
	}
	return false
}

// validateHTTPSURL 仅接受不携带凭据和 fragment 的 HTTPS 下载地址。
func validateHTTPSURL(value string) (string, error) {
	urlValue := strings.TrimSpace(value)
	if urlValue == "" {
		return "", errors.New("native artifact URL is required")
	}
	parsed, err := url.Parse(urlValue)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil {
		return "", fmt.Errorf("native artifact URL must be HTTPS without userinfo: %q", value)
	}
	return parsed.String(), nil
}

func normalizeSHA256(value string) (string, error) {
	digest := strings.ToLower(strings.TrimSpace(value))
	if len(digest) != sha256.Size*2 {
		return "", errors.New("native artifact SHA256 must be exactly 64 hexadecimal characters")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", fmt.Errorf("native artifact SHA256 is not hexadecimal: %w", err)
	}
	return digest, nil
}

// normalizeArtifactFormat 合并格式字段并拒绝未知或互相冲突的归档格式。
func normalizeArtifactFormat(value, alternate string) (string, error) {
	format := strings.ToLower(strings.TrimSpace(value))
	other := strings.ToLower(strings.TrimSpace(alternate))
	if format != "" && other != "" && format != other {
		return "", fmt.Errorf("native artifact format fields disagree: %q and %q", value, alternate)
	}
	if format == "" {
		format = other
	}
	switch format {
	case NativeArtifactFormatZip, NativeArtifactFormatGzip, NativeArtifactFormatTar, NativeArtifactFormatTarGz, NativeArtifactFormatDeb, "tgz", "gz":
		if format == "tgz" {
			return NativeArtifactFormatTarGz, nil
		}
		if format == "gz" {
			return NativeArtifactFormatGzip, nil
		}
		return format, nil
	default:
		return "", fmt.Errorf("unsupported native artifact format: %q", format)
	}
}

// cleanArchivePath 规范化归档内相对路径并拒绝绝对路径与目录穿越。
func cleanArchivePath(value string) (string, error) {
	if err := validateArchivePathText(value); err != nil {
		return "", err
	}
	archivePath := value
	// Official Unix tarballs commonly prefix every entry with ./ (including a
	// root directory entry). Strip only that benign prefix; traversal components
	// remain rejected below after normalization.
	for strings.HasPrefix(archivePath, "./") {
		archivePath = strings.TrimPrefix(archivePath, "./")
	}
	if archivePath == "." {
		return "", nil
	}
	if archivePath == "" {
		return "", errors.New("archive path is empty")
	}
	if err := validateArchivePathComponents(value, archivePath); err != nil {
		return "", err
	}
	clean := path.Clean(archivePath)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return "", fmt.Errorf("archive path escapes extraction root: %q", value)
	}
	return clean, nil
}

// validateArchivePathText 拒绝空白漂移、NUL、反斜杠和绝对归档路径。
func validateArchivePathText(value string) error {
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsRune(value, '\x00') {
		return errors.New("archive path is empty or contains NUL")
	}
	if strings.ContainsRune(value, '\\') || strings.HasPrefix(value, "/") {
		return fmt.Errorf("archive path is absolute or uses an unsafe separator: %q", value)
	}
	return nil
}

// validateArchivePathComponents 拒绝空、当前目录和父目录组件。
func validateArchivePathComponents(original, normalized string) error {
	for _, part := range strings.Split(normalized, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("archive path contains unsafe component: %q", original)
		}
	}
	return nil
}

// download 通过 HTTPS 下载有界 artifact，并在落盘后核验固定 SHA-256。
func (i *NativeArtifactInstaller) download(ctx context.Context, urlValue, destination, expected string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, urlValue, nil)
	if err != nil {
		return fmt.Errorf("create native artifact download request: %w", err)
	}
	client := *i.httpClient
	priorRedirect := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL == nil || !strings.EqualFold(request.URL.Scheme, "https") {
			return errors.New("native artifact redirect must remain HTTPS")
		}
		if priorRedirect != nil {
			return priorRedirect(request, via)
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download native artifact: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("download native artifact returned HTTP status %s", response.Status)
	}
	return writeNativeArtifactDownload(response.Body, destination, expected, i.maxArtifactSize)
}

// writeNativeArtifactDownload 有界写入响应体，并核验大小与内容摘要。
func writeNativeArtifactDownload(input io.Reader, destination, expected string, maxArtifactSize int64) error {
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create native artifact download file: %w", err)
	}
	digest := sha256.New()
	limited := io.LimitReader(input, maxArtifactSize+1)
	_, copyErr := io.Copy(io.MultiWriter(file, digest), limited)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("download native artifact bytes: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close native artifact download file: %w", closeErr)
	}
	info, err := os.Stat(destination)
	if err != nil {
		return fmt.Errorf("stat native artifact download file: %w", err)
	}
	if info.Size() > maxArtifactSize {
		return fmt.Errorf("native artifact exceeds maximum size of %d bytes", maxArtifactSize)
	}
	actual := hex.EncodeToString(digest.Sum(nil))
	if actual != expected {
		return fmt.Errorf("native artifact SHA256 does not match: got %s, want %s", actual, expected)
	}
	return nil
}

func extractNativeArchive(archivePath, payloadDir, format, binaryPath string, maxBytes int64, allowSymlinks bool) error {
	switch format {
	case NativeArtifactFormatZip:
		return extractNativeZip(archivePath, payloadDir, maxBytes)
	case NativeArtifactFormatGzip:
		return extractNativeGzip(archivePath, payloadDir, binaryPath, maxBytes)
	case NativeArtifactFormatTar, NativeArtifactFormatTarGz:
		return extractNativeTar(archivePath, payloadDir, format == NativeArtifactFormatTarGz, maxBytes, allowSymlinks)
	case NativeArtifactFormatDeb:
		return extractNativeDeb(archivePath, payloadDir, maxBytes, allowSymlinks)
	default:
		return fmt.Errorf("unsupported native artifact format: %q", format)
	}
}

// extractNativeGzip 解压单文件 gzip artifact 到 payloadDir/binaryPath。
// gzip 本身没有目录清单，因此目标路径由已校验的 BinaryPath 唯一决定。
func extractNativeGzip(archivePath, payloadDir, binaryPath string, maxBytes int64) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open native gzip artifact: %w", err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open native gzip artifact: %w", err)
	}
	defer gzipReader.Close()
	destination, err := extractionDestination(payloadDir, binaryPath)
	if err != nil {
		return fmt.Errorf("resolve native gzip destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create native gzip parent: %w", err)
	}
	if _, err := writeLimitedArchiveFile(destination, gzipReader, maxBytes); err != nil {
		return fmt.Errorf("extract native gzip artifact: %w", err)
	}
	return nil
}

// extractNativeZip 逐项安全解压 ZIP，并限制全部展开内容大小。
func extractNativeZip(archivePath, payloadDir string, maxBytes int64) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open native ZIP artifact: %w", err)
	}
	defer reader.Close()
	seen := make(map[string]struct{})
	var total int64
	for _, entry := range reader.File {
		written, err := extractNativeZipEntry(entry, payloadDir, maxBytes-total, seen)
		if err != nil {
			return err
		}
		total += written
	}
	return nil
}

// extractNativeZipEntry 校验并展开一个 ZIP 项，返回实际写入字节数。
func extractNativeZipEntry(entry *zip.File, payloadDir string, remaining int64, seen map[string]struct{}) (int64, error) {
	name, skip, err := validateNativeZipEntry(entry, seen)
	if err != nil {
		return 0, err
	}
	if skip {
		return 0, nil
	}
	destination, err := extractionDestination(payloadDir, name)
	if err != nil {
		return 0, err
	}
	mode := entry.FileInfo().Mode()
	if mode&os.ModeSymlink != 0 {
		return 0, fmt.Errorf("reject symlink native ZIP entry %q", entry.Name)
	}
	if entry.FileInfo().IsDir() {
		return 0, createNativeArchiveDirectory(destination, "ZIP", entry.Name)
	}
	return extractNativeZipFile(entry, destination, remaining)
}

// validateNativeZipEntry 规范化 ZIP 项名称并拒绝空文件与重复路径。
func validateNativeZipEntry(entry *zip.File, seen map[string]struct{}) (string, bool, error) {
	name, err := cleanArchivePath(strings.TrimSuffix(entry.Name, "/"))
	if err != nil {
		return "", false, fmt.Errorf("reject native ZIP entry %q: %w", entry.Name, err)
	}
	if name == "" {
		if entry.FileInfo().IsDir() {
			return "", true, nil
		}
		return "", false, fmt.Errorf("reject empty native ZIP entry %q", entry.Name)
	}
	if _, exists := seen[name]; exists {
		return "", false, fmt.Errorf("reject duplicate native ZIP entry %q", entry.Name)
	}
	seen[name] = struct{}{}
	return name, false, nil
}

// extractNativeZipFile 展开一个已校验的普通 ZIP 文件。
func extractNativeZipFile(entry *zip.File, destination string, remaining int64) (int64, error) {
	mode := entry.FileInfo().Mode()
	if mode&os.ModeType != 0 || entry.UncompressedSize64 > uint64(remaining) {
		return 0, fmt.Errorf("reject non-regular or oversized native ZIP entry %q", entry.Name)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return 0, fmt.Errorf("create native ZIP parent for %q: %w", entry.Name, err)
	}
	input, err := entry.Open()
	if err != nil {
		return 0, fmt.Errorf("open native ZIP entry %q: %w", entry.Name, err)
	}
	written, copyErr := writeArchiveFile(destination, input, int64(entry.UncompressedSize64))
	closeErr := input.Close()
	if copyErr != nil {
		return 0, fmt.Errorf("extract native ZIP entry %q: %w", entry.Name, copyErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("close native ZIP entry %q: %w", entry.Name, closeErr)
	}
	if err := preserveArchiveMode(destination, entry.Mode()); err != nil {
		return 0, fmt.Errorf("preserve native ZIP entry mode %q: %w", entry.Name, err)
	}
	return written, nil
}

// createNativeArchiveDirectory 创建归档目录并保留包含格式与原始名称的错误上下文。
func createNativeArchiveDirectory(destination, format, name string) error {
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return fmt.Errorf("create native %s directory %q: %w", format, name, err)
	}
	return nil
}

func extractNativeTar(archivePath, payloadDir string, compressed bool, maxBytes int64, allowSymlinks bool) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open native tar artifact: %w", err)
	}
	defer file.Close()
	var input io.Reader = file
	var gzipReader *gzip.Reader
	if compressed {
		gzipReader, err = gzip.NewReader(file)
		if err != nil {
			return fmt.Errorf("open native gzip artifact: %w", err)
		}
		defer gzipReader.Close()
		input = gzipReader
	}
	return extractNativeTarReader(input, payloadDir, maxBytes, allowSymlinks)
}

// extractNativeTarReader 逐项安全解压 tar，并拒绝重复、越界和未知类型。
func extractNativeTarReader(input io.Reader, payloadDir string, maxBytes int64, allowSymlinks bool) error {
	reader := tar.NewReader(input)
	seen := make(map[string]struct{})
	var total int64
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("read native tar entry: %w", nextErr)
		}
		// Official Linux archives commonly carry POSIX extended attributes as
		// metadata records. They do not represent filesystem entries and must
		// never be written to the payload; the tar reader consumes their body
		// when the next header is read.
		if header.Typeflag == tar.TypeXHeader || header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		written, err := extractNativeTarEntry(reader, header, payloadDir, maxBytes-total, allowSymlinks, seen)
		if err != nil {
			return err
		}
		total += written
	}
	return nil
}

// extractNativeTarEntry 校验并展开一个 tar 项，返回实际写入字节数。
func extractNativeTarEntry(reader io.Reader, header *tar.Header, payloadDir string, remaining int64, allowSymlinks bool, seen map[string]struct{}) (int64, error) {
	name, skip, err := validateNativeTarEntry(header, seen)
	if err != nil {
		return 0, err
	}
	if skip {
		return 0, nil
	}
	destination, err := extractionDestination(payloadDir, name)
	if err != nil {
		return 0, err
	}
	switch header.Typeflag {
	case tar.TypeDir:
		return 0, createNativeArchiveDirectory(destination, "tar", header.Name)
	case tar.TypeSymlink:
		return 0, extractNativeTarSymlink(header, name, destination, allowSymlinks)
	case tar.TypeReg, tar.TypeRegA:
		return extractNativeTarFile(reader, header, destination, remaining)
	default:
		return 0, fmt.Errorf("reject native tar entry %q with unsupported type %d", header.Name, header.Typeflag)
	}
	return 0, nil
}

// validateNativeTarEntry 规范化 tar 项名称并拒绝空文件与重复路径。
func validateNativeTarEntry(header *tar.Header, seen map[string]struct{}) (string, bool, error) {
	name, err := cleanArchivePath(strings.TrimSuffix(header.Name, "/"))
	if err != nil {
		return "", false, fmt.Errorf("reject native tar entry %q: %w", header.Name, err)
	}
	if name == "" {
		if header.Typeflag == tar.TypeDir {
			return "", true, nil
		}
		return "", false, fmt.Errorf("reject empty native tar entry %q", header.Name)
	}
	if _, exists := seen[name]; exists {
		return "", false, fmt.Errorf("reject duplicate native tar entry %q", header.Name)
	}
	seen[name] = struct{}{}
	return name, false, nil
}

// extractNativeTarSymlink 创建已验证且留在 payload 内的归档符号链接。
func extractNativeTarSymlink(header *tar.Header, name, destination string, allowSymlinks bool) error {
	if !allowSymlinks {
		return fmt.Errorf("reject symlink native tar entry %q with unsupported type", header.Name)
	}
	if err := validateArchiveSymlinkTarget(name, header.Linkname); err != nil {
		return fmt.Errorf("reject native tar symlink %q: %w", header.Name, err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create native tar symlink parent %q: %w", header.Name, err)
	}
	if err := os.Symlink(header.Linkname, destination); err != nil {
		// Windows 需把 Win32 5/1314 保留为 typed authorization_required 根因，
		// 由支持审批 UI 的宿主弹出授权；安装器本身不得改 ACL 或复制文件冒充链接。
		return fmt.Errorf("create native tar symlink %q: %w", header.Name, securefs.WrapErrorForPath(err, destination))
	}
	return nil
}

// extractNativeTarFile 展开一个已校验的普通 tar 文件。
func extractNativeTarFile(reader io.Reader, header *tar.Header, destination string, remaining int64) (int64, error) {
	if header.Size < 0 || header.Size > remaining {
		return 0, fmt.Errorf("reject oversized native tar entry %q", header.Name)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return 0, fmt.Errorf("create native tar parent for %q: %w", header.Name, err)
	}
	written, err := writeArchiveFile(destination, reader, header.Size)
	if err != nil {
		return 0, fmt.Errorf("extract native tar entry %q: %w", header.Name, err)
	}
	if err := preserveArchiveMode(destination, header.FileInfo().Mode()); err != nil {
		return 0, fmt.Errorf("preserve native tar entry mode %q: %w", header.Name, err)
	}
	return written, nil
}

// extractNativeDeb 在进程内解析 DEB ar 和 data.tar.zst，不调用 PATH 中的系统工具。
func validateArchiveSymlinkTarget(name, linkname string) error {
	linkname = strings.TrimSpace(linkname)
	if linkname == "" {
		return errors.New("symlink target is empty")
	}
	if strings.ContainsRune(linkname, '\x00') {
		return errors.New("symlink target contains NUL")
	}
	cleanTarget := path.Clean(path.Join(path.Dir(name), linkname))
	if path.IsAbs(linkname) || cleanTarget == ".." || strings.HasPrefix(cleanTarget, "../") {
		return fmt.Errorf("symlink target escapes payload: %q", linkname)
	}
	return nil
}

func extractionDestination(payloadDir, archivePath string) (string, error) {
	destination := filepath.Join(payloadDir, filepath.FromSlash(archivePath))
	relative, err := filepath.Rel(payloadDir, destination)
	if err != nil {
		return "", fmt.Errorf("resolve native archive destination %q: %w", archivePath, err)
	}
	if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("native archive entry escapes extraction root: %q", archivePath)
	}
	return destination, nil
}

func writeArchiveFile(destination string, input io.Reader, size int64) (int64, error) {
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create extracted archive file: %w", err)
	}
	written, copyErr := io.CopyN(file, input, size)
	closeErr := file.Close()
	if copyErr != nil {
		return written, copyErr
	}
	if closeErr != nil {
		return written, closeErr
	}
	return written, nil
}

func preserveArchiveMode(filename string, mode os.FileMode) error {
	permissions := mode.Perm()
	if permissions == 0 {
		return nil
	}
	return os.Chmod(filename, permissions)
}

// writeLimitedArchiveFile 写入有界归档文件，并拒绝超出声明上限的数据。
func writeLimitedArchiveFile(destination string, input io.Reader, maxBytes int64) (int64, error) {
	if maxBytes < 0 {
		return 0, errors.New("archive byte limit cannot be negative")
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create extracted archive file: %w", err)
	}
	limited := io.LimitReader(input, maxBytes+1)
	written, copyErr := io.Copy(file, limited)
	closeErr := file.Close()
	if copyErr != nil {
		return written, copyErr
	}
	if closeErr != nil {
		return written, closeErr
	}
	if written > maxBytes {
		return written, fmt.Errorf("extracted archive exceeds maximum size of %d bytes", maxBytes)
	}
	return written, nil
}

// prepareNativeLauncher 验证解压目标并生成只指向最终受管二进制的 launcher。
func prepareNativeLauncher(stageDir, finalDir string, normalized normalizedNativeArtifactSpec) (NativeInstallResult, error) {
	target := filepath.Join(stageDir, "payload", filepath.FromSlash(normalized.binaryPath))
	info, err := os.Lstat(target)
	if err != nil {
		return NativeInstallResult{}, fmt.Errorf("native artifact binary %s is missing: %w", normalized.binaryPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return NativeInstallResult{}, fmt.Errorf("native artifact binary %s is not a regular file", normalized.binaryPath)
	}
	if err := os.Chmod(target, 0o755); err != nil {
		return NativeInstallResult{}, fmt.Errorf("mark native artifact binary executable: %w", err)
	}
	launcherRelative := filepath.Join("launcher", normalized.launcherName)
	launcher := filepath.Join(stageDir, launcherRelative)
	if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
		return NativeInstallResult{}, fmt.Errorf("create native artifact launcher directory: %w", err)
	}
	absoluteTarget, err := filepath.Abs(filepath.Join(finalDir, "payload", filepath.FromSlash(normalized.binaryPath)))
	if err != nil {
		return NativeInstallResult{}, fmt.Errorf("resolve native artifact binary path: %w", err)
	}
	content := "#!/bin/sh\nexec " + shellQuote(absoluteTarget) + " \"$@\"\n"
	if err := writeExecutableFile(launcher, []byte(content)); err != nil {
		return NativeInstallResult{}, fmt.Errorf("write managed native artifact launcher: %w", err)
	}
	return NativeInstallResult{
		Name:         normalized.name,
		Version:      normalized.version,
		BinaryPath:   filepath.Join("payload", filepath.FromSlash(normalized.binaryPath)),
		LauncherPath: launcherRelative,
		SHA256:       normalized.sha256,
	}, nil
}

func writeExecutableFile(filename string, content []byte) error {
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
