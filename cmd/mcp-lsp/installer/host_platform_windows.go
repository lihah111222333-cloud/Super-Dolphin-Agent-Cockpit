//go:build windows

package installer

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// detectWindowsHostPlatform 通过 Windows 原生 API 读取宿主系统、NativeArch、进程架构、版本和 build；
// 失败时返回 typed error，不使用 PATH、环境变量或模拟值兜底。
func detectWindowsHostPlatform() (WindowsHostPlatform, error) {
	processArch, nativeArch, err := detectWindowsArchitectures()
	if err != nil {
		return WindowsHostPlatform{}, err
	}
	version, build, err := detectWindowsVersion()
	if err != nil {
		return WindowsHostPlatform{}, err
	}
	return WindowsHostPlatform{
		OS:             WindowsHostOSWindows,
		Arch:           nativeArch,
		NativeArch:     nativeArch,
		ProcessArch:    processArch,
		WindowsVersion: version,
		WindowsBuild:   build,
	}, nil
}

// detectWindowsArchitectures 使用 IsWow64Process2 区分当前进程架构和 Windows 原生架构；
// NativeArch 只由 nativeMachine 决定，不能用 ProcessArch 替代。
func detectWindowsArchitectures() (processArch string, nativeArch string, err error) {
	var processMachine uint16
	var nativeMachine uint16
	if err := windows.IsWow64Process2(windows.CurrentProcess(), &processMachine, &nativeMachine); err != nil {
		return "", "", fmt.Errorf("detect Windows architecture with IsWow64Process2: %w", err)
	}
	nativeArch, err = NormalizeWindowsImageFileMachine(nativeMachine)
	if err != nil {
		return "", "", fmt.Errorf("normalize native Windows machine 0x%04x: %w", nativeMachine, err)
	}
	if processMachine == WindowsImageFileMachineUnknown {
		return nativeArch, nativeArch, nil
	}
	processArch, err = NormalizeWindowsImageFileMachine(processMachine)
	if err != nil {
		return "", "", fmt.Errorf("normalize process Windows machine 0x%04x: %w", processMachine, err)
	}
	return processArch, nativeArch, nil
}

// detectWindowsVersion 使用 RtlGetVersion 读取 Windows 原生版本和 build；任何缺失事实都阻断安装。
func detectWindowsVersion() (string, uint32, error) {
	info := windows.RtlGetVersion()
	if info == nil {
		return "", 0, fmt.Errorf("%w: Windows version API returned nil", ErrUnsupportedWindowsHostPlatform)
	}
	if info.MajorVersion == 0 && info.MinorVersion == 0 {
		return "", 0, fmt.Errorf("%w: Windows version is empty", ErrUnsupportedWindowsHostPlatform)
	}
	if info.BuildNumber == 0 {
		return "", 0, fmt.Errorf("%w: Windows build is empty", ErrUnsupportedWindowsHostPlatform)
	}
	return fmt.Sprintf("%d.%d", info.MajorVersion, info.MinorVersion), info.BuildNumber, nil
}
