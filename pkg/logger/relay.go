package logger

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"
)

type RelayPayload struct {
	SourceProcess string         `json:"source_process,omitempty"`
	Level         string         `json:"level"`
	Msg           string         `json:"msg"`
	Fields        map[string]any `json:"fields,omitempty"`
}

type RelayHook func(context.Context, RelayPayload)

type relayHookHolder struct {
	hook RelayHook
}

type relayDisabledKey struct{}

var relayHookState atomic.Value

func init() {
	relayHookState.Store(relayHookHolder{})
}

// SetRelayHook 设置relayhook。
func SetRelayHook(h RelayHook) {
	relayHookState.Store(relayHookHolder{hook: h})
}

// ClearRelayHook 清理relayhook。
func ClearRelayHook() {
	relayHookState.Store(relayHookHolder{})
}

func currentRelayHook() RelayHook {
	holder, _ := relayHookState.Load().(relayHookHolder)
	return holder.hook
}

// WithRelayDisabled 设置relaydisabled。
func WithRelayDisabled(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, relayDisabledKey{}, true)
}

func relayDisabled(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	disabled, _ := ctx.Value(relayDisabledKey{}).(bool)
	return disabled
}

type relayHandler struct {
	next   slog.Handler
	attrs  []slog.Attr
	groups []string
}

func wrapRelayHandler(next slog.Handler) slog.Handler {
	if next == nil {
		return next
	}
	return &relayHandler{next: next}
}

// Enabled 判断日志是否启用。
func (h *relayHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

// Handle 处理日志请求。
func (h *relayHandler) Handle(ctx context.Context, rec slog.Record) error {
	err := h.next.Handle(ctx, rec)
	if relayDisabled(ctx) {
		return err
	}
	hook := currentRelayHook()
	if hook == nil {
		return err
	}
	payload := RelayPayload{
		Level:  relayLevelString(rec.Level),
		Msg:    strings.TrimSpace(rec.Message),
		Fields: relayRecordFields(h.groups, h.attrs, rec),
	}
	hook(ctx, payload)
	return err
}

// WithAttrs 设置attrs。
func (h *relayHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cloned := append([]slog.Attr{}, h.attrs...)
	cloned = append(cloned, attrs...)
	groups := append([]string{}, h.groups...)
	return &relayHandler{next: h.next.WithAttrs(attrs), attrs: cloned, groups: groups}
}

// WithGroup 设置group。
func (h *relayHandler) WithGroup(name string) slog.Handler {
	groups := append([]string{}, h.groups...)
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		groups = append(groups, trimmed)
	}
	attrs := append([]slog.Attr{}, h.attrs...)
	return &relayHandler{next: h.next.WithGroup(name), attrs: attrs, groups: groups}
}

func relayLevelString(level slog.Level) string {
	switch {
	case level >= slog.LevelError:
		return "ERROR"
	case level >= slog.LevelWarn:
		return "WARN"
	case level <= slog.LevelDebug:
		return "DEBUG"
	default:
		return "INFO"
	}
}

func relayRecordFields(groups []string, attrs []slog.Attr, rec slog.Record) map[string]any {
	fields := map[string]any{}
	if !rec.Time.IsZero() {
		fields["origin_time"] = rec.Time.Format(time.RFC3339Nano)
	}
	prefix := strings.Join(groups, ".")
	for _, attr := range attrs {
		relayAppendAttr(fields, prefix, attr)
	}
	rec.Attrs(func(attr slog.Attr) bool {
		relayAppendAttr(fields, prefix, attr)
		return true
	})
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// relayAppendAttr 处理relayappendattr。
func relayAppendAttr(dst map[string]any, prefix string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}
	if attr.Value.Kind() == slog.KindGroup {
		nextPrefix := prefix
		if key := strings.TrimSpace(attr.Key); key != "" {
			if nextPrefix != "" {
				nextPrefix += "."
			}
			nextPrefix += key
		}
		for _, child := range attr.Value.Group() {
			relayAppendAttr(dst, nextPrefix, child)
		}
		return
	}
	key := strings.TrimSpace(attr.Key)
	if key == "" {
		return
	}
	if prefix != "" {
		key = prefix + "." + key
	}
	dst[key] = relayValueAny(attr.Value)
}

// relayValueAny 处理relay值任意值。
func relayValueAny(value slog.Value) any {
	switch value.Kind() {
	case slog.KindString:
		return value.String()
	case slog.KindInt64:
		return value.Int64()
	case slog.KindUint64:
		return value.Uint64()
	case slog.KindFloat64:
		return value.Float64()
	case slog.KindBool:
		return value.Bool()
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindTime:
		return value.Time().Format(time.RFC3339Nano)
	case slog.KindAny:
		return relayAnyValue(value.Any())
	default:
		return fmt.Sprint(value)
	}
}

func relayAnyValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case error:
		return typed.Error()
	case fmt.Stringer:
		return typed.String()
	default:
		return value
	}
}
