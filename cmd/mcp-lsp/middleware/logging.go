package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// Handler 是 MCP-LSP 工具处理函数的统一签名。
type Handler func(context.Context, json.RawMessage) (any, error)

// Middleware 包装 Handler 以附加横切关注点。
type Middleware func(Handler) Handler

const slowActionLogThreshold = 5 * time.Second

// Chain 把多个中间件按顺序串起来。
func Chain(handler Handler, middlewares ...Middleware) Handler {
	wrapped := handler
	for idx := range slices.Backward(middlewares) {
		if middlewares[idx] == nil {
			continue
		}
		wrapped = middlewares[idx](wrapped)
	}
	return wrapped
}

// Logging 记录请求耗时和错误。
func Logging(logger *slog.Logger, toolName ...string) Middleware {
	if logger == nil {
		logger = pkglogger.Get()
	}
	name := ""
	if len(toolName) > 0 {
		name = strings.TrimSpace(toolName[0])
	}
	return func(next Handler) Handler {
		return func(ctx context.Context, params json.RawMessage) (any, error) {
			start := time.Now()
			action := loggingAction(params)
			slowTimer := time.AfterFunc(slowActionLogThreshold, func() {
				logger.WarnContext(ctx, "mcp-lsp action still running",
					pkglogger.String("tool", name),
					pkglogger.String("action", action),
					pkglogger.Int64("duration_ms", time.Since(start).Milliseconds()),
					pkglogger.String("status", "running_slow"),
				)
			})
			defer slowTimer.Stop()
			logger.DebugContext(ctx, "mcp-lsp request",
				pkglogger.String("tool", name),
				pkglogger.String("action", action),
				pkglogger.Int("request_bytes", len(params)),
				pkglogger.String("status", "started"),
			)
			result, err := next(ctx, params)
			if err != nil {
				code, retryable, _, meta := common.ClassifyToolError(name, err)
				attrs := loggingFailureAttrs(name, start, code, retryable, meta)
				attrs = append(attrs, pkglogger.String("action", action))
				logger.WarnContext(ctx, "mcp-lsp request failed",
					append(attrs, pkglogger.String("error_kind", loggingErrorKind(err)))...,
				)
				return result, err
			}
			responseBytes := 0
			if raw, marshalErr := json.Marshal(result); marshalErr == nil {
				responseBytes = len(raw)
			}
			logger.DebugContext(ctx, "mcp-lsp response",
				pkglogger.String("tool", name),
				pkglogger.String("action", action),
				pkglogger.Int64("duration_ms", time.Since(start).Milliseconds()),
				pkglogger.Int("response_bytes", responseBytes),
				pkglogger.String("status", "succeeded"),
			)
			return result, nil
		}
	}
}

// loggingAction 只提取可安全记录的短 action，不记录原始工具参数。
func loggingAction(params json.RawMessage) string {
	var payload struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		return ""
	}
	return safeLogToken(payload.Action)
}

// loggingFailureAttrs 构造失败日志的稳定字段，按错误分类附加经过白名单校验的归因信息。
func loggingFailureAttrs(tool string, start time.Time, code string, retryable bool, meta map[string]any) []any {
	attrs := loggingBaseAttrs(tool, start, code, retryable)
	attrs = append(attrs, loggingTimeoutAttrs(meta)...)
	switch code {
	case "language_unsupported":
		attrs = append(attrs, loggingLanguageAttrs(meta)...)
	case "capability_unsupported":
		attrs = append(attrs, loggingCapabilityAttrs(meta)...)
	}
	return attrs
}

func loggingBaseAttrs(tool string, start time.Time, code string, retryable bool) []any {
	return []any{
		pkglogger.String("tool", tool),
		pkglogger.Int64("duration_ms", time.Since(start).Milliseconds()),
		pkglogger.String("status", "failed"),
		slog.String("error_code", code),
		slog.Bool("retryable", retryable),
	}
}

func loggingTimeoutAttrs(meta map[string]any) []any {
	attrs := make([]any, 0, 2)
	if timeoutMillis, ok := meta["timeout_ms"].(int64); ok && timeoutMillis > 0 {
		attrs = append(attrs, slog.Int64("timeout_ms", timeoutMillis))
	}
	if maxOutstanding, ok := meta["max_outstanding"].(int); ok && maxOutstanding > 0 {
		attrs = append(attrs, slog.Int("max_outstanding", maxOutstanding))
	}
	return attrs
}

func loggingLanguageAttrs(meta map[string]any) []any {
	return []any{
		slog.String("requested_language", safeLogToken(metaString(meta, "requested_language"))),
		slog.String("detected_language", safeLogToken(metaString(meta, "detected_language"))),
		slog.String("resolved_language", safeLogToken(metaString(meta, "resolved_language"))),
		slog.String("file_extension", safeLogExtension(metaString(meta, "file_extension"))),
		slog.String("adapter_status", safeLogToken(metaString(meta, "adapter_status"))),
	}
}

// loggingCapabilityAttrs 追加 LSP 能力错误的服务端归因字段，拒绝路径和不受限文本。
func loggingCapabilityAttrs(meta map[string]any) []any {
	attrs := make([]any, 0, 7)
	if method := safeLSPMethod(metaString(meta, "lsp_method")); method != "" {
		attrs = append(attrs, slog.String("lsp_method", method))
	}
	if executable := safeLogToken(metaString(meta, "server_executable")); executable != "" {
		attrs = append(attrs, slog.String("server_executable", executable))
	}
	if serverName := safeLogToken(metaString(meta, "server_name")); serverName != "" {
		attrs = append(attrs, slog.String("server_name", serverName))
	}
	if serverVersion := safeLogToken(metaString(meta, "server_version")); serverVersion != "" {
		attrs = append(attrs, slog.String("server_version", serverVersion))
	}
	if pid := metaPositiveInt(meta, "server_pid"); pid > 0 {
		attrs = append(attrs, slog.Int("server_pid", pid))
	}
	if known, ok := meta["capabilities_known"].(bool); ok {
		attrs = append(attrs, slog.Bool("capabilities_known", known))
	}
	if snapshot := safeCapabilitySnapshot(metaString(meta, "capability_snapshot")); snapshot != "" {
		attrs = append(attrs, slog.String("capability_snapshot", snapshot))
	}
	return attrs
}

func metaString(meta map[string]any, key string) string {
	if value, ok := meta[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

// metaPositiveInt 读取正整数型归因字段，拒绝负数和超出平台 int 范围的值。
func metaPositiveInt(meta map[string]any, key string) int {
	switch value := meta[key].(type) {
	case int:
		if value > 0 {
			return value
		}
	case int64:
		if value > 0 && value <= int64(^uint(0)>>1) {
			return int(value)
		}
	case json.Number:
		if parsed, err := value.Int64(); err == nil && parsed > 0 && parsed <= int64(^uint(0)>>1) {
			return int(parsed)
		}
	}
	return 0
}

// safeLogToken 仅保留短的枚举型日志 token，拒绝路径、空白和控制字符。
func safeLogToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 96 {
		return ""
	}
	for _, char := range value {
		if !safeLogTokenChar(char) {
			return ""
		}
	}
	return value
}

// safeLogTokenChar 判断字符是否属于日志 token 的固定安全字符集。
func safeLogTokenChar(char rune) bool {
	return strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-+", char)
}

func safeLogExtension(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 32 || !strings.HasPrefix(value, ".") {
		return ""
	}
	return safeLogToken(value)
}

// safeLSPMethod 只接受已知 LSP 命名空间下的短方法名，防止日志写入原始参数。
func safeLSPMethod(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 96 || strings.ContainsAny(value, " 	\r\n") {
		return ""
	}
	if strings.HasPrefix(value, "textDocument/") || strings.HasPrefix(value, "workspace/") ||
		strings.HasPrefix(value, "callHierarchy/") || strings.HasPrefix(value, "typeHierarchy/") {
		return value
	}
	return ""
}

// safeCapabilitySnapshot 校验能力快照只包含固定的小写键值字符，避免日志注入任意文本。
func safeCapabilitySnapshot(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 2048 {
		return ""
	}
	for _, char := range value {
		if !safeCapabilitySnapshotChar(char) {
			return ""
		}
	}
	return value
}

// safeCapabilitySnapshotChar 判断字符是否属于能力快照允许的固定字符集。
func safeCapabilitySnapshotChar(char rune) bool {
	switch {
	case char >= 'a' && char <= 'z':
		return true
	case char >= '0' && char <= '9':
		return true
	case char == '_' || char == '=' || char == ',':
		return true
	default:
		return false
	}
}

func loggingErrorKind(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "handler_error"
	}
}
