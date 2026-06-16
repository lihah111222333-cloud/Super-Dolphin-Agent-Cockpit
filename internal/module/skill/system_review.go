package skill

import (
	"errors"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skill/skillhash"
)

// ErrSkillSystemReviewRequired is returned before any system-scope skill write
// that lacks an explicit human/admin review decision.
var ErrSkillSystemReviewRequired = errors.New("skill system review required")

// RequireSkillSystemReview gates writes into the user/system skill root. Project
// scope never reaches this gate. System scope requires a stable slug, content
// hash, repo fingerprint, reviewer and reason so callers can persist/audit the
// decision before retrying the write.
// RequireSkillSystemReview 处理require技能systemreview。
func RequireSkillSystemReview(scope, slug, contentHash, repoFingerprint, approvedBy, reason string) error {
	if strings.EqualFold(strings.TrimSpace(scope), skillScopeSystem) &&
		(strings.TrimSpace(slug) == "" || strings.TrimSpace(contentHash) == "" || strings.TrimSpace(repoFingerprint) == "" || strings.TrimSpace(approvedBy) == "" || strings.TrimSpace(reason) == "") {
		return ErrSkillSystemReviewRequired
	}
	return nil
}

func skillContentHash(content string) string { return skillhash.Content(content) }

func skillDirContentHash(root string) (string, error) { return skillhash.Dir(root) }

// resolveSymlinkPath 解析symlink路径。
