package skill

import (
	"errors"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/skill/skillhash"
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
