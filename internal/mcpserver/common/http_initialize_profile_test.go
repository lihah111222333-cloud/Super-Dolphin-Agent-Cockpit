package common

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestLocalInitializeRejectsIncompleteIdentity 失败握手不得消耗会话或冒充客户端身份。
func TestLocalInitializeRejectsIncompleteIdentity(t *testing.T) {
	server, err := NewLocalHTTPServer("orch", "test", testToolProvider{}, LocalHTTPProfile{
		WorkspaceRoot: t.TempDir(), BearerToken: "test-local-operator-credential",
	}, newTestLoggerRuntime())
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		`{}`, `{"capabilities":null}`, `{"capabilities":[]}`, `{"clientInfo":null}`,
		`{"clientInfo":{}}`, `{"clientInfo":{"name":"test"}}`,
		`{"clientInfo":{"name":1,"version":"1"}}`, `{"_meta":null}`,
	} {
		t.Run(invalid, func(t *testing.T) {
			params := map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{},
				"clientInfo": map[string]any{"name": "test", "version": "1"}}
			if invalid == `{}` {
				delete(params, "clientInfo")
			} else if err := json.Unmarshal([]byte(invalid), &params); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": params})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			server.handleMCP(response, localProfileRequest(server, http.MethodPost, string(raw), ""))
			if response.Code != http.StatusBadRequest || len(server.sessions) != 0 {
				t.Fatalf("invalid identity allocated session: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

// TestLocalSessionInitializationBarrier 验证初始化状态隔离、非法通知不解锁及关闭后不可复活。
func TestLocalSessionInitializationBarrier(t *testing.T) {
	server, err := NewLocalHTTPServer("orch", "test", testToolProvider{}, LocalHTTPProfile{
		WorkspaceRoot: t.TempDir(), BearerToken: "test-local-operator-credential",
	}, newTestLoggerRuntime())
	if err != nil {
		t.Fatal(err)
	}
	a, b := &httpMCPSession{}, &httpMCPSession{}
	list := jsonRPCRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	assertLocalLifecycleCode(t, server.dispatch(context.Background(), a, list), codeInvalidReq)
	invalid := jsonRPCRequest{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "notifications/initialized"}
	assertLocalLifecycleCode(t, server.dispatch(context.Background(), a, invalid), codeInvalidReq)
	assertLocalLifecycleCode(t, server.dispatch(context.Background(), a, list), codeInvalidReq)
	notice := jsonRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"}
	for range 2 {
		if response := server.dispatch(context.Background(), a, notice); response != nil {
			t.Fatalf("notification produced response: %#v", response)
		}
	}
	assertLocalLifecycleCode(t, server.dispatch(context.Background(), a, list), 0)
	assertLocalLifecycleCode(t, server.dispatch(context.Background(), b, list), codeInvalidReq)
	a.terminate()
	server.dispatch(context.Background(), a, notice)
	assertLocalLifecycleCode(t, server.dispatch(context.Background(), a, list), codeInvalidReq)
}

// assertLocalLifecycleCode 保留真实 dispatcher 错误语义，不把 nil 通知结果视为请求成功。
func assertLocalLifecycleCode(t *testing.T, response *jsonRPCResponse, expected int) {
	t.Helper()
	if response == nil {
		t.Fatal("request returned no response")
	}
	if expected == 0 {
		if response.Error != nil {
			t.Fatalf("unexpected error: %#v", response.Error)
		}
		return
	}
	if response.Error == nil || response.Error.Code != expected {
		t.Fatalf("error=%#v, want code=%d", response.Error, expected)
	}
}
