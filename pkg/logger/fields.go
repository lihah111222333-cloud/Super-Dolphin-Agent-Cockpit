package logger

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
)

// Type aliases for packages that must not import log/slog directly.
type (
	Logger         = slog.Logger
	Level          = slog.Level
	Attr           = slog.Attr
	Handler        = slog.Handler
	HandlerOptions = slog.HandlerOptions
)

// Level re-exports.
const (
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
)

type ctxKey struct{}

type traceIDKey struct{}
type spanIDKey struct{}
type parentSpanIDKey struct{}

// WithContext 把日志器写入 context。
func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ctxKey{}, l)
}

// WithTraceID 把 trace ID 写入 context。
func WithTraceID(ctx context.Context, traceID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, traceIDKey{}, traceID)
}

// WithSpanID 把 span ID 写入 context。
func WithSpanID(ctx context.Context, spanID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, spanIDKey{}, spanID)
}

// WithParentSpanID 把 parent span ID 写入 context。
func WithParentSpanID(ctx context.Context, parentSpanID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, parentSpanIDKey{}, parentSpanID)
}

// WithTraceIDs 把 trace/span ID 写入 context。
func WithTraceIDs(ctx context.Context, traceID, spanID string) context.Context {
	ctx = WithTraceID(ctx, traceID)
	return WithSpanID(ctx, spanID)
}

// WithTraceContext 把完整 trace 上下文写入 context。
func WithTraceContext(ctx context.Context, traceID, spanID, parentSpanID string) context.Context {
	ctx = WithTraceIDs(ctx, traceID, spanID)
	return WithParentSpanID(ctx, parentSpanID)
}

// NewTraceID 创建traceID。
func NewTraceID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

// NewSpanID 创建spanID。
func NewSpanID() (string, error) {
	var data [8]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

// WithChildTraceSpan 设置childtracespan。
func WithChildTraceSpan(ctx context.Context) (context.Context, string, error) {
	traceID := TraceIDFromContext(ctx)
	if traceID == "" {
		return ctx, "", nil
	}
	parentSpanID := SpanIDFromContext(ctx)
	spanID, err := NewSpanID()
	if err != nil {
		return nil, "", err
	}
	return WithTraceContext(ctx, traceID, spanID, parentSpanID), spanID, nil
}

// TraceIDFromContext 从 context 读取 trace ID。
func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	traceID, _ := ctx.Value(traceIDKey{}).(string)
	return traceID
}

// SpanIDFromContext 从 context 读取 span ID。
func SpanIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	spanID, _ := ctx.Value(spanIDKey{}).(string)
	return spanID
}

// ParentSpanIDFromContext 从 context 读取 parent span ID。
func ParentSpanIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	parentSpanID, _ := ctx.Value(parentSpanIDKey{}).(string)
	return parentSpanID
}

// FromContext 从 context 读取日志器，没有时返回全局日志器。
func FromContext(ctx context.Context) *slog.Logger {
	base := getLogger()
	if ctx == nil {
		return base
	}
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		base = l
	}
	return withTraceAttrs(ctx, base)
}

// Get 返回全局日志器。
func Get() *slog.Logger { return getLogger() }

// With 返回带固定字段的日志器。
func With(args ...any) *slog.Logger { return getLogger().With(args...) }

// Info 写入 info 日志。
func Info(msg string, args ...any) { getLogger().Info(msg, args...) }

// Error 写入 error 日志。
func Error(msg string, args ...any) { getLogger().Error(msg, args...) }

// Warn 写入 warn 日志。
func Warn(msg string, args ...any) { getLogger().Warn(msg, args...) }

// Debug 写入 debug 日志。
func Debug(msg string, args ...any) { getLogger().Debug(msg, args...) }

// Infof 按格式写入 info 日志。
func Infof(format string, args ...any) { getLogger().Info(fmt.Sprintf(format, args...)) }

// Errorf 按格式写入 error 日志。
func Errorf(format string, args ...any) { getLogger().Error(fmt.Sprintf(format, args...)) }

// Warnf 按格式写入 warn 日志。
func Warnf(format string, args ...any) { getLogger().Warn(fmt.Sprintf(format, args...)) }

// Debugf 按格式写入 debug 日志。
func Debugf(format string, args ...any) { getLogger().Debug(fmt.Sprintf(format, args...)) }

// Fatal 写入 fatal 日志后退出。
func Fatal(msg string, args ...any) {
	getLogger().Error(msg, args...)
	shutdownDBHandler()
	ShutdownFileHandler()
	exitFunc(1)
}

// Infow 按键值字段写入 info 日志。
func Infow(msg string, keysAndValues ...any) { getLogger().Info(msg, keysAndValues...) }

// Warnw 按键值字段写入 warn 日志。
func Warnw(msg string, keysAndValues ...any) { getLogger().Warn(msg, keysAndValues...) }

// Errorw 按键值字段写入 error 日志。
func Errorw(msg string, keysAndValues ...any) { getLogger().Error(msg, keysAndValues...) }

// Debugw 按键值字段写入 debug 日志。
func Debugw(msg string, keysAndValues ...any) { getLogger().Debug(msg, keysAndValues...) }

// InfoLevel 返回 info 级别。
func InfoLevel() slog.Level { return slog.LevelInfo }

// WarnLevel 返回 warn 级别。
func WarnLevel() slog.Level { return slog.LevelWarn }

// ErrorLevel 返回 error 级别。
func ErrorLevel() slog.Level { return slog.LevelError }

// DebugLevel 返回 debug 级别。
func DebugLevel() slog.Level { return slog.LevelDebug }

// CurrentLogFilePath 返回当前日志文件路径。
func CurrentLogFilePath() string {
	logFileMu.Lock()
	defer logFileMu.Unlock()
	return logFilePath
}

// IsDebugEnabled 判断 debug 日志是否启用。
func IsDebugEnabled() bool {
	return getLogger().Enabled(context.Background(), slog.LevelDebug)
}

// SetForTest 在测试中替换全局日志器。
func SetForTest(l *slog.Logger) { storeLogger(l) }

// New 创建 slog 日志器。
func New(handler slog.Handler) *slog.Logger { return slog.New(handler) }

// NewTextHandler 创建文本格式 slog handler。
func NewTextHandler(out io.Writer, opts *slog.HandlerOptions) slog.Handler {
	return slog.NewTextHandler(out, opts)
}

// Any 创建任意值日志字段。
func Any(key string, value any) Attr { return slog.Any(key, value) }

// String 创建字符串日志字段。
func String(key, value string) Attr { return slog.String(key, value) }

// Int 创建 int 日志字段。
func Int(key string, value int) Attr { return slog.Int(key, value) }

// Int64 创建 int64 日志字段。
func Int64(key string, value int64) Attr { return slog.Int64(key, value) }

// ResolveProjectLogDir 解析项目日志目录。
func ResolveProjectLogDir(homeDir, cwd string) (string, string) {
	return resolveProjectLogDir(homeDir, cwd)
}

const (
	FieldTimestamp          = "@timestamp"
	FieldLogLevel           = "log.level"
	FieldServiceName        = "service.name"
	FieldServiceVersion     = "service.version"
	FieldEnv                = "env"
	FieldECSTraceID         = "trace.id"
	FieldECSSpanID          = "span.id"
	FieldECSParentSpanID    = "span.parent_id"
	FieldECSErrorMessage    = "error.message"
	FieldECSErrorStackTrace = "error.stack_trace"
	FieldEventDuration      = "event.duration"

	FieldTraceID      = "trace_id"
	FieldSpanID       = "span_id"
	FieldParentSpanID = "parent_span_id"
	FieldFunction     = "function"
	FieldStacktrace   = "stacktrace"
	FieldAgentID      = "agent_id"
	FieldGatewayID    = "gateway_id"
	FieldThreadID     = "thread_id"
	FieldAction       = "action"
	FieldComponent    = "component"
	FieldModule       = "module"
	FieldError        = "error"
	FieldStatus       = "status"
	FieldLatencyMS    = "latency_ms"
	FieldCount        = "count"
	FieldPath         = "path"
	FieldMethod       = "method"
	FieldUserID       = "user_id"
	FieldSource       = "source"
	FieldEventType    = "event_type"
	FieldToolName     = "tool_name"
	FieldDurationMS   = "duration_ms"
	FieldAddr         = "addr"
	FieldConn         = "conn"
	FieldRemote       = "remote"
	FieldKey          = "key"
	FieldSkill        = "skill"
	FieldOrigin       = "origin"
	FieldMax          = "max"
	FieldDataLen      = "data_len"
	FieldParamsLen    = "params_len"
	FieldID           = "id"
	FieldName         = "name"
	FieldCwd          = "cwd"
	FieldRunKey       = "run_key"
	FieldRoot         = "root"
	FieldBytes        = "bytes"
	FieldLen          = "len"
	FieldListen       = "listen"
	FieldPort         = "port"
	FieldVersion      = "version"
	FieldTopic        = "topic"
	FieldSeq          = "seq"
	FieldDAG          = "dag"
	FieldNode         = "node"
	FieldURL          = "url"
	FieldVarsSet      = "vars_set"
	FieldReqID        = "req_id"
	FieldCallID       = "call_id"
	FieldRaw          = "raw"
	FieldTurnID       = "turn_id"
	FieldCommand      = "command"
	FieldRunID        = "run_id"
	FieldExitCode     = "exit_code"
	FieldCardKey      = "card_key"
	FieldLanguage     = "language"
	FieldSubscriber   = "subscriber"
	FieldFilter       = "filter"
	FieldDecision     = "decision"
	FieldPID          = "pid"
	FieldState        = "state"
)
