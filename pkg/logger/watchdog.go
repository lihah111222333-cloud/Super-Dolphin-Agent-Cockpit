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

func startFileWatcher(path string) {
	logMu.Lock()
	stopFileWatcherLocked()
	stop := make(chan struct{})
	fileWatcherStop = stop
	logMu.Unlock()
	go watchLogFile(path, stop)
}

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
				continue
			}
			logMu.Lock()
			closeLogFileLocked()
			logFile = f
			logMu.Unlock()
			rebuildLoggerWithFile(f)
			Info("log file watchdog: reopened", FieldPath, path)
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

// ShutdownFileHandler releases the log file and stops the watchdog.
func ShutdownFileHandler() {
	logMu.Lock()
	defer logMu.Unlock()
	stopFileWatcherLocked()
	if logFile != nil {
		closeLogFileLocked()
		logFile = nil
	}
}
