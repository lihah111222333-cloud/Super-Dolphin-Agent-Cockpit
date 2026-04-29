package skilllibrary

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SkillEntry 是一条已扫描的 library 条目。
type SkillEntry struct {
	Dir     string     // 该 skill 在 library 的绝对目录
	SkillMD string     // SKILL.md 字节内容（保持 string 易于直接 hash/parse）
	Meta    *SkillMeta // 同目录下的 .skill-meta.json
}

// Scan 扫描 libraryDir 下所有形如 <dir>/{SKILL.md, .skill-meta.json} 的子目录。
// 缺 SKILL.md 或 sidecar 一律跳过，不报错。
// 隐藏（以 . 开头）目录跳过。非目录条目跳过。
// 不存在的 libraryDir 返回 (nil, nil)。
// 结果按 Meta.Name 升序排序。
func Scan(libraryDir string) ([]SkillEntry, error) {
	entries, err := os.ReadDir(libraryDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("skilllibrary: scan: %w", err)
	}
	var out []SkillEntry
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(libraryDir, e.Name())
		skillBytes, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			continue // SKILL.md missing or unreadable
		}
		meta, err := ReadMeta(dir)
		if err != nil {
			continue // missing/invalid sidecar
		}
		out = append(out, SkillEntry{Dir: dir, SkillMD: string(skillBytes), Meta: meta})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Meta.Name < out[j].Meta.Name })
	return out, nil
}
