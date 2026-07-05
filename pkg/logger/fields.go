package logger

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
)

// 日志类型别名让上层包无需直接导入 log/slog。
type (
	Logger         = slog.Logger
	Level          = slog.Level
	Attr           = slog.Attr
	Handler        = slog.Handler
	HandlerOptions = slog.HandlerOptions
)

// slog level 常量复导出，保持调用侧只依赖 pkg/logger。
const (
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
)

type ctxKey struct{}

// traceIDKey 是 context 中 trace ID 的私有 key，避免和调用方 key 冲突。
type traceIDKey struct{}

// spanIDKey 是 context 中当前 span ID 的私有 key。
type spanIDKey struct{}

// parentSpanIDKey 是 context 中父 span ID 的私有 key。
type parentSpanIDKey struct{}

// WithContext 把日志器写入 context；nil context 会先补成 Background。
func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ctxKey{}, l)
}

// WithTraceID 把 trace ID 写入 context，供 FromContext 自动补日志字段。
func WithTraceID(ctx context.Context, traceID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, traceIDKey{}, traceID)
}

// WithSpanID 把当前 span ID 写入 context。
func WithSpanID(ctx context.Context, spanID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, spanIDKey{}, spanID)
}

// WithParentSpanID 把父 span ID 写入 context。
func WithParentSpanID(ctx context.Context, parentSpanID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, parentSpanIDKey{}, parentSpanID)
}

// WithTraceIDs 一次性写入 trace ID 和当前 span ID。
func WithTraceIDs(ctx context.Context, traceID, spanID string) context.Context {
	ctx = WithTraceID(ctx, traceID)
	return WithSpanID(ctx, spanID)
}

// WithTraceContext 一次性写入 trace、span 和 parent span 上下文。
func WithTraceContext(ctx context.Context, traceID, spanID, parentSpanID string) context.Context {
	ctx = WithTraceIDs(ctx, traceID, spanID)
	return WithParentSpanID(ctx, parentSpanID)
}

// NewTraceID 生成 16 字节随机 trace ID，读取随机数失败时返回错误。
func NewTraceID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

// NewSpanID 生成 8 字节随机 span ID，读取随机数失败时返回错误。
func NewSpanID() (string, error) {
	var data [8]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

// WithChildTraceSpan 在已有 trace ID 下创建子 span，并保留原 span 作为 parent。
// context 未携带 trace ID 时保持原 context，不强行创建新 trace。
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

// TraceIDFromContext 从 context 读取 trace ID；nil 或类型不匹配时返回空字符串。
func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	traceID, _ := ctx.Value(traceIDKey{}).(string)
	return traceID
}

// SpanIDFromContext 从 context 读取 span ID；nil 或类型不匹配时返回空字符串。
func SpanIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	spanID, _ := ctx.Value(spanIDKey{}).(string)
	return spanID
}

// ParentSpanIDFromContext 从 context 读取 parent span ID；nil 或类型不匹配时返回空字符串。
func ParentSpanIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	parentSpanID, _ := ctx.Value(parentSpanIDKey{}).(string)
	return parentSpanID
}

// FromContext 从 context 读取日志器，并自动附加 trace 字段。
// context 缺失 logger 时使用当前全局日志器。
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

// Get 返回当前全局日志器。
func Get() *slog.Logger { return getLogger() }

// Get 返回 runtime 当前日志器。
func (r *Runtime) Get() *slog.Logger { return r.getLogger() }

// With 返回绑定固定字段的全局日志器派生实例。
func With(args ...any) *slog.Logger { return getLogger().With(args...) }

// Info 通过当前全局日志器写入 info 日志。
func Info(msg string, args ...any) { getLogger().Info(msg, args...) }

// Error 通过当前全局日志器写入 error 日志。
func Error(msg string, args ...any) { getLogger().Error(msg, args...) }

// Warn 通过当前全局日志器写入 warn 日志。
func Warn(msg string, args ...any) { getLogger().Warn(msg, args...) }

// Debug 通过当前全局日志器写入 debug 日志。
func Debug(msg string, args ...any) { getLogger().Debug(msg, args...) }

// Infof 先格式化消息再通过全局日志器写入 info 日志。
func Infof(format string, args ...any) { getLogger().Info(fmt.Sprintf(format, args...)) }

// Errorf 先格式化消息再通过全局日志器写入 error 日志。
func Errorf(format string, args ...any) { getLogger().Error(fmt.Sprintf(format, args...)) }

// Warnf 先格式化消息再通过全局日志器写入 warn 日志。
func Warnf(format string, args ...any) { getLogger().Warn(fmt.Sprintf(format, args...)) }

// Debugf 先格式化消息再通过全局日志器写入 debug 日志。
func Debugf(format string, args ...any) { getLogger().Debug(fmt.Sprintf(format, args...)) }

// Fatal 写入 error 级别日志后关闭 DB/file handler 并以 1 退出。
func Fatal(msg string, args ...any) {
	currentRuntime().Fatal(msg, args...)
}

// Fatal 写入 error 级别日志后关闭 runtime handler 并以 1 退出。
func (r *Runtime) Fatal(msg string, args ...any) {
	r.getLogger().Error(msg, args...)
	r.shutdownDBHandler()
	r.ShutdownFileHandler()
	r.exitFunc(1)
}

// Infow 兼容 zap 风格键值字段写入 info 日志。
func Infow(msg string, keysAndValues ...any) { getLogger().Info(msg, keysAndValues...) }

// Warnw 兼容 zap 风格键值字段写入 warn 日志。
func Warnw(msg string, keysAndValues ...any) { getLogger().Warn(msg, keysAndValues...) }

// Errorw 兼容 zap 风格键值字段写入 error 日志。
func Errorw(msg string, keysAndValues ...any) { getLogger().Error(msg, keysAndValues...) }

// Debugw 兼容 zap 风格键值字段写入 debug 日志。
func Debugw(msg string, keysAndValues ...any) { getLogger().Debug(msg, keysAndValues...) }

// InfoLevel 返回 slog info 级别，供调用方避免直接依赖 slog。
func InfoLevel() slog.Level { return slog.LevelInfo }

// WarnLevel 返回 slog warn 级别，供调用方避免直接依赖 slog。
func WarnLevel() slog.Level { return slog.LevelWarn }

// ErrorLevel 返回 slog error 级别，供调用方避免直接依赖 slog。
func ErrorLevel() slog.Level { return slog.LevelError }

// DebugLevel 返回 slog debug 级别，供调用方避免直接依赖 slog。
func DebugLevel() slog.Level { return slog.LevelDebug }

// CurrentLogFilePath 返回当前文件日志路径；未初始化文件 handler 时返回空字符串。
func CurrentLogFilePath() string {
	return currentRuntime().CurrentLogFilePath()
}

// CurrentLogFilePath 返回 runtime 当前文件日志路径；未初始化文件 handler 时返回空字符串。
func (r *Runtime) CurrentLogFilePath() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.logFilePath
}

// IsDebugEnabled 判断当前全局日志器是否启用 debug 级别。
func IsDebugEnabled() bool {
	return getLogger().Enabled(context.Background(), slog.LevelDebug)
}

// SetForTest 在测试中替换全局日志器，并同步 slog 默认 logger。
func SetForTest(l *slog.Logger) { currentRuntime().SetForTest(l) }

// SetForTest 在测试中替换 runtime 日志器，并同步 slog 默认 logger。
func (r *Runtime) SetForTest(l *slog.Logger) { r.storeLogger(l) }

// New 包装 slog.New，保持外部调用只依赖本包导出的别名。
func New(handler slog.Handler) *slog.Logger { return slog.New(handler) }

// NewTextHandler 包装 slog.NewTextHandler，供外部包通过 logger 包创建文本 handler。
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

// ResolveProjectLogDir 解析项目日志目录，并返回推断出的项目名。
func ResolveProjectLogDir(homeDir, cwd string) (string, string) {
	return resolveProjectLogDir(homeDir, cwd)
}

// ECS 字段名与项目内常用日志字段名。
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
