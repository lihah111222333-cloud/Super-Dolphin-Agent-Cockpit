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

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

const (
	// WindowsSqruffVersion 是 Windows 产品私有 sqruff crate 的锁定版本。
	WindowsSqruffVersion         = "0.38.0"
	windowsSqruffInstallLockFile = ".sqruff-install.lock"
	windowsSqruffBinaryName      = "sqruff.exe"
)

// EnsureWindowsSqruff 使用产品私有 Rust/Cargo 在 Windows 缓存中物化 sqruff。
// 它不查询宿主 PATH、Python 或 Cargo；缓存未就绪时只由本次安装动作联网并写盘。
func EnsureWindowsSqruff(ctx context.Context, productRoot string, client *http.Client) (path string, err error) {
	if ctx == nil {
		return "", errors.New("Windows sqruff install context is nil")
	}
	root, err := resolveWindowsInstallProductRoot(productRoot)
	if err != nil {
		return "", fmt.Errorf("resolve Windows sqruff product root: %w", err)
	}
	productRoot = root
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return "", err
	}
	target, err := windowsRustTargetTriple(platform.NativeArch)
	if err != nil {
		return "", fmt.Errorf("resolve Windows sqruff Cargo target: %w", err)
	}
	rust, err := EnsureWindowsRustToolchain(ctx, productRoot, client)
	if err != nil {
		return "", fmt.Errorf("ensure product-owned Rust toolchain for sqruff: %w", err)
	}
	if err := os.MkdirAll(rust.CargoHome, 0o700); err != nil {
		return "", fmt.Errorf("create product-owned Cargo home for sqruff: %w", err)
	}
	lock, err := acquireAssetOSLock(ctx, filepath.Join(rust.CargoHome, windowsSqruffInstallLockFile))
	if err != nil {
		return "", fmt.Errorf("lock product-owned sqruff Cargo home: %w", err)
	}
	defer func() {
		if closeErr := lock.Close(); err == nil && closeErr != nil {
			path = ""
			err = fmt.Errorf("release product-owned sqruff Cargo home lock: %w", closeErr)
		}
	}()
	if path, err := resolveWindowsSqruffForPlatform(rust, platform); err == nil {
		return path, nil
	}

	args := windowsSqruffCargoInstallArgs(rust.CargoHome, target)
	command := hiddenexec.CommandContext(ctx, rust.CargoPath, args...)
	command.Dir = rust.CargoHome
	command.Env = WindowsRustToolchainEnvironment(os.Environ(), rust)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", newProcessFailureError(
			"windows-sqruff-cargo-install",
			"sqruff",
			joinProcessFailureCause(ctx.Err(), err),
			output,
			len(args),
			1,
		)
	}
	path, err = resolveWindowsSqruffForPlatform(rust, platform)
	if err != nil {
		return "", fmt.Errorf("resolve product-owned sqruff after Cargo install: %w", err)
	}
	return path, nil
}

// ResolveWindowsSqruff 只读解析产品私有缓存中的原生 sqruff，不联网、不写盘、不查询 PATH。
func ResolveWindowsSqruff(productRoot string) (string, error) {
	root, err := resolveWindowsInstallProductRoot(productRoot)
	if err != nil {
		return "", fmt.Errorf("resolve Windows sqruff product root: %w", err)
	}
	productRoot = root
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		return "", err
	}
	rust, err := ResolveWindowsRustToolchain(productRoot)
	if err != nil {
		return "", fmt.Errorf("resolve product-owned Rust toolchain for sqruff: %w", err)
	}
	return resolveWindowsSqruffForPlatform(rust, platform)
}

func resolveWindowsSqruffForPlatform(rust WindowsRustToolchainPaths, platform WindowsHostPlatform) (string, error) {
	path := windowsSqruffBinaryPath(rust)
	if err := validateWindowsInstallerExistingFile(path); err != nil {
		return "", fmt.Errorf("product-owned sqruff is not ready: %w", err)
	}
	if err := validateWindowsSqruffExecutable(path, platform.NativeArch); err != nil {
		return "", err
	}
	return filepath.Clean(path), nil
}

func windowsSqruffBinaryPath(rust WindowsRustToolchainPaths) string {
	return filepath.Join(rust.CargoHome, "bin", windowsSqruffBinaryName)
}

func windowsSqruffCargoInstallArgs(cargoHome, target string) []string {
	return []string{"install", "sqruff", "--version", WindowsSqruffVersion, "--locked", "--root", cargoHome, "--target", target}
}

func validateWindowsSqruffExecutable(path, architecture string) error {
	file, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("read product-owned sqruff PE: %w", err)
	}
	machine := file.FileHeader.Machine
	if closeErr := file.Close(); closeErr != nil {
		return fmt.Errorf("close product-owned sqruff PE: %w", closeErr)
	}
	want := map[string]uint16{
		WindowsHostArchARM64: WindowsImageFileMachineARM64,
		WindowsHostArchX64:   WindowsImageFileMachineAMD64,
		WindowsHostArchX86:   WindowsImageFileMachineI386,
	}[architecture]
	if want == 0 {
		return fmt.Errorf("unsupported Windows sqruff architecture: %q", architecture)
	}
	if machine != want {
		return fmt.Errorf("product-owned sqruff PE machine mismatch: want_arch=%q want=0x%04x got=0x%04x", architecture, want, machine)
	}
	return nil
}
