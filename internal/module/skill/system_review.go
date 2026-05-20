package skill

import (
	"errors"
	"fmt"
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
func RequireSkillSystemReview(scope, slug, contentHash, repoFingerprint, approvedBy, reason string) error {
	if !strings.EqualFold(strings.TrimSpace(scope), skillScopeSystem) {
		return nil
	}
	missing := missingSystemReviewFields(slug, contentHash, repoFingerprint, approvedBy, reason)
	if len(missing) > 0 {
		return fmt.Errorf("%w: missing %s", ErrSkillSystemReviewRequired, strings.Join(missing, ","))
	}
	return nil
}

func missingSystemReviewFields(slug, contentHash, repoFingerprint, approvedBy, reason string) []string {
	fields := []struct{ name, value string }{
		{"slug", slug},
		{"content_hash", contentHash},
		{"repo_fingerprint", repoFingerprint},
		{"approved_by", approvedBy},
		{"reason", reason},
	}
	missing := make([]string, 0, len(fields))
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			missing = append(missing, field.name)
		}
	}
	return missing
}

func skillContentHash(content string) string { return skillhash.Content(content) }

func skillDirContentHash(root string) (string, error) { return skillhash.Dir(root) }
