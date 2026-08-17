//go:build windows

package tools

import (
	"encoding/json"
	"time"
)

// patchEditTimeoutTier 选择 Windows 当前构建的 patch_edit 超时策略。
// 平台选择由本文件的 build tag 完成，公共工具实现不读取 runtime.GOOS。
func patchEditTimeoutTier(params json.RawMessage) time.Duration {
	return patchEditTimeoutTierForOS(params, "windows")
}

// fileToolTimeoutTier 选择 Windows 当前构建的 file 超时策略。
// Windows 冷安装允许安装器自己的有界超时完整执行。
func fileToolTimeoutTier(params json.RawMessage) time.Duration {
	return fileToolTimeoutTierForOS(params, "windows")
}
