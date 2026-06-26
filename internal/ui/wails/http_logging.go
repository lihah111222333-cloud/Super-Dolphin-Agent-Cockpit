package wails

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// requestIDHeader 是 HTTP 请求链路 ID 的头名。
const requestIDHeader = "X-Request-ID"

// withHTTPLogging 给 HTTP asset server 注入 trace、request id 和访问日志。
func withHTTPLogging(logger *slog.Logger, next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	if logger == nil {
		logger = pkglogger.Get()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, _, requestID, err := httpTraceContext(r.Context(), r)
		if err != nil {
			pkglogger.FromContext(ctx).Error("http request trace init failed", pkglogger.FieldError, err)
			http.Error(w, "invalid trace context", http.StatusBadRequest)
			return
		}
		r = r.WithContext(pkglogger.WithContext(ctx, logger))
		w.Header().Set(requestIDHeader, requestID)

		startedAt := time.Now()
		rec := &httpLoggingResponseWriter{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		status := rec.statusCode()
		attrs := []any{
			"request_id", requestID,
			"request.method", r.Method,
			"url.path", r.URL.Path,
			"http.response.status_code", status,
			pkglogger.FieldEventDuration, time.Since(startedAt).Milliseconds(),
			"client.ip", clientIP(r),
			"http.response.body.bytes", rec.bytesWritten,
		}
		if userAgent := strings.TrimSpace(r.UserAgent()); userAgent != "" {
			attrs = append(attrs, "user_agent.original", userAgent)
		}
		if status >= http.StatusInternalServerError {
			attrs = append(attrs, pkglogger.FieldError, fmt.Errorf("http status %d", status))
			pkglogger.FromContext(r.Context()).Error("http request", attrs...)
			return
		}
		pkglogger.FromContext(r.Context()).Info("http request", attrs...)
	})
}

// httpTraceContext 从 traceparent 或 X-Request-ID 建立请求 trace 上下文。
func httpTraceContext(ctx context.Context, r *http.Request) (context.Context, string, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	requestID := strings.TrimSpace(r.Header.Get(requestIDHeader))
	traceID, spanID, err := parseHTTPTraceparent(r.Header.Get("traceparent"))
	if err != nil {
		return ctx, "", "", err
	}
	if traceID == "" {
		if validTraceID(requestID) {
			traceID = requestID
		} else {
			generated, err := pkglogger.NewTraceID()
			if err != nil {
				return ctx, "", "", err
			}
			traceID = generated
		}
	}
	if spanID == "" {
		generated, err := pkglogger.NewSpanID()
		if err != nil {
			return ctx, "", "", err
		}
		spanID = generated
	}
	if requestID == "" {
		requestID = traceID
	}
	return pkglogger.WithTraceContext(ctx, traceID, spanID, ""), traceID, requestID, nil
}

// parseHTTPTraceparent 解析 W3C traceparent，格式错误会阻断请求。
func parseHTTPTraceparent(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", nil
	}
	parts := strings.Split(raw, "-")
	if len(parts) != 4 {
		return "", "", fmt.Errorf("traceparent must have 4 dash-separated fields")
	}
	if parts[0] != "00" {
		return "", "", fmt.Errorf("unsupported traceparent version %q", parts[0])
	}
	traceID, spanID := parts[1], parts[2]
	if !validTraceID(traceID) {
		return "", "", fmt.Errorf("invalid trace id")
	}
	if !validSpanID(spanID) {
		return "", "", fmt.Errorf("invalid span id")
	}
	if len(parts[3]) != 2 || !isLowerHex(parts[3]) {
		return "", "", fmt.Errorf("invalid trace flags")
	}
	return traceID, spanID, nil
}

// validTraceID 判断 trace id 是否为非零小写十六进制 16 字节值。
// HTTP 入口用 bool 版本快速拒绝坏 traceparent，错误文本由调用方统一生成。
func validTraceID(value string) bool {
	return len(value) == 32 && isLowerHex(value) && !allZeroHex(value)
}

// validSpanID 判断 span id 是否为非零小写十六进制 8 字节值。
// HTTP 入口用 bool 版本快速拒绝坏 traceparent，错误文本由调用方统一生成。
func validSpanID(value string) bool {
	return len(value) == 16 && isLowerHex(value) && !allZeroHex(value)
}

// clientIP 从代理头或 RemoteAddr 提取客户端 IP。
func clientIP(r *http.Request) string {
	if forwardedFor := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwardedFor != "" {
		first := strings.TrimSpace(strings.Split(forwardedFor, ",")[0])
		if first != "" {
			return first
		}
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

// httpLoggingResponseWriter 包装 ResponseWriter 以记录状态码和响应字节数。
type httpLoggingResponseWriter struct {
	http.ResponseWriter
	status       int
	bytesWritten int64
}

// WriteHeader 只记录首次响应状态码，后续重复写头保持 net/http 原有行为。
func (w *httpLoggingResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Write 透传响应体写入并累计实际写出的字节数，未显式写头时按 200 记录。
func (w *httpLoggingResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytesWritten += int64(n)
	return n, err
}

// statusCode 返回最终响应状态码，未显式写头时按 200 处理。
func (w *httpLoggingResponseWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// Flush 在底层 writer 支持时透传刷新能力，避免日志包装破坏 streaming 响应。
func (w *httpLoggingResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack 在底层支持时透传连接接管，并把未写头的连接升级记录为 101。
func (w *httpLoggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijack")
	}
	if w.status == 0 {
		w.status = http.StatusSwitchingProtocols
	}
	return hijacker.Hijack()
}

// Push 仅在底层 writer 支持 HTTP/2 push 时透传，否则按标准 ErrNotSupported 返回。
func (w *httpLoggingResponseWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}
