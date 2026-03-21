package codexapp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"golang.org/x/net/websocket"
)

type stubRuntimeReporter struct {
	last  contract.RuntimeReport
	calls int
	err   error
}

func (s *stubRuntimeReporter) ReportRuntime(_ context.Context, report contract.RuntimeReport) error {
	s.calls++
	s.last = report
	return s.err
}

func TestNewDriverUsesEnvServerURLAndName(t *testing.T) {
	t.Setenv("CODEX_APP_SERVER_URL", " ws://127.0.0.1:9123 ")
	got, ok := newDriver(nil, nil, nil, nil).(*driver)
	if !ok {
		t.Fatalf("newDriver() type = %T, want *driver", newDriver(nil, nil, nil, nil))
	}
	if got.logger == nil {
		t.Fatal("newDriver() logger = nil")
	}
	if got.serverURL != "ws://127.0.0.1:9123" {
		t.Fatalf("serverURL = %q, want ws://127.0.0.1:9123", got.serverURL)
	}
	if got.Name() != "codex" {
		t.Fatalf("Name() = %q, want codex", got.Name())
	}
}

func TestNewDriverFactoryCreateReturnsCodexDriver(t *testing.T) {
	t.Parallel()

	factory := NewDriverFactory(nil, nil, nil, nil)
	if factory.Name != "codex" {
		t.Fatalf("factory.Name = %q, want codex", factory.Name)
	}
	got, ok := factory.Create().(*driver)
	if !ok {
		t.Fatalf("factory.Create() type = %T, want *driver", factory.Create())
	}
	if got.Name() != "codex" {
		t.Fatalf("created driver Name() = %q, want codex", got.Name())
	}
}

func TestDriverReportRuntimeUsesParsedServerURLPort(t *testing.T) {
	reporter := &stubRuntimeReporter{}
	t.Setenv("CODEX_APP_SERVER_URL", " ws://127.0.0.1:9123/ws ")
	got := newDriver(nil, nil, nil, reporter).(*driver)
	got.reportRuntime(" agent-1 ")
	if reporter.calls != 1 {
		t.Fatalf("ReportRuntime() calls = %d, want 1", reporter.calls)
	}
	if reporter.last.AgentID != "agent-1" {
		t.Fatalf("AgentID = %q, want agent-1", reporter.last.AgentID)
	}
	if reporter.last.Provider != "codex" {
		t.Fatalf("Provider = %q, want codex", reporter.last.Provider)
	}
	if reporter.last.Port != 9123 {
		t.Fatalf("Port = %d, want 9123", reporter.last.Port)
	}
}

func TestNewSessionInitializesStateAndCapabilities(t *testing.T) {
	t.Parallel()

	s, err := newSession(nil, startCodexTestServer(t), " agent-1 ", nil, nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	defer closeCodexTestSession(t, s)

	if s.agentID != "agent-1" {
		t.Fatalf("agentID = %q, want agent-1", s.agentID)
	}
	if s.transport == nil || !s.transport.Running() {
		t.Fatal("newSession() transport is not running")
	}
	if s.ctx == nil || s.cancel == nil || s.readLoopDone == nil {
		t.Fatal("newSession() did not initialize runtime state")
	}
	for cap, want := range codexCapabilities {
		if s.caps[cap] != want {
			t.Fatalf("caps[%q] = %v, want %v", cap, s.caps[cap], want)
		}
	}
}

func TestSessionCapabilitiesReturnsClone(t *testing.T) {
	t.Parallel()

	s := &session{caps: cloneCaps(codexCapabilities)}
	got := s.Capabilities()
	got[dto.CapThreadList] = false
	if !s.caps.Has(dto.CapThreadList) {
		t.Fatal("Capabilities() returned aliased map")
	}
}

func startCodexTestServer(t *testing.T) string {
	t.Helper()

	server := httptest.NewServer(websocket.Handler(func(conn *websocket.Conn) {
		defer conn.Close()
		for {
			var msg string
			if err := websocket.Message.Receive(conn, &msg); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func closeCodexTestSession(t *testing.T, s *session) {
	t.Helper()

	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.waitReadLoopStopped(ctx); err != nil {
		t.Fatalf("waitReadLoopStopped() error = %v", err)
	}
}
