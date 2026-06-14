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

// watchLogFile 监听日志文件。
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

func stopFileWatcherLocked() {
	if fileWatcherStop != nil {
		close(fileWatcherStop)
		fileWatcherStop = nil
	}
}

func closeLogFileLocked() {
	if logFile != nil {
		_ = logFile.Sync()
		_ = logFile.Close()
	}
}

// ShutdownFileHandler 处理shutdown文件处理器。
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
