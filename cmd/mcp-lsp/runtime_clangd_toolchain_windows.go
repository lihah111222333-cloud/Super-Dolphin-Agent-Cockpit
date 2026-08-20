//go:build windows

package main

import (
	"debug/pe"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

var runtimeServerWindowsClangdDriverNames = []string{"clang++.exe", "clang-cl.exe", "clang.exe"}

// runtimeServerWindowsClangdArguments 为产品自带的 clangd 放行同一 LLVM 目录内的编译器驱动。
// clangd 只有在 query-driver 白名单命中时才会读取 Windows SDK/MSVC 的系统头路径；外部
// clangd 不属于产品缓存，必须保持调用方参数不变。
func runtimeServerWindowsClangdArguments(command multilsp.ServerCommand, binary string) ([]string, error) {
	args := slices.Clone(command.Args)
	if !strings.EqualFold(windowsRuntimeExecutableStem(command.Executable), "clangd") || hasWindowsClangdQueryDriver(args) {
		return args, nil
	}
	drivers, owned, err := runtimeServerWindowsClangdDrivers(binary)
	if err != nil {
		return nil, err
	}
	if !owned {
		return args, nil
	}
	return append(args, "--query-driver="+strings.Join(drivers, ",")), nil
}

// runtimeServerWindowsClangdEnvironment 把产品 LLVM bin 目录置于 clangd 子进程 PATH 首位，
// 使 compile_commands.json 中的 clang++/clang-cl 能解析到与 clangd 同架构的驱动。
func runtimeServerWindowsClangdEnvironment(serverBinary string, env []string) ([]string, error) {
	drivers, owned, err := runtimeServerWindowsClangdDrivers(serverBinary)
	if err != nil {
		return nil, err
	}
	if !owned {
		return append([]string(nil), env...), nil
	}
	driverDir := filepath.Dir(drivers[0])
	pathValue := runtimeServerWindowsEnvironmentValue(env, "PATH")
	if pathValue == "" {
		pathValue = os.Getenv("PATH")
	}
	if pathValue != "" {
		driverDir += string(os.PathListSeparator) + pathValue
	}
	return replaceRuntimeServerWindowsEnvironment(env, "PATH", driverDir), nil
}

func runtimeServerWindowsClangdDrivers(serverBinary string) ([]string, bool, error) {
	productRoot, owned, err := runtimeServerWindowsOwnedProductRoot(serverBinary)
	if err != nil {
		return nil, false, err
	}
	if !owned || !strings.EqualFold(filepath.Base(filepath.Clean(serverBinary)), "clangd.exe") {
		return nil, false, nil
	}
	platform, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		return nil, false, fmt.Errorf("detect Windows native architecture for product clangd: %w", err)
	}
	if platform.OS != installer.WindowsHostOSWindows {
		return nil, false, fmt.Errorf("product clangd requires Windows host, got %q", platform.OS)
	}
	asset, err := installer.WindowsLSPAssetForPlatform(installer.WindowsLSPProductClangd, platform)
	if err != nil {
		return nil, false, fmt.Errorf("validate product clangd asset for native architecture %q: %w", platform.NativeArch, err)
	}
	readyRoot := filepath.Join(productRoot, "cache", installer.WindowsLSPAssetCacheSubdir, string(installer.WindowsLSPProductClangd), asset.Version, asset.Architecture, strings.ToLower(asset.SHA256), "ready")
	wantBinary := filepath.Join(readyRoot, filepath.FromSlash(asset.BinaryPath))
	if !strings.EqualFold(filepath.Clean(serverBinary), filepath.Clean(wantBinary)) {
		return nil, false, fmt.Errorf("product clangd binary does not match native %q asset: got=%s want=%s", platform.NativeArch, securefs.RedactPath(serverBinary), securefs.RedactPath(wantBinary))
	}
	if _, err := installer.WindowsShortProcessPathWithinRoot(productRoot, serverBinary); err != nil {
		return nil, false, fmt.Errorf("validate product clangd binary: %w", err)
	}
	driverDir := filepath.Dir(wantBinary)
	relativeDriverDir, err := filepath.Rel(readyRoot, driverDir)
	if err != nil || filepath.IsAbs(relativeDriverDir) || relativeDriverDir == ".." || strings.HasPrefix(relativeDriverDir, ".."+string(filepath.Separator)) {
		return nil, false, fmt.Errorf("product clangd compiler driver directory escaped locked ready tree: %s", securefs.RedactPath(driverDir))
	}
	drivers := make([]string, 0, len(runtimeServerWindowsClangdDriverNames))
	for _, name := range runtimeServerWindowsClangdDriverNames {
		driverPath := filepath.Join(driverDir, name)
		info, statErr := os.Lstat(driverPath)
		if statErr != nil {
			return nil, false, fmt.Errorf("product clangd compiler driver %q is unavailable: %w", name, statErr)
		}
		if !info.Mode().IsRegular() {
			return nil, false, fmt.Errorf("product clangd compiler driver %q is not a regular file", name)
		}
		if _, err := installer.WindowsShortProcessPathWithinRoot(productRoot, driverPath); err != nil {
			return nil, false, fmt.Errorf("validate product clangd compiler driver %q: %w", name, err)
		}
		if err := validateRuntimeServerWindowsClangdDriverPE(driverPath, platform.NativeArch); err != nil {
			return nil, false, fmt.Errorf("validate product clangd compiler driver %q: %w", name, err)
		}
		drivers = append(drivers, driverPath)
	}
	return drivers, true, nil
}

// validateRuntimeServerWindowsClangdDriverPE 校验 query-driver 只能指向 ready 树内、与宿主 NativeArch 一致的 PE 文件。
func validateRuntimeServerWindowsClangdDriverPE(path, nativeArch string) error {
	image, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("read PE: %w", err)
	}
	machine := image.FileHeader.Machine
	if closeErr := image.Close(); closeErr != nil {
		return fmt.Errorf("close PE: %w", closeErr)
	}
	architecture, err := installer.NormalizeWindowsImageFileMachine(machine)
	if err != nil {
		return fmt.Errorf("normalize PE machine 0x%04x: %w", machine, err)
	}
	if architecture != nativeArch {
		return fmt.Errorf("PE machine mismatch: want native architecture %q, got %q (machine 0x%04x)", nativeArch, architecture, machine)
	}
	return nil
}

func hasWindowsClangdQueryDriver(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(arg)), "--query-driver=") {
			return true
		}
	}
	return false
}
