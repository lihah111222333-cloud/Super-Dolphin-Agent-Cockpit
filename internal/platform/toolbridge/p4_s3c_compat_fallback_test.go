package toolbridge

import (
	"fmt"
	"testing"
)

// TestToolbridgeCompatibilityFallbackRemoved 固定代理 JSON-RPC 的 fail-closed 行为。
// 未支持的方法必须返回 MethodMiss，而不是用 200 ACK 伪装成功，避免客户端依赖不存在的兼容方法。
// 这里复用 handler_test.go 的代理 fixture，只覆盖“未知方法”这一条边界。
func TestToolbridgeCompatibilityFallbackRemoved(t *testing.T) {
	t.Parallel()

	// 这些方法看起来合理但当前不支持；每一个都必须返回 MethodMiss。
	unknownMethods := []string{
		"tools/describe",   // 形似 tools/* 扩展
		"prompts/list",     // 真实 MCP 方法，但本代理不转发
		"completions/list", // 预留形态的方法名
		"proxy.ping",       // 虚构的兼容方法
		"",                 // 空方法名
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
