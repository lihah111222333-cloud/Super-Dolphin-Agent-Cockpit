package cliadapter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/module/nativefilter"
)

// SettingsFileName 是 harness 在 workspace .claude/ 下落地 native-filter 决议的文件名。
//
// spec §8.2 字面写的是 "settings.json"，但那是用户可能手写的项目级配置文件，
// 直接覆盖会破坏用户内容。改用 Claude 工具约定的 settings.local.json：
//   - 默认 .gitignore，IDE / harness 用作本地覆盖
//   - 不与项目级 settings.json 冲突
//
// spec §8.3 实测验证完成后如果证明 Claude CLI 仅读 settings.json，再改这里
// 并补 spec deviation note。
const SettingsFileName = "settings.local.json"

// settingsEnvelope 是写到 settings.local.json 的实际 JSON 形态。
//
// 与 nativefilter.ClaudeSettings 的字段一一对应；多出 _harness_managed_at
// ISO 时间戳标记，方便：
//   - 排查谁/什么时候写的
//   - 让用户看到这文件是 harness 自动生成的
type settingsEnvelope struct {
	Permissions      nativefilter.ClaudePermissions `json:"permissions"`
	HarnessManagedAt string                         `json:"_harness_managed_at,omitempty"`
}

// WriteClaudeSettingsLocal 把 ClaudeSettings 序列化写到
// <workspaceDir>/.claude/<SettingsFileName>。
//
// 行为：
//   - workspaceDir 为空 → ErrEmptyArgs
//   - .claude/ 不存在自动创建
//   - 总是覆盖 settings.local.json（这文件按 Claude 工具约定就是 harness 拥有的）
//   - 文件以 0o644 写入
//
// 暂未做 atomic rename / backup——这文件是从 active skill 状态可重新生成的，
// 中断恢复成本是"重启 session 重写一次"，不存在数据丢失。
func WriteClaudeSettingsLocal(workspaceDir string, settings nativefilter.ClaudeSettings) error {
	if workspaceDir == "" {
		return ErrEmptyArgs
	}
	claudeDir := filepath.Join(workspaceDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return fmt.Errorf("cliadapter: mkdir .claude: %w", err)
	}
	envelope := settingsEnvelope{
		Permissions:      settings.Permissions,
		HarnessManagedAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return fmt.Errorf("cliadapter: marshal settings: %w", err)
	}
	target := filepath.Join(claudeDir, SettingsFileName)
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return fmt.Errorf("cliadapter: write %s: %w", SettingsFileName, err)
	}
	return nil
}
