package thread

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/sessionpaths"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
)

const scratchpadEnabledFlag = "scratchpad_enabled"

type scratchpadPartialCleanupError struct {
	operation string
	cause     error
}

// Error 返回不包含本地路径的稳定部分清理失败描述。
func (e *scratchpadPartialCleanupError) Error() string {
	return "thread " + e.operation + " completed with scratchpad cleanup failure"
}

// Unwrap 保留底层清理原因，供内部错误分类和诊断使用。
func (e *scratchpadPartialCleanupError) Unwrap() error {
	return e.cause
}

func newScratchpadPartialCleanupError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return &scratchpadPartialCleanupError{operation: operation, cause: err}
}

func joinScratchpadPartialCleanupError(operation string, mainErr, cleanupErr error) error {
	partialErr := newScratchpadPartialCleanupError(operation, cleanupErr)
	if mainErr == nil {
		return partialErr
	}
	if partialErr == nil {
		return mainErr
	}
	return errors.Join(mainErr, partialErr)
}

func (s *service) prepareScratchpadBuildCtx(req StartRequest, threadID string, buildCtx contract.BuildCtx) (contract.BuildCtx, func() error, error) {
	if dir := strings.TrimSpace(buildCtx.ScratchpadDir); dir != "" {
		return buildCtx, func() error { return nil }, nil
	}
	if !scratchpadEnabled(req, buildCtx) {
		return buildCtx, func() error { return nil }, nil
	}
	dir, err := ensureManagedScratchpadDir(buildCtx, req, threadID, s.cfg)
	if err != nil {
		return contract.BuildCtx{}, nil, err
	}
	buildCtx.ScratchpadDir = dir
	return buildCtx, func() error { return s.cleanupScratchpadDir(dir) }, nil
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

func (s *service) cleanupScratchpadDir(dir string) error {
	if s != nil && s.scratchpadCleanup != nil {
		return s.scratchpadCleanup(dir)
	}
	return cleanupManagedScratchpadDir(dir)
}

func (s *service) cleanupThreadScratchpadRecord(ctx context.Context, threadID string, binding *threadBindingRecord) error {
	dir, err := s.threadScratchpadDirRecord(ctx, threadID, binding)
	if err != nil {
		return err
	}
	return s.cleanupScratchpadDir(dir)
}

func (s *service) threadScratchpadDirRecord(ctx context.Context, threadID string, binding *threadBindingRecord) (string, error) {
	offline, err := s.buildOfflineConfigRecord(ctx, threadID, binding)
	if err != nil {
		return "", err
	}
	return configScratchpadDir(offline.Runtime, "scratchpadDir", "scratchpad_dir"), nil
}
