package rpc

import (
	"context"
	"encoding/json"
	"testing"

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

	opts := prepareServerOptions(nil)
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
	opts := prepareServerOptions(&jrpc2.ServerOptions{Logger: logger})
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

func newTestServer() *Server {
	return NewServer(Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
}

func echoHandler(result string) handler.Func {
	return StrictHandler(func(context.Context, struct{}) (string, error) {
		return result, nil
	})
}
