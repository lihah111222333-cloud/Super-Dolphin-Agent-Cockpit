package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

func TestNewServerInitializesAddressAndState(t *testing.T) {
	t.Parallel()

	server := newTestServer()
	if server.addr != "127.0.0.1:0" {
		t.Fatalf("addr = %q, want 127.0.0.1:0", server.addr)
	}
	if len(server.methods) != 0 || len(server.active) != 0 {
		t.Fatalf("initial state = methods:%d active:%d", len(server.methods), len(server.active))
	}
}

func TestServerRegisterMergesHandlerMaps(t *testing.T) {
	t.Parallel()

	server := newTestServer()
	server.Register(handler.Map{"one": echoHandler("one")}, handler.Map{"two": echoHandler("two")})
	if len(server.methods) != 2 {
		t.Fatalf("len(methods) = %d, want 2", len(server.methods))
	}
}

func TestServerDispatchCallsRegisteredHandler(t *testing.T) {
	t.Parallel()

	server := newTestServer()
	server.Register(handler.Map{"echo": StrictHandler(func(_ context.Context, req struct {
		Value string `json:"value"`
	}) (string, error) {
		return "pong:" + req.Value, nil
	})})
	raw, err := server.Dispatch(context.Background(), "echo", json.RawMessage(`{"value":"ok"}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if string(raw) != `"pong:ok"` {
		t.Fatalf("Dispatch() = %s, want %q", raw, `"pong:ok"`)
	}
}

func TestRegisterAllHandlersAddsGroupedHandlers(t *testing.T) {
	t.Parallel()

	server := newTestServer()
	registerAllHandlers(server, serverParams{
		Handlers: []handler.Map{{"first": echoHandler("first")}, {"second": echoHandler("second")}},
	})
	if len(server.methods) != 2 {
		t.Fatalf("len(methods) = %d, want 2", len(server.methods))
	}
	raw, err := server.Dispatch(context.Background(), "second", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch(second) error = %v", err)
	}
	if string(raw) != `"second"` {
		t.Fatalf("Dispatch(second) = %s, want %q", raw, `"second"`)
	}
}

func TestPrepareServerOptionsDefaultsStayQuiet(t *testing.T) {
	t.Parallel()

	opts := prepareServerOptions(nil, nil)
	if opts == nil {
		t.Fatal("prepareServerOptions(nil) returned nil")
	}
	if !opts.AllowPush {
		t.Fatal("AllowPush = false, want true")
	}
	if opts.Logger != nil {
		t.Fatal("Logger should be nil by default to avoid jrpc2 debug text logs")
	}
}

func TestPrepareServerOptionsPreservesExplicitLogger(t *testing.T) {
	t.Parallel()

	called := false
	logger := jrpc2.Logger(func(string) { called = true })
	opts := prepareServerOptions(nil, &jrpc2.ServerOptions{Logger: logger})
	if opts == nil {
		t.Fatal("prepareServerOptions returned nil")
	}
	if !opts.AllowPush {
		t.Fatal("AllowPush = false, want true")
	}
	if opts.Logger == nil {
		t.Fatal("Logger = nil, want explicit logger to be preserved")
	}
	opts.Logger("probe")
	if !called {
		t.Fatal("explicit logger was not invoked")
	}
}

func TestPrepareServerOptionsInjectsRPCLogWhenProvided(t *testing.T) {
	t.Parallel()

	fallback := stubRPCLogger{}
	opts := prepareServerOptions(fallback, nil)
	if opts == nil {
		t.Fatal("prepareServerOptions returned nil")
	}
	if opts.RPCLog != fallback {
		t.Fatal("RPCLog was not injected")
	}
}

func TestPrepareServerOptionsPreservesExplicitRPCLog(t *testing.T) {
	t.Parallel()

	explicit := stubRPCLogger{}
	fallback := stubRPCLogger{}
	opts := prepareServerOptions(fallback, &jrpc2.ServerOptions{RPCLog: explicit})
	if opts == nil {
		t.Fatal("prepareServerOptions returned nil")
	}
	if opts.RPCLog != explicit {
		t.Fatal("explicit RPCLog should be preserved")
	}
}

func TestRPCRequestTrackerLogsPendingRequestsOnConnectionExit(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	tracker := newRPCRequestTracker(logger)
	tracker.pending["req-1"] = rpcPendingRequest{
		ID:            "req-1",
		Method:        "thread/start",
		ThreadID:      "thread-1",
		ParamsPreview: `{"cwd":"/tmp/project","baseInstructions":"hello"}`,
		StartedAt:     time.Now().Add(-2 * time.Second),
	}

	tracker.logConnectionExit(errors.New("connection reset by peer"))

	output := logBuf.String()
	for _, want := range []string{
		`"msg":"rpc connection exited with pending requests"`,
		`"pending_count":1`,
		`"method":"thread/start"`,
		`"thread_id":"thread-1"`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("log output missing %q: %s", want, output)
		}
	}
}

func TestServerRunPublishesActualControlRPCAddr(t *testing.T) {
	t.Setenv(controlRPCAddrEnv, "127.0.0.1:0")
	server := newTestServer()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()

	deadline := time.After(time.Second)
	for {
		got := os.Getenv(controlRPCAddrEnv)
		if got != "" && got != "127.0.0.1:0" {
			cancel()
			if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("Run() error = %v", err)
			}
			if strings.HasSuffix(got, ":0") {
				t.Fatalf("%s = %q, want concrete listener port", controlRPCAddrEnv, got)
			}
			return
		}
		select {
		case err := <-done:
			t.Fatalf("Run() returned before publishing addr: %v", err)
		case <-deadline:
			cancel()
			t.Fatalf("timed out waiting for %s to be published; current=%q", controlRPCAddrEnv, got)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type stubRPCLogger struct{}

func (stubRPCLogger) LogRequest(context.Context, *jrpc2.Request)   {}
func (stubRPCLogger) LogResponse(context.Context, *jrpc2.Response) {}

func newTestServer() *Server {
	return NewServer(Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
}

func echoHandler(result string) handler.Func {
	return StrictHandler(func(context.Context, struct{}) (string, error) {
		return result, nil
	})
}
