package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func TestRuntimeVueTSBridgeForwardsOfficialRequestAndMirrorsDocumentLifecycle(t *testing.T) {
	vue := &runtimeVueBridgeTestClient{}
	requestBody := json.RawMessage(`{"configFileName":"C:/workspace/tsconfig.json"}`)
	ts := &runtimeVueBridgeTestClient{
		requestResult: json.RawMessage(`{"type":"response","body":{"configFileName":"C:/workspace/tsconfig.json"}}`),
	}
	bridge := newRuntimeVueTSBridgeClient(vue, ts)

	var notifications []struct {
		method string
		params any
	}
	send := func(_ context.Context, method string, params any) error {
		notifications = append(notifications, struct {
			method string
			params any
		}{method: method, params: params})
		return nil
	}
	params := json.RawMessage(`[7,"_vue:projectInfo",{"file":"C:/workspace/App.vue","needFileNameList":false}]`)
	if err := bridge.handleServerNotification(context.Background(), "tsserver/request", params, send); err != nil {
		t.Fatalf("handleServerNotification() error = %v", err)
	}
	if got, want := ts.requestMethod, "workspace/executeCommand"; got != want {
		t.Fatalf("TypeScript request method = %q, want %q", got, want)
	}
	var execute struct {
		Command   string            `json:"command"`
		Arguments []json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(ts.requestParams, &execute); err != nil {
		t.Fatalf("decode executeCommand params: %v", err)
	}
	if execute.Command != "typescript.tsserverRequest" || len(execute.Arguments) != 2 {
		t.Fatalf("executeCommand payload = %#v, want command plus two arguments", execute)
	}
	if got, want := string(execute.Arguments[0]), `"_vue:projectInfo"`; got != want {
		t.Fatalf("tsserver command = %s, want %s", got, want)
	}
	if len(notifications) != 1 || notifications[0].method != "tsserver/response" {
		t.Fatalf("response notifications = %#v, want one tsserver/response", notifications)
	}
	responseJSON, err := json.Marshal(notifications[0].params)
	if err != nil {
		t.Fatalf("encode response params: %v", err)
	}
	var response [][]json.RawMessage
	if err := json.Unmarshal(responseJSON, &response); err != nil {
		t.Fatalf("decode response params: %v", err)
	}
	if len(response) != 1 || len(response[0]) != 2 || string(response[0][0]) != "7" || string(response[0][1]) != string(requestBody) {
		t.Fatalf("response params = %s, want [[7,%s]]", responseJSON, requestBody)
	}

	ctx := context.Background()
	if err := bridge.Initialize(ctx, "file:///workspace"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := bridge.DidOpen(ctx, "file:///workspace/App.vue", "vue", 1, "<script setup>\nconst value = 1\n</script>\n"); err != nil {
		t.Fatalf("DidOpen() error = %v", err)
	}
	if err := bridge.DidChange(ctx, "file:///workspace/App.vue", 2, []protocol.TextDocumentContentChangeEvent{{Text: "<script setup>\nconst value = 2\n</script>\n"}}); err != nil {
		t.Fatalf("DidChange() error = %v", err)
	}
	if err := bridge.DidClose(ctx, "file:///workspace/App.vue"); err != nil {
		t.Fatalf("DidClose() error = %v", err)
	}
	if got, want := ts.lifecycle, []string{"initialize", "didOpen", "didChange", "didClose"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("TypeScript lifecycle = %#v, want %#v", got, want)
	}

	if err := bridge.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestRuntimeVueTSBridgeRejectsMalformedRequestWithoutSyntheticResponse(t *testing.T) {
	ts := &runtimeVueBridgeTestClient{}
	bridge := newRuntimeVueTSBridgeClient(&runtimeVueBridgeTestClient{}, ts)
	sent := false
	err := bridge.handleServerNotification(context.Background(), "tsserver/request", json.RawMessage(`[{"bad":true}]`), func(context.Context, string, any) error {
		sent = true
		return nil
	})
	if err == nil || sent {
		t.Fatalf("malformed request error = %v, sent=%v; want fail-fast with no response", err, sent)
	}
}

func TestRuntimeVueTSBridgeRejectsMissingTypeScriptResponseBody(t *testing.T) {
	ts := &runtimeVueBridgeTestClient{requestResult: json.RawMessage(`{"type":"response"}`)}
	bridge := newRuntimeVueTSBridgeClient(&runtimeVueBridgeTestClient{}, ts)
	sent := false
	err := bridge.handleServerNotification(context.Background(), "tsserver/request", json.RawMessage(`[7,"_vue:projectInfo",{}]`), func(context.Context, string, any) error {
		sent = true
		return nil
	})
	if err == nil || sent {
		t.Fatalf("missing TypeScript response body error = %v, sent=%v; want fail-fast with no synthetic response", err, sent)
	}
}

func TestRuntimeVueTSBridgeHealthRequiresBothTransports(t *testing.T) {
	withoutHealth := newRuntimeVueTSBridgeClient(&runtimeVueBridgeTestClient{}, &runtimeVueBridgeTestClient{})
	if withoutHealth.Healthy() {
		t.Fatal("bridge without health-capable transports reported healthy")
	}

	vue := &runtimeVueBridgeHealthTestClient{Client: &runtimeVueBridgeTestClient{}, healthy: true}
	ts := &runtimeVueBridgeHealthTestClient{Client: &runtimeVueBridgeTestClient{}, healthy: true}
	bridge := newRuntimeVueTSBridgeClient(vue, ts)
	if !bridge.Healthy() {
		t.Fatal("bridge with two healthy transports reported unhealthy")
	}
	ts.healthy = false
	if bridge.Healthy() {
		t.Fatal("bridge with an unhealthy TypeScript transport reported healthy")
	}
}

func TestRuntimeVueTSBridgeInitializationOptionsUseLockedPluginWithoutDeprecatedHybridMode(t *testing.T) {
	opts := runtimeVueTSBridgeInitializationOptions(runtimeVueTSBridgeSpec{
		typescriptModuleRoot: "C:/cohort/node_modules/typescript",
		vuePluginLocation:    "C:/cohort/node_modules/@vue/language-server",
	})
	if _, found := opts["hybridMode"]; found {
		t.Fatal("Vue bridge initialization options still set deprecated hybridMode")
	}
	tsserver, ok := opts["tsserver"].(map[string]any)
	if !ok || tsserver["fallbackPath"] != "C:/cohort/node_modules/typescript" || tsserver["useSyntaxServer"] != "never" {
		t.Fatalf("tsserver options = %#v, want locked fallbackPath and useSyntaxServer=never", opts["tsserver"])
	}
	plugins, ok := opts["plugins"].([]any)
	if !ok || len(plugins) != 1 {
		t.Fatalf("Vue plugin options = %#v, want one plugin", opts["plugins"])
	}
	plugin, ok := plugins[0].(map[string]any)
	if !ok || plugin["name"] != "@vue/typescript-plugin" || plugin["location"] != "C:/cohort/node_modules/@vue/language-server" || plugin["configNamespace"] != "typescript" {
		t.Fatalf("Vue TypeScript plugin options = %#v, want locked cohort plugin", plugins[0])
	}
}

func TestRuntimeVueTSBridgeRoutesScriptSemanticRequestsToTypeScriptCompanion(t *testing.T) {
	vue := &runtimeVueBridgeTestClient{requestResult: json.RawMessage(`{"provider":"vue"}`)}
	ts := &runtimeVueBridgeTestClient{requestResult: json.RawMessage(`{"provider":"typescript"}`)}
	bridge := newRuntimeVueTSBridgeClient(vue, ts)
	uri := "file:///workspace/App.vue"
	text := "<template>\n  <button>{{ message }}</button>\n</template>\n<script setup lang=\"ts\">\nconst message: string = 'hello'\n</script>\n"
	if err := bridge.DidOpen(context.Background(), uri, "vue", 1, text); err != nil {
		t.Fatalf("DidOpen() error = %v", err)
	}
	for _, method := range []string{"textDocument/hover", "textDocument/definition", "textDocument/references"} {
		params := map[string]any{
			"textDocument": map[string]any{"uri": uri},
			"position":     map[string]any{"line": 4, "character": 7},
		}
		if method == "textDocument/references" {
			params["context"] = map[string]any{"includeDeclaration": true}
		}
		got, err := bridge.Request(context.Background(), method, params)
		if err != nil {
			t.Fatalf("Request(%s) error = %v", method, err)
		}
		if string(got) != string(ts.requestResult) {
			t.Fatalf("Request(%s) result = %s, want TypeScript companion result %s", method, got, ts.requestResult)
		}
	}

	templateResult, err := bridge.Request(context.Background(), "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 1, "character": 17},
	})
	if err != nil {
		t.Fatalf("template Request() error = %v", err)
	}
	if string(templateResult) != string(vue.requestResult) {
		t.Fatalf("template Request() result = %s, want Vue result %s", templateResult, vue.requestResult)
	}
}

func TestRuntimeVueUTF16OffsetAndScriptClassification(t *testing.T) {
	if got, err := runtimeVueUTF16Offset("😀x", protocol.Position{Line: 0, Character: 2}); err != nil || got != 4 {
		t.Fatalf("runtimeVueUTF16Offset() = (%d, %v), want (4, nil)", got, err)
	}
	text := "<template>😀</template>\n<script setup>\nconst value = 1\n</script>\n"
	if inScript, err := runtimeVuePositionInScript(text, protocol.Position{Line: 2, Character: 6}); err != nil || !inScript {
		t.Fatalf("runtimeVuePositionInScript(script) = (%v, %v), want (true, nil)", inScript, err)
	}
	if inScript, err := runtimeVuePositionInScript(text, protocol.Position{Line: 0, Character: 10}); err != nil || inScript {
		t.Fatalf("runtimeVuePositionInScript(template) = (%v, %v), want (false, nil)", inScript, err)
	}
}

type runtimeVueBridgeTestClient struct {
	requestMethod string
	requestParams json.RawMessage
	requestResult json.RawMessage
	lifecycle     []string
}

type runtimeVueBridgeHealthTestClient struct {
	multilsp.Client
	healthy bool
}

func (c *runtimeVueBridgeHealthTestClient) Healthy() bool { return c.healthy }

func (c *runtimeVueBridgeTestClient) Initialize(context.Context, string) error {
	c.lifecycle = append(c.lifecycle, "initialize")
	return nil
}

func (c *runtimeVueBridgeTestClient) Shutdown(context.Context) error {
	c.lifecycle = append(c.lifecycle, "shutdown")
	return nil
}

func (c *runtimeVueBridgeTestClient) Request(_ context.Context, method string, params any) (json.RawMessage, error) {
	c.requestMethod = method
	c.requestParams, _ = json.Marshal(params)
	if c.requestResult == nil {
		return nil, errors.New("test TypeScript request result is not configured")
	}
	return c.requestResult, nil
}

func (c *runtimeVueBridgeTestClient) Notify(context.Context, string, any) error { return nil }

func (c *runtimeVueBridgeTestClient) DidOpen(context.Context, string, string, int, string) error {
	c.lifecycle = append(c.lifecycle, "didOpen")
	return nil
}

func (c *runtimeVueBridgeTestClient) DidChange(context.Context, string, int, []protocol.TextDocumentContentChangeEvent) error {
	c.lifecycle = append(c.lifecycle, "didChange")
	return nil
}

func (c *runtimeVueBridgeTestClient) DidClose(context.Context, string) error {
	c.lifecycle = append(c.lifecycle, "didClose")
	return nil
}

func (c *runtimeVueBridgeTestClient) Close() error {
	c.lifecycle = append(c.lifecycle, "close")
	return nil
}

var _ multilsp.Client = (*runtimeVueBridgeTestClient)(nil)
