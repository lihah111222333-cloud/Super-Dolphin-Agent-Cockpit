package logger

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"
)

// RelayPayload 是日志 relay hook 接收的稳定 payload。
type RelayPayload struct {
	SourceProcess string         `json:"source_process,omitempty"` // 来源进程，可由 hook 调用方补充。
	Level         string         `json:"level"`                    // relay 使用的大写日志级别。
	Msg           string         `json:"msg"`                      // slog record message。
	Fields        map[string]any `json:"fields,omitempty"`         // 展平后的 slog attrs 和 record attrs。
}

// RelayHook 接收已写入本地日志后的 relay payload。
type RelayHook func(context.Context, RelayPayload)

// relayHookHolder 包装 hook，便于 atomic.Value 存储固定具体类型。
type relayHookHolder struct {
	hook RelayHook
}

// relayDisabledKey 是 context 中禁用 relay 的私有 key。
type relayDisabledKey struct{}

var relayHookState atomic.Value

func init() {
	relayHookState.Store(relayHookHolder{})
}

// SetRelayHook 安装全局 relay hook；后续日志写入本地后会同步调用它。
func SetRelayHook(h RelayHook) {
	relayHookState.Store(relayHookHolder{hook: h})
}

// ClearRelayHook 清空全局 relay hook。
func ClearRelayHook() {
	relayHookState.Store(relayHookHolder{})
}

// currentRelayHook 返回当前安装的 relay hook。
func currentRelayHook() RelayHook {
	holder, _ := relayHookState.Load().(relayHookHolder)
	return holder.hook
}

// WithRelayDisabled 在 context 中标记本次日志不触发 relay。
func WithRelayDisabled(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, relayDisabledKey{}, true)
}

// relayDisabled 判断当前 context 是否禁用了 relay。
func relayDisabled(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	disabled, _ := ctx.Value(relayDisabledKey{}).(bool)
	return disabled
}

// relayHandler 在底层 handler 写入后把日志记录同步给 relay hook。
type relayHandler struct {
	next   slog.Handler
	attrs  []slog.Attr
	groups []string
}

// wrapRelayHandler 为 handler 增加 relay 能力；nil handler 保持 nil。
func wrapRelayHandler(next slog.Handler) slog.Handler {
	if next == nil {
		return next
	}
	return &relayHandler{next: next}
}

// Enabled 透传到底层 handler，保持 slog 级别判断一致。
func (h *relayHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

// Handle 先写本地日志，再在未禁用 relay 且 hook 存在时发送 payload。
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

// WithAttrs 复制已绑定字段，确保 relay payload 包含 handler attrs。
func (h *relayHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cloned := append([]slog.Attr{}, h.attrs...)
	cloned = append(cloned, attrs...)
	groups := append([]string{}, h.groups...)
	return &relayHandler{next: h.next.WithAttrs(attrs), attrs: cloned, groups: groups}
}

// WithGroup 记录分组路径，确保 relay payload 的字段 key 与 slog group 对齐。
func (h *relayHandler) WithGroup(name string) slog.Handler {
	groups := append([]string{}, h.groups...)
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		groups = append(groups, trimmed)
	}
	attrs := append([]slog.Attr{}, h.attrs...)
	return &relayHandler{next: h.next.WithGroup(name), attrs: attrs, groups: groups}
}

// relayLevelString 将 slog level 映射成 relay 约定的大写级别。
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

// relayRecordFields 合并 handler attrs 与 record attrs，并展开 group key。
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

// relayAppendAttr 递归展开 attr，并用 prefix 保留 slog group 层级。
func relayAppendAttr(dst map[string]any, prefix string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	attr = sanitizeLogAttr(attr)
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

// relayValueAny 将 slog.Value 转成 relay payload 可 JSON 编码的值。
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

// relayAnyValue 处理 Any 值中的 error 和 Stringer，避免直接暴露复杂对象。
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
