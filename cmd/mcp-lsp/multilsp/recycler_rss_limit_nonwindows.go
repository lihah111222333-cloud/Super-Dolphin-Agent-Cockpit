//go:build !windows

package multilsp

// hostDefaultGoRSSLimitBytes 是非 Windows 构建的 gopls 单进程树默认 RSS 上限。
const hostDefaultGoRSSLimitBytes = defaultGoRSSLimitBytes
