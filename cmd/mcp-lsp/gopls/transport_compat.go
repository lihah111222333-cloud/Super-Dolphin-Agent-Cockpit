package gopls

import (
	"encoding/json"
	"fmt"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Compatibility protocol contract for the gopls LSP transport.
// Collapses the previously inline `defaultServerRequestResult` switch
// into an explicit, named set so the P4 plan's "gopls transport
// compat fallback 需要显式 protocol contract 与守卫测试, 不能继续散落
// 在 transport/client 实现中" (§309-311) becomes concrete and guardable.
//
// Each frozen method is either:
//   - ACK'd with an empty struct result (client/registerCapability,
//     client/unregisterCapability, window/workDoneProgress/create,
//     and the workspace/*/refresh family), or
//   - ACK'd with an `[]null` array matching the number of requested
//     items (workspace/configuration).
//
// Any method outside this set returns ErrMethodNotSupported so the
// transport layer can still surface genuinely unknown server-initiated
// methods as JSON-RPC `MethodNotFound` rather than silently ACK'ing.
//
// The archtest in
// internal/archtest/gopls_transport_compat_guard_test.go pins the
// method literals to this file so future additions have to land in
// the contract file rather than buried inside transport.go.

// gopls-initiated server requests ACK'd with an empty struct result.
const (
	GoplsCompatMethodClientRegisterCapability     = "client/registerCapability"
	GoplsCompatMethodClientUnregisterCapability   = "client/unregisterCapability"
	GoplsCompatMethodWindowWorkDoneProgressCreate = "window/workDoneProgress/create"
)

// gopls-initiated workspace/*/refresh notifications ACK'd with an
// empty struct result.
const (
	GoplsCompatMethodWorkspaceSemanticTokensRefresh = "workspace/semanticTokens/refresh"
	GoplsCompatMethodWorkspaceCodeLensRefresh       = "workspace/codeLens/refresh"
	GoplsCompatMethodWorkspaceInlayHintRefresh      = "workspace/inlayHint/refresh"
	GoplsCompatMethodWorkspaceDiagnosticRefresh     = "workspace/diagnostic/refresh"
)

// gopls-initiated server request answered with an empty []any slice
// whose length matches the requested `items` count.
const GoplsCompatMethodWorkspaceConfiguration = "workspace/configuration"

// goplsCompatEmptyStructMethods lists every server-initiated method
// this transport ACKs with `struct{}{}`. Adding a new method here
// makes it the only place the freeze needs to change.
var goplsCompatEmptyStructMethods = []string{
	GoplsCompatMethodClientRegisterCapability,
	GoplsCompatMethodClientUnregisterCapability,
	GoplsCompatMethodWindowWorkDoneProgressCreate,
	GoplsCompatMethodWorkspaceSemanticTokensRefresh,
	GoplsCompatMethodWorkspaceCodeLensRefresh,
	GoplsCompatMethodWorkspaceInlayHintRefresh,
	GoplsCompatMethodWorkspaceDiagnosticRefresh,
}

// goplsCompatEmptyStructMethodSet materialises the slice as an O(1)
// lookup table for dispatchCompatServerRequest.
var goplsCompatEmptyStructMethodSet = func() map[string]struct{} {
	out := make(map[string]struct{}, len(goplsCompatEmptyStructMethods))
	for _, m := range goplsCompatEmptyStructMethods {
		out[m] = struct{}{}
	}
	return out
}()

// dispatchCompatServerRequest resolves a server-initiated request
// against the frozen compatibility contract. A method outside the
// contract returns ErrMethodNotSupported, letting the caller surface
// it as JSON-RPC `MethodNotFound` instead of silently ACK'ing.
//
// P22 P4 S6a / plan §321: every hit on the compatibility table is
// logged with a stable `event=gopls.compat_fallback.hit` anchor so
// ops dashboards can count compat fallbacks by method without
// pattern-matching free-text messages. The ErrMethodNotSupported
// branch is NOT a hit by this definition — it surfaces as
// JSON-RPC MethodNotFound and belongs to a different contract
// (future observability: genuinely unknown methods).
func dispatchCompatServerRequest(method string, params json.RawMessage) (any, error) {
	if _, ok := goplsCompatEmptyStructMethodSet[method]; ok {
		pkglogger.Get().Info("gopls compat fallback hit",
			"event", "gopls.compat_fallback.hit",
			"method", method,
			"variant", "empty_struct",
		)
		return struct{}{}, nil
	}
	if method == GoplsCompatMethodWorkspaceConfiguration {
		pkglogger.Get().Info("gopls compat fallback hit",
			"event", "gopls.compat_fallback.hit",
			"method", method,
			"variant", "workspace_configuration",
		)
		return emptyConfigurationResult(params), nil
	}
	return nil, fmt.Errorf("%w: %s", ErrMethodNotSupported, method)
}
