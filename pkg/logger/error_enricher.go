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

func wrapErrorEnricherHandler(next slog.Handler, mode Mode) slog.Handler {
	if next == nil || normalizeMode(mode) != Production {
		return next
	}
	return &errorEnricherHandler{next: next}
}

// Enabled 判断日志是否启用。
func (h *errorEnricherHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

// Handle 处理日志请求。
func (h *errorEnricherHandler) Handle(ctx context.Context, rec slog.Record) error {
	if rec.Level >= slog.LevelError {
		rec = enrichErrorRecord(rec)
	}
	return h.next.Handle(ctx, rec)
}

// WithAttrs 设置attrs。
func (h *errorEnricherHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &errorEnricherHandler{next: h.next.WithAttrs(attrs)}
}

// WithGroup 设置group。
func (h *errorEnricherHandler) WithGroup(name string) slog.Handler {
	return &errorEnricherHandler{next: h.next.WithGroup(name)}
}

// enrichErrorRecord 补充错误记录。
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

// callerInfo 处理callerinfo。
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
