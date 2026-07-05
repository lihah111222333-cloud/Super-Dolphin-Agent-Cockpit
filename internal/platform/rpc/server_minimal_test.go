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

	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
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
		ParamsSummary: SafeRPCLogSummary("thread/start", `{"cwd":"/tmp/project","baseInstructions":"hello"}`),
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
	cancel, done := startRPCRunnerForTest(t, server.Run)

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

func TestValidateControlRPCAddrAllowsOnlyLoopbackHosts(t *testing.T) {
	t.Parallel()

	for _, addr := range []string{"127.0.0.1:0", "localhost:4512", "[::1]:4512"} {
		t.Run("allow_"+addr, func(t *testing.T) {
			if err := validateControlRPCAddr(addr); err != nil {
				t.Fatalf("validateControlRPCAddr(%q) error = %v", addr, err)
			}
		})
	}

	for _, addr := range []string{"0.0.0.0:4512", ":4512", "192.168.1.10:4512", "[::]:4512"} {
		t.Run("reject_"+addr, func(t *testing.T) {
			if err := validateControlRPCAddr(addr); err == nil || !strings.Contains(err.Error(), "loopback") {
				t.Fatalf("validateControlRPCAddr(%q) error = %v, want loopback validation failure", addr, err)
			}
		})
	}
}

func TestControlRPCConnectionAuthRequiresRegisterToken(t *testing.T) {
	t.Parallel()

	assigner := controlRPCAuthAssigner{
		base: handler.Map{
			mcpdto.MethodRegister: StrictHandler(func(_ context.Context, _ mcpdto.RegisterRequest) (map[string]bool, error) {
				return map[string]bool{"ok": true}, nil
			}),
			"thread/start": StrictHandler(func(_ context.Context, _ struct{}) (map[string]bool, error) {
				return map[string]bool{"ok": true}, nil
			}),
		},
		auth: newControlRPCConnectionAuth("secret"),
	}
	local := jrpcserver.NewLocal(assigner, &jrpcserver.LocalOptions{Server: prepareServerOptions(nil, nil)})
	defer local.Close()

	var out map[string]bool
	if err := local.Client.CallResult(context.Background(), "thread/start", map[string]any{}, &out); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("thread/start before register error = %v, want unauthorized", err)
	}
	if err := local.Client.CallResult(context.Background(), mcpdto.MethodRegister, mcpdto.RegisterRequest{SessionToken: "wrong"}, &out); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("register with wrong token error = %v, want unauthorized", err)
	}
	if err := local.Client.CallResult(context.Background(), mcpdto.MethodRegister, mcpdto.RegisterRequest{SessionToken: "secret"}, &out); err != nil {
		t.Fatalf("register with correct token error = %v", err)
	}
	if err := local.Client.CallResult(context.Background(), "thread/start", map[string]any{}, &out); err != nil {
		t.Fatalf("thread/start after register error = %v", err)
	}
}

type stubRPCLogger struct{}

func (stubRPCLogger) LogRequest(ctx context.Context, req *jrpc2.Request) {
	_ = ctx
	_ = req
}

func (stubRPCLogger) LogResponse(ctx context.Context, resp *jrpc2.Response) {
	_ = ctx
	_ = resp
}

func newTestServer() *Server {
	return NewServer(Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
}

func echoHandler(result string) handler.Func {
	return StrictHandler(func(context.Context, struct{}) (string, error) {
		return result, nil
	})
}
