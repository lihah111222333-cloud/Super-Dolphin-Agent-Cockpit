package skillforge

import (
	"fmt"
	"os"
	"path/filepath"
)

// Forge 把 libDir/<name>/SKILL.md 转换为 cacheDir/<name>/{SKILL.md, references/...}。
// summaryOverride: H2 标题 → 手写摘要；nil 表示全部自动抽。
//
// 此函数处理单个 skill；批量调度由 skilllibrary.Reconcile 负责。
// 任意阶段失败均返回 wrapped error，附 skill 名上下文。
func Forge(libDir, cacheDir, name string, summaryOverride map[string]string) error {
	src := filepath.Join(libDir, name, "SKILL.md")
	raw, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("skillforge: read source for %q: %w", name, err)
	}
	ps, err := Parse(string(raw))
	if err != nil {
		return fmt.Errorf("skillforge: parse %q: %w", name, err)
	}
	res, err := Render(ps, summaryOverride)
	if err != nil {
		return fmt.Errorf("skillforge: render %q: %w", name, err)
	}
	if err := AtomicWriteSkill(cacheDir, name, res); err != nil {
		return fmt.Errorf("skillforge: write %q: %w", name, err)
	}
	return nil
}
