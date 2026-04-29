package skillforge

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var renamePath = os.Rename

// AtomicWriteSkill 把 RenderResult 发布到 cacheDir/<name>/。
// A2 不是"删旧目录再 rename"的伪原子；目录树跨平台无法保证覆盖非空目录原子。
// 这里实现 non-lossy publish：先把旧 target 改名为 backup，发布失败时恢复旧版本。
func AtomicWriteSkill(cacheDir, name string, res *RenderResult) error {
	if res == nil {
		return fmt.Errorf("skillforge: AtomicWriteSkill(nil RenderResult)")
	}
	if name == "" {
		return fmt.Errorf("skillforge: AtomicWriteSkill empty name")
	}
	target := filepath.Join(cacheDir, name)
	legacyTmp := filepath.Join(cacheDir, name+".tmp")
	suffix := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	tmp := filepath.Join(cacheDir, fmt.Sprintf(".%s.tmp-%s", name, suffix))
	backup := filepath.Join(cacheDir, fmt.Sprintf(".%s.bak-%s", name, suffix))

	if err := cleanStaging(legacyTmp, tmp, backup); err != nil {
		return err
	}
	if err := writeRenderResult(tmp, res); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	return publishWithBackup(target, tmp, backup)
}

// cleanStaging 清理上一轮留下的 legacy / tmp / backup 残骸。
func cleanStaging(legacyTmp, tmp, backup string) error {
	stages := []struct {
		path string
		kind string
	}{
		{legacyTmp, "legacy tmp"},
		{tmp, "tmp"},
		{backup, "backup"},
	}
	for _, s := range stages {
		if err := os.RemoveAll(s.path); err != nil {
			return fmt.Errorf("skillforge: cleanup %s: %w", s.kind, err)
		}
	}
	return nil
}

// publishWithBackup 通过"先备份旧 target、再 rename tmp→target"实现 non-lossy 发布。
// 三处 rename 全部走 renamePath 单一注入点，便于测试 hook publish 失败场景。
// publish 失败且 target 已被腾空时，把 backup 恢复回 target，保证旧版本不丢。
func publishWithBackup(target, tmp, backup string) error {
	hadTarget := dirExists(target)
	if hadTarget {
		if err := renamePath(target, backup); err != nil {
			_ = os.RemoveAll(tmp)
			return fmt.Errorf("skillforge: move old target to backup: %w", err)
		}
	}
	if err := renamePath(tmp, target); err != nil {
		if hadTarget && !dirExists(target) {
			_ = renamePath(backup, target)
		}
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("skillforge: publish tmp to target: %w", err)
	}
	if hadTarget {
		_ = os.RemoveAll(backup)
	}
	return nil
}

func writeRenderResult(dir string, res *RenderResult) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("skillforge: mkdir tmp: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(res.SkillMD), 0o644); err != nil {
		return fmt.Errorf("skillforge: write SKILL.md: %w", err)
	}
	if len(res.References) == 0 {
		return nil
	}
	refDir := filepath.Join(dir, "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		return fmt.Errorf("skillforge: mkdir references: %w", err)
	}
	for fname, body := range res.References {
		if err := os.WriteFile(filepath.Join(refDir, fname), []byte(body), 0o644); err != nil {
			return fmt.Errorf("skillforge: write reference %s: %w", fname, err)
		}
	}
	return nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
