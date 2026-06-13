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

const requestIDHeader = "X-Request-ID"

// withHTTPLogging 设置HTTPlogging。
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

// httpTraceContext 处理HTTPtrace上下文。
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

// parseHTTPTraceparent 解析HTTPtraceparent。
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

func validTraceID(value string) bool {
	return len(value) == 32 && isLowerHex(value) && !allZeroHex(value)
}

func validSpanID(value string) bool {
	return len(value) == 16 && isLowerHex(value) && !allZeroHex(value)
}

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

type httpLoggingResponseWriter struct {
	http.ResponseWriter
	status       int
	bytesWritten int64
}

// WriteHeader 写入头部。
func (w *httpLoggingResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Write 写入桌面 UI 桥接。
func (w *httpLoggingResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytesWritten += int64(n)
	return n, err
}

func (w *httpLoggingResponseWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// Flush 刷出缓存的响应数据。
func (w *httpLoggingResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack 接管底层 HTTP 连接。
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

// Push 发送 HTTP/2 push 响应。
func (w *httpLoggingResponseWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}
