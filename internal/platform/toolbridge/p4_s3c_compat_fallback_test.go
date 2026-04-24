package toolbridge

import (
	"fmt"
	"testing"
)

// TestToolbridgeCompatibilityFallbackRemoved is the P22 P4 S3c
// behavioral guard pre-registered at P4 §TDD line 257. It locks the
// fail-closed contract on the proxy JSON-RPC dispatch: unknown methods
// must return jsonRPCCodeMethodMiss (-32601) with a method-not-found
// body, NOT a silent 200-ACK or any other compatibility fallback
// (§fallback / §fail-closed).
//
// The pre-P4 concern was about compatibility drift — if the proxy
// silently ACKs methods it doesn't implement, downstream MCP clients
// can depend on speculative methods that might behave differently (or
// simply be absent) in a real server, and we'd never notice. The
// fail-closed path ensures clients surface the mismatch immediately.
//
// This test deliberately shares fixtures with handler_test.go's
// existing proxy-error coverage (see TestProxyToolCall_RejectsMissing
// RuntimeAsInvalidParams for a sibling pattern); the handshake methods
// covered here are the ones we positively support, whereas the
// unknown-method case closes the door on silent compatibility.
func TestToolbridgeCompatibilityFallbackRemoved(t *testing.T) {
	t.Parallel()

	// A selection of plausible-but-not-supported method names. Each
	// must fail with MethodMiss; none may silent-ACK.
	unknownMethods := []string{
		"tools/describe",   // looks like a tools/* extension
		"prompts/list",     // a real MCP method we do not proxy
		"completions/list", // future-looking method
		"proxy.ping",       // made-up "compat" method
		"",                 // empty method name
	}

	for _, method := range unknownMethods {
		method := method
		t.Run(fmt.Sprintf("method=%q", method), func(t *testing.T) {
			t.Parallel()
			h, _ := newHandlerForTest()
			body := fmt.Sprintf(`{"jsonrpc":"2.0","id":"req-1","method":%q,"params":{}}`, method)

			got := callProxyRequest(t, h, "/mcp/orch/agent-1", body)
			if got.Error == nil {
				t.Fatalf("proxy response error = nil, want method-not-found (unknown method %q must not silent-ACK)", method)
			}
			if got.Error.Code != jsonRPCCodeMethodMiss {
				t.Errorf("proxy error code = %d, want %d (jsonRPCCodeMethodMiss) for unknown method %q", got.Error.Code, jsonRPCCodeMethodMiss, method)
			}
			if got.Error.Message != "method not found" {
				t.Errorf("proxy error message = %q, want %q", got.Error.Message, "method not found")
			}
		})
	}
}
