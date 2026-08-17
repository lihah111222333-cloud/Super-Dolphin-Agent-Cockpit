//go:build mcp_lsp_short_idle_precheck

package config

// allowShortLSPIdleTimeoutPrecheck 只供显式带测试 build tag 的快速生命周期预检使用；该构建不能作为十五分钟交付证明。
const allowShortLSPIdleTimeoutPrecheck = true
