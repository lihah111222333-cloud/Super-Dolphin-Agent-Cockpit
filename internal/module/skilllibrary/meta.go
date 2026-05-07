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

	dtoskill "github.com/anthropic-ai/super-agent-v3/internal/dto/skill"
)

// Origin, SkillMeta — canonical definitions live in dto/skill;
// aliases here preserve backward compatibility for all existing
// skilllibrary.Origin / skilllibrary.SkillMeta references.
type Origin = dtoskill.Origin

const (
	OriginBuiltin     = dtoskill.OriginBuiltin
	OriginMarketplace = dtoskill.OriginMarketplace
	OriginLocal       = dtoskill.OriginLocal
	OriginDevOverride = dtoskill.OriginDevOverride
)

// SkillMeta is a type alias for dtoskill.SkillMeta.
type SkillMeta = dtoskill.SkillMeta

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
