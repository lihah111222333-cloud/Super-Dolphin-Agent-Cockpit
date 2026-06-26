package logger

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"runtime/debug"
	"strings"
)

type errorEnricherHandler struct {
	next slog.Handler
}

// wrapErrorEnricherHandler 只在生产模式为 error 日志追加调用点和堆栈。
func wrapErrorEnricherHandler(next slog.Handler, mode Mode) slog.Handler {
	if next == nil || normalizeMode(mode) != Production {
		return next
	}
	return &errorEnricherHandler{next: next}
}

// Enabled 透传到底层 handler，保持 slog 级别判断一致。
func (h *errorEnricherHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

// Handle 在 error 级别及以上追加 source/function/stacktrace 后再写入底层 handler。
func (h *errorEnricherHandler) Handle(ctx context.Context, rec slog.Record) error {
	if rec.Level >= slog.LevelError {
		rec = enrichErrorRecord(rec)
	}
	return h.next.Handle(ctx, rec)
}

// WithAttrs 复制底层 handler 的字段绑定并保留错误增强包装。
func (h *errorEnricherHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &errorEnricherHandler{next: h.next.WithAttrs(attrs)}
}

// WithGroup 复制底层 handler 的分组绑定并保留错误增强包装。
func (h *errorEnricherHandler) WithGroup(name string) slog.Handler {
	return &errorEnricherHandler{next: h.next.WithGroup(name)}
}

// enrichErrorRecord 为错误日志补充调用位置和当前 goroutine 堆栈。
func enrichErrorRecord(rec slog.Record) slog.Record {
	if source, function := callerInfo(); source != "" || function != "" {
		if source != "" {
			rec.AddAttrs(slog.String(FieldSource, source))
		}
		if function != "" {
			rec.AddAttrs(slog.String(FieldFunction, function))
		}
	}
	if stack := strings.TrimSpace(string(debug.Stack())); stack != "" {
		rec.AddAttrs(slog.String(FieldStacktrace, stack))
	}
	return rec
}

// callerInfo 跳过 logger 包内部帧，返回第一处业务调用位置。
func callerInfo() (string, string) {
	pcs := make([]uintptr, 16)
	n := runtime.Callers(4, pcs)
	if n == 0 {
		return "", ""
	}
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if !strings.Contains(frame.Function, "/pkg/logger.") && !strings.Contains(frame.Function, "pkg/logger.") {
			return fmt.Sprintf("%s:%d", frame.File, frame.Line), frame.Function
		}
		if !more {
			break
		}
	}
	return "", ""
}
