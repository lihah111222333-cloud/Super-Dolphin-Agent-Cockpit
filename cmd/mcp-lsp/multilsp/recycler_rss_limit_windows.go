//go:build windows

package multilsp

// hostDefaultGoRSSLimitBytes 是 Windows 构建的 gopls Job 单进程树默认 RSS 上限。
const hostDefaultGoRSSLimitBytes = defaultGoWindowsRSSLimitBytes
