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

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.checkOrphanStatus(getPpid, getCwd, statPath); err != nil {
				pkglogger.Error("mcp-lsp orphan watchdog triggered", "error", err)
				return err
			}
		}
	}
}

// checkOrphanStatus 检测 PPID 和 CWD 状态。
// 当 PPID=1 且 CWD 获取失败或文件系统 Stat 失败时返回自愈终止错误。
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
		return fmt.Errorf("%w: cwd error (%v)", errOrphanProcessSelfTerminated, err)
	}

	cleanedCwd := filepath.Clean(cwd)
	if _, err := statPath(cleanedCwd); err != nil {
		return fmt.Errorf("%w: cwd stat error (%v)", errOrphanProcessSelfTerminated, err)
	}

	return nil
}
