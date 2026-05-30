package wails

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/metrics"
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
