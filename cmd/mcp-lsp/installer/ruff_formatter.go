package installer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// RuffFormatterVersion 是产品锁定的 Astral Ruff formatter 版本。
const RuffFormatterVersion = "0.15.10"

// RuffFormatterAsset 描述一个按目标平台固定的 Ruff 发布资产。
type RuffFormatterAsset struct {
	GOOS, GOARCH                    string
	URL, SHA256, Format, BinaryPath string
}

// RuffFormatterAssetFor 返回目标平台的固定 Ruff 资产；不支持的平台显式失败。
func RuffFormatterAssetFor(goos, goarch string) (RuffFormatterAsset, error) {
	base := "https://github.com/astral-sh/ruff/releases/download/" + RuffFormatterVersion + "/ruff-"
	assets := map[string]RuffFormatterAsset{
		"windows/arm64": {goos, goarch, base + "aarch64-pc-windows-msvc.zip", "1776bf104277b3fbb3b3e4b481655f492f6df10210e2e00cd94132e66e999bd4", NativeArtifactFormatZip, "ruff.exe"},
		"windows/amd64": {goos, goarch, base + "x86_64-pc-windows-msvc.zip", "6f8f9a445102107ee3c0a05c8f386bacb32238199ecbc0983b9b06c5ea3d7c5e", NativeArtifactFormatZip, "ruff.exe"},
		"windows/386":   {goos, goarch, base + "i686-pc-windows-msvc.zip", "a8b4132914f197d1fef5f48fd7f0f8e840546a814daf9f680109344407da79ac", NativeArtifactFormatZip, "ruff.exe"},
		"linux/arm64":   {goos, goarch, base + "aarch64-unknown-linux-gnu.tar.gz", "b775a5a09484549ac3fd377b5ce34955cf633165169671d1c4a215c113ce15df", NativeArtifactFormatTarGz, "ruff-aarch64-unknown-linux-gnu/ruff"},
		"linux/amd64":   {goos, goarch, base + "x86_64-unknown-linux-gnu.tar.gz", "e3e9e5c791542f00d95edc74a506e1ac24efc0af9574de01ab338187bf1ff9f6", NativeArtifactFormatTarGz, "ruff-x86_64-unknown-linux-gnu/ruff"},
		"linux/386":     {goos, goarch, base + "i686-unknown-linux-gnu.tar.gz", "6f9b23d07d90ef3ac148c8b81fc8ea37647f1241e4db18be1b0a24df43d479f8", NativeArtifactFormatTarGz, "ruff-i686-unknown-linux-gnu/ruff"},
		"darwin/arm64":  {goos, goarch, base + "aarch64-apple-darwin.tar.gz", "77c1df502dcfaaec52c6ce203b504b8554c88ab66ac01313410fa68ad9aafd5b", NativeArtifactFormatTarGz, "ruff-aarch64-apple-darwin/ruff"},
		"darwin/amd64":  {goos, goarch, base + "x86_64-apple-darwin.tar.gz", "7210e06196de876771cc0bad0f1d57678e709d039f184b491fdaa600d6a95a5e", NativeArtifactFormatTarGz, "ruff-x86_64-apple-darwin/ruff"},
	}
	asset, ok := assets[goos+"/"+goarch]
	if !ok {
		return RuffFormatterAsset{}, fmt.Errorf("Ruff formatter has no pinned asset for %s/%s", goos, goarch)
	}
	return asset, nil
}

// ResolveOrInstallRuffFormatter 返回产品缓存中的 Ruff launcher，缺失时校验下载并原子安装。
func ResolveOrInstallRuffFormatter(ctx context.Context, productRoot string) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("Ruff formatter install context is nil")
	}
	if filepath.IsAbs(productRoot) == false || productRoot == "" {
		return "", fmt.Errorf("Ruff formatter product root must be absolute")
	}
	asset, err := RuffFormatterAssetFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	root := filepath.Join(productRoot, "cache", "lsp-formatters")
	inst, err := NewNativeArtifactInstaller(NativeArtifactInstallerConfig{InstallRoot: root})
	if err != nil {
		return "", fmt.Errorf("create Ruff formatter installer: %w", err)
	}
	result, err := inst.InstallArtifact(ctx, NativeArtifactSpec{Name: "ruff", Version: RuffFormatterVersion, URL: asset.URL, SHA256: asset.SHA256, Format: asset.Format, BinaryPath: asset.BinaryPath, LauncherName: "ruff"})
	if err != nil {
		return "", fmt.Errorf("install Ruff formatter: %w", err)
	}
	if _, err := os.Stat(result.BinaryPath); err != nil {
		return "", fmt.Errorf("Ruff formatter binary is not ready: %w", err)
	}
	return result.BinaryPath, nil
}
