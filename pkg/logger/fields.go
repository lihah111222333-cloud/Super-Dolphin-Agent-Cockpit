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

func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ctxKey{}, l)
}

func WithTraceID(ctx context.Context, traceID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, traceIDKey{}, traceID)
}

func WithSpanID(ctx context.Context, spanID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, spanIDKey{}, spanID)
}

func WithParentSpanID(ctx context.Context, parentSpanID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, parentSpanIDKey{}, parentSpanID)
}

func WithTraceIDs(ctx context.Context, traceID, spanID string) context.Context {
	ctx = WithTraceID(ctx, traceID)
	return WithSpanID(ctx, spanID)
}

func WithTraceContext(ctx context.Context, traceID, spanID, parentSpanID string) context.Context {
	ctx = WithTraceIDs(ctx, traceID, spanID)
	return WithParentSpanID(ctx, parentSpanID)
}

func NewTraceID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func NewSpanID() (string, error) {
	var data [8]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

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

func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	traceID, _ := ctx.Value(traceIDKey{}).(string)
	return traceID
}

func SpanIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	spanID, _ := ctx.Value(spanIDKey{}).(string)
	return spanID
}

func ParentSpanIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	parentSpanID, _ := ctx.Value(parentSpanIDKey{}).(string)
	return parentSpanID
}

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

func Get() *slog.Logger { return getLogger() }

func With(args ...any) *slog.Logger { return getLogger().With(args...) }

func Info(msg string, args ...any)  { getLogger().Info(msg, args...) }
func Error(msg string, args ...any) { getLogger().Error(msg, args...) }
func Warn(msg string, args ...any)  { getLogger().Warn(msg, args...) }
func Debug(msg string, args ...any) { getLogger().Debug(msg, args...) }

func Infof(format string, args ...any)  { getLogger().Info(fmt.Sprintf(format, args...)) }
func Errorf(format string, args ...any) { getLogger().Error(fmt.Sprintf(format, args...)) }
func Warnf(format string, args ...any)  { getLogger().Warn(fmt.Sprintf(format, args...)) }
func Debugf(format string, args ...any) { getLogger().Debug(fmt.Sprintf(format, args...)) }

func Fatal(msg string, args ...any) {
	getLogger().Error(msg, args...)
	shutdownDBHandler()
	ShutdownFileHandler()
	exitFunc(1)
}

func Infow(msg string, keysAndValues ...any)  { getLogger().Info(msg, keysAndValues...) }
func Warnw(msg string, keysAndValues ...any)  { getLogger().Warn(msg, keysAndValues...) }
func Errorw(msg string, keysAndValues ...any) { getLogger().Error(msg, keysAndValues...) }
func Debugw(msg string, keysAndValues ...any) { getLogger().Debug(msg, keysAndValues...) }

func InfoLevel() slog.Level  { return slog.LevelInfo }
func WarnLevel() slog.Level  { return slog.LevelWarn }
func ErrorLevel() slog.Level { return slog.LevelError }
func DebugLevel() slog.Level { return slog.LevelDebug }

func CurrentLogFilePath() string {
	logFileMu.Lock()
	defer logFileMu.Unlock()
	return logFilePath
}

func IsDebugEnabled() bool {
	return getLogger().Enabled(context.Background(), slog.LevelDebug)
}

func SetForTest(l *slog.Logger) { storeLogger(l) }

func New(handler slog.Handler) *slog.Logger { return slog.New(handler) }

func NewTextHandler(out io.Writer, opts *slog.HandlerOptions) slog.Handler {
	return slog.NewTextHandler(out, opts)
}

func Any(key string, value any) Attr { return slog.Any(key, value) }

func String(key, value string) Attr { return slog.String(key, value) }

func Int(key string, value int) Attr { return slog.Int(key, value) }

func Int64(key string, value int64) Attr { return slog.Int64(key, value) }

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
