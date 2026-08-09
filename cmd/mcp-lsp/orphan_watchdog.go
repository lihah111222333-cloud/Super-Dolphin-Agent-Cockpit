// Package main 是 mcp-lsp sidecar 进程的入口，通过 MCP stdio 协议暴露 LSP 工具能力。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// errOrphanProcessSelfTerminated 是检测到进程为孤儿进程（PPID=1 且 CWD 不存在）时的 Sentinel 错误。
var errOrphanProcessSelfTerminated = errors.New("mcp-lsp: orphan process detected (PPID=1 and CWD invalid), self-terminating")

const defaultOrphanCheckInterval = 30 * time.Second

// orphanWatchdogRunner 定期校验本 mcp-lsp 进程是否为孤儿进程（PPID=1）且当前工作目录已被删除。
// 满足条件时返回 Sentinel 错误，触发 RunGroup 优雅取消并释放全部资源。
type orphanWatchdogRunner struct {
	interval time.Duration
	getPpid  func() int
	getCwd   func() (string, error)
	statPath func(string) (os.FileInfo, error)
}

// newOrphanWatchdogRunner 构建默认的孤儿进程自愈检测 runner。
func newOrphanWatchdogRunner() platformrunner.Runner {
	return &orphanWatchdogRunner{
		interval: defaultOrphanCheckInterval,
		getPpid:  os.Getppid,
		getCwd:   os.Getwd,
		statPath: os.Stat,
	}
}

// Run 启动后台检测循环，直到 context 取消或检测到孤儿状态。
func (w *orphanWatchdogRunner) Run(ctx context.Context) error {
	if w == nil {
		return nil
	}
	interval, getPpid, getCwd, statPath := w.resolveProbes()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := w.checkOrphanStatus(getPpid, getCwd, statPath); err != nil {
			if errors.Is(err, errOrphanProcessSelfTerminated) {
				pkglogger.Error("mcp-lsp orphan watchdog triggered", "error", err)
				return err
			}
			pkglogger.Warn("mcp-lsp orphan watchdog probe failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// resolveProbes 补齐 watchdog 的时钟和只读进程探针，便于 Run 聚焦生命周期控制。
func (w *orphanWatchdogRunner) resolveProbes() (
	time.Duration,
	func() int,
	func() (string, error),
	func(string) (os.FileInfo, error),
) {
	interval := w.interval
	if interval <= 0 {
		interval = defaultOrphanCheckInterval
	}
	getPpid := w.getPpid
	if getPpid == nil {
		getPpid = os.Getppid
	}
	getCwd := w.getCwd
	if getCwd == nil {
		getCwd = os.Getwd
	}
	statPath := w.statPath
	if statPath == nil {
		statPath = os.Stat
	}
	return interval, getPpid, getCwd, statPath
}

// checkOrphanStatus 检测 PPID 和 CWD 状态。
// 只有 PPID=1 且 CWD 明确返回 ENOENT 时才触发自愈终止；权限或暂态错误不授权破坏性动作。
func (w *orphanWatchdogRunner) checkOrphanStatus(
	getPpid func() int,
	getCwd func() (string, error),
	statPath func(string) (os.FileInfo, error),
) error {
	ppid := getPpid()
	if !isParentOrphaned(ppid) {
		return nil
	}

	cwd, err := getCwd()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: cwd no longer exists: %v", errOrphanProcessSelfTerminated, err)
		}
		return fmt.Errorf("mcp-lsp orphan watchdog read cwd: %w", err)
	}

	cleanedCwd := filepath.Clean(cwd)
	if _, err := statPath(cleanedCwd); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: cwd no longer exists: %v", errOrphanProcessSelfTerminated, err)
		}
		return fmt.Errorf("mcp-lsp orphan watchdog stat cwd: %w", err)
	}

	return nil
}
