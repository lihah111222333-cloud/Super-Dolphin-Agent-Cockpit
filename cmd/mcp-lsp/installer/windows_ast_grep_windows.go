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
	// WindowsASTGrepVersion 是 Windows sidecar 使用的固定 ast-grep npm 版本。
	WindowsASTGrepVersion = "0.43.0"
	// WindowsASTGrepNativeAssetName 是 native tarball 在产品 LSP asset cache 中的稳定名称。
	WindowsASTGrepNativeAssetName = "ast-grep-native"
	// WindowsASTGrepNativeBinaryPath 是 native tarball 解包后必须存在的相对路径。
	WindowsASTGrepNativeBinaryPath = "package/ast-grep.exe"
)

var ErrWindowsASTGrepBinaryInvalid = errors.New("ast-grep Windows binary failed locked identity validation")

// WindowsASTGrepAssetFact 描述一个 native npm tarball 及其最终 PE 文件的锁定事实。
// WindowsAssetCache 直接按 URL 和 tarball SHA-256 下载、解包并原子发布；readiness
// 再复验落盘 EXE 的 SHA-256 与 PE machine，禁止 npm optional shim 或 PATH 掩盖错误架构。
type WindowsASTGrepAssetFact struct {
	Architecture        string
	NativePackage       string
	NativePackageURL    string
	NativePackageSHA256 string
	ExecutableSHA256    string
	PEMachine           uint16
}

// WindowsASTGrepNativeManifest 返回由固定 native npm tarball 构成的产品 asset manifest。
// 它复用 WindowsAssetCache 的 HTTPS、SHA、归档路径、锁和原子 ready 发布，避免 npm
// optionalDependencies 在 Windows ARM64 上只留下元数据时再落入不可信 PATH。
func WindowsASTGrepNativeManifest() WindowsLockedAssetManifest {
	assets := make(map[string]WindowsLockedAsset, len(WindowsASTGrepAssetFacts()))
	for architecture, fact := range WindowsASTGrepAssetFacts() {
		assets[architecture] = WindowsLockedAsset{
			Architecture: architecture,
			Version:      WindowsASTGrepVersion,
			URL:          fact.NativePackageURL,
			SHA256:       fact.NativePackageSHA256,
			Format:       WindowsLockedAssetFormatTarGz,
			BinaryPath:   WindowsASTGrepNativeBinaryPath,
		}
	}
	return WindowsLockedAssetManifest{Name: WindowsASTGrepNativeAssetName, Assets: assets}
}

// WindowsASTGrepNativeExecutablePath 只计算锁定 native ready 路径，不联网、不写盘。
func WindowsASTGrepNativeExecutablePath(productRoot, architecture string) (string, error) {
	root := strings.TrimSpace(productRoot)
	if root == "" {
		return "", errors.New("ast-grep product root is empty")
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("ast-grep product root must be absolute: %q", productRoot)
	}
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve ast-grep product root: %w", err)
	}
	root = filepath.Clean(resolvedRoot)
	platform := WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: architecture}
	asset, err := WindowsASTGrepAssetForPlatform(platform)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "cache", WindowsLSPAssetCacheSubdir, WindowsASTGrepNativeAssetName,
		WindowsASTGrepVersion, asset.Architecture, strings.ToLower(asset.NativePackageSHA256), "ready",
		filepath.FromSlash(WindowsASTGrepNativeBinaryPath)), nil
}

// EnsureWindowsASTGrepNativeExecutable 按 NativeArch 下载并复验 native ast-grep，
// 使用 WindowsAssetCache 的跨进程锁和原子 ready 树；ProcessArch 不参与选择。
func EnsureWindowsASTGrepNativeExecutable(ctx context.Context, productRoot string, client *http.Client) (string, error) {
	if ctx == nil {
		return "", errors.New("ast-grep asset context is nil")
	}
	cache, err := NewWindowsLSPAssetCache(productRoot, client)
	if err != nil {
		return "", err
	}
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return "", err
	}
	path, err := cache.EnsureForPlatform(ctx, WindowsASTGrepNativeManifest(), platform)
	if err != nil {
		return "", err
	}
	if err := ValidateWindowsASTGrepExecutable(path, platform.NativeArch); err != nil {
		return "", err
	}
	return path, nil
}

// WindowsASTGrepAssetFacts 返回 Windows 原生架构的固定 ast-grep native 包事实。
func WindowsASTGrepAssetFacts() map[string]WindowsASTGrepAssetFact {
	return map[string]WindowsASTGrepAssetFact{
		WindowsHostArchARM64: {
			Architecture:        WindowsHostArchARM64,
			NativePackage:       "@ast-grep/cli-win32-arm64-msvc",
			NativePackageURL:    "https://registry.npmjs.org/@ast-grep/cli-win32-arm64-msvc/-/cli-win32-arm64-msvc-0.43.0.tgz",
			NativePackageSHA256: "dbdbd3fe58f425a87250df7b41ae011cbe0d03dbfba4b2bcf4d84b24eedf1732",
			ExecutableSHA256:    "dca10b8c2079e8cf7c03fdb7735fd1b723a428ef78b3293b4e104c90805f64f0",
			PEMachine:           WindowsImageFileMachineARM64,
		},
		WindowsHostArchX64: {
			Architecture:        WindowsHostArchX64,
			NativePackage:       "@ast-grep/cli-win32-x64-msvc",
			NativePackageURL:    "https://registry.npmjs.org/@ast-grep/cli-win32-x64-msvc/-/cli-win32-x64-msvc-0.43.0.tgz",
			NativePackageSHA256: "1f5653e97e15643e36e14312e8fa79335cc65477a67c86b48a2475b44cfbf694",
			ExecutableSHA256:    "119f1736d0dcf709335631e21062bd770091abda50acf48ab1514294ab5b6b8b",
			PEMachine:           WindowsImageFileMachineAMD64,
		},
	}
}

// WindowsASTGrepAssetForPlatform 只按 NativeArch 选择 ast-grep，不使用 ProcessArch 回退。
func WindowsASTGrepAssetForPlatform(platform WindowsHostPlatform) (WindowsASTGrepAssetFact, error) {
	if !strings.EqualFold(strings.TrimSpace(platform.OS), WindowsHostOSWindows) {
		return WindowsASTGrepAssetFact{}, fmt.Errorf("ast-grep Windows asset requires Windows, got %q", platform.OS)
	}
	architecture, err := NormalizeWindowsArchitectureAlias(platform.NativeArch)
	if err != nil {
		return WindowsASTGrepAssetFact{}, err
	}
	fact, ok := WindowsASTGrepAssetFacts()[architecture]
	if !ok {
		return WindowsASTGrepAssetFact{}, fmt.Errorf("ast-grep has no locked Windows native asset for %q", architecture)
	}
	return fact, nil
}

// ValidateWindowsASTGrepExecutable 验证 native ast-grep 的固定 SHA-256 与 PE machine。
func ValidateWindowsASTGrepExecutable(path, architecture string) error {
	fact, err := WindowsASTGrepAssetForPlatform(WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: architecture})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrWindowsASTGrepBinaryInvalid, err)
	}
	resolved, err := requireWindowsResolverFile(path, ErrWindowsASTGrepBinaryInvalid)
	if err != nil {
		return err
	}
	contents, err := os.ReadFile(resolved)
	if err != nil {
		return fmt.Errorf("read ast-grep executable: %w", err)
	}
	digest := sha256.Sum256(contents)
	actualSHA := hex.EncodeToString(digest[:])
	if !strings.EqualFold(actualSHA, fact.ExecutableSHA256) {
		return fmt.Errorf("%w: executable SHA256=%s want=%s", ErrWindowsASTGrepBinaryInvalid, actualSHA, fact.ExecutableSHA256)
	}
	image, err := pe.NewFile(bytes.NewReader(contents))
	if err != nil {
		return fmt.Errorf("%w: parse PE: %v", ErrWindowsASTGrepBinaryInvalid, err)
	}
	defer image.Close()
	if image.FileHeader.Machine != fact.PEMachine {
		return fmt.Errorf("%w: PE machine=0x%04x want=0x%04x", ErrWindowsASTGrepBinaryInvalid, image.FileHeader.Machine, fact.PEMachine)
	}
	return nil
}
