package skillforge

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed all:embedded_skills
var embeddedFS embed.FS

const embeddedRoot = "embedded_skills"

// ListEmbeddedSkillNames 返回所有内置 skill 名（embedded_skills/<name>/SKILL.md 存在的）。
// 结果按名字升序排序。
func ListEmbeddedSkillNames() ([]string, error) {
	entries, err := embeddedFS.ReadDir(embeddedRoot)
	if err != nil {
		return nil, fmt.Errorf("skillforge: list embedded: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// 验证 SKILL.md 存在
		if _, err := embeddedFS.ReadFile(embeddedRoot + "/" + e.Name() + "/SKILL.md"); err != nil {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// ReadEmbeddedSkill 返回 embedded_skills/<name>/SKILL.md 的字节内容。
// 拒绝包含路径分隔符或 "." 的 name（防路径穿越）。
func ReadEmbeddedSkill(name string) ([]byte, error) {
	if name == "" || strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") {
		return nil, fmt.Errorf("skillforge: invalid skill name %q", name)
	}
	return embeddedFS.ReadFile(embeddedRoot + "/" + name + "/SKILL.md")
}
