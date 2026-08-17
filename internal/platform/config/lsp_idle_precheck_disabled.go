//go:build !mcp_lsp_short_idle_precheck

package config

// allowShortLSPIdleTimeoutPrecheck 在正式构建中固定为 false，保证运行时生命周期不能被环境变量缩短到十五分钟以下。
const allowShortLSPIdleTimeoutPrecheck = false
