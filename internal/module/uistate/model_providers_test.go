package uistate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

func newModelProviderTestServer(t *testing.T) *rpc.Server {
	t.Helper()
	svc, _, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	server := rpc.NewServer(rpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewUIStateHandlers(svc).Handlers)
	return server
}

func TestModelProvidersListReturnsDefaultTemplatesAndEnvStatus(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "secret-prefix-middle-suffix")
	server := newModelProviderTestServer(t)

	result, err := server.Dispatch(context.Background(), "modelProviders/list", json.RawMessage(`{"cwd":"/repo/app"}`))
	if err != nil {
		t.Fatalf("Dispatch(modelProviders/list) error = %v", err)
	}
	for _, fragment := range []string{"secret-prefix", "middle", "suffix", "secr", "ffix"} {
		if strings.Contains(string(result), fragment) {
			t.Fatalf("modelProviders/list leaked api key fragment %q: %s", fragment, result)
		}
	}

	var got modelProviderRegistry
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", result, err)
	}
	if len(got.Vendors) != 3 {
		t.Fatalf("len(vendors) = %d, want 3", len(got.Vendors))
	}
	if got.Vendors[0].ID != "openrouter" || !got.Vendors[0].Configured || got.Vendors[0].MaskedEnv == "" {
		t.Fatalf("openrouter status = %#v, want configured with masked env", got.Vendors[0])
	}
	if got.Vendors[0].MaskedEnv != "********" {
		t.Fatalf("openrouter maskedEnv = %q, want constant mask", got.Vendors[0].MaskedEnv)
	}
}

func TestModelProvidersSaveRejectsInvalidRegistry(t *testing.T) {
	server := newModelProviderTestServer(t)

	_, err := server.Dispatch(context.Background(), "modelProviders/save", json.RawMessage(`{
        "cwd":"/repo/app",
        "registry":{"vendors":[{"id":"bad","label":"Bad","enabled":true,"baseURL":"ftp://bad","envKey":"bad-key","codexModelProvider":"bad","defaultModel":"bad","tokenPool":{"fallbackVendorId":"missing"}}]}
    }`))
	if err == nil {
		t.Fatal("Dispatch(modelProviders/save) error = nil, want validation failure")
	}
}

func TestShortcutBindingsPreferenceValidation(t *testing.T) {
	validBinding := func(key string) map[string]any {
		return map[string]any{
			"key":   key,
			"meta":  false,
			"ctrl":  true,
			"alt":   false,
			"shift": false,
		}
	}
	tooManyBindings := make(map[string]any, 65)
	for index := 0; index < 65; index++ {
		tooManyBindings["command."+strings.Repeat("x", index+1)] = validBinding("k")
	}

	tests := []struct {
		name    string
		value   any
		wantErr bool
	}{
		{name: "empty", value: map[string]any{}},
		{name: "valid", value: map[string]any{"chat.new": validBinding("n")}},
		{name: "not object", value: []any{}, wantErr: true},
		{name: "too many bindings", value: tooManyBindings, wantErr: true},
		{name: "blank command id", value: map[string]any{"": validBinding("n")}, wantErr: true},
		{name: "blank key", value: map[string]any{"chat.new": validBinding("")}, wantErr: true},
		{name: "key too long", value: map[string]any{"chat.new": validBinding(strings.Repeat("k", 33))}, wantErr: true},
		{name: "binding not object", value: map[string]any{"chat.new": "n"}, wantErr: true},
		{name: "missing field", value: map[string]any{"chat.new": map[string]any{
			"key": "n", "meta": false, "ctrl": true, "alt": false,
		}}, wantErr: true},
		{name: "extra field", value: map[string]any{"chat.new": map[string]any{
			"key": "n", "meta": false, "ctrl": true, "alt": false, "shift": false, "run": true,
		}}, wantErr: true},
		{name: "modifier not bool", value: map[string]any{"chat.new": map[string]any{
			"key": "n", "meta": false, "ctrl": "yes", "alt": false, "shift": false,
		}}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validatePreferenceValue("settings.shortcuts.bindings", test.value)
			if test.wantErr && err == nil {
				t.Fatal("validatePreferenceValue() error = nil, want validation failure")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("validatePreferenceValue() error = %v, want nil", err)
			}
		})
	}
}

func TestShortcutBindingsCloneJSONValueDoesNotAliasNestedBindings(t *testing.T) {
	original := map[string]any{
		"chat.new": map[string]any{
			"key": "n", "meta": false, "ctrl": true, "alt": false, "shift": false,
		},
	}
	cloned, ok := cloneJSONValue(original).(map[string]any)
	if !ok {
		t.Fatalf("cloneJSONValue() type = %T, want map[string]any", cloned)
	}
	clonedBinding, ok := cloned["chat.new"].(map[string]any)
	if !ok {
		t.Fatalf("cloned binding type = %T, want map[string]any", cloned["chat.new"])
	}
	clonedBinding["key"] = "m"
	originalBinding := original["chat.new"].(map[string]any)
	if got := originalBinding["key"]; got != "n" {
		t.Fatalf("original key = %v after cloned mutation, want n", got)
	}
	originalBinding["ctrl"] = false
	if got := clonedBinding["ctrl"]; got != true {
		t.Fatalf("cloned ctrl = %v after original mutation, want true", got)
	}
}

// TestModelProvidersRejectMissingCwd 确认模型提供方 RPC 不会写入默认偏好作用域。
func TestModelProvidersRejectMissingCwd(t *testing.T) {
	server := newModelProviderTestServer(t)

	tests := []struct {
		name    string
		method  string
		payload string
	}{
		{
			name:    "list",
			method:  "modelProviders/list",
			payload: `{}`,
		},
		{
			name:   "save",
			method: "modelProviders/save",
			payload: `{
				"registry":{"vendors":[{"id":"openrouter","label":"OpenRouter","enabled":true,"baseURL":"https://openrouter.ai/api/v1","envKey":"OPENROUTER_API_KEY","codexModelProvider":"openrouter","defaultModel":"openai/gpt-4.1"}]}
			}`,
		},
		{
			name:    "apply",
			method:  "modelProviders/apply",
			payload: `{"vendorId":"openrouter"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := server.Dispatch(context.Background(), tt.method, json.RawMessage(tt.payload))
			if err == nil || !strings.Contains(err.Error(), "cwd is required") {
				t.Fatalf("Dispatch(%s) error = %v, want cwd required", tt.method, err)
			}
		})
	}
}

func TestModelProvidersApplyWritesCodexPreferences(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-deepseek-secret")
	server := newModelProviderTestServer(t)

	_, err := server.Dispatch(context.Background(), "modelProviders/save", json.RawMessage(`{
        "cwd":"/repo/app",
        "registry":{"vendors":[{"id":"deepseek","label":"DeepSeek","enabled":true,"baseURL":"https://api.deepseek.com/v1","envKey":"DEEPSEEK_API_KEY","codexModelProvider":"deepseek","defaultModel":"deepseek-chat","codexHome":"/tmp/codex","codexInstanceKey":"work"}]}
    }`))
	if err != nil {
		t.Fatalf("Dispatch(modelProviders/save) error = %v", err)
	}
	if _, err := server.Dispatch(context.Background(), "modelProviders/apply", json.RawMessage(`{"cwd":"/repo/app","vendorId":"deepseek"}`)); err != nil {
		t.Fatalf("Dispatch(modelProviders/apply) error = %v", err)
	}

	assertPreference := func(key, want string) {
		t.Helper()
		result, err := server.Dispatch(context.Background(), "ui/preferences/get", json.RawMessage(`{"cwd":"/repo/app","key":"`+key+`"}`))
		if err != nil {
			t.Fatalf("Dispatch(ui/preferences/get %s) error = %v", key, err)
		}
		var got string
		if err := json.Unmarshal(result, &got); err != nil {
			t.Fatalf("json.Unmarshal(%s) error = %v", result, err)
		}
		if got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	assertPreference("settings.provider.codex.codexModelProvider", "deepseek")
	assertPreference("settings.provider.codex.codexHome", "/tmp/codex")
	assertPreference("settings.provider.codex.codexInstanceKey", "work")
}

func TestModelProvidersApplyPreservesCodexIdentityWhenVendorOmitsBinding(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-openrouter-secret")
	server := newModelProviderTestServer(t)

	setPreference := func(key, value string) {
		t.Helper()
		result, err := server.Dispatch(context.Background(), "ui/preferences/set", json.RawMessage(`{
			"cwd":"/repo/app",
			"key":"`+key+`",
			"value":"`+value+`"
		}`))
		if err != nil {
			t.Fatalf("Dispatch(ui/preferences/set %s) error = %v", key, err)
		}
		if string(result) != `{"ok":true}` {
			t.Fatalf("Dispatch(ui/preferences/set %s) = %s, want ok", key, result)
		}
	}
	setPreference("settings.provider.codex.codexHome", "/existing/codex")
	setPreference("settings.provider.codex.codexInstanceKey", "existing-key")

	_, err := server.Dispatch(context.Background(), "modelProviders/save", json.RawMessage(`{
		"cwd":"/repo/app",
		"registry":{"vendors":[{"id":"openrouter","label":"OpenRouter","enabled":true,"baseURL":"https://openrouter.ai/api/v1","envKey":"OPENROUTER_API_KEY","codexModelProvider":"openrouter","defaultModel":"openai/gpt-4.1"}]}
	}`))
	if err != nil {
		t.Fatalf("Dispatch(modelProviders/save) error = %v", err)
	}
	if _, err := server.Dispatch(context.Background(), "modelProviders/apply", json.RawMessage(`{"cwd":"/repo/app","vendorId":"openrouter"}`)); err != nil {
		t.Fatalf("Dispatch(modelProviders/apply) error = %v", err)
	}

	assertPreference := func(key, want string) {
		t.Helper()
		result, err := server.Dispatch(context.Background(), "ui/preferences/get", json.RawMessage(`{"cwd":"/repo/app","key":"`+key+`"}`))
		if err != nil {
			t.Fatalf("Dispatch(ui/preferences/get %s) error = %v", key, err)
		}
		var got string
		if err := json.Unmarshal(result, &got); err != nil {
			t.Fatalf("json.Unmarshal(%s) error = %v", result, err)
		}
		if got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	assertPreference("settings.provider.codex.codexModelProvider", "openrouter")
	assertPreference("settings.provider.codex.codexHome", "/existing/codex")
	assertPreference("settings.provider.codex.codexInstanceKey", "existing-key")
}

func TestModelProvidersApplyRejectsMissingEnv(t *testing.T) {
	t.Setenv("QWEN_API_KEY", "")
	server := newModelProviderTestServer(t)

	_, err := server.Dispatch(context.Background(), "modelProviders/save", json.RawMessage(`{
        "cwd":"/repo/app",
        "registry":{"vendors":[{"id":"qwen","label":"Qwen","enabled":true,"baseURL":"https://dashscope.aliyuncs.com/compatible-mode/v1","envKey":"QWEN_API_KEY","codexModelProvider":"qwen","defaultModel":"qwen-plus"}]}
    }`))
	if err != nil {
		t.Fatalf("Dispatch(modelProviders/save) error = %v", err)
	}
	_, err = server.Dispatch(context.Background(), "modelProviders/apply", json.RawMessage(`{"cwd":"/repo/app","vendorId":"qwen"}`))
	if err == nil || !strings.Contains(err.Error(), "environment variable QWEN_API_KEY is not configured") {
		t.Fatalf("Dispatch(modelProviders/apply) error = %v, want missing env", err)
	}
}
