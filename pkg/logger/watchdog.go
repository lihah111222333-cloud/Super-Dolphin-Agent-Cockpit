package logger

import (
	"os"
	"path/filepath"
	"time"
)

var (
	fileWatcherStop   chan struct{}
	fileWatchInterval = 30 * time.Second
)

// watchLogFile 定期检查日志文件是否被外部删除，并在缺失时重新创建。
// stop 关闭后立即退出，避免 InitWithFileOptions 重建 watcher 时泄漏 goroutine。
func watchLogFile(path string, stop chan struct{}) {
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
			logFileMu.Lock()
			closeLogFileLocked()
			logFile = f
			logFileMu.Unlock()
			rebuildLoggerWithFile(f)
			Info("log file watchdog: reopened deleted log file", "path", path)
		}
	}
}

// stopFileWatcherLocked 在持有 logFileMu 时关闭当前文件 watcher。
func stopFileWatcherLocked() {
	if fileWatcherStop != nil {
		close(fileWatcherStop)
		fileWatcherStop = nil
	}
}

// closeLogFileLocked 在持有 logFileMu 时同步并关闭当前日志文件。
func closeLogFileLocked() {
	if logFile != nil {
		_ = logFile.Sync()
		_ = logFile.Close()
	}
}

// ShutdownFileHandler 关闭 agent 专属日志、文件 watcher 和主日志文件。
func ShutdownFileHandler() {
	closeAllAgentLoggers()
	logFileMu.Lock()
	defer logFileMu.Unlock()
	stopFileWatcherLocked()
	if logFile != nil {
		closeLogFileLocked()
		logFile = nil
	}
	logFileConsole = nil
}
