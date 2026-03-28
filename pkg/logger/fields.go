package logger

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

// Type aliases for packages that must not import log/slog directly.
type (
	Logger = slog.Logger
	Level  = slog.Level
	Attr   = slog.Attr
)

// Level re-exports.
const (
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
)

type ctxKey struct{}

// WithContext attaches a logger to the context.
func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext retrieves the logger from context, falling back to the global.
func FromContext(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
			return l
		}
	}
	return Get()
}

// Get returns the current global logger.
func Get() *slog.Logger { return defaultLogger.Load() }

// With returns a child logger with additional attributes.
func With(args ...any) *slog.Logger { return Get().With(args...) }

// Convenience logging functions.
func Info(msg string, args ...any)  { Get().Info(msg, args...) }
func Error(msg string, args ...any) { Get().Error(msg, args...) }
func Warn(msg string, args ...any)  { Get().Warn(msg, args...) }
func Debug(msg string, args ...any) { Get().Debug(msg, args...) }

func Infof(format string, args ...any)  { Get().Info(fmt.Sprintf(format, args...)) }
func Errorf(format string, args ...any) { Get().Error(fmt.Sprintf(format, args...)) }
func Warnf(format string, args ...any)  { Get().Warn(fmt.Sprintf(format, args...)) }
func Debugf(format string, args ...any) { Get().Debug(fmt.Sprintf(format, args...)) }

// Fatal logs an error then exits the process.
func Fatal(msg string, args ...any) {
	Get().Error(msg, args...)
	ShutdownFileHandler()
	os.Exit(1)
}

// CurrentLogFilePath returns the path of the active log file, or empty.
func CurrentLogFilePath() string {
	logMu.Lock()
	defer logMu.Unlock()
	if logFile == nil {
		return ""
	}
	return logFile.Name()
}

// IsDebugEnabled reports whether Debug messages are emitted.
func IsDebugEnabled() bool {
	return Get().Enabled(context.Background(), slog.LevelDebug)
}

// SetForTest replaces the global logger (test use only).
func SetForTest(l *slog.Logger) { storeLogger(l) }

// ResolveProjectLogDir is the exported version for app-level callers.
func ResolveProjectLogDir(homeDir, cwd string) (string, string) {
	return resolveProjectLogDir(homeDir, cwd)
}

// Standard field key constants (aligned with V2).
const (
	FieldTraceID    = "trace_id"
	FieldAgentID    = "agent_id"
	FieldThreadID   = "thread_id"
	FieldAction     = "action"
	FieldComponent  = "component"
	FieldModule     = "module"
	FieldError      = "error"
	FieldStatus     = "status"
	FieldLatencyMS  = "latency_ms"
	FieldDurationMS = "duration_ms"
	FieldCount      = "count"
	FieldPath       = "path"
	FieldMethod     = "method"
	FieldSource     = "source"
	FieldEventType  = "event_type"
	FieldToolName   = "tool_name"
	FieldAddr       = "addr"
	FieldPort       = "port"
	FieldCwd        = "cwd"
	FieldPID        = "pid"
	FieldState      = "state"
	FieldKey        = "key"
	FieldName       = "name"
	FieldID         = "id"
	FieldURL        = "url"
	FieldVersion    = "version"
	FieldTurnID     = "turn_id"
	FieldCommand    = "command"
	FieldExitCode   = "exit_code"
	FieldDecision   = "decision"
	FieldSeq        = "seq"
	FieldReqID      = "req_id"
	FieldCallID     = "call_id"
	FieldDAG        = "dag"
	FieldNode       = "node"
	FieldRunKey     = "run_key"
)
