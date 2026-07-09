package tools

import (
	"strings"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	// editLogMessage* 是 patch_edit 工具阶段日志的稳定消息名，便于外部日志检索聚合。
	editLogMessageStageStarted   = "mcp-lsp patch_edit stage started"
	editLogMessageStageCompleted = "mcp-lsp patch_edit stage completed"
	editLogMessageStageFailed    = "mcp-lsp patch_edit stage failed"
	editLogMessageStageSkipped   = "mcp-lsp patch_edit stage skipped"
)

// editStageLogger 串起一次 edit 调用内的阶段日志。
// action/requestedPath 保存模型入参，filePath 在解析到真实路径后补齐，用于追踪路径解析问题。
type editStageLogger struct {
	action        string
	requestedPath string
	filePath      string
	started       time.Time
}

// newEditStageLogger 创建一次 edit 调用的阶段日志器。
func newEditStageLogger(action string, requestedPath string) *editStageLogger {
	return &editStageLogger{
		action:        strings.TrimSpace(action),
		requestedPath: strings.TrimSpace(requestedPath),
		started:       time.Now(),
	}
}

// setFilePath 记录解析后的真实文件路径；nil receiver 允许错误路径共用调用点。
func (l *editStageLogger) setFilePath(path string) {
	if l == nil {
		return
	}
	l.filePath = strings.TrimSpace(path)
}

// Started 记录阶段开始并返回开始时间，调用方必须把返回值传回 Completed/Failed。
func (l *editStageLogger) Started(stage string, attrs ...any) time.Time {
	stageStarted := time.Now()
	if l == nil {
		return stageStarted
	}
	pkglogger.Info(editLogMessageStageStarted, l.attrs(stage, "started", time.Time{}, attrs...)...)
	return stageStarted
}

// Completed 记录阶段完成和阶段耗时；stageStarted 为空时只输出整次调用耗时。
func (l *editStageLogger) Completed(stage string, stageStarted time.Time, attrs ...any) {
	if l == nil {
		return
	}
	pkglogger.Info(editLogMessageStageCompleted, l.attrs(stage, "completed", stageStarted, attrs...)...)
}

// Failed 记录阶段失败原因和耗时；错误文本进日志字段，不改写返回错误。
func (l *editStageLogger) Failed(stage string, stageStarted time.Time, err error, attrs ...any) {
	if l == nil {
		return
	}
	if err != nil {
		attrs = append(attrs, "error", err.Error())
	}
	pkglogger.Warn(editLogMessageStageFailed, l.attrs(stage, "failed", stageStarted, attrs...)...)
}

// Skipped 记录可预期的跳过分支，例如 manager 缺失或 no_change。
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

// attrs 统一补齐 edit 阶段日志字段，确保每条日志都带 action、stage 和路径。
func (l *editStageLogger) attrs(stage string, status string, stageStarted time.Time, attrs ...any) []any {
	action := l.action
	if action == "" {
		action = "unknown"
	}
	out := []any{
		"tool", "patch_edit",
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

// elapsedMillis 返回非负毫秒耗时，避免系统时钟回拨导致日志出现负数。
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
