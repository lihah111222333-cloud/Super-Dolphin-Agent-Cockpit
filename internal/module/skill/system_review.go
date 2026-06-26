package skill

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skill/skillhash"
)

// ErrSkillSystemReviewRequired 表示 system scope skill 写入缺少人工审批信息。
var ErrSkillSystemReviewRequired = errors.New("skill system review required")

// RequireSkillSystemReview 校验 system scope 写入必须携带完整审批上下文。
// project scope 不进入该 gate；system scope 缺少 slug/hash/repo/reviewer/reason 任一字段都会阻断。
func RequireSkillSystemReview(scope, slug, contentHash, repoFingerprint, approvedBy, reason string) error {
	if strings.EqualFold(strings.TrimSpace(scope), skillScopeSystem) &&
		(strings.TrimSpace(slug) == "" || strings.TrimSpace(contentHash) == "" || strings.TrimSpace(repoFingerprint) == "" || strings.TrimSpace(approvedBy) == "" || strings.TrimSpace(reason) == "") {
		return ErrSkillSystemReviewRequired
	}
	return nil
}

func skillContentHash(content string) string { return skillhash.Content(content) }

func skillDirContentHash(root string) (string, error) { return skillhash.Dir(root) }

// resolveSymlinkPath 在安全上限内解析 symlink 链。
// EvalSymlinks 失败时退化为逐段解析，最多 16 层，避免循环链接卡住调用方。
func resolveSymlinkPath(path string) string {
	if realPath, err := filepath.EvalSymlinks(path); err == nil {
		return realPath
	}
	for i := 0; i < 16; i++ {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			break
		}
		target, err := os.Readlink(path)
		if err != nil {
			break
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = filepath.Clean(target)
	}
	return path
}

func resolveValidMirrorRootSymlink(root string) string {
	resolved := resolveSymlinkPath(root)
	if filepath.Base(resolved) == "skills" {
		return resolved
	}
	return root
}
