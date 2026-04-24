package bootstrap

import (
	"context"
	"strings"
	"testing"

	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
)

// TestPendingHooks_BootAgentIDDoesNotFallback asserts P22 P4 S5b /
// plan §316: cfg.AgentID is the sole authoritative source for the
// PendingHooks identity. Pre-S5b a missing cfg.AgentID would fall
// back to boot.AgentID, letting a peer read pending reviews under
// an identity that was never provisioned in the current config.
// The fail-closed contract means the call now errors before it ever
// reaches the wire.
func TestPendingHooks_BootAgentIDDoesNotFallback(t *testing.T) {
	t.Parallel()

	// Even with a live conn, cfg.AgentID empty must error-out before
	// the RPC is dispatched so the peer cannot see boot.AgentID.
	var calls int
	local := jrpcserver.NewLocal(handler.Map{
		mcpdto.MethodHookPending: platformrpc.StrictHandler(func(context.Context, mcpdto.HookPendingRequest) (mcpdto.HookPendingResponse, error) {
			calls++
			return mcpdto.HookPendingResponse{}, nil
		}),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	defer local.Close()

	client := &Client{
		conn: local.Client,
		cfg:  Config{AgentID: ""}, // authoritative: empty
		boot: bootSnapshot{AgentID: "boot-ghost-agent"},
	}
	_, err := client.PendingHooks(context.Background())
	if err == nil {
		t.Fatalf("PendingHooks() returned nil err, want errHookPendingAgentIDRequired()")
	}
	if !strings.Contains(err.Error(), "agent_id") {
		t.Fatalf("PendingHooks() error = %v, want message naming agent_id", err)
	}
	if calls != 0 {
		t.Fatalf("PendingHooks() dispatched to peer %d times; expected zero before failing closed", calls)
	}
}

// TestHandleCallback_UnknownMethodFailsClosed asserts P22 P4 S5b /
// plan §315: handleCallback must not silently ACK an unknown method
// anymore. Pre-S5b the trailing `return map[string]bool{"ok": true}`
// swallowed every method that wasn't tools/list, tools/call, a hook
// method, shutdown, or config_changed — meaning a peer typo or a
// new control-plane method we never opted into looked like success.
//
// The test invokes handleCallback directly against an in-memory
// client and asserts a JSON-RPC-style MethodNotFound error. Using
// the private entry point lets the test assert the fail-closed
// surface without spinning up a peer.
func TestHandleCallback_UnknownMethodFailsClosed(t *testing.T) {
	t.Parallel()

	// jrpc2.NewServer + NewLocal give us a Request with an unknown
	// method plumbed through OnCallback. Register the method on the
	// local peer so jrpc2 doesn't reject it at the transport layer;
	// the handler itself just re-invokes the client's OnCallback.
	var handlerResult any
	var handlerErr error
	local := jrpcserver.NewLocal(handler.Map{
		"never.opted.in": platformrpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return nil, nil
		}),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	defer local.Close()

	client := &Client{
		conn: local.Client,
	}

	// Simulate the callback path by calling handleCallback directly
	// with a fake jrpc2.Request. Since jrpc2.Request cannot be
	// constructed from the public API, exercise the behavior via
	// dispatchRequest (notification path) + the public surface
	// instead: a notification for an unknown method must not error
	// (no response surface) but an internal dispatchLifecycleRequest
	// must return handled=false.
	req := fakeJRPCRequest(t, "never.opted.in")
	handlerResult, handlerErr = client.handleCallback(context.Background(), req)
	if handlerErr == nil {
		t.Fatalf("handleCallback(unknown method) err = nil, want MethodNotFound")
	}
	if !strings.Contains(handlerErr.Error(), "unknown callback method") {
		t.Fatalf("handleCallback(unknown method) err = %q, want message to mention 'unknown callback method'", handlerErr.Error())
	}
	if handlerResult != nil {
		t.Fatalf("handleCallback(unknown method) result = %v, want nil", handlerResult)
	}
}

// fakeJRPCRequest constructs a minimal jrpc2.Request via the same
// NewLocal loopback used elsewhere in this package's tests. The
// request is synthesised by registering a passthrough handler that
// captures the *jrpc2.Request it receives, then invoking a synthetic
// call from the test client side.
func fakeJRPCRequest(t *testing.T, method string) *jrpc2.Request {
	t.Helper()

	captured := make(chan *jrpc2.Request, 1)
	local := jrpcserver.NewLocal(handler.Map{
		method: handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
			captured <- req
			return nil, nil
		}),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	t.Cleanup(func() { _ = local.Close() })

	// Fire a no-wait notify so the handler captures its Request.
	if err := local.Client.Notify(context.Background(), method, nil); err != nil {
		t.Fatalf("Notify(%q) error = %v", method, err)
	}
	req := <-captured
	return req
}
