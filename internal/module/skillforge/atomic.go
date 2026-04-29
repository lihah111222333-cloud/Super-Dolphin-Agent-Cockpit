package skillforge

import (
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWriteSkill 把 RenderResult 原子写入 cacheDir/<name>/。
// 实现 spec §4.4 的 A2：写到 <name>.tmp/ → 删旧 <name>/ → rename(.tmp, <name>)。
// 短暂 rename 窗口期（毫秒级）；读端遇 ENOENT 应重试一次。
//
// nil res / 空 name 返回错误，不 panic。
func AtomicWriteSkill(cacheDir, name string, res *RenderResult) error {
	if res == nil {
		return fmt.Errorf("skillforge: AtomicWriteSkill(nil RenderResult)")
	}
	if name == "" {
		return fmt.Errorf("skillforge: AtomicWriteSkill empty name")
	}
	target := filepath.Join(cacheDir, name)
	tmp := filepath.Join(cacheDir, name+".tmp")

	if err := prepareTmp(tmp, res); err != nil {
		return err
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("skillforge: remove old target: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("skillforge: rename tmp to target: %w", err)
	}
	return nil
}

// prepareTmp 清残留 .tmp 并将 RenderResult 写入其中。
func prepareTmp(tmp string, res *RenderResult) error {
	// 清残留 .tmp（前一次崩溃留下的）
	if err := os.RemoveAll(tmp); err != nil {
		return fmt.Errorf("skillforge: cleanup stale tmp: %w", err)
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return fmt.Errorf("skillforge: mkdir tmp: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "SKILL.md"), []byte(res.SkillMD), 0o644); err != nil {
		return fmt.Errorf("skillforge: write SKILL.md: %w", err)
	}
	if len(res.References) == 0 {
		return nil
	}
	return writeReferences(filepath.Join(tmp, "references"), res.References)
}

// writeReferences 把 references map 写入 refDir/。
func writeReferences(refDir string, refs map[string]string) error {
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		return fmt.Errorf("skillforge: mkdir references: %w", err)
	}
	for fname, body := range refs {
		if err := os.WriteFile(filepath.Join(refDir, fname), []byte(body), 0o644); err != nil {
			return fmt.Errorf("skillforge: write reference %s: %w", fname, err)
		}
	}
	return nil
}
