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

// NewAgentLogger 创建绑定 agent_id 的日志器，并在文件日志已开启时额外写入 agent 专属文件。
// 主日志未打开或 agent 文件创建失败时回退到全局日志器，不能影响主日志链路。
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

	// agent logger 必须和主日志共享控制台/主文件，同时追加 agent 专属文件。
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

// openOrReuseAgentFile 复用或打开 agent 专属日志文件。
// 打开失败返回 nil，由调用方回退到主日志器。
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

// CloseAgentLogger 关闭指定 agent 的专属日志文件。
// 未知或已关闭 ID 会被忽略，便于 shutdown 路径重复调用。
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

// closeAllAgentLoggers 关闭全部 agent 专属日志文件，供进程退出和文件 handler 关闭时调用。
func closeAllAgentLoggers() {
	agentFilesMu.Lock()
	defer agentFilesMu.Unlock()
	for id, f := range agentFiles {
		_ = f.Sync()
		_ = f.Close()
		delete(agentFiles, id)
	}
}
