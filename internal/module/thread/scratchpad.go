package thread

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
)

const (
	scratchpadEnabledFlag      = "scratchpad_enabled"
	managedScratchpadNamespace = "super-agent-v3"
	managedScratchpadLeaf      = "scratchpad"
)

func (s *service) prepareScratchpadBuildCtx(req StartRequest, threadID string, buildCtx contract.BuildCtx) (contract.BuildCtx, func(), error) {
	if dir := strings.TrimSpace(buildCtx.ScratchpadDir); dir != "" {
		return buildCtx, func() {}, nil
	}
	if !scratchpadEnabled(req, buildCtx) {
		return buildCtx, func() {}, nil
	}
	dir, err := ensureManagedScratchpadDir(buildCtx, req, threadID, s.cfg)
	if err != nil {
		return contract.BuildCtx{}, nil, err
	}
	buildCtx.ScratchpadDir = dir
	return buildCtx, func() { _ = cleanupManagedScratchpadDir(dir) }, nil
}

func scratchpadEnabled(req StartRequest, buildCtx contract.BuildCtx) bool {
	if strings.TrimSpace(buildCtx.ScratchpadDir) != "" {
		return true
	}
	if configBool(req.Config, "scratchpadEnabled", "scratchpad_enabled") {
		return true
	}
	return buildCtx.SessionFlags[scratchpadEnabledFlag]
}

func configScratchpadDir(cfg map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := cfg[key].(string)
		if !ok {
			continue
		}
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func ensureManagedScratchpadDir(buildCtx contract.BuildCtx, req StartRequest, threadID string, cfg *contract.Config) (string, error) {
	projectRoot := managedScratchpadProjectRoot(buildCtx, req, cfg)
	dir := filepath.Join(
		os.TempDir(),
		managedScratchpadNamespace,
		sanitizeScratchpadPath(projectRoot),
		strings.TrimSpace(threadID),
		managedScratchpadLeaf,
	)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func managedScratchpadProjectRoot(buildCtx contract.BuildCtx, req StartRequest, cfg *contract.Config) string {
	return util.FirstNonEmpty(
		strings.TrimSpace(buildCtx.GitRoot),
		strings.TrimSpace(req.GitRoot),
		strings.TrimSpace(buildCtx.CWD),
		strings.TrimSpace(req.CWD),
		strings.TrimSpace(configProjectRoot(cfg)),
		"project",
	)
}

func configProjectRoot(cfg *contract.Config) string {
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.ProjectRoot)
}

// sanitizeScratchpadPath 清理scratchpad路径。
func sanitizeScratchpadPath(raw string) string {
	normalized := filepath.ToSlash(strings.TrimSpace(raw))
	var builder strings.Builder
	lastDash := false
	for _, r := range normalized {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(unicode.ToLower(r))
			lastDash = false
		case lastDash:
		default:
			builder.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug != "" {
		return slug
	}
	hash := sha256.Sum256([]byte(normalized))
	return "project-" + hex.EncodeToString(hash[:4])
}

func cleanupManagedScratchpadDir(dir string) error {
	if !isManagedScratchpadDir(dir) {
		return nil
	}
	return os.RemoveAll(filepath.Dir(filepath.Clean(dir)))
}

// isManagedScratchpadDir 判断 scratchpad 路径是否属于本进程管理的临时命名空间。
func isManagedScratchpadDir(dir string) bool {
	cleaned := filepath.Clean(strings.TrimSpace(dir))
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return false
	}
	root := filepath.Join(os.TempDir(), managedScratchpadNamespace)
	rel, err := filepath.Rel(root, cleaned)
	if err != nil {
		return false
	}
	return rel != "." && rel != "" && !strings.HasPrefix(rel, "..")
}

func (s *service) cleanupThreadScratchpad(ctx context.Context, threadID string, binding *bindingstore.Binding) {
	dir := s.threadScratchpadDir(ctx, threadID, binding)
	if err := cleanupManagedScratchpadDir(dir); err != nil && s.logger != nil {
		s.logger.Warn("thread scratchpad cleanup failed", "thread_id", threadID, "scratchpad_dir", dir, "error", err)
	}
}

func (s *service) threadScratchpadDir(ctx context.Context, threadID string, binding *bindingstore.Binding) string {
	offline, err := s.buildOfflineConfig(ctx, threadID, binding)
	if err != nil {
		return ""
	}
	return configScratchpadDir(offline.Runtime, "scratchpadDir", "scratchpad_dir")
}
