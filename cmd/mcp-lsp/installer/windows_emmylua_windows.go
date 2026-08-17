//go:build windows

package installer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	// WindowsLSPProductEmmyLua 是独立的 EmmyLua Analyzer Rust 产品标识；它不属于 LuaLS catalog 条目。
	WindowsLSPProductEmmyLua WindowsLSPProduct = "emmylua-analyzer-rust"
	// WindowsEmmyLuaVersion 是官方 Windows ARM64 release 的固定版本。
	WindowsEmmyLuaVersion = "0.25.1"
	// WindowsEmmyLuaBinaryName 是归档和 ready tree 中真实的 EmmyLua 启动文件名。
	WindowsEmmyLuaBinaryName = "emmylua_ls.exe"
	// WindowsEmmyLuaArchiveURL 是 EmmyLua 0.25.1 官方 Windows ARM64 release 资产地址。
	WindowsEmmyLuaArchiveURL = "https://github.com/EmmyLuaLs/emmylua-analyzer-rust/releases/download/0.25.1/emmylua_ls-win32-arm64.zip"
	// WindowsEmmyLuaArchiveSHA256 是官方 Windows ARM64 zip 的锁定 SHA-256。
	WindowsEmmyLuaArchiveSHA256 = "f6f335f01fccca6f000a6240fb78c6fbab069230b1bb4347361ef3f64550390a"
	// WindowsEmmyLuaExecutableSHA256 是 zip 内 emmylua_ls.exe 的锁定 SHA-256。
	WindowsEmmyLuaExecutableSHA256 = "c05a85e354de013e0300c42197592355d425a8ef7fae7ef1eb3febd68c1791ac"
	// WindowsEmmyLuaPEMachine 是 exe 的 IMAGE_FILE_MACHINE_ARM64 值。
	WindowsEmmyLuaPEMachine = WindowsImageFileMachineARM64
)

var (
	// ErrWindowsEmmyLuaRequiresARM64 表示 EmmyLua 资产只能在 Windows native ARM64 进程中使用。
	ErrWindowsEmmyLuaRequiresARM64 = errors.New("EmmyLua Windows asset requires native Windows ARM64")
	// ErrWindowsEmmyLuaBinaryInvalid 表示 ready tree 中的 EmmyLua 文件没有匹配固定 SHA 或 PE machine。
	ErrWindowsEmmyLuaBinaryInvalid = errors.New("EmmyLua Windows binary failed locked identity validation")
)

// WindowsEmmyLuaAssetFacts 返回独立 EmmyLua manifest 的固定 ARM64 资产副本。
// 该函数不读取网络或文件系统，调用方修改返回 map 不会改变清单真值。
func WindowsEmmyLuaAssetFacts() map[string]WindowsLockedAsset {
	return map[string]WindowsLockedAsset{
		WindowsHostArchARM64: {
			Architecture:      WindowsHostArchARM64,
			Version:           WindowsEmmyLuaVersion,
			URL:               WindowsEmmyLuaArchiveURL,
			SHA256:            WindowsEmmyLuaArchiveSHA256,
			Format:            WindowsLockedAssetFormatZip,
			BinaryPath:        WindowsEmmyLuaBinaryName,
			MinWindowsVersion: windowsLSPCatalogMinWindowsVersion,
			MinWindowsBuild:   windowsLSPCatalogMinWindowsBuild,
		},
	}
}

// WindowsEmmyLuaManifest 返回独立 EmmyLua ARM64 manifest；它不加入 WindowsLSPCatalog，避免伪装成 LuaLS。
func WindowsEmmyLuaManifest() WindowsLockedAssetManifest {
	return WindowsLockedAssetManifest{
		Name:   string(WindowsLSPProductEmmyLua),
		Assets: WindowsEmmyLuaAssetFacts(),
	}
}

// WindowsEmmyLuaAssetForArchitecture 只接受 ARM64，x64/x86 请求保持显式 typed unsupported。
func WindowsEmmyLuaAssetForArchitecture(architecture string) (WindowsLockedAsset, error) {
	asset, err := WindowsEmmyLuaManifest().AssetForArchitecture(architecture)
	if err != nil {
		return WindowsLockedAsset{}, err
	}
	if asset.Architecture != WindowsHostArchARM64 {
		return WindowsLockedAsset{}, fmt.Errorf("%w: selected %q", ErrWindowsEmmyLuaRequiresARM64, asset.Architecture)
	}
	return asset, nil
}

// WindowsEmmyLuaAssetForPlatform 只按 Windows native ARM64 选择资产；ProcessArch 仅作为诊断事实保留。
func WindowsEmmyLuaAssetForPlatform(platform WindowsHostPlatform) (WindowsLockedAsset, error) {
	if platform.NativeArch != WindowsHostArchARM64 {
		return WindowsLockedAsset{}, fmt.Errorf("%w: native=%q process=%q (ProcessArch is diagnostic only)", ErrWindowsEmmyLuaRequiresARM64, platform.NativeArch, platform.ProcessArch)
	}
	asset, err := WindowsEmmyLuaManifest().AssetForPlatform(platform)
	if err != nil {
		return WindowsLockedAsset{}, err
	}
	return asset, nil
}

// WindowsEmmyLuaCommandArguments 返回 EmmyLua 官方证明所用的确定性 stdio 参数副本。
func WindowsEmmyLuaCommandArguments() []string {
	return []string{"--communication", "stdio", "--log-level", "error", "--resources-path", "none"}
}

// WindowsEmmyLuaProvisionResult 描述已校验的 EmmyLua ready 路径、资产、宿主事实和启动参数。
type WindowsEmmyLuaProvisionResult struct {
	Asset      WindowsLockedAsset
	Platform   WindowsHostPlatform
	Executable string
	Args       []string
}

// ProvisionWindowsEmmyLua 在产品缓存中下载并校验 EmmyLua ARM64；它不使用 PATH 或跨架构回退。
func ProvisionWindowsEmmyLua(ctx context.Context, productRoot string, client *http.Client) (WindowsEmmyLuaProvisionResult, error) {
	if ctx == nil {
		return WindowsEmmyLuaProvisionResult{}, errors.New("Windows EmmyLua provision context is nil")
	}
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return WindowsEmmyLuaProvisionResult{}, fmt.Errorf("detect Windows host for EmmyLua: %w", err)
	}
	cache, err := NewWindowsLSPAssetCache(productRoot, client)
	if err != nil {
		return WindowsEmmyLuaProvisionResult{}, fmt.Errorf("create Windows EmmyLua asset cache: %w", err)
	}
	return provisionWindowsEmmyLuaForPlatform(ctx, cache, platform)
}

func provisionWindowsEmmyLuaForPlatform(ctx context.Context, cache *WindowsAssetCache, platform WindowsHostPlatform) (WindowsEmmyLuaProvisionResult, error) {
	if ctx == nil {
		return WindowsEmmyLuaProvisionResult{}, errors.New("Windows EmmyLua provision context is nil")
	}
	if cache == nil {
		return WindowsEmmyLuaProvisionResult{}, errors.New("Windows EmmyLua asset cache is nil")
	}
	asset, err := WindowsEmmyLuaAssetForPlatform(platform)
	if err != nil {
		return WindowsEmmyLuaProvisionResult{}, err
	}
	executable, err := cache.EnsureForPlatform(ctx, WindowsEmmyLuaManifest(), platform)
	if err != nil {
		return WindowsEmmyLuaProvisionResult{}, fmt.Errorf("ensure Windows EmmyLua asset: %w", err)
	}
	if err := ValidateWindowsEmmyLuaExecutable(executable); err != nil {
		return WindowsEmmyLuaProvisionResult{}, err
	}
	return WindowsEmmyLuaProvisionResult{
		Asset:      asset,
		Platform:   platform,
		Executable: executable,
		Args:       WindowsEmmyLuaCommandArguments(),
	}, nil
}

// ResolveWindowsEmmyLuaAssetPath 只读解析 EmmyLua ARM64 ready tree，不联网、不建目录、不改 cache。
func ResolveWindowsEmmyLuaAssetPath(productRoot string) (string, error) {
	root, err := resolveWindowsInstallProductRoot(productRoot)
	if err != nil {
		return "", err
	}
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return "", err
	}
	asset, err := WindowsEmmyLuaAssetForPlatform(platform)
	if err != nil {
		return "", err
	}
	manifest := WindowsEmmyLuaManifest()
	readyPath := filepath.Join(
		root,
		"cache",
		WindowsLSPAssetCacheSubdir,
		cacheSegment(manifest.Name),
		cacheSegment(asset.Version),
		asset.Architecture,
		strings.ToLower(asset.SHA256),
		"ready",
		filepath.FromSlash(asset.BinaryPath),
	)
	return requireWindowsResolverFile(readyPath, ErrWindowsLSPInstallCacheMiss)
}

// ValidateWindowsEmmyLuaExecutable 验证真实 exe 的固定 SHA-256 与 PE ARM64 machine。
func ValidateWindowsEmmyLuaExecutable(path string) error {
	resolved, err := requireWindowsResolverFile(path, ErrWindowsEmmyLuaBinaryInvalid)
	if err != nil {
		return err
	}
	contents, err := os.ReadFile(resolved)
	if err != nil {
		return fmt.Errorf("read EmmyLua executable: %w", err)
	}
	digest := sha256.Sum256(contents)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), WindowsEmmyLuaExecutableSHA256) {
		return fmt.Errorf("%w: executable SHA256=%s want=%s", ErrWindowsEmmyLuaBinaryInvalid, hex.EncodeToString(digest[:]), WindowsEmmyLuaExecutableSHA256)
	}
	image, err := pe.NewFile(bytes.NewReader(contents))
	if err != nil {
		return fmt.Errorf("%w: parse PE: %v", ErrWindowsEmmyLuaBinaryInvalid, err)
	}
	defer image.Close()
	if image.FileHeader.Machine != WindowsEmmyLuaPEMachine {
		return fmt.Errorf("%w: PE machine=0x%04x want=0x%04x", ErrWindowsEmmyLuaBinaryInvalid, image.FileHeader.Machine, WindowsEmmyLuaPEMachine)
	}
	return nil
}
