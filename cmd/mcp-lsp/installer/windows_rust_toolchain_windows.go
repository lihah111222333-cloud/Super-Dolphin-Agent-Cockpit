//go:build windows

package installer

import (
	"context"
	"debug/pe"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	windowsRustToolchainAssetRoot       = "rust-toolchain-bootstrap"
	windowsRustToolchainStateRoot       = "rust-toolchain"
	windowsRustupVersion                = "1.28.2"
	windowsRustToolchainVersion         = "1.96.0"
	windowsRustToolchainInstallLockFile = ".rust-toolchain-install.lock"
)

// WindowsRustToolchainPaths 描述产品私有 Rust 工具链的受管路径。
type WindowsRustToolchainPaths struct {
	CargoHome     string
	RustupHome    string
	CargoPath     string
	RustcPath     string
	toolchainRoot string
	rustStdLibDir string
}

// WindowsRustToolchainBootstrapAssetForPlatform 返回按 NativeArch 锁定的官方 rustup-init 资产。
func WindowsRustToolchainBootstrapAssetForPlatform(platform WindowsHostPlatform) (WindowsLockedAsset, error) {
	assets := map[string]WindowsLockedAsset{
		WindowsHostArchARM64: rustupBootstrapAsset(WindowsHostArchARM64, "aarch64-pc-windows-msvc", "de9f7d29ccd39efa59a3dda3ec363b396e09b92681229b9b8f6aaa4c84285e9c"),
		WindowsHostArchX64:   rustupBootstrapAsset(WindowsHostArchX64, "x86_64-pc-windows-msvc", "88d8258dcf6ae4f7a80c7d1088e1f36fa7025a1cfd1343731b4ee6f385121fc0"),
		WindowsHostArchX86:   rustupBootstrapAsset(WindowsHostArchX86, "i686-pc-windows-msvc", "d33375f474f105e529ff3225529a8d6a79a8a4e23f6eab88fba427889e538f34"),
	}
	return (WindowsLockedAssetManifest{Name: "rustup-init", Assets: assets}).AssetForPlatform(platform)
}

func rustupBootstrapAsset(architecture, target, digest string) WindowsLockedAsset {
	return WindowsLockedAsset{
		Architecture: architecture,
		Version:      windowsRustupVersion,
		URL:          "https://static.rust-lang.org/rustup/archive/" + windowsRustupVersion + "/" + target + "/rustup-init.exe",
		SHA256:       digest,
		Format:       WindowsLockedAssetFormatRaw,
		BinaryPath:   "rustup-init.exe",
	}
}

// EnsureWindowsRustToolchain 安装并复验与 NativeArch 一致的产品私有 Cargo/Rustc 闭包。
func EnsureWindowsRustToolchain(ctx context.Context, productRoot string, client *http.Client) (result WindowsRustToolchainPaths, err error) {
	root, err := resolveWindowsInstallProductRoot(productRoot)
	if err != nil {
		return WindowsRustToolchainPaths{}, fmt.Errorf("resolve Windows Rust toolchain product root: %w", err)
	}
	productRoot = root
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return WindowsRustToolchainPaths{}, err
	}
	installLock, err := acquireWindowsRustToolchainInstallLock(ctx, productRoot, platform.NativeArch)
	if err != nil {
		return WindowsRustToolchainPaths{}, fmt.Errorf("lock product-owned Rust toolchain: %w", err)
	}
	defer func() {
		if closeErr := installLock.Close(); closeErr != nil && err == nil {
			result = WindowsRustToolchainPaths{}
			err = fmt.Errorf("release product-owned Rust toolchain lock: %w", closeErr)
		}
	}()
	if paths, resolveErr := resolveWindowsRustToolchainForPlatform(productRoot, platform); resolveErr == nil {
		return paths, nil
	}
	asset, err := WindowsRustToolchainBootstrapAssetForPlatform(platform)
	if err != nil {
		return WindowsRustToolchainPaths{}, err
	}
	cache, err := NewWindowsAssetCache(filepath.Join(productRoot, "cache", windowsRustToolchainAssetRoot), client)
	if err != nil {
		return WindowsRustToolchainPaths{}, fmt.Errorf("create Rust toolchain bootstrap cache: %w", err)
	}
	bootstrap, err := cache.EnsureForPlatform(ctx, WindowsLockedAssetManifest{Name: "rustup-init", Assets: map[string]WindowsLockedAsset{asset.Architecture: asset}}, platform)
	if err != nil {
		return WindowsRustToolchainPaths{}, fmt.Errorf("materialize Rust toolchain bootstrap: %w", err)
	}
	paths, err := windowsRustToolchainPaths(productRoot, platform.NativeArch)
	if err != nil {
		return WindowsRustToolchainPaths{}, err
	}
	stateRoot := filepath.Dir(paths.CargoHome)
	target, err := windowsRustTargetTriple(platform.NativeArch)
	if err != nil {
		return WindowsRustToolchainPaths{}, err
	}
	command := hiddenexec.CommandContext(ctx, bootstrap,
		"-y",
		"--no-modify-path",
		"--profile", "minimal",
		"--default-host", target,
		"--default-toolchain", windowsRustToolchainVersion+"-"+target,
	)
	command.Dir = stateRoot
	command.Env = WindowsRustToolchainEnvironment(os.Environ(), paths)
	output, err := command.CombinedOutput()
	if err != nil {
		return WindowsRustToolchainPaths{}, fmt.Errorf("install product-owned Rust toolchain: %w: %s", err, truncateRustToolchainOutput(output))
	}
	return resolveWindowsRustToolchainForPlatform(productRoot, platform)
}

// ResolveWindowsRustToolchain 只读解析已经发布的产品私有 Rust 工具链，不查询 PATH、不联网。
func ResolveWindowsRustToolchain(productRoot string) (WindowsRustToolchainPaths, error) {
	root, err := resolveWindowsInstallProductRoot(productRoot)
	if err != nil {
		return WindowsRustToolchainPaths{}, fmt.Errorf("resolve Windows Rust toolchain product root: %w", err)
	}
	productRoot = root
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return WindowsRustToolchainPaths{}, err
	}
	return resolveWindowsRustToolchainForPlatform(productRoot, platform)
}

// acquireWindowsRustToolchainInstallLock 为共享 Rustup/Cargo home 建立产品专属 OS 锁。
func acquireWindowsRustToolchainInstallLock(ctx context.Context, productRoot, architecture string) (*assetOSLock, error) {
	lockPath, err := windowsRustToolchainInstallLockPath(productRoot, architecture)
	if err != nil {
		return nil, err
	}
	stateRoot := filepath.Dir(lockPath)
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create product-owned Rust toolchain root: %w", securefs.WrapErrorForPath(err, stateRoot))
	}
	if err := securefs.RestrictPrivateOwnerOnly(stateRoot, 0o700); err != nil {
		return nil, fmt.Errorf("secure product-owned Rust toolchain root: %w", securefs.WrapErrorForPath(err, stateRoot))
	}
	return acquireAssetOSLock(ctx, lockPath)
}

func windowsRustToolchainInstallLockPath(productRoot, architecture string) (string, error) {
	root, err := resolveWindowsInstallProductRoot(productRoot)
	if err != nil {
		return "", err
	}
	paths, err := windowsRustToolchainPaths(root, architecture)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(paths.CargoHome), windowsRustToolchainInstallLockFile), nil
}

func resolveWindowsRustToolchainForPlatform(productRoot string, platform WindowsHostPlatform) (WindowsRustToolchainPaths, error) {
	paths, err := windowsRustToolchainPaths(productRoot, platform.NativeArch)
	if err != nil {
		return WindowsRustToolchainPaths{}, err
	}
	for _, item := range []struct {
		label string
		path  string
	}{{label: "cargo", path: paths.CargoPath}, {label: "rustc", path: paths.RustcPath}} {
		if err := validateWindowsInstallerExistingFile(item.path); err != nil {
			return WindowsRustToolchainPaths{}, fmt.Errorf("product-owned Rust toolchain %s is not ready: %w", item.label, err)
		}
		if err := validateWindowsRustToolchainPE(item.path, platform.NativeArch); err != nil {
			return WindowsRustToolchainPaths{}, err
		}
	}
	if err := validateWindowsRustStdLib(paths.rustStdLibDir); err != nil {
		return WindowsRustToolchainPaths{}, err
	}
	return paths, nil
}

func windowsRustToolchainPaths(productRoot, architecture string) (WindowsRustToolchainPaths, error) {
	target, err := windowsRustTargetTriple(architecture)
	if err != nil {
		return WindowsRustToolchainPaths{}, err
	}
	stateRoot := filepath.Join(productRoot, "cache", windowsRustToolchainStateRoot, windowsRustToolchainVersion, architecture)
	cargoHome := filepath.Join(stateRoot, "cargo-home")
	rustupHome := filepath.Join(stateRoot, "rustup-home")
	toolchainRoot := filepath.Join(rustupHome, "toolchains", windowsRustToolchainVersion+"-"+target)
	binDir := filepath.Join(toolchainRoot, "bin")
	return WindowsRustToolchainPaths{
		CargoHome:     cargoHome,
		RustupHome:    rustupHome,
		CargoPath:     filepath.Join(binDir, "cargo.exe"),
		RustcPath:     filepath.Join(binDir, "rustc.exe"),
		toolchainRoot: toolchainRoot,
		rustStdLibDir: filepath.Join(toolchainRoot, "lib", "rustlib", target, "lib"),
	}, nil
}

func windowsRustTargetTriple(architecture string) (string, error) {
	switch architecture {
	case WindowsHostArchARM64:
		return "aarch64-pc-windows-msvc", nil
	case WindowsHostArchX64:
		return "x86_64-pc-windows-msvc", nil
	case WindowsHostArchX86:
		return "i686-pc-windows-msvc", nil
	default:
		return "", fmt.Errorf("unsupported Windows Rust toolchain architecture: %q", architecture)
	}
}

// WindowsRustToolchainEnvironment 绑定 Rustup/Cargo home 并把受管工具链前置到 PATH。
func WindowsRustToolchainEnvironment(base []string, paths WindowsRustToolchainPaths) []string {
	result := append([]string(nil), base...)
	binDir := filepath.Dir(paths.CargoPath)
	pathValue := windowsRustToolchainEnvironmentValue(result, "PATH")
	if pathValue != "" {
		binDir += string(os.PathListSeparator) + pathValue
	}
	result = replaceWindowsRustToolchainEnvironment(result, "PATH", binDir)
	result = replaceWindowsRustToolchainEnvironment(result, "CARGO_HOME", paths.CargoHome)
	result = replaceWindowsRustToolchainEnvironment(result, "RUSTUP_HOME", paths.RustupHome)
	result = replaceWindowsRustToolchainEnvironment(result, "RUSTUP_DIST_SERVER", "https://static.rust-lang.org")
	return replaceWindowsRustToolchainEnvironment(result, "RUSTUP_UPDATE_ROOT", "https://static.rust-lang.org/rustup")
}

func replaceWindowsRustToolchainEnvironment(env []string, key, value string) []string {
	prefix := strings.ToLower(key) + "="
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(strings.ToLower(entry), prefix) {
			result = append(result, entry)
		}
	}
	return append(result, key+"="+value)
}

func windowsRustToolchainEnvironmentValue(env []string, key string) string {
	prefix := strings.ToLower(key) + "="
	for index := len(env) - 1; index >= 0; index-- {
		if strings.HasPrefix(strings.ToLower(env[index]), prefix) {
			return env[index][len(key)+1:]
		}
	}
	return ""
}

func validateWindowsRustStdLib(stdLibDir string) error {
	entries, err := os.ReadDir(stdLibDir)
	if err != nil {
		return fmt.Errorf("product-owned Rust std library is not ready: %w", err)
	}
	found := false
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "libstd-") || !strings.HasSuffix(name, ".rlib") {
			continue
		}
		path := filepath.Join(stdLibDir, name)
		if err := validateWindowsInstallerExistingFile(path); err != nil {
			return fmt.Errorf("product-owned Rust std library %s is not ready: %w", name, err)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("read product-owned Rust std library %s: %w", name, err)
		}
		if info.Size() == 0 {
			return fmt.Errorf("product-owned Rust std library %s is empty", name)
		}
		found = true
	}
	if !found {
		return errors.New("product-owned Rust std library is not ready: missing libstd-*.rlib")
	}
	return nil
}

func validateWindowsRustToolchainPE(path, architecture string) error {
	file, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("read product-owned Rust toolchain PE %s: %w", filepath.Base(path), err)
	}
	machine := file.FileHeader.Machine
	_ = file.Close()
	want := map[string]uint16{WindowsHostArchARM64: WindowsImageFileMachineARM64, WindowsHostArchX64: WindowsImageFileMachineAMD64, WindowsHostArchX86: WindowsImageFileMachineI386}[architecture]
	if want == 0 || machine != want {
		return fmt.Errorf("product-owned Rust toolchain PE machine mismatch: file=%s want_arch=%q want=0x%04x got=0x%04x", filepath.Base(path), architecture, want, machine)
	}
	return nil
}

func truncateRustToolchainOutput(output []byte) string {
	const limit = 2048
	text := strings.TrimSpace(string(output))
	if len(text) <= limit {
		return text
	}
	return text[len(text)-limit:]
}
