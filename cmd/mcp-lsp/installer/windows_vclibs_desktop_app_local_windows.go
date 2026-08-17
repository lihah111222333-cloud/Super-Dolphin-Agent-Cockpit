//go:build windows

package installer

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	// WindowsVCLibsDesktopPackageIdentity 是微软 VC++ Desktop 框架包的固定身份，缓存复验时必须精确匹配。
	WindowsVCLibsDesktopPackageIdentity = "Microsoft.VCLibs.140.00.UWPDesktop"
	// WindowsVCLibsDesktopPackageVersion 是当前锁定的微软 VC++ Desktop 框架包版本。
	WindowsVCLibsDesktopPackageVersion = "14.0.33321.0"
	// WindowsVCLibsDesktopPackagePublisher 是 AppxManifest 中必须精确匹配的微软发布者身份。
	WindowsVCLibsDesktopPackagePublisher = "CN=Microsoft Corporation, O=Microsoft Corporation, L=Redmond, S=Washington, C=US"
)

var windowsVCLibsDesktopRequiredDLLs = []string{
	"concrt140.dll",
	"msvcp140.dll",
	"msvcp140_1.dll",
	"msvcp140_2.dll",
	"msvcp140_atomic_wait.dll",
	"msvcp140_codecvt_ids.dll",
	"vcruntime140.dll",
}

var windowsVCLibsDesktopLockedAssets = map[string]WindowsLockedAsset{
	WindowsHostArchARM64: {
		Architecture:      WindowsHostArchARM64,
		Version:           WindowsVCLibsDesktopPackageVersion,
		URL:               "https://download.microsoft.com/download/4/7/c/47c6134b-d61f-4024-83bd-b9c9ea951c25/Microsoft.VCLibs.arm64.14.00.Desktop.appx",
		SHA256:            "9a7f6d69ea6cf042ea8680b7cd0bfaa9c04f0f6cc89055d43f7f6cd0250508d3",
		Format:            WindowsLockedAssetFormatZip,
		BinaryPath:        "vcruntime140.dll",
		MinWindowsVersion: "10.0",
		MinWindowsBuild:   10042,
	},
	WindowsHostArchX64: {
		Architecture:      WindowsHostArchX64,
		Version:           WindowsVCLibsDesktopPackageVersion,
		URL:               "https://download.microsoft.com/download/4/7/c/47c6134b-d61f-4024-83bd-b9c9ea951c25/Microsoft.VCLibs.x64.14.00.Desktop.appx",
		SHA256:            "b56a9101f706f9d95f815f5b7fa6efbac972e86573d378b96a07cff5540c5961",
		Format:            WindowsLockedAssetFormatZip,
		BinaryPath:        "vcruntime140.dll",
		MinWindowsVersion: "10.0",
		MinWindowsBuild:   10042,
	},
	WindowsHostArchX86: {
		Architecture:      WindowsHostArchX86,
		Version:           WindowsVCLibsDesktopPackageVersion,
		URL:               "https://download.microsoft.com/download/4/7/c/47c6134b-d61f-4024-83bd-b9c9ea951c25/Microsoft.VCLibs.x86.14.00.Desktop.appx",
		SHA256:            "a7fb9d76e07b36d868179eb53ffd13740c25242176fa363f154798cf34edd4a9",
		Format:            WindowsLockedAssetFormatZip,
		BinaryPath:        "vcruntime140.dll",
		MinWindowsVersion: "10.0",
		MinWindowsBuild:   10042,
	},
}

// WindowsVCLibsDesktopAssetFacts 返回 ARM64、x64 与 x86 的微软官方 Appx 锁定资产副本，调用方修改结果不会污染全局事实。
func WindowsVCLibsDesktopAssetFacts() map[string]WindowsLockedAsset {
	facts := make(map[string]WindowsLockedAsset, len(windowsVCLibsDesktopLockedAssets))
	for architecture, asset := range windowsVCLibsDesktopLockedAssets {
		facts[architecture] = asset
	}
	return facts
}

// WindowsVCLibsDesktopAssetForPlatform 根据真实 Windows 原生架构和系统版本选择唯一锁定的 VC++ Desktop Appx。
func WindowsVCLibsDesktopAssetForPlatform(platform WindowsHostPlatform) (WindowsLockedAsset, error) {
	return SelectWindowsLockedAsset(windowsVCLibsDesktopManifest(), platform)
}

// ProvisionWindowsVCLibsDesktopAppLocal 在私有产品缓存中自动下载并复验架构匹配的 VC++ DLL，返回可加入子进程 DLL 搜索路径的绝对目录。
func ProvisionWindowsVCLibsDesktopAppLocal(ctx context.Context, productRoot string, client *http.Client) (string, error) {
	if ctx == nil {
		return "", errors.New("Windows VCLibs Desktop provision context is nil")
	}
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return "", fmt.Errorf("detect Windows host for VCLibs Desktop: %w", err)
	}
	cache, err := NewWindowsLSPAssetCache(productRoot, client)
	if err != nil {
		return "", fmt.Errorf("create Windows VCLibs Desktop asset cache: %w", err)
	}
	return provisionWindowsVCLibsDesktopAppLocalForPlatform(ctx, cache, platform, windowsVCLibsDesktopManifest())
}

func windowsVCLibsDesktopManifest() WindowsLockedAssetManifest {
	return WindowsLockedAssetManifest{
		Name:   "windows-vclibs-desktop-app-local",
		Assets: WindowsVCLibsDesktopAssetFacts(),
	}
}

func provisionWindowsVCLibsDesktopAppLocalForPlatform(ctx context.Context, cache *WindowsAssetCache, platform WindowsHostPlatform, manifest WindowsLockedAssetManifest) (string, error) {
	if ctx == nil {
		return "", errors.New("Windows VCLibs Desktop provision context is nil")
	}
	if cache == nil {
		return "", errors.New("Windows VCLibs Desktop asset cache is nil")
	}
	runtimePath, err := cache.EnsureForPlatform(ctx, manifest, platform)
	if err != nil {
		return "", fmt.Errorf("ensure Windows VCLibs Desktop asset: %w", err)
	}
	runtimeRoot := filepath.Dir(runtimePath)
	if err := validateWindowsVCLibsDesktopReadyRoot(runtimeRoot, platform.NativeArch); err != nil {
		return "", err
	}
	absoluteRoot, err := filepath.Abs(runtimeRoot)
	if err != nil {
		return "", fmt.Errorf("resolve Windows VCLibs Desktop runtime root: %w", err)
	}
	return filepath.Clean(absoluteRoot), nil
}

type windowsVCLibsDesktopAppxPackage struct {
	Identity windowsVCLibsDesktopAppxIdentity `xml:"Identity"`
}

type windowsVCLibsDesktopAppxIdentity struct {
	Name                  string `xml:"Name,attr"`
	Publisher             string `xml:"Publisher,attr"`
	Version               string `xml:"Version,attr"`
	ProcessorArchitecture string `xml:"ProcessorArchitecture,attr"`
}

func validateWindowsVCLibsDesktopReadyRoot(runtimeRoot, nativeArchitecture string) error {
	normalizedArchitecture, err := NormalizeWindowsArchitectureAlias(nativeArchitecture)
	if err != nil {
		return fmt.Errorf("normalize Windows VCLibs Desktop architecture: %w", err)
	}
	manifestPath := filepath.Join(runtimeRoot, "AppxManifest.xml")
	manifestFile, err := os.Open(manifestPath)
	if err != nil {
		return fmt.Errorf("open Windows VCLibs Desktop AppxManifest: %w", securefs.WrapErrorForPath(err, manifestPath))
	}
	var appx windowsVCLibsDesktopAppxPackage
	decodeErr := xml.NewDecoder(io.LimitReader(manifestFile, 1<<20)).Decode(&appx)
	closeErr := manifestFile.Close()
	if decodeErr != nil {
		return fmt.Errorf("decode Windows VCLibs Desktop AppxManifest: %w", decodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close Windows VCLibs Desktop AppxManifest: %w", closeErr)
	}
	wantManifestArchitecture, err := windowsVCLibsDesktopManifestArchitecture(normalizedArchitecture)
	if err != nil {
		return err
	}
	identity := appx.Identity
	if identity.Name != WindowsVCLibsDesktopPackageIdentity ||
		identity.Version != WindowsVCLibsDesktopPackageVersion ||
		identity.Publisher != WindowsVCLibsDesktopPackagePublisher ||
		!strings.EqualFold(identity.ProcessorArchitecture, wantManifestArchitecture) {
		return fmt.Errorf("Windows VCLibs Desktop Appx identity mismatch: name=%q version=%q publisher=%q architecture=%q, want name=%q version=%q publisher=%q architecture=%q",
			identity.Name, identity.Version, identity.Publisher, identity.ProcessorArchitecture,
			WindowsVCLibsDesktopPackageIdentity, WindowsVCLibsDesktopPackageVersion, WindowsVCLibsDesktopPackagePublisher, wantManifestArchitecture)
	}
	for _, name := range windowsVCLibsDesktopRequiredDLLs {
		path := filepath.Join(runtimeRoot, name)
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return fmt.Errorf("inspect required Windows VCLibs Desktop DLL %q: %w", name, securefs.WrapErrorForPath(statErr, path))
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("required Windows VCLibs Desktop DLL is not a non-empty regular file: %q", path)
		}
	}
	return nil
}

// validateWindowsVCLibsDesktopReadyRootAgainstPayload 只读比较 Appx 与 ready tree 的
// 完整目录、文件大小和内容摘要，阻断任一附加 DLL、签名或 BlockMap 被删除/替换。
func validateWindowsVCLibsDesktopReadyRootAgainstPayload(payloadPath, runtimeRoot string) (err error) {
	archiveEntries, err := snapshotWindowsVCLibsDesktopAppx(payloadPath)
	if err != nil {
		return err
	}
	readyEntries, err := snapshotAssetTree(runtimeRoot)
	if err != nil {
		return fmt.Errorf("snapshot Windows VCLibs Desktop ready tree: %w", securefs.WrapErrorForPath(err, runtimeRoot))
	}
	if len(archiveEntries) != len(readyEntries) {
		return fmt.Errorf("Windows VCLibs Desktop ready tree entry count=%d, locked Appx count=%d", len(readyEntries), len(archiveEntries))
	}
	keys := make([]string, 0, len(archiveEntries))
	for name := range archiveEntries {
		keys = append(keys, name)
	}
	slices.Sort(keys)
	for _, name := range keys {
		if readyEntry, ok := readyEntries[name]; !ok || readyEntry != archiveEntries[name] {
			return fmt.Errorf("Windows VCLibs Desktop ready tree differs from locked Appx at %q", name)
		}
	}
	return nil
}

// snapshotWindowsVCLibsDesktopAppx 建立不落盘的完整 Appx 目录/文件摘要清单，
// 并拒绝目录穿越、重复项、符号链接和非普通文件。
func snapshotWindowsVCLibsDesktopAppx(payloadPath string) (entries map[string]assetTreeEntry, err error) {
	archive, err := zip.OpenReader(payloadPath)
	if err != nil {
		return nil, fmt.Errorf("open locked Windows VCLibs Desktop Appx: %w", securefs.WrapErrorForPath(err, payloadPath))
	}
	defer func() {
		if closeErr := archive.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close locked Windows VCLibs Desktop Appx: %w", securefs.WrapErrorForPath(closeErr, payloadPath)))
			entries = nil
		}
	}()
	entries = make(map[string]assetTreeEntry, len(archive.File))
	for _, entry := range archive.File {
		name, normalizeErr := normalizeAssetRelativePath(entry.Name)
		if normalizeErr != nil {
			return nil, fmt.Errorf("%w: normalize Windows VCLibs Desktop Appx entry %q: %w", ErrWindowsUnsafeAssetArchive, entry.Name, normalizeErr)
		}
		parts := strings.Split(name, "/")
		for index := 1; index < len(parts); index++ {
			parent := strings.Join(parts[:index], "/")
			if existing, ok := entries[parent]; ok && existing.kind != "directory" {
				return nil, fmt.Errorf("Windows VCLibs Desktop Appx parent collides with file: %q", parent)
			}
			entries[parent] = assetTreeEntry{kind: "directory"}
		}
		info := entry.FileInfo()
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("Windows VCLibs Desktop Appx contains symlink: %q", name)
		}
		if info.IsDir() {
			if existing, ok := entries[name]; ok && existing.kind != "directory" {
				return nil, fmt.Errorf("Windows VCLibs Desktop Appx directory collides with file: %q", name)
			}
			entries[name] = assetTreeEntry{kind: "directory"}
			continue
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("Windows VCLibs Desktop Appx contains unsupported file type %s at %q", info.Mode(), name)
		}
		if _, duplicate := entries[name]; duplicate {
			return nil, fmt.Errorf("Windows VCLibs Desktop Appx contains duplicate entry: %q", name)
		}
		if entry.UncompressedSize64 > uint64(maxInt64Value) {
			return nil, fmt.Errorf("Windows VCLibs Desktop Appx entry is too large: %q", name)
		}
		stream, openErr := entry.Open()
		if openErr != nil {
			return nil, fmt.Errorf("open Windows VCLibs Desktop Appx entry %q: %w", name, securefs.WrapErrorForPath(openErr, payloadPath))
		}
		hasher := sha256.New()
		_, hashErr := io.Copy(hasher, stream)
		closeErr := stream.Close()
		if hashErr != nil {
			if closeErr != nil {
				return nil, errors.Join(
					fmt.Errorf("hash Windows VCLibs Desktop Appx entry %q: %w", name, securefs.WrapErrorForPath(hashErr, payloadPath)),
					fmt.Errorf("close Windows VCLibs Desktop Appx entry %q: %w", name, securefs.WrapErrorForPath(closeErr, payloadPath)),
				)
			}
			return nil, fmt.Errorf("hash Windows VCLibs Desktop Appx entry %q: %w", name, securefs.WrapErrorForPath(hashErr, payloadPath))
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close Windows VCLibs Desktop Appx entry %q: %w", name, securefs.WrapErrorForPath(closeErr, payloadPath))
		}
		entries[name] = assetTreeEntry{kind: "file", size: int64(entry.UncompressedSize64), hash: hex.EncodeToString(hasher.Sum(nil))}
	}
	return entries, nil
}

func windowsVCLibsDesktopManifestArchitecture(nativeArchitecture string) (string, error) {
	switch nativeArchitecture {
	case WindowsHostArchARM64:
		return "arm64", nil
	case WindowsHostArchX64:
		return "x64", nil
	case WindowsHostArchX86:
		return "x86", nil
	default:
		return "", fmt.Errorf("unsupported Windows VCLibs Desktop architecture %q", nativeArchitecture)
	}
}
