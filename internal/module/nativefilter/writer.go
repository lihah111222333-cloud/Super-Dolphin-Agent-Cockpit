package nativefilter

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteWorkspaceSettings 把 body 原子写到 workspaceDir/.claude/settings.json。
// 用 .tmp + rename 避免 Claude CLI 在我们半写状态下读到。
//
// 简化决策：单 tmp+rename，**不**复用 P1 atomic.go 的 tmp+backup 双 rename 模式。
// 理由：settings.json 是单文件不是目录，rename 跨平台对单文件覆盖有原子保证；
// spec §4.4 的 backup 模式针对的是目录树。Claude CLI 重启读到旧 settings 也无害。
//
// workspaceDir 必须存在；缺失返回 error。
func WriteWorkspaceSettings(workspaceDir string, body []byte) error {
	if workspaceDir == "" {
		return fmt.Errorf("nativefilter: WriteWorkspaceSettings empty workspaceDir")
	}
	dir := filepath.Join(workspaceDir, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("nativefilter: mkdir %s: %w", dir, err)
	}
	target := filepath.Join(dir, "settings.json")
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("nativefilter: write tmp: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("nativefilter: rename to settings.json: %w", err)
	}
	return nil
}
