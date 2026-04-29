// Package nativefilter renders Anthropic-CLI / OpenAI-codex native skill +
// tool filtering decisions into per-CLI configuration files. See spec §8 +
// 2026-04-30 实测 appendix（Skill: 冒号语法）。
package nativefilter

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// Config 对应 ~/.multi-agent/native-cli-filter.json schema（spec §8.1）。
// 缺省字段（如 allowed_tools=null）解析为 nil slice。
type Config struct {
	Claude ClaudeConfig `json:"claude"`
	Codex  CodexConfig  `json:"codex"`
}

// ClaudeConfig 是 Claude 侧过滤声明。disabled_skills 在 BuildClaudeSettings
// 时会被 wrap 成 "Skill:<name>" 冒号格式（实测确认有效语法）。
type ClaudeConfig struct {
	DisabledSkills []string `json:"disabled_skills,omitempty"`
	DisabledTools  []string `json:"disabled_tools,omitempty"`
	// AllowedTools nil 表示无 allowlist（任意工具默认可用）；非 nil 时表示
	// 仅允许列出的工具。本期 P5 不实现 allowlist 模式（spec §8.3 fallback
	// 档位 2，未实测），字段保留给未来。
	AllowedTools []string `json:"allowed_tools,omitempty"`
}

// CodexConfig 是 Codex 侧过滤声明。enforcement 在 P5 走 stub（实测 codex
// 0.121.0 未发现等价工具屏蔽机制；schema 留字段方便 P5.x 实测后补真实现）。
type CodexConfig struct {
	DisabledTools []string `json:"disabled_tools,omitempty"`
}

// LoadConfig 读取 path 上的 JSON。文件不存在视为"未配置"返回空 Config（不是
// 错误，便于 default 部署）。解析失败（malformed JSON / 类型不匹配）返回 error。
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("nativefilter: read %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("nativefilter: parse %s: %w", path, err)
	}
	return c, nil
}
