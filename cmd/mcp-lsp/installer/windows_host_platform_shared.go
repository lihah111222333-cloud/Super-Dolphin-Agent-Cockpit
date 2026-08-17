package installer

// 本文件故意不加 windows build tag：宿主事实 DTO、架构别名与 PE 常量是
// 跨平台契约；真实 Win32 探测与非 Windows fail-fast 实现分别位于带标签文件。

import (
	"errors"
	"fmt"
	"strings"
)

const (
	// WindowsHostOSWindows 是 Windows 宿主系统标识；安装桥只接受该值。
	WindowsHostOSWindows = "windows"
	// WindowsHostArchARM64 是 Windows 原生 ARM64 catalog 资产选择键。
	WindowsHostArchARM64 = "arm64"
	// WindowsHostArchX64 是 Windows 原生 AMD64/x86-64 catalog 资产选择键。
	WindowsHostArchX64 = "x64"
	// WindowsHostArchX86 是 Windows 原生 32 位 x86 catalog 资产选择键。
	WindowsHostArchX86 = "x86"
)

const (
	// WindowsImageFileMachineUnknown 是 Windows PE 未声明进程机器类型的值。
	WindowsImageFileMachineUnknown uint16 = 0x0000
	// WindowsImageFileMachineI386 是 IMAGE_FILE_MACHINE_I386 的值。
	WindowsImageFileMachineI386 uint16 = 0x014c
	// WindowsImageFileMachineAMD64 是 IMAGE_FILE_MACHINE_AMD64 的值。
	WindowsImageFileMachineAMD64 uint16 = 0x8664
	// WindowsImageFileMachineARM64 是 IMAGE_FILE_MACHINE_ARM64 的值。
	WindowsImageFileMachineARM64 uint16 = 0xaa64
)

var (
	// ErrUnsupportedWindowsHostPlatform 表示当前构建目标没有 Windows 宿主探测实现，或原生版本事实缺失。
	ErrUnsupportedWindowsHostPlatform = errors.New("unsupported Windows host platform")
	// ErrUnsupportedWindowsHostArchitecture 表示 Windows 原生 API 返回了未锁定的机器架构。
	ErrUnsupportedWindowsHostArchitecture = errors.New("unsupported Windows host architecture")
)

// WindowsHostPlatform 描述 Windows 安装桥依赖的原生宿主事实；它只能由 Windows API 或受控矩阵测试提供。
type WindowsHostPlatform struct {
	// OS 是宿主系统标识，生产安装必须等于 WindowsHostOSWindows。
	OS string
	// Arch 是兼容字段，Windows 生产实现将其保持为 NativeArch。
	Arch string
	// NativeArch 是 Windows 原生架构，只能是 arm64、x64 或 x86，并决定 catalog 资产选择。
	NativeArch string
	// ProcessArch 是当前进程架构，仅用于诊断，不能选择或回退 catalog 资产。
	ProcessArch string
	// WindowsVersion 是 Windows 原生版本字符串，例如 10.0；缺失时必须 fail-fast。
	WindowsVersion string
	// WindowsBuild 是 Windows 原生 build 号；缺失或未满足 catalog 门槛时必须 fail-fast。
	WindowsBuild uint32
}

// DetectWindowsHostPlatform 读取 Windows 原生宿主事实；非 Windows 直接返回 typed unsupported 错误。
func DetectWindowsHostPlatform() (WindowsHostPlatform, error) {
	return detectWindowsHostPlatform()
}

// NormalizeWindowsArchitectureAlias 将 Windows 架构别名规范化为精确的 arm64、x64 或 x86；未知值直接失败。
func NormalizeWindowsArchitectureAlias(alias string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(alias)) {
	case "arm64", "aarch64", "armv8", "arm64-v8a":
		return WindowsHostArchARM64, nil
	case "amd64", "x64", "x86_64", "x86-64":
		return WindowsHostArchX64, nil
	case "386", "x86", "i386", "i486", "i586", "i686", "x86-32", "ia32":
		return WindowsHostArchX86, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedWindowsHostArchitecture, alias)
	}
}

// NormalizeWindowsImageFileMachine 将 Windows PE IMAGE_FILE_MACHINE 值规范化为精确架构；未知值直接失败。
func NormalizeWindowsImageFileMachine(machine uint16) (string, error) {
	switch machine {
	case WindowsImageFileMachineARM64:
		return WindowsHostArchARM64, nil
	case WindowsImageFileMachineAMD64:
		return WindowsHostArchX64, nil
	case WindowsImageFileMachineI386:
		return WindowsHostArchX86, nil
	case WindowsImageFileMachineUnknown:
		return "", fmt.Errorf("%w: IMAGE_FILE_MACHINE_UNKNOWN", ErrUnsupportedWindowsHostArchitecture)
	default:
		return "", fmt.Errorf("%w: IMAGE_FILE_MACHINE_0x%04x", ErrUnsupportedWindowsHostArchitecture, machine)
	}
}
