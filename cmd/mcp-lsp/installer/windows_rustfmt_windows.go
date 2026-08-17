//go:build windows

package installer

import (
	"context"
	"debug/pe"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
)

const (
	windowsRustfmtProductRoot = "rustfmt-assets"
	windowsRustfmtVersion     = "1.96.0"
	windowsRustfmtCargoBinary = "cargo-fmt.exe"
)

// WindowsRustfmtAssetForPlatform 返回 Rust 官方 rustfmt companion 的锁定资产。
// rustfmt 不是 LSP server；它只供产品自有 rust-analyzer 的 format 请求使用，严格按 NativeArch 选源。
func WindowsRustfmtAssetForPlatform(platform WindowsHostPlatform) (WindowsLockedAsset, error) {
	manifest := WindowsLockedAssetManifest{Name: "rustfmt", Assets: map[string]WindowsLockedAsset{
		WindowsHostArchARM64: catalogAsset(WindowsHostArchARM64, windowsRustfmtVersion, "https://static.rust-lang.org/dist/2026-05-28/rustfmt-1.96.0-aarch64-pc-windows-msvc.tar.xz", "d9e403d778e0ad95d814275b1265057478d4cde463d8bf620846056a7f00a59d", WindowsLockedAssetFormatTarXz, "rustfmt-1.96.0-aarch64-pc-windows-msvc/rustfmt-preview/bin/rustfmt.exe"),
		WindowsHostArchX64:   catalogAsset(WindowsHostArchX64, windowsRustfmtVersion, "https://static.rust-lang.org/dist/2026-05-28/rustfmt-1.96.0-x86_64-pc-windows-msvc.tar.xz", "7ae6d141dfb844355c4756a41f39ed45b74ff9295fff86bd0bf9b559a83c5d5d", WindowsLockedAssetFormatTarXz, "rustfmt-1.96.0-x86_64-pc-windows-msvc/rustfmt-preview/bin/rustfmt.exe"),
		WindowsHostArchX86:   catalogAsset(WindowsHostArchX86, windowsRustfmtVersion, "https://static.rust-lang.org/dist/2026-05-28/rustfmt-1.96.0-i686-pc-windows-msvc.tar.xz", "75a69f518db96b5c46fa4b98d169688e7670c8bff29b7f1831f6dcfdfc6311ab", WindowsLockedAssetFormatTarXz, "rustfmt-1.96.0-i686-pc-windows-msvc/rustfmt-preview/bin/rustfmt.exe"),
	}}
	return manifest.AssetForPlatform(platform)
}

// EnsureWindowsRustfmt 物化产品私有 rustfmt/cargo-fmt companion；不查询外部 PATH、不注册为 LSP。
func EnsureWindowsRustfmt(ctx context.Context, productRoot string, client *http.Client) (string, error) {
	if productRoot == "" {
		return "", errors.New("Rustfmt product root is empty")
	}
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return "", err
	}
	asset, err := WindowsRustfmtAssetForPlatform(platform)
	if err != nil {
		return "", err
	}
	cache, err := NewWindowsAssetCache(filepath.Join(productRoot, "cache", windowsRustfmtProductRoot), client)
	if err != nil {
		return "", fmt.Errorf("create Rustfmt companion cache: %w", err)
	}
	manifest := WindowsLockedAssetManifest{Name: "rustfmt", Assets: map[string]WindowsLockedAsset{asset.Architecture: asset}}
	path, err := cache.EnsureForPlatform(ctx, manifest, platform)
	if err != nil {
		return "", fmt.Errorf("materialize Rustfmt companion: %w", err)
	}
	if err := ValidateWindowsRustfmtTools(path, platform.NativeArch); err != nil {
		return "", err
	}
	return path, nil
}

// ResolveWindowsRustfmtPath 只读解析 ready 中的 product-private rustfmt，不联网、不创建目录、不查 PATH。
func ResolveWindowsRustfmtPath(productRoot string) (string, error) {
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return "", err
	}
	asset, err := WindowsRustfmtAssetForPlatform(platform)
	if err != nil {
		return "", err
	}
	assetDir := filepath.Join(productRoot, "cache", windowsRustfmtProductRoot, cacheSegment("rustfmt"), cacheSegment(asset.Version), asset.Architecture, asset.SHA256)
	path := filepath.Join(assetDir, "ready", filepath.FromSlash(asset.BinaryPath))
	if err := validateWindowsInstallerExistingFile(path); err != nil {
		return "", fmt.Errorf("Rustfmt companion is not ready: %w", err)
	}
	if err := ValidateWindowsRustfmtTools(path, platform.NativeArch); err != nil {
		return "", err
	}
	return path, nil
}

// WindowsRustfmtBinDir 返回已校验 rustfmt 与 cargo-fmt 所在的私有 bin 目录。
func WindowsRustfmtBinDir(rustfmtPath string) string { return filepath.Dir(rustfmtPath) }

// ValidateWindowsRustfmtTools 校验 rustfmt 和 cargo-fmt 均为目标 NativeArch 的 PE 文件。
func ValidateWindowsRustfmtTools(rustfmtPath, architecture string) error {
	if err := validateWindowsInstallerExistingFile(rustfmtPath); err != nil {
		return fmt.Errorf("Rustfmt executable is invalid: %w", err)
	}
	cargoFmtPath := filepath.Join(filepath.Dir(rustfmtPath), windowsRustfmtCargoBinary)
	for _, path := range []string{rustfmtPath, cargoFmtPath} {
		file, err := pe.Open(path)
		if err != nil {
			return fmt.Errorf("read Rustfmt PE %s: %w", filepath.Base(path), err)
		}
		machine := file.FileHeader.Machine
		_ = file.Close()
		want := map[string]uint16{WindowsHostArchARM64: WindowsImageFileMachineARM64, WindowsHostArchX64: WindowsImageFileMachineAMD64, WindowsHostArchX86: WindowsImageFileMachineI386}[architecture]
		if want == 0 || machine != want {
			return fmt.Errorf("Rustfmt PE machine mismatch: file=%s want_arch=%q want=0x%04x got=0x%04x", filepath.Base(path), architecture, want, machine)
		}
	}
	return nil
}
