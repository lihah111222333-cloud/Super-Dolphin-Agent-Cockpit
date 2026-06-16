package multilsp

import (
	"encoding/json"
	"fmt"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Compatibility protocol contract for the multi-language LSP transport.
// Collapses the previously inline `defaultServerRequestResult` switch
// into an explicit, named set so the P4 plan's "LSP transport
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
// internal/archtest/multilsp_transport_compat_guard_test.go pins the
// method literals to this file so future additions have to land in
// the contract file rather than buried inside transport.go.

// Server-initiated requests ACK'd with an empty struct result.
const (
	LSPCompatMethodClientRegisterCapability     = "client/registerCapability"
	LSPCompatMethodClientUnregisterCapability   = "client/unregisterCapability"
	LSPCompatMethodWindowWorkDoneProgressCreate = "window/workDoneProgress/create"
)

// Server-initiated workspace/*/refresh notifications ACK'd with an
// empty struct result.
const (
	LSPCompatMethodWorkspaceSemanticTokensRefresh = "workspace/semanticTokens/refresh"
	LSPCompatMethodWorkspaceCodeLensRefresh       = "workspace/codeLens/refresh"
	LSPCompatMethodWorkspaceInlayHintRefresh      = "workspace/inlayHint/refresh"
	LSPCompatMethodWorkspaceDiagnosticRefresh     = "workspace/diagnostic/refresh"
)

// Server-initiated request answered with an empty []any slice
// whose length matches the requested `items` count.

// LSPCompatMethodWorkspaceConfiguration is part of the multilsp package API.
const LSPCompatMethodWorkspaceConfiguration = "workspace/configuration"

// lspCompatEmptyStructMethods lists every server-initiated method
// this transport ACKs with `struct{}{}`. Adding a new method here
// makes it the only place the freeze needs to change.
var lspCompatEmptyStructMethods = []string{
	LSPCompatMethodClientRegisterCapability,
	LSPCompatMethodClientUnregisterCapability,
	LSPCompatMethodWindowWorkDoneProgressCreate,
	LSPCompatMethodWorkspaceSemanticTokensRefresh,
	LSPCompatMethodWorkspaceCodeLensRefresh,
	LSPCompatMethodWorkspaceInlayHintRefresh,
	LSPCompatMethodWorkspaceDiagnosticRefresh,
}

func isLSPCompatEmptyStructMethod(method string) bool {
	for _, candidate := range lspCompatEmptyStructMethods {
		if candidate == method {
			return true
		}
	}
	return false
}

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
	if isLSPCompatEmptyStructMethod(method) {
		pkglogger.Get().Info("LSP compat fallback hit",
			"event", "gopls.compat_fallback.hit",
			"method", method,
			"variant", "empty_struct",
		)
		return struct{}{}, nil
	}
	if method == LSPCompatMethodWorkspaceConfiguration {
		pkglogger.Get().Info("LSP compat fallback hit",
			"event", "gopls.compat_fallback.hit",
			"method", method,
			"variant", "workspace_configuration",
		)
		return emptyConfigurationResult(params), nil
	}
	return nil, fmt.Errorf("%w: %s", ErrMethodNotSupported, method)
}
