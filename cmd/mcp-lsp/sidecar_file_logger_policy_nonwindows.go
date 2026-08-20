//go:build !windows

package main

// sidecarFileLoggerCanDeferPermissionError 保持非 Windows 既有的启动期 fail-fast 行为。
func sidecarFileLoggerCanDeferPermissionError(error) bool { return false }
