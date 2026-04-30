// Package skilllibrary manages the on-disk skill library at
// ~/.multi-agent/skills-library/, including sidecar metadata,
// install/uninstall, and library → cache reconciliation.
package skilllibrary

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Origin 表示 skill 来源（spec §5.1）。
type Origin string

const (
	OriginBuiltin     Origin = "builtin"
	OriginMarketplace Origin = "marketplace"
	OriginLocal       Origin = "local"
	OriginDevOverride Origin = "dev-override"
)

// SkillMeta 是 .skill-meta.json sidecar 的完整 schema（spec §3.1）。
type SkillMeta struct {
	Name                   string              `json:"name"`
	Origin                 Origin              `json:"origin"`
	Version                string              `json:"version"`
	VersionHash            string              `json:"version_hash"`
	InstalledAt            string              `json:"installed_at,omitempty"`
	Signature              *string             `json:"signature"`
	AllowedTools           []string            `json:"allowed_tools,omitempty"`
	DisableModelInvocation bool                `json:"disable_model_invocation,omitempty"`
	Pinned                 bool                `json:"pinned,omitempty"`
	Disabled               bool                `json:"disabled,omitempty"`
	ReplacesNative         map[string][]string `json:"replaces_native,omitempty"`
	SectionSummaries       map[string]string   `json:"section_summaries,omitempty"`
}

const metaFilename = ".skill-meta.json"

// WriteMeta 把 m 写入 skillDir/.skill-meta.json（缺目录自动创建）。
func WriteMeta(skillDir string, m SkillMeta) error {
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return fmt.Errorf("skilllibrary: mkdir: %w", err)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("skilllibrary: marshal meta: %w", err)
	}
	return os.WriteFile(filepath.Join(skillDir, metaFilename), b, 0o644)
}

// ReadMeta 读 skillDir/.skill-meta.json。
// 文件不存在时返回 fs.ErrNotExist（os.IsNotExist 可识别）。
// Name 字段为空视为格式错误。
func ReadMeta(skillDir string) (*SkillMeta, error) {
	p := filepath.Join(skillDir, metaFilename)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var m SkillMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("skilllibrary: unmarshal meta %s: %w", p, err)
	}
	if m.Name == "" {
		return nil, errors.New("skilllibrary: meta missing name field")
	}
	return &m, nil
}
