// Package cliadapter 封装 harness 与底层 CLI 子进程之间的环境装配，
// 例如把共享 skill cache 挂到 Claude CLI 期望的 <workspace>/.claude/skills/。
package cliadapter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrEmptyArgs 表示 SetupWorkspaceSkills 调用时缺必要参数。
var ErrEmptyArgs = errors.New("cliadapter: empty workspace or cache dir")

// SetupWorkspaceSkills 在 workspace/<workspaceDir> 下创建 .claude/skills 引用，
// 让 Claude CLI 子进程 native 发现共享缓存里的 skill。
//
// 行为：
//  1. workspaceDir 或 cacheDir 为空 → ErrEmptyArgs
//  2. cacheDir 不存在 → 立即创建（避免 dangling symlink）
//  3. <workspaceDir>/.claude/ 目录自动创建
//  4. 已有的 <workspaceDir>/.claude/skills（普通目录或 symlink）会被替换
//  5. POSIX 用 os.Symlink；Windows 走 platform-specific 实现（junction / symlink fallback）
func SetupWorkspaceSkills(workspaceDir, cacheDir string) error {
	if workspaceDir == "" || cacheDir == "" {
		return ErrEmptyArgs
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return fmt.Errorf("cliadapter: ensure cache dir: %w", err)
	}
	claudeDir := filepath.Join(workspaceDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return fmt.Errorf("cliadapter: mkdir .claude: %w", err)
	}
	target := filepath.Join(claudeDir, "skills")
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("cliadapter: clear stale skills entry: %w", err)
	}
	return platformLink(target, cacheDir)
}
