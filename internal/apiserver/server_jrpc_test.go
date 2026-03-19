package apiserver_test

import (
	"context"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/creachadair/jrpc2/server"

	"github.com/anthropic/super-agent-v3/internal/apiserver"
)

// TestPingRPC verifies the jrpc2 framework integration works end-to-end.
// This is the V3 RPC smoke test — if this passes, the framework is wired correctly.
func TestPingRPC(t *testing.T) {
	srv := apiserver.NewServer(apiserver.ServerConfig{})
	defer srv.Close()

	// Use jrpc2 local server for in-memory testing (no HTTP needed)
	local := server.NewLocal(handler.Map{
		"system/ping": handler.New(srv.Ping),
	}, nil)
	defer local.Close()

	ctx := context.Background()

	var result string
	if err := local.Client.CallResult(ctx, "system/ping", nil, &result); err != nil {
		t.Fatalf("system/ping failed: %v", err)
	}
	if result != "pong" {
		t.Fatalf("expected pong, got %q", result)
	}
	t.Logf("system/ping returned: %s", result)
}

// TestMethodNotFound verifies jrpc2 returns proper error code for unknown methods.
func TestMethodNotFound(t *testing.T) {
	local := server.NewLocal(handler.Map{
		"system/ping": handler.New(func(ctx context.Context) string { return "ok" }),
	}, nil)
	defer local.Close()

	ctx := context.Background()

	var result string
	err := local.Client.CallResult(ctx, "nonexistent/method", nil, &result)
	if err == nil {
		t.Fatal("expected error for nonexistent method")
	}

	// jrpc2 should return standard MethodNotFound code
	if jerr, ok := err.(*jrpc2.Error); ok {
		if jerr.Code != -32601 {
			t.Fatalf("expected code -32601 (MethodNotFound), got %d", jerr.Code)
		}
		t.Logf("correctly got MethodNotFound: %v", jerr)
	} else {
		t.Logf("error type: %T, value: %v", err, err)
	}
}

// TestTypedHandler verifies jrpc2 auto-unmarshals typed request structs.
func TestTypedHandler(t *testing.T) {
	type AddReq struct {
		A int `json:"a"`
		B int `json:"b"`
	}

	local := server.NewLocal(handler.Map{
		"math/add": handler.New(func(ctx context.Context, req AddReq) int {
			return req.A + req.B
		}),
	}, nil)
	defer local.Close()

	ctx := context.Background()

	var result int
	if err := local.Client.CallResult(ctx, "math/add", AddReq{A: 3, B: 7}, &result); err != nil {
		t.Fatalf("math/add failed: %v", err)
	}
	if result != 10 {
		t.Fatalf("expected 10, got %d", result)
	}
	t.Logf("math/add(3, 7) = %d", result)
}

// TestSSEBridgeCreation verifies SSE bridge initializes correctly.
func TestSSEBridgeCreation(t *testing.T) {
	bridge := apiserver.NewSSEBridge()
	if bridge == nil {
		t.Fatal("SSEBridge should not be nil")
	}
	// Broadcast with no clients should not panic
	bridge.Broadcast("test/event", map[string]any{"key": "value"})
	t.Log("SSEBridge created and broadcast succeeded with no clients")
}

// TestMarshalNotification verifies notification JSON format.
func TestMarshalNotification(t *testing.T) {
	data, err := apiserver.MarshalNotification("thread/started", map[string]any{
		"threadId": "t-123",
	})
	if err != nil {
		t.Fatalf("MarshalNotification failed: %v", err)
	}

	expected := `"jsonrpc":"2.0"`
	if got := string(data); !contains(got, expected) {
		t.Fatalf("expected notification to contain %q, got %s", expected, got)
	}
	if got := string(data); !contains(got, `"method":"thread/started"`) {
		t.Fatalf("expected method field, got %s", got)
	}
	t.Logf("notification: %s", data)
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
