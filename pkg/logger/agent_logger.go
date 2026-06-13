package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	agentFilesMu sync.Mutex
	agentFiles   = map[string]*os.File{}
)

// NewAgentLogger creates an *slog.Logger that writes to both the main log
// file and a per-agent log file (agent-{agentID}.log) in the same directory.
// The returned logger has "agent_id" pre-bound so every entry is tagged.
//
// If no main log file is open (tests, early init) it returns the global
// logger with agent_id bound.  If the per-agent file cannot be created it
// falls back to the global logger — main log flow is never interrupted.
// NewAgentLogger 创建代理日志器。
func NewAgentLogger(agentID string) *slog.Logger {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return getLogger()
	}

	logFileMu.Lock()
	mainFile := logFile
	mainPath := logFilePath
	mode := activeMode
	level := activeLevel
	console := logFileConsole
	logFileMu.Unlock()

	if mainFile == nil || mainPath == "" {
		return withTraceAttrs(context.Background(), getLogger().With("agent_id", agentID))
	}

	dir := filepath.Dir(mainPath)
	agentPath := filepath.Join(dir, fmt.Sprintf("agent-%s.log", agentID))

	agentFile := openOrReuseAgentFile(agentID, agentPath)
	if agentFile == nil {
		return getLogger().With("agent_id", agentID)
	}

	// Build a writer that fans out to console + main file + agent file.
	var out io.Writer
	if console != nil {
		out = io.MultiWriter(console, mainFile, agentFile)
	} else {
		out = io.MultiWriter(outputWriterForMode(mode), mainFile, agentFile)
	}

	l := slog.New(newHandler(mode, level, out))
	if globalProject != "" {
		l = l.With("project", globalProject)
	}
	return l.With("agent_id", agentID)
}

// openOrReuseAgentFile returns an existing open file handle for the agent,
// or opens a new one.  Returns nil on failure (caller falls back gracefully).
func openOrReuseAgentFile(agentID, path string) *os.File {
	agentFilesMu.Lock()
	defer agentFilesMu.Unlock()
	if f, ok := agentFiles[agentID]; ok {
		return f
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	agentFiles[agentID] = f
	return f
}

// CloseAgentLogger closes the per-agent log file for the given agent.
// Safe to call multiple times or with unknown IDs.
// CloseAgentLogger 关闭代理日志器。
func CloseAgentLogger(agentID string) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}
	agentFilesMu.Lock()
	defer agentFilesMu.Unlock()
	if f, ok := agentFiles[agentID]; ok {
		_ = f.Sync()
		_ = f.Close()
		delete(agentFiles, agentID)
	}
}

// closeAllAgentLoggers closes every open per-agent log file.
// Called by ShutdownFileHandler during process exit.
func closeAllAgentLoggers() {
	agentFilesMu.Lock()
	defer agentFilesMu.Unlock()
	for id, f := range agentFiles {
		_ = f.Sync()
		_ = f.Close()
		delete(agentFiles, id)
	}
}
