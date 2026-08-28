//go:build !darwin && !windows

package config

// packagedSQLitePlatform 固定 Darwin 与 Windows 之外的平台使用非 Windows 数据目录策略。
const packagedSQLitePlatform = "nonwindows"
