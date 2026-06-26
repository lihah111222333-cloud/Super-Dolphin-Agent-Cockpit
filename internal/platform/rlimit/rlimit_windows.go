//go:build windows

package rlimit

// Init 在 Windows 上不调整 NOFILE；系统没有 Unix rlimit 等价入口。
func Init() {}
