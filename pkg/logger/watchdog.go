package logger

import (
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
			if _, err := os.Stat(path); err == nil {
				continue
			}
			_ = os.MkdirAll(filepath.Dir(path), 0o755)
			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				Warn("log file watchdog: reopen failed", "path", path, "error", err)
				continue
			}
			r.mu.Lock()
			r.closeLogFileLocked()
			r.logFile = f
			r.mu.Unlock()
			r.rebuildLoggerWithFile(f)
			Info("log file watchdog: reopened deleted log file", "path", path)
		}
	}
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

// ShutdownFileHandler 关闭 agent 专属日志、文件 watcher 和主日志文件。
func ShutdownFileHandler() {
	currentRuntime().ShutdownFileHandler()
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
