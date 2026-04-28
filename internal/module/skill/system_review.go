package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
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

func skillContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func skillDirContentHash(root string) string {
	var parts []string
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		parts = append(parts, filepath.ToSlash(rel)+"\x00"+skillContentHash(string(data)))
		return nil
	}); err != nil {
		pkglogger.Warn("skill: dir content hash walk failed", "root", root, "error", err)
	}
	sort.Strings(parts)
	return skillContentHash(strings.Join(parts, "\x00"))
}
