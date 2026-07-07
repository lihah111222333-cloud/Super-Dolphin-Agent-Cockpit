package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// NewAgentLogger 创建绑定 agent_id 的日志器，并在文件日志已开启时额外写入 agent 专属文件。
// 主日志未打开或 agent 文件创建失败时回退到全局日志器，不能影响主日志链路。
func NewAgentLogger(agentID string) *slog.Logger {
	return currentRuntime().NewAgentLogger(agentID)
}

// NewAgentLogger 创建绑定 agent_id 的 runtime 日志器，并在文件日志已开启时额外写入 agent 专属文件。
func (r *Runtime) NewAgentLogger(agentID string) *slog.Logger {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return r.getLogger()
	}

	r.mu.Lock()
	mainFile := r.logFile
	mainPath := r.logFilePath
	mode := r.activeMode
	level := r.activeLevel
	console := r.logFileConsole
	project := r.project
	r.mu.Unlock()

	if mainFile == nil || mainPath == "" {
		return withTraceAttrs(context.Background(), r.getLogger().With("agent_id", agentID))
	}

	dir := filepath.Dir(mainPath)
	agentPath := filepath.Join(dir, fmt.Sprintf("agent-%s.log", agentID))

	agentFile := r.openOrReuseAgentFile(agentID, agentPath)
	if agentFile == nil {
		return r.getLogger().With("agent_id", agentID)
	}

	// agent logger 必须和主日志共享控制台/主文件，同时追加 agent 专属文件。
	var out io.Writer
	if console != nil {
		out = io.MultiWriter(console, mainFile, agentFile)
	} else {
		out = io.MultiWriter(outputWriterForMode(mode), mainFile, agentFile)
	}

	l := slog.New(r.newHandler(mode, level, out))
	if project != "" {
		l = l.With("project", project)
	}
	return l.With("agent_id", agentID)
}

func (r *Runtime) openOrReuseAgentFile(agentID, path string) *os.File {
	r.agentFilesMu.Lock()
	defer r.agentFilesMu.Unlock()
	if f, ok := r.agentFiles[agentID]; ok {
		return f
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	r.agentFiles[agentID] = f
	return f
}

// CloseAgentLogger 关闭指定 agent 的专属日志文件。
// 未知或已关闭 ID 会被忽略，便于 shutdown 路径重复调用。
func CloseAgentLogger(agentID string) {
	currentRuntime().CloseAgentLogger(agentID)
}

// CloseAgentLogger 关闭 runtime 中指定 agent 的专属日志文件。
func (r *Runtime) CloseAgentLogger(agentID string) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}
	r.agentFilesMu.Lock()
	defer r.agentFilesMu.Unlock()
	if f, ok := r.agentFiles[agentID]; ok {
		_ = f.Sync()
		_ = f.Close()
		delete(r.agentFiles, agentID)
	}
}

func (r *Runtime) closeAllAgentLoggers() {
	r.agentFilesMu.Lock()
	defer r.agentFilesMu.Unlock()
	for id, f := range r.agentFiles {
		_ = f.Sync()
		_ = f.Close()
		delete(r.agentFiles, id)
	}
}
