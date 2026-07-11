package thread

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/sessionpaths"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
)

const scratchpadEnabledFlag = "scratchpad_enabled"

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

// ensureManagedScratchpadDir 为本次线程创建受控 scratchpad 目录并固定权限。
// 路径派生归 sessionpaths，目录创建和权限仍由 thread 模块负责。
func ensureManagedScratchpadDir(buildCtx contract.BuildCtx, req StartRequest, threadID string, cfg *contract.Config) (string, error) {
	projectRoot := managedScratchpadProjectRoot(buildCtx, req, cfg)
	dir := sessionpaths.ManagedScratchpadDir(os.TempDir(), projectRoot, threadID)
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

// cleanupManagedScratchpadDir 只清理 sessionpaths 管理的临时 scratchpad。
// 删除 leaf 的父目录以保持按 thread 清理的旧行为，不接管外部配置目录。
func cleanupManagedScratchpadDir(dir string) error {
	if !sessionpaths.IsManagedScratchpadDir(os.TempDir(), dir) {
		return nil
	}
	return os.RemoveAll(filepath.Dir(filepath.Clean(dir)))
}

func (s *service) cleanupThreadScratchpadRecord(ctx context.Context, threadID string, binding *threadBindingRecord) {
	dir := s.threadScratchpadDirRecord(ctx, threadID, binding)
	if err := cleanupManagedScratchpadDir(dir); err != nil && s.logger != nil {
		s.logger.Warn("thread scratchpad cleanup failed", "thread_id", threadID, "scratchpad_dir", dir, "error", err)
	}
}

func (s *service) threadScratchpadDirRecord(ctx context.Context, threadID string, binding *threadBindingRecord) string {
	offline, err := s.buildOfflineConfigRecord(ctx, threadID, binding)
	if err != nil {
		return ""
	}
	return configScratchpadDir(offline.Runtime, "scratchpadDir", "scratchpad_dir")
}
