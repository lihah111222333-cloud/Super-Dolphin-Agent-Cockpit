package installer

// 本文件故意不加 windows build tag：这里只定义可跨平台校验的 Windows
// 资产 wire/catalog 契约，非 Windows 构建也要编译这些类型以执行配置与字段守卫。

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// WindowsLockedAssetFormat 标识 Windows 锁定资产的载荷格式；只允许目录中声明的固定格式。
type WindowsLockedAssetFormat string

const (
	// WindowsLockedAssetFormatRaw 表示无需解包、直接校验并发布单个 Windows 可执行文件。
	WindowsLockedAssetFormatRaw WindowsLockedAssetFormat = "raw"
	// WindowsLockedAssetFormatZip 表示必须安全解包的 Windows ZIP 资产。
	WindowsLockedAssetFormatZip WindowsLockedAssetFormat = "zip"
	// WindowsLockedAssetFormatTarGz 表示必须安全解包的 Windows gzip tar 资产。
	WindowsLockedAssetFormatTarGz WindowsLockedAssetFormat = "tar.gz"
	// WindowsLockedAssetFormatTarXz 表示必须安全解包的 Windows xz tar 资产。
	WindowsLockedAssetFormatTarXz WindowsLockedAssetFormat = "tar.xz"
)

var (
	// ErrWindowsUnsupportedAssetArchitecture 表示 Windows 主机原生架构没有锁定资产。
	ErrWindowsUnsupportedAssetArchitecture = errors.New("unsupported locked asset architecture")
	// ErrWindowsInvalidLockedAsset 表示 Windows 资产目录违反固定 URL、摘要、格式或路径合同。
	ErrWindowsInvalidLockedAsset = errors.New("invalid locked asset")
	// ErrWindowsAssetChecksumMismatch 表示下载的 Windows 资产摘要与锁定 SHA-256 不一致。
	ErrWindowsAssetChecksumMismatch = errors.New("locked asset SHA-256 mismatch")
	// ErrWindowsAssetHTTPStatus 表示 Windows 资产下载返回非 2xx HTTP 状态。
	ErrWindowsAssetHTTPStatus = errors.New("locked asset HTTP request returned non-2xx status")
	// ErrWindowsUnsafeAssetArchive 表示 Windows 资产归档包含路径穿越、链接或特殊文件。
	ErrWindowsUnsafeAssetArchive = errors.New("unsafe locked asset archive")
	// ErrWindowsUnsupportedWindowsVersion 表示主机 Windows 版本或 build 低于资产要求。
	ErrWindowsUnsupportedWindowsVersion = errors.New("unsupported Windows version for locked asset")
)

// WindowsUnsupportedAssetArchitectureError 记录 Windows 原生架构缺少锁定资产及可用架构。
type WindowsUnsupportedAssetArchitectureError struct {
	// AssetName 是未找到原生架构资产的 Windows 产品标识。
	AssetName string
	// Architecture 是主机要求的 Windows 原生架构。
	Architecture string
	// Available 是目录中可供选择的标准化 Windows 架构列表。
	Available []string
}

// WindowsUnsupportedWindowsVersionError 记录 Windows 主机版本或 build 不满足资产最低要求。
type WindowsUnsupportedWindowsVersionError struct {
	// AssetName 是版本约束不满足的 Windows 产品标识。
	AssetName string
	// WindowsVersion 是主机报告的 Windows major.minor 版本。
	WindowsVersion string
	// WindowsBuild 是主机报告的 Windows build 号。
	WindowsBuild uint32
	// RequiredVersion 是资产要求的最低 Windows major.minor 版本。
	RequiredVersion string
	// RequiredBuild 是资产要求的最低 Windows build 号。
	RequiredBuild uint32
}

// Error 返回 Windows 版本不支持错误的可诊断文本；该错误不会触发下载或写盘。
func (e *WindowsUnsupportedWindowsVersionError) Error() string {
	if e == nil {
		return ErrWindowsUnsupportedWindowsVersion.Error()
	}
	return fmt.Sprintf("%s for asset %q: host %s build %d, required %s build %d",
		ErrWindowsUnsupportedWindowsVersion, e.AssetName, e.WindowsVersion, e.WindowsBuild,
		e.RequiredVersion, e.RequiredBuild)
}

// Unwrap 返回 Windows 版本不支持错误的哨兵值，供 errors.Is 进行精确分类。
func (e *WindowsUnsupportedWindowsVersionError) Unwrap() error {
	return ErrWindowsUnsupportedWindowsVersion
}

// Error 返回 Windows 架构不支持错误的可诊断文本；该错误不会触发跨架构或仿真回退。
func (e *WindowsUnsupportedAssetArchitectureError) Error() string {
	if e == nil {
		return ErrWindowsUnsupportedAssetArchitecture.Error()
	}
	return fmt.Sprintf("%s %q has no locked asset for native architecture %q (available: %s)",
		ErrWindowsUnsupportedAssetArchitecture, e.AssetName, e.Architecture, strings.Join(e.Available, ", "))
}

// Unwrap 返回 Windows 架构不支持错误的哨兵值，供 errors.Is 进行精确分类。
func (e *WindowsUnsupportedAssetArchitectureError) Unwrap() error {
	return ErrWindowsUnsupportedAssetArchitecture
}

// WindowsLockedAsset 描述一个仅供 Windows 原生架构使用的固定远程资产及其 ready 路径。
type WindowsLockedAsset struct {
	// Architecture 是资产支持的标准化 Windows 原生架构。
	Architecture string `json:"architecture"`
	// Version 是不可使用 latest 或空值替代的固定资产版本。
	Version string `json:"version"`
	// URL 是下载固定 Windows 资产的绝对 http(s) 地址。
	URL string `json:"url"`
	// SHA256 是下载内容的 64 位十六进制 SHA-256 摘要。
	SHA256 string `json:"sha256"`
	// Format 指定 Windows 资产是原始文件还是受安全检查的归档。
	Format WindowsLockedAssetFormat `json:"format"`
	// BinaryPath 是 ready 树中必须存在的相对 Windows 可执行文件路径。
	BinaryPath string `json:"binary_path"`
	// MinWindowsVersion 是可选的 Windows major.minor 最低版本。
	MinWindowsVersion string `json:"min_windows_version,omitempty"`
	// MinWindowsBuild 是可选的 Windows 最低 build 号。
	MinWindowsBuild uint32 `json:"min_windows_build,omitempty"`
}

// WindowsLockedAssetManifest 按 Windows 原生架构保存产品的固定资产目录。
type WindowsLockedAssetManifest struct {
	// Name 是 Windows 产品在缓存路径和诊断中的稳定标识。
	Name string `json:"name"`
	// Assets 将标准化 Windows 架构映射到唯一的锁定资产。
	Assets map[string]WindowsLockedAsset `json:"assets"`
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// Validate 校验 Windows 资产目录的架构、版本、URL、摘要、格式和相对路径合同。
func (m WindowsLockedAssetManifest) Validate() error {
	if len(m.Assets) == 0 {
		return fmt.Errorf("%w: asset %q has no architecture entries", ErrWindowsInvalidLockedAsset, m.Name)
	}
	seen := make(map[string]struct{}, len(m.Assets))
	for key, asset := range m.Assets {
		architecture, err := NormalizeWindowsArchitectureAlias(key)
		if err != nil {
			return fmt.Errorf("%w: asset %q has invalid architecture key %q: %w", ErrWindowsInvalidLockedAsset, m.Name, key, err)
		}
		if _, ok := seen[architecture]; ok {
			return fmt.Errorf("%w: asset %q has duplicate architecture %q", ErrWindowsInvalidLockedAsset, m.Name, architecture)
		}
		seen[architecture] = struct{}{}
		if err := asset.validateForArchitecture(architecture); err != nil {
			return fmt.Errorf("%w: asset %q architecture %q: %w", ErrWindowsInvalidLockedAsset, m.Name, architecture, err)
		}
	}
	return nil
}

// SelectWindowsLockedAsset 按 Windows 主机原生架构和版本选择资产，不允许跨架构或仿真回退。
func SelectWindowsLockedAsset(m WindowsLockedAssetManifest, platform WindowsHostPlatform) (WindowsLockedAsset, error) {
	if err := m.Validate(); err != nil {
		return WindowsLockedAsset{}, err
	}
	if platform.OS != WindowsHostOSWindows {
		return WindowsLockedAsset{}, fmt.Errorf("%w: locked assets require Windows, got %q", ErrWindowsInvalidLockedAsset, platform.OS)
	}
	asset, err := m.AssetForArchitecture(platform.NativeArch)
	if err != nil {
		return WindowsLockedAsset{}, err
	}
	if err := asset.validateWindowsPlatform(m.Name, platform); err != nil {
		return WindowsLockedAsset{}, err
	}
	return asset, nil
}

// AssetForPlatform 按 Windows 主机原生架构和版本选择该目录中的固定资产。
func (m WindowsLockedAssetManifest) AssetForPlatform(platform WindowsHostPlatform) (WindowsLockedAsset, error) {
	return SelectWindowsLockedAsset(m, platform)
}

// AssetForArchitecture 按指定 Windows 原生架构选择固定资产，不进行架构转换或仿真。
func (m WindowsLockedAssetManifest) AssetForArchitecture(architecture string) (WindowsLockedAsset, error) {
	if err := m.Validate(); err != nil {
		return WindowsLockedAsset{}, err
	}
	normalized, err := NormalizeWindowsArchitectureAlias(architecture)
	if err != nil {
		return WindowsLockedAsset{}, &WindowsUnsupportedAssetArchitectureError{
			AssetName: m.Name, Architecture: architecture, Available: m.availableArchitectures(),
		}
	}
	asset, ok := m.Assets[normalized]
	if !ok {
		for key, candidate := range m.Assets {
			keyArch, keyErr := NormalizeWindowsArchitectureAlias(key)
			if keyErr == nil && keyArch == normalized {
				asset = candidate
				ok = true
				break
			}
		}
	}
	if !ok {
		return WindowsLockedAsset{}, &WindowsUnsupportedAssetArchitectureError{
			AssetName: m.Name, Architecture: normalized, Available: m.availableArchitectures(),
		}
	}
	asset.Architecture = normalized
	return asset, nil
}

func (m WindowsLockedAssetManifest) availableArchitectures() []string {
	available := make([]string, 0, len(m.Assets))
	for key := range m.Assets {
		architecture, err := NormalizeWindowsArchitectureAlias(key)
		if err == nil {
			available = append(available, architecture)
		}
	}
	sort.Strings(available)
	return available
}

func (a WindowsLockedAsset) validateForArchitecture(architecture string) error {
	if strings.TrimSpace(a.Architecture) != "" {
		declared, err := NormalizeWindowsArchitectureAlias(a.Architecture)
		if err != nil {
			return fmt.Errorf("invalid declared architecture %q: %w", a.Architecture, err)
		}
		if declared != architecture {
			return fmt.Errorf("declared architecture %q does not match key %q", declared, architecture)
		}
	}
	if strings.TrimSpace(a.Version) == "" || strings.ContainsAny(a.Version, " \r\n\t") || strings.EqualFold(strings.TrimSpace(a.Version), "latest") {
		return errors.New("version must be a non-empty fixed value")
	}
	parsed, err := url.Parse(strings.TrimSpace(a.URL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("URL must be an absolute http(s) URL: %q", a.URL)
	}
	if !sha256Pattern.MatchString(strings.TrimSpace(a.SHA256)) {
		return errors.New("SHA256 must be exactly 64 hexadecimal characters")
	}
	switch a.Format {
	case WindowsLockedAssetFormatRaw:
		binaryPath := strings.TrimSpace(a.BinaryPath)
		if binaryPath == "" {
			binaryPath = path.Base(parsed.Path)
		}
		if _, err := normalizeAssetRelativePath(binaryPath); err != nil {
			return fmt.Errorf("invalid raw binary path: %w", err)
		}
	case WindowsLockedAssetFormatZip, WindowsLockedAssetFormatTarGz, WindowsLockedAssetFormatTarXz:
		if _, err := normalizeAssetRelativePath(a.BinaryPath); err != nil {
			return fmt.Errorf("invalid archive binary path: %w", err)
		}
	default:
		return fmt.Errorf("unsupported asset format %q", a.Format)
	}
	if version := strings.TrimSpace(a.MinWindowsVersion); version != "" {
		if _, _, err := parseWindowsVersion(version); err != nil {
			return fmt.Errorf("invalid minimum Windows version %q: %w", version, err)
		}
	}
	return nil
}

func (a WindowsLockedAsset) validateWindowsPlatform(assetName string, platform WindowsHostPlatform) error {
	if strings.TrimSpace(platform.WindowsVersion) == "" || platform.WindowsBuild == 0 {
		return &WindowsUnsupportedWindowsVersionError{
			AssetName: assetName, WindowsVersion: platform.WindowsVersion, WindowsBuild: platform.WindowsBuild,
			RequiredVersion: a.MinWindowsVersion, RequiredBuild: a.MinWindowsBuild,
		}
	}
	hostMajor, hostMinor, err := parseWindowsVersion(platform.WindowsVersion)
	if err != nil {
		return &WindowsUnsupportedWindowsVersionError{
			AssetName: assetName, WindowsVersion: platform.WindowsVersion, WindowsBuild: platform.WindowsBuild,
			RequiredVersion: a.MinWindowsVersion, RequiredBuild: a.MinWindowsBuild,
		}
	}
	if strings.TrimSpace(a.MinWindowsVersion) != "" {
		minMajor, minMinor, parseErr := parseWindowsVersion(a.MinWindowsVersion)
		if parseErr != nil || hostMajor < minMajor || (hostMajor == minMajor && hostMinor < minMinor) {
			return &WindowsUnsupportedWindowsVersionError{
				AssetName: assetName, WindowsVersion: platform.WindowsVersion, WindowsBuild: platform.WindowsBuild,
				RequiredVersion: a.MinWindowsVersion, RequiredBuild: a.MinWindowsBuild,
			}
		}
	}
	if a.MinWindowsBuild != 0 && platform.WindowsBuild < a.MinWindowsBuild {
		return &WindowsUnsupportedWindowsVersionError{
			AssetName: assetName, WindowsVersion: platform.WindowsVersion, WindowsBuild: platform.WindowsBuild,
			RequiredVersion: a.MinWindowsVersion, RequiredBuild: a.MinWindowsBuild,
		}
	}
	return nil
}

func parseWindowsVersion(value string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return 0, 0, errors.New("Windows version must be major.minor")
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 1 {
		return 0, 0, errors.New("Windows major version is invalid")
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil || minor < 0 {
		return 0, 0, errors.New("Windows minor version is invalid")
	}
	return major, minor, nil
}

// normalizeAssetRelativePath 校验 Windows 资产相对路径，拒绝卷标、绝对路径和目录穿越。
func normalizeAssetRelativePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsRune(raw, '\x00') {
		return "", errors.New("path is empty or contains NUL")
	}
	normalized := strings.ReplaceAll(raw, "\\", "/")
	if strings.ContainsRune(normalized, ':') {
		return "", fmt.Errorf("path contains a Windows volume or stream separator: %q", raw)
	}
	if strings.HasPrefix(normalized, "/") || strings.HasPrefix(normalized, "//") {
		return "", fmt.Errorf("path must be relative: %q", raw)
	}
	clean := path.Clean(normalized)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path escapes extraction root: %q", raw)
	}
	for _, segment := range strings.Split(clean, "/") {
		if segment == ".." || segment == "" {
			return "", fmt.Errorf("path contains unsafe segment: %q", raw)
		}
	}
	return clean, nil
}
