package tools

import (
	"strings"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	editLogMessageStageStarted   = "mcp-lsp edit stage started"
	editLogMessageStageCompleted = "mcp-lsp edit stage completed"
	editLogMessageStageFailed    = "mcp-lsp edit stage failed"
	editLogMessageStageSkipped   = "mcp-lsp edit stage skipped"
)

type editStageLogger struct {
	action        string
	requestedPath string
	filePath      string
	started       time.Time
}

func newEditStageLogger(action string, requestedPath string) *editStageLogger {
	return &editStageLogger{
		action:        strings.TrimSpace(action),
		requestedPath: strings.TrimSpace(requestedPath),
		started:       time.Now(),
	}
}

func (l *editStageLogger) setFilePath(path string) {
	if l == nil {
		return
	}
	l.filePath = strings.TrimSpace(path)
}

// Started 记录阶段开始并返回开始时间。
func (l *editStageLogger) Started(stage string, attrs ...any) time.Time {
	stageStarted := time.Now()
	if l == nil {
		return stageStarted
	}
	pkglogger.Info(editLogMessageStageStarted, l.attrs(stage, "started", time.Time{}, attrs...)...)
	return stageStarted
}

// Completed 记录阶段完成和耗时。
func (l *editStageLogger) Completed(stage string, stageStarted time.Time, attrs ...any) {
	if l == nil {
		return
	}
	pkglogger.Info(editLogMessageStageCompleted, l.attrs(stage, "completed", stageStarted, attrs...)...)
}

// Failed 记录阶段失败原因和耗时。
func (l *editStageLogger) Failed(stage string, stageStarted time.Time, err error, attrs ...any) {
	if l == nil {
		return
	}
	if err != nil {
		attrs = append(attrs, "error", err.Error())
	}
	pkglogger.Warn(editLogMessageStageFailed, l.attrs(stage, "failed", stageStarted, attrs...)...)
}

// Skipped 记录阶段跳过原因。
func (l *editStageLogger) Skipped(stage string, reason string, attrs ...any) {
	if l == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason != "" {
		attrs = append(attrs, "reason", reason)
	}
	pkglogger.Info(editLogMessageStageSkipped, l.attrs(stage, "skipped", time.Time{}, attrs...)...)
}

func (l *editStageLogger) attrs(stage string, status string, stageStarted time.Time, attrs ...any) []any {
	action := l.action
	if action == "" {
		action = "unknown"
	}
	out := []any{
		"tool", "edit",
		"action", action,
		"stage", strings.TrimSpace(stage),
		"status", strings.TrimSpace(status),
		"elapsed_ms", elapsedMillis(l.started),
	}
	if !stageStarted.IsZero() {
		out = append(out, "stage_elapsed_ms", elapsedMillis(stageStarted))
	}
	if l.requestedPath != "" {
		out = append(out, "requested_file_path", l.requestedPath)
	}
	if l.filePath != "" {
		out = append(out, "file_path", l.filePath)
	}
	return append(out, attrs...)
}

func elapsedMillis(start time.Time) int64 {
	if start.IsZero() {
		return 0
	}
	elapsed := time.Since(start)
	if elapsed < 0 {
		return 0
	}
	return elapsed.Milliseconds()
}
