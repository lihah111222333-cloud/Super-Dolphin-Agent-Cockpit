//go:build windows

package cliadapter

import (
	"fmt"
	"os"
	"os/exec"
)

// platformLink 在 Windows 上优先用 directory junction（mklink /J），
// 失败再退化到普通 symlink（需 Developer Mode）。
// 跨盘场景 Phase 2 不做硬拷贝兜底（YAGNI）。
func platformLink(target, source string) error {
	cmd := exec.Command("cmd", "/C", "mklink", "/J", target, source)
	if out, err := cmd.CombinedOutput(); err == nil {
		return nil
	} else {
		if err2 := os.Symlink(source, target); err2 != nil {
			return fmt.Errorf("cliadapter: junction failed (%s); symlink failed: %w", string(out), err2)
		}
	}
	return nil
}
