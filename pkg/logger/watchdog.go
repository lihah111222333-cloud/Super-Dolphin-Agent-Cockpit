package logger

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const fileWatchInterval = 30 * time.Second

// watchLogFile 定期检查日志文件是否被外部删除，并在缺失时重新创建。
// stop 关闭后立即退出，避免 InitWithFileOptions 重建 watcher 时泄漏 goroutine。
func (r *Runtime) watchLogFile(path string, stop chan struct{}) {
	ticker := time.NewTicker(fileWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			_, err := os.Stat(path)
			if err == nil {
				continue
			}
			if !errors.Is(err, os.ErrNotExist) {
			r.Get().Error("log file watchdog: stat failed", "path", path, "error", err)
				return
			}
			if err := r.reopenLogFile(path); err != nil {
			r.Get().Error("log file watchdog: reopen failed", "path", path, "error", err)
				return
			}
			r.Get().Info("log file watchdog: reopened deleted log file", "path", path)
		}
	}
}

// reopenLogFile 同步重建被删除的日志文件，并在成功后替换 runtime 当前文件。
func (r *Runtime) reopenLogFile(path string) error {
	if err := ensurePrivateLogDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("reopen log file directory: %w", err)
	}
	f, err := openPrivateAppendFile(path)
	if err != nil {
		return fmt.Errorf("reopen log file: %w", err)
	}
	r.mu.Lock()
	r.closeLogFileLocked()
	r.logFile = f
	r.mu.Unlock()
	r.rebuildLoggerWithFile(f)
	return nil
}

func (r *Runtime) stopFileWatcherLocked() {
	if r.fileWatcherStop != nil {
		close(r.fileWatcherStop)
		r.fileWatcherStop = nil
	}
}

func (r *Runtime) closeLogFileLocked() {
	if r.logFile != nil {
		_ = r.logFile.Sync()
		_ = r.logFile.Close()
	}
}

// ShutdownFileHandler 关闭 runtime 的 agent 专属日志、文件 watcher 和主日志文件。
func (r *Runtime) ShutdownFileHandler() {
	r.closeAllAgentLoggers()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopFileWatcherLocked()
	if r.logFile != nil {
		r.closeLogFileLocked()
		r.logFile = nil
	}
	r.logFileConsole = nil
}
