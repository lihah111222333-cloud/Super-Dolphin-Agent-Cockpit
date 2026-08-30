package toolbridge

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// assertProxyResultNoJSONRPCError verifies that a tools/call response carries
// its failure in the MCP result envelope rather than as a JSON-RPC error.
func assertProxyResultNoJSONRPCError(t *testing.T, got proxyJSONRPCResponse) {
	t.Helper()
	if got.Error != nil {
		t.Fatalf("proxy response error = %+v, want JSON-RPC result", got.Error)
	}
}

// requireProxyResultMap returns the decoded MCP result object or fails the
// current test when the response shape is not an object.
func requireProxyResultMap(t *testing.T, got proxyJSONRPCResponse) map[string]any {
	t.Helper()
	result, ok := got.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want object", got.Result)
	}
	return result
}

// assertToolErrorResult checks the stable MCP isError marker used by host
// tool failures.
func assertToolErrorResult(t *testing.T, result map[string]any) {
	t.Helper()
	if isError, _ := result["isError"].(bool); !isError {
		t.Fatalf("isError = %v, want true (result=%#v)", result["isError"], result)
	}
}

// containsDynamicToolName reports whether a provider dynamic-tool list has a
// tool with the requested canonical name.
func containsDynamicToolName(tools []contract.DynamicToolSchema, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}
