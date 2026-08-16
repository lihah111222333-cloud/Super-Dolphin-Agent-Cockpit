package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestDecodeInitializeResultPreservesSemanticTokensLegend(t *testing.T) {
	capabilities, _, err := decodeInitializeResultAttribution(json.RawMessage(`{
		"capabilities":{"semanticTokensProvider":{"legend":{
			"tokenTypes":["namespace","function"],
			"tokenModifiers":["declaration","readonly"]
		}}}
	}`))
	if err != nil {
		t.Fatalf("decodeInitializeResultAttribution() error = %v", err)
	}
	tokenTypes, tokenModifiers, err := semanticTokensLegendFromProvider(capabilities.SemanticTokensProvider)
	if err != nil {
		t.Fatalf("semanticTokensLegendFromProvider() error = %v", err)
	}
	if got := strings.Join(tokenTypes, ","); got != "namespace,function" {
		t.Errorf("token types = %q", got)
	}
	if got := strings.Join(tokenModifiers, ","); got != "declaration,readonly" {
		t.Errorf("token modifiers = %q", got)
	}
}

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
	if !serverCapabilityAvailable(tracker.serverCapabilities(protocol.ServerCapabilities{}).DiagnosticProvider) {
		t.Fatal("dynamic diagnostic registration was not reflected in server capabilities")
	}

	if _, err := handler(context.Background(), LSPCompatMethodClientUnregisterCapability, json.RawMessage(`{
		"unregisterations":[{"id":"pyright-diagnostics","method":"textDocument/diagnostic"}]
	}`)); err != nil {
		t.Fatalf("client/unregisterCapability: %v", err)
	}
	if serverCapabilityAvailable(tracker.serverCapabilities(protocol.ServerCapabilities{}).DiagnosticProvider) {
		t.Fatal("dynamic diagnostic registration survived unregister")
	}
}

func TestDocumentSymbolCapabilityTracksDynamicRegistrationLifecycle(t *testing.T) {
	tracker := newDynamicRegistrationTracker()
	handler := dynamicRegistrationRequestHandler(tracker, nil)
	client := &dynamicDocumentSymbolsClient{tracker: tracker}

	if _, err := handler(context.Background(), LSPCompatMethodClientRegisterCapability, json.RawMessage(`{
		"registrations":[{"id":"sql-symbols","method":"textDocument/documentSymbol","registerOptions":{}}]
	}`)); err != nil {
		t.Fatalf("client/registerCapability: %v", err)
	}
	if !clientSupportsDocumentSymbols(client) {
		t.Fatal("documentSymbol capability remained unavailable after dynamic registration")
	}

	if _, err := handler(context.Background(), LSPCompatMethodClientUnregisterCapability, json.RawMessage(`{
		"unregisterations":[{"id":"sql-symbols","method":"textDocument/documentSymbol"}]
	}`)); err != nil {
		t.Fatalf("client/unregisterCapability: %v", err)
	}
	if clientSupportsDocumentSymbols(client) {
		t.Fatal("documentSymbol capability survived dynamic unregister")
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

func TestRequestDocumentSymbolsSkipsUnadvertisedCapability(t *testing.T) {
	client := &unadvertisedDocumentSymbolsClient{}
	_, err := (&manager{}).requestDocumentSymbols(context.Background(), client, documentRef{})
	if !errors.Is(err, lspmanager.ErrUnsupportedCapability) {
		t.Fatalf("requestDocumentSymbols() error = %v, want ErrUnsupportedCapability", err)
	}
	var codedErr *common.CodedToolError
	if !errors.As(err, &codedErr) {
		t.Fatalf("requestDocumentSymbols() error = %T, want *common.CodedToolError", err)
	}
	const wantHint = "next: use a language server that advertises or dynamically registers textDocument/documentSymbol"
	if codedErr.Hint != wantHint {
		t.Fatalf("requestDocumentSymbols() hint = %q, want %q", codedErr.Hint, wantHint)
	}
	if client.requested {
		t.Fatal("requestDocumentSymbols() sent textDocument/documentSymbol despite absent server capability")
	}
}

type dynamicDocumentSymbolsClient struct {
	noopClient
	tracker *dynamicRegistrationTracker
}

func (c *dynamicDocumentSymbolsClient) ServerCapabilities() protocol.ServerCapabilities {
	return c.tracker.serverCapabilities(protocol.ServerCapabilities{})
}

type unadvertisedDocumentSymbolsClient struct {
	noopClient
	requested bool
}

func (c *unadvertisedDocumentSymbolsClient) Request(context.Context, string, any) (json.RawMessage, error) {
	c.requested = true
	return json.RawMessage("[]"), nil
}

func (unadvertisedDocumentSymbolsClient) ServerCapabilities() protocol.ServerCapabilities {
	return protocol.ServerCapabilities{}
}
