package wails

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/metrics"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
)

func TestHTTPAssetRoutesExposePrometheusMetricsEndpoint(t *testing.T) {
	skillmetrics.ResetForTesting()
	t.Cleanup(skillmetrics.ResetForTesting)
	skillmetrics.IncHostToolCallOutcome(skillmetrics.HostToolOutcomeOK)

	mux := http.NewServeMux()
	registerHTTPAssetRoutes(mux, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "asset fallback should not handle /metrics", http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, metrics.PrometheusMetricsPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, `host_tool_calls_total{outcome="ok"} 1`) {
		t.Fatalf("metrics endpoint did not expose host tool counters:\n%s", body)
	}
}

func TestHTTPLoggingAddsRequestFields(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := withHTTPLogging(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	requestID := rec.Header().Get(requestIDHeader)
	if requestID == "" {
		t.Fatal("missing X-Request-ID response header")
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(logs.String())), &payload); err != nil {
		t.Fatalf("unmarshal request log: %v\n%s", err, logs.String())
	}
	if got := payload["request_id"]; got != requestID {
		t.Fatalf("request_id = %#v, want %q", got, requestID)
	}
	if got := payload["request.method"]; got != http.MethodPost {
		t.Fatalf("request.method = %#v, want POST", got)
	}
	if got := payload["url.path"]; got != "/api/test" {
		t.Fatalf("url.path = %#v, want /api/test", got)
	}
	if got := payload["http.response.status_code"]; got != float64(http.StatusCreated) {
		t.Fatalf("http.response.status_code = %#v, want 201", got)
	}
	if got := payload[pkglogger.FieldTraceID]; got == "" {
		t.Fatalf("missing %s in %#v", pkglogger.FieldTraceID, payload)
	}
}

func TestNewHTTPAssetServerUsesConfiguredAddr(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_HTTP_ADDR", "127.0.0.1:0")

	result := NewHTTPAssetServer(httpAssetServerParams{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	server, ok := result.Runner.(*httpAssetServer)
	if !ok {
		t.Fatalf("Runner = %T, want *httpAssetServer", result.Runner)
	}
	if server.addr != "127.0.0.1:0" {
		t.Fatalf("addr = %q, want configured ephemeral bind", server.addr)
	}
}

func TestHTTPAssetServerRejectsNonLoopbackConfiguredAddr(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_HTTP_ADDR", "0.0.0.0:0")

	result := NewHTTPAssetServer(httpAssetServerParams{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	server, ok := result.Runner.(*httpAssetServer)
	if !ok {
		t.Fatalf("Runner = %T, want *httpAssetServer", result.Runner)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := server.Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("Run() error = %v, want loopback validation failure", err)
	}
}

func TestValidateHTTPAssetAddrAllowsOnlyLoopbackHosts(t *testing.T) {
	t.Parallel()

	for _, addr := range []string{"127.0.0.1:4511", "localhost:4511", "[::1]:4511"} {
		t.Run("allow_"+addr, func(t *testing.T) {
			if err := validateHTTPAssetAddr(addr); err != nil {
				t.Fatalf("validateHTTPAssetAddr(%q) error = %v", addr, err)
			}
		})
	}

	for _, addr := range []string{"0.0.0.0:4511", ":4511", "192.168.1.10:4511", "[::]:4511"} {
		t.Run("reject_"+addr, func(t *testing.T) {
			if err := validateHTTPAssetAddr(addr); err == nil || !strings.Contains(err.Error(), "loopback") {
				t.Fatalf("validateHTTPAssetAddr(%q) error = %v, want loopback validation failure", addr, err)
			}
		})
	}
}
