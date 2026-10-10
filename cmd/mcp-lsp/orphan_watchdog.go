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

// errOrphanProcessSelfTerminated 是检测到进程为孤儿进程（PPID 失活或工作区已被删除）时的 Sentinel 错误。
var errOrphanProcessSelfTerminated = errors.New("mcp-lsp: orphan process detected (parent inactive or CWD invalid), self-terminating")

const defaultOrphanCheckInterval = time.Second

// orphanWatchdogRunner 定期校验本 mcp-lsp 进程是否为孤儿进程（父进程已失活）或当前工作目录已被删除。
// 满足条件时返回 Sentinel 错误，触发 RunGroup 优雅取消并释放全部资源。
type orphanWatchdogRunner struct {
	interval         time.Duration
	getPpid          func() int
	isParentOrphaned func(int) bool
	getCwd           func() (string, error)
	statPath         func(string) (os.FileInfo, error)
}

// newOrphanWatchdogRunner 构建默认的孤儿进程自愈检测 runner。
func newOrphanWatchdogRunner() platformrunner.Runner {
	parentPID := os.Getppid()
	return &orphanWatchdogRunner{
		interval: defaultOrphanCheckInterval,
		getPpid:  os.Getppid,
		isParentOrphaned: func(ppid int) bool {
			return ppid != parentPID || isParentOrphaned(ppid)
		},
		getCwd:   os.Getwd,
		statPath: os.Stat,
	}
}

// Run 启动后台检测循环，直到 context 取消或检测到孤儿状态。
func (w *orphanWatchdogRunner) Run(ctx context.Context) error {
	if w == nil {
		return nil
	}
	interval, getPpid, isOrphanedProbe, getCwd, statPath := w.resolveProbes()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := w.checkOrphanStatus(getPpid, isOrphanedProbe, getCwd, statPath); err != nil {
			if errors.Is(err, errOrphanProcessSelfTerminated) {
				// 先交给 RunGroup 取消；退出根因由 runtime 记录，不能被已满的 stderr 管道拦住。
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
	func(int) bool,
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
	isOrphanedProbe := w.isParentOrphaned
	if isOrphanedProbe == nil {
		isOrphanedProbe = isParentOrphaned
	}
	getCwd := w.getCwd
	if getCwd == nil {
		getCwd = os.Getwd
	}
	statPath := w.statPath
	if statPath == nil {
		statPath = os.Stat
	}
	return interval, getPpid, isOrphanedProbe, getCwd, statPath
}

// checkOrphanStatus 检测父进程存活状态与工作区 CWD 状态。
// 满足以下任一条件时返回 errOrphanProcessSelfTerminated 触发自愈终止：
// 1. 父进程失活（POSIX 下 PPID<=1 或 ESRCH，Windows 下非 STILL_ACTIVE）；
// 2. 当前工作区目录已被删除 (os.ErrNotExist)。
func (w *orphanWatchdogRunner) checkOrphanStatus(
	getPpid func() int,
	isOrphanedProbe func(int) bool,
	getCwd func() (string, error),
	statPath func(string) (os.FileInfo, error),
) error {
	ppid := getPpid()
	if isOrphanedProbe != nil && isOrphanedProbe(ppid) {
		return fmt.Errorf("%w: parent process (ppid=%d) is no longer active", errOrphanProcessSelfTerminated, ppid)
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
