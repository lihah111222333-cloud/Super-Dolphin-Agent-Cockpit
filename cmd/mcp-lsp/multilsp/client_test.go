package multilsp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func TestInitOptionsSettingsAnswerWorkspaceConfiguration(t *testing.T) {
	initOptions := map[string]any{
		"settings": map[string]any{
			"python": map[string]any{
				"pythonPath": "/__super_dolphin_no_system_python__/python",
			},
		},
	}
	handler := configurationRequestHandlerFromInitOptions(initOptions)
	if handler == nil {
		t.Fatal("configurationRequestHandlerFromInitOptions() = nil, want handler")
	}

	result, err := handler(context.Background(), LSPCompatMethodWorkspaceConfiguration, json.RawMessage(`{"items":[{"section":"python"},{"section":"python.analysis"}]}`))
	if err != nil {
		t.Fatalf("workspace/configuration handler: %v", err)
	}
	items, ok := result.([]any)
	if !ok {
		t.Fatalf("workspace/configuration result = %T, want []any", result)
	}
	if len(items) != 2 {
		t.Fatalf("workspace/configuration item count = %d, want 2", len(items))
	}
	python, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("python section = %#v, want map", items[0])
	}
	if got := python["pythonPath"]; got != "/__super_dolphin_no_system_python__/python" {
		t.Fatalf("python.pythonPath = %#v, want packaged no-system interpreter sentinel", got)
	}
	if items[1] != nil {
		t.Fatalf("python.analysis section = %#v, want nil for unset section", items[1])
	}
}

func TestDynamicRegistrationTracksDiagnosticProvider(t *testing.T) {
	tracker := newDynamicRegistrationTracker()
	handler := dynamicRegistrationRequestHandler(tracker, nil)

	if _, err := handler(context.Background(), LSPCompatMethodClientRegisterCapability, json.RawMessage(`{
		"registrations":[{"id":"pyright-diagnostics","method":"textDocument/diagnostic","registerOptions":{}}]
	}`)); err != nil {
		t.Fatalf("client/registerCapability: %v", err)
	}
	if !diagnosticProviderAvailable(tracker.serverCapabilities(protocol.ServerCapabilities{}).DiagnosticProvider) {
		t.Fatal("dynamic diagnostic registration was not reflected in server capabilities")
	}

	if _, err := handler(context.Background(), LSPCompatMethodClientUnregisterCapability, json.RawMessage(`{
		"unregisterations":[{"id":"pyright-diagnostics","method":"textDocument/diagnostic"}]
	}`)); err != nil {
		t.Fatalf("client/unregisterCapability: %v", err)
	}
	if diagnosticProviderAvailable(tracker.serverCapabilities(protocol.ServerCapabilities{}).DiagnosticProvider) {
		t.Fatal("dynamic diagnostic registration survived unregister")
	}
}

func TestDynamicRegistrationHandlerDelegatesWorkspaceConfiguration(t *testing.T) {
	next := configurationRequestHandlerFromInitOptions(map[string]any{
		"settings": map[string]any{"python": map[string]any{"pythonPath": "/tmp/python"}},
	})
	handler := dynamicRegistrationRequestHandler(newDynamicRegistrationTracker(), next)

	result, err := handler(context.Background(), LSPCompatMethodWorkspaceConfiguration, json.RawMessage(`{"items":[{"section":"python"}]}`))
	if err != nil {
		t.Fatalf("workspace/configuration: %v", err)
	}
	items, ok := result.([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("workspace/configuration result = %#v, want one item", result)
	}
}
