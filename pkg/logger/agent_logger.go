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

// NewAgentLogger 创建绑定 agent_id 的 runtime 日志器，并在文件日志已开启时额外写入 agent 专属文件。
func (r *Runtime) NewAgentLogger(agentID string) (*slog.Logger, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return r.getLogger(), nil
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
		return withTraceAttrs(context.Background(), r.getLogger().With("agent_id", agentID)), nil
	}

	dir := filepath.Dir(mainPath)
	agentPath := filepath.Join(dir, fmt.Sprintf("agent-%s.log", agentID))

	agentFile, err := r.openOrReuseAgentFile(agentID, agentPath)
	if err != nil {
		return nil, fmt.Errorf("new agent logger %q: %w", agentID, err)
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
	return l.With("agent_id", agentID), nil
}

// openOrReuseAgentFile 打开或复用 agent 专属日志文件，并在每次复用时重新收紧权限。
func (r *Runtime) openOrReuseAgentFile(agentID, path string) (*os.File, error) {
	r.agentFilesMu.Lock()
	defer r.agentFilesMu.Unlock()
	if f, ok := r.agentFiles[agentID]; ok {
		if err := f.Chmod(privateLogFileMode); err != nil {
			return nil, fmt.Errorf("tighten reused agent log file permissions: %w", err)
		}
		return f, nil
	}
	f, err := openPrivateAppendFile(path)
	if err != nil {
		return nil, err
	}
	r.agentFiles[agentID] = f
	return f, nil
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
