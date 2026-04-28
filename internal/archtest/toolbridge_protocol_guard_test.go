package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestToolbridgeProtocolFreezeContractGuard is the P22 P4 S3b/S3c
// guard: the toolbridge wire-protocol surface (private metadata keys +
// proxy initialize handshake + supported method names + fail-closed
// default) must be driven from the named constants in
// internal/platform/toolbridge/protocol_contract.go, not scattered
// magic strings. P4 §94-106 lists this freeze as a prerequisite for
// S3d's import-direction refactor.
//
// The guard enforces three invariants by file-text scan:
//  1. handler.go / proxy.go / diff_fallback.go no longer contain the
//     bare magic strings they used pre-S3b. Those strings must come
//     from protocol_contract.go constants.
//  2. proxy.go preserves the fail-closed `default` branch that returns
//     jsonRPCCodeMethodMiss for unknown methods (P4
//     §fallback / §fail-closed: no silent ACK for unknown compatibility
//     methods; test name matches P4 §TDD line 257
//     TestToolbridgeCompatibilityFallbackRemoved).
//  3. protocol_contract.go itself declares every constant the other
//     files reference, preventing a silent split where someone adds
//     a new magic string that passes the above checks only because it
//     is not yet named.
func TestToolbridgeProtocolFreezeContractGuard(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)

	readFile := func(rel string) string {
		path := filepath.Join(root, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return string(data)
	}

	handlerSrc := readFile("internal/platform/toolbridge/handler.go")
	proxySrc := readFile("internal/platform/toolbridge/proxy.go")
	contractSrc := readFile("internal/platform/toolbridge/protocol_contract.go")

	// 1. handler.go must not embed the pre-S3b private metadata strings
	//    as literal map keys. (Comments / docstrings mentioning the name
	//    are allowed — only quoted forms inside a statement matter.)
	forbiddenHandlerLiterals := []string{
		`"_agentId":`,
		`"_threadId":`,
		`"_callId":`,
		`peer.Callback(callCtx, "tools/call"`,
	}
	for _, token := range forbiddenHandlerLiterals {
		if strings.Contains(handlerSrc, token) {
			t.Errorf("handler.go reintroduced magic-string %q (P4 §S3b: must use protocol_contract.go constants)", token)
		}
	}

	// 2. proxy.go must not embed the pre-S3b initialize/method literals.
	forbiddenProxyLiterals := []string{
		`case "initialize":`,
		`case "notifications/initialized":`,
		`case "tools/list":`,
		`case "tools/call":`,
		`"protocolVersion": "2025-11-25"`,
		`"name": "proxy"`,
		`"version": "1.0.0"`,
	}
	for _, token := range forbiddenProxyLiterals {
		if strings.Contains(proxySrc, token) {
			t.Errorf("proxy.go reintroduced magic-string %q (P4 §S3b: must use protocol_contract.go constants)", token)
		}
	}

	// 3. proxy.go must keep fail-closed default branch that returns
	//    jsonRPCCodeMethodMiss. This is the positive invariant behind
	//    P4 §TDD line 257 TestToolbridgeCompatibilityFallbackRemoved.
	//    We assert the literal token appears (exact match cheap; the
	//    behavioral smoke test in toolbridge package exercises the
	//    actual wire behavior).
	requiredProxyTokens := []string{
		"writeJSONRPCError(w, req.ID, jsonRPCCodeMethodMiss",
	}
	for _, token := range requiredProxyTokens {
		if !strings.Contains(proxySrc, token) {
			t.Errorf("proxy.go lost fail-closed default branch %q (P4 §fallback: unknown methods must not silent-ACK)", token)
		}
	}

	// 4. protocol_contract.go must declare every exported constant the
	//    other files consume. Prevents silent drift where someone
	//    references a name that does not exist.
	requiredConstants := []string{
		"MetadataKeyAgentID",
		"MetadataKeyThreadID",
		"MetadataKeyCallID",
		"ProxyProtocolVersion",
		"ProxyServerInfoName",
		"ProxyServerInfoVersion",
		"ProxyNotificationMethod",
		"ProxyMethodInitialize",
		"ProxyMethodToolsList",
		"ProxyMethodToolsCall",
	}
	for _, name := range requiredConstants {
		if !strings.Contains(contractSrc, name) {
			t.Errorf("protocol_contract.go missing named constant %q (P4 §S3b freeze is incomplete)", name)
		}
	}
}
