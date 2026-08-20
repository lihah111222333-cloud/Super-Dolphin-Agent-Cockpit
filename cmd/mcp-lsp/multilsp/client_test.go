package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestNormalizeJDTLSProfileTypeDefinitionCapability(t *testing.T) {
	for profile, wantHidden := range map[string]bool{
		ServerProfileJDTLS160: true,
		"":                    false,
		"jdk-jdtls@1.61.0":    false,
		"other-server":        false,
	} {
		capabilities := normalizeServerProfileCapabilities(protocol.ServerCapabilities{TypeDefinitionProvider: true}, profile)
		if got := capabilities.TypeDefinitionProvider == nil; got != wantHidden {
			t.Fatalf("profile=%q typeDefinitionProvider nil=%t want=%t", profile, got, wantHidden)
		}
	}
}

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

func TestDecodeInitializeResultDisablesSemanticTokensWithoutLegend(t *testing.T) {
	for name, raw := range map[string]string{
		"missing legend":   `{"capabilities":{"semanticTokensProvider":{"full":true}}}`,
		"boolean provider": `{"capabilities":{"semanticTokensProvider":true}}`,
	} {
		t.Run(name, func(t *testing.T) {
			capabilities, _, err := decodeInitializeResultAttribution(json.RawMessage(raw))
			if err != nil {
				t.Fatalf("decodeInitializeResultAttribution() error = %v", err)
			}
			if semanticTokensFullCapabilityAvailable(capabilities.SemanticTokensProvider) {
				t.Fatalf("semanticTokensProvider = %#v, want disabled for missing legend", capabilities.SemanticTokensProvider)
			}
		})
	}
}

func TestBrokenSemanticTokensDoesNotSuppressOtherDynamicCapabilities(t *testing.T) {
	tracker := newDynamicRegistrationTracker()
	handler := dynamicRegistrationRequestHandler(tracker, nil)
	for _, raw := range []string{
		`{"registrations":[{"id":"completion","method":"textDocument/completion"}]}`,
		`{"registrations":[{"id":"definition","method":"textDocument/definition"}]}`,
		`{"registrations":[{"id":"semantic-full","method":"textDocument/semanticTokens","registerOptions":{"full":{"delta":true}}}]}`,
	} {
		if _, err := handler(context.Background(), LSPCompatMethodClientRegisterCapability, json.RawMessage(raw)); err != nil {
			t.Fatalf("register dynamic capability: %v", err)
		}
	}

	capabilities := tracker.serverCapabilities(protocol.ServerCapabilities{
		SemanticTokensProvider: invalidSemanticTokensProvider{},
	})
	if capabilities.SemanticTokensProvider != nil {
		t.Fatalf("broken semantic tokens provider = %#v, want nil", capabilities.SemanticTokensProvider)
	}
	if !serverCapabilityAvailable(capabilities.CompletionProvider) {
		t.Fatal("dynamic completion was suppressed by broken semantic tokens legend")
	}
	if !serverCapabilityAvailable(capabilities.DefinitionProvider) {
		t.Fatal("dynamic definition was suppressed by broken semantic tokens legend")
	}

	client := &client{
		capabilities:               protocol.ServerCapabilities{SemanticTokensProvider: invalidSemanticTokensProvider{}},
		dynamicRegistrations:       tracker,
		semanticTokensLegendBroken: true,
	}
	snapshot := client.ServerCapabilities()
	if snapshot.SemanticTokensProvider != nil || !serverCapabilityAvailable(snapshot.CompletionProvider) || !serverCapabilityAvailable(snapshot.DefinitionProvider) {
		t.Fatalf("client capability snapshot = %#v, want only semantic tokens disabled", snapshot)
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

func TestRequestCompletionSkipsUnadvertisedCapability(t *testing.T) {
	client := &unadvertisedCompletionClient{}
	_, err := requestDocumentForClientWithCapability[*protocol.CompletionList](
		context.Background(),
		&manager{},
		client,
		documentRef{},
		protocol.MethodCompletion,
		nil,
		nil,
		clientSupportsCompletion,
	)
	if !errors.Is(err, lspmanager.ErrUnsupportedCapability) {
		t.Fatalf("requestDocumentForClientWithCapability() error = %v, want ErrUnsupportedCapability", err)
	}
	var codedErr *common.CodedToolError
	if !errors.As(err, &codedErr) {
		t.Fatalf("requestDocumentForClientWithCapability() error = %T, want *common.CodedToolError", err)
	}
	if codedErr.Code != "capability_unsupported" || codedErr.Retryable {
		t.Fatalf("completion capability error = %#v, want non-retryable capability_unsupported", codedErr)
	}
	if codedErr.Meta["lsp_method"] != protocol.MethodCompletion {
		t.Fatalf("completion capability metadata = %#v, want lsp_method=%q", codedErr.Meta, protocol.MethodCompletion)
	}
	if client.requested {
		t.Fatal("requestDocumentForClientWithCapability() sent textDocument/completion despite absent server capability")
	}
}

func TestClientSupportsCompletionPreservesTrueAndUnknown(t *testing.T) {
	cases := []struct {
		name   string
		client Client
		want   bool
	}{
		{name: "advertised", client: &advertisedCompletionClient{}, want: true},
		{name: "unknown_client", client: noopClient{}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clientSupportsCompletion(tc.client); got != tc.want {
				t.Fatalf("clientSupportsCompletion() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestClientCapabilitiesAdvertiseWorkspaceApplyEdit(t *testing.T) {
	caps := clientCapabilities()
	if caps.Workspace == nil || !caps.Workspace.ApplyEdit {
		t.Fatalf("client capabilities must advertise workspace applyEdit for code actions: %#v", caps.Workspace)
	}
	raw, err := json.Marshal(caps)
	if err != nil {
		t.Fatalf("marshal client capabilities: %v", err)
	}
	var payload struct {
		Workspace struct {
			ApplyEdit bool `json:"applyEdit"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode marshaled client capabilities: %v", err)
	}
	if !payload.Workspace.ApplyEdit {
		t.Fatalf("serialized client capabilities omitted workspace.applyEdit: %s", raw)
	}
	var textDocument struct {
		Completion struct {
			CompletionItem struct {
				SnippetSupport bool `json:"snippetSupport"`
			} `json:"completionItem"`
		} `json:"completion"`
	}
	var full struct {
		TextDocument struct {
			Completion json.RawMessage `json:"completion"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(raw, &full); err != nil {
		t.Fatalf("decode textDocument capabilities: %v", err)
	}
	if err := json.Unmarshal(full.TextDocument.Completion, &textDocument.Completion); err != nil {
		t.Fatalf("decode completion capabilities: %v", err)
	}
	if !textDocument.Completion.CompletionItem.SnippetSupport {
		t.Fatalf("serialized client capabilities omitted completionItem.snippetSupport: %s", raw)
	}
}

func TestClientSupportsMethodUsesAdvertisedCapabilities(t *testing.T) {
	cases := []struct {
		name   string
		method string
		enable func(*protocol.ServerCapabilities)
	}{
		{name: "hover", method: protocol.MethodHover, enable: func(c *protocol.ServerCapabilities) { c.HoverProvider = true }},
		{name: "definition", method: protocol.MethodDefinition, enable: func(c *protocol.ServerCapabilities) { c.DefinitionProvider = true }},
		{name: "implementation", method: protocol.MethodImplementation, enable: func(c *protocol.ServerCapabilities) { c.ImplementationProvider = true }},
		{name: "type_definition", method: protocol.MethodTypeDefinition, enable: func(c *protocol.ServerCapabilities) { c.TypeDefinitionProvider = true }},
		{name: "signature_help", method: protocol.MethodSignatureHelp, enable: func(c *protocol.ServerCapabilities) { c.SignatureHelpProvider = true }},
		{name: "references", method: protocol.MethodReferences, enable: func(c *protocol.ServerCapabilities) { c.ReferencesProvider = true }},
		{name: "document_symbol", method: protocol.MethodDocumentSymbol, enable: func(c *protocol.ServerCapabilities) { c.DocumentSymbolProvider = true }},
		{name: "completion", method: protocol.MethodCompletion, enable: func(c *protocol.ServerCapabilities) { c.CompletionProvider = true }},
		{name: "call_hierarchy", method: protocol.MethodPrepareCallHierarchy, enable: func(c *protocol.ServerCapabilities) { c.CallHierarchyProvider = true }},
		{name: "type_hierarchy", method: protocol.MethodPrepareTypeHierarchy, enable: func(c *protocol.ServerCapabilities) { c.TypeHierarchyProvider = true }},
		{name: "folding_range", method: protocol.MethodFoldingRange, enable: func(c *protocol.ServerCapabilities) { c.FoldingRangeProvider = true }},
		{name: "semantic_tokens_full", method: protocol.MethodSemanticTokensFull, enable: func(c *protocol.ServerCapabilities) { c.SemanticTokensProvider = true }},
		{name: "rename", method: protocol.MethodRename, enable: func(c *protocol.ServerCapabilities) { c.RenameProvider = true }},
		{name: "code_action", method: protocol.MethodCodeAction, enable: func(c *protocol.ServerCapabilities) { c.CodeActionProvider = true }},
		{name: "formatting", method: protocol.MethodFormatting, enable: func(c *protocol.ServerCapabilities) { c.DocumentFormattingProvider = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &staticCapabilitiesClient{}
			if clientSupportsMethod(client, tc.method) {
				t.Fatalf("clientSupportsMethod(%q) = true with absent initialize capability", tc.method)
			}
			tc.enable(&client.capabilities)
			if !clientSupportsMethod(client, tc.method) {
				t.Fatalf("clientSupportsMethod(%q) = false with advertised initialize capability", tc.method)
			}
		})
	}
	if !clientSupportsMethod(noopClient{}, protocol.MethodImplementation) {
		t.Fatal("clientSupportsMethod() rejected legacy client without a capability snapshot")
	}
	if !clientSupportsMethod(&staticCapabilitiesClient{}, "textDocument/futureMethod") {
		t.Fatal("clientSupportsMethod() rejected an unknown future method")
	}
}

func TestConcreteClientAllowsDynamicallyRegisterableDocumentSymbolsBeforeRegistration(t *testing.T) {
	client := &client{
		capabilities:         protocol.ServerCapabilities{},
		dynamicRegistrations: newDynamicRegistrationTracker(),
	}
	if !clientSupportsMethod(client, protocol.MethodDocumentSymbol) {
		t.Fatal("concrete client rejected document_symbol before JDTLS dynamic registration")
	}
	handler := dynamicRegistrationRequestHandler(client.dynamicRegistrations, nil)
	if _, err := handler(context.Background(), LSPCompatMethodClientRegisterCapability, json.RawMessage(`{
		"registrations":[{"id":"jdtls-symbols","method":"textDocument/documentSymbol"}]
	}`)); err != nil {
		t.Fatalf("register dynamic document_symbol capability: %v", err)
	}
	if !clientSupportsMethod(client, protocol.MethodDocumentSymbol) {
		t.Fatal("concrete client rejected document_symbol after dynamic registration")
	}
	if _, err := handler(context.Background(), LSPCompatMethodClientUnregisterCapability, json.RawMessage(`{
		"unregisterations":[{"id":"jdtls-symbols","method":"textDocument/documentSymbol"}]
	}`)); err != nil {
		t.Fatalf("unregister dynamic document_symbol capability: %v", err)
	}
	if clientSupportsMethod(client, protocol.MethodDocumentSymbol) {
		t.Fatal("concrete client kept document_symbol enabled after dynamic unregister")
	}
}

func TestConcreteClientAllowsDynamicallyRegisterableCompletionBeforeRegistration(t *testing.T) {
	client := &client{
		capabilities:         protocol.ServerCapabilities{},
		dynamicRegistrations: newDynamicRegistrationTracker(),
	}
	if !clientSupportsMethod(client, protocol.MethodCompletion) {
		t.Fatal("concrete client rejected completion before dynamic registration")
	}
	handler := dynamicRegistrationRequestHandler(client.dynamicRegistrations, nil)
	if _, err := handler(context.Background(), LSPCompatMethodClientRegisterCapability, json.RawMessage(`{
		"registrations":[{"id":"css-html-completion","method":"textDocument/completion"}]
	}`)); err != nil {
		t.Fatalf("register dynamic completion capability: %v", err)
	}
	if !clientSupportsMethod(client, protocol.MethodCompletion) {
		t.Fatal("concrete client rejected completion after dynamic registration")
	}
	if _, err := handler(context.Background(), LSPCompatMethodClientUnregisterCapability, json.RawMessage(`{
		"unregisterations":[{"id":"css-html-completion","method":"textDocument/completion"}]
	}`)); err != nil {
		t.Fatalf("unregister dynamic completion capability: %v", err)
	}
	if clientSupportsMethod(client, protocol.MethodCompletion) {
		t.Fatal("concrete client kept completion enabled after dynamic unregister")
	}
}

func TestRequestImplementationSkipsUnadvertisedCapability(t *testing.T) {
	client := &staticCapabilitiesClient{}
	_, err := requestDocumentForClientWithCapability[[]protocol.LocationResult](
		context.Background(),
		&manager{},
		client,
		documentRef{},
		protocol.MethodImplementation,
		nil,
		nil,
		clientMethodCapabilityGuard(protocol.MethodImplementation),
	)
	assertCapabilityRequestWasSkipped(t, err, protocol.MethodImplementation, client.requested)
	var codedErr *common.CodedToolError
	if !errors.As(err, &codedErr) {
		t.Fatalf("implementation capability error = %T, want *common.CodedToolError", err)
	}
	if known, _ := codedErr.Meta["capabilities_known"].(bool); !known {
		t.Fatalf("implementation capability metadata = %#v, want capabilities_known=true", codedErr.Meta)
	}
	if snapshot, _ := codedErr.Meta["capability_snapshot"].(string); !strings.Contains(snapshot, "implementation=false") {
		t.Fatalf("implementation capability metadata = %#v, want implementation=false snapshot", codedErr.Meta)
	}
}

func TestPrepareCallHierarchySkipsUnadvertisedCapability(t *testing.T) {
	client := &staticCapabilitiesClient{}
	_, err := prepareHierarchy[protocol.CallHierarchyItem](
		context.Background(),
		&manager{},
		client,
		protocol.MethodPrepareCallHierarchy,
		"file:///workspace/main.php",
		protocol.Position{},
	)
	assertCapabilityRequestWasSkipped(t, err, protocol.MethodPrepareCallHierarchy, client.requested)
}

func TestDynamicRegistrationTracksAllGuardedOptionalMethods(t *testing.T) {
	cases := []struct {
		name               string
		registrationMethod string
		requestMethod      string
		registerOptions    map[string]any
	}{
		{name: "completion", registrationMethod: protocol.MethodCompletion, requestMethod: protocol.MethodCompletion},
		{name: "document_symbol", registrationMethod: protocol.MethodDocumentSymbol, requestMethod: protocol.MethodDocumentSymbol},
		{name: "definition", registrationMethod: protocol.MethodDefinition, requestMethod: protocol.MethodDefinition},
		{name: "implementation", registrationMethod: protocol.MethodImplementation, requestMethod: protocol.MethodImplementation},
		{name: "type_definition", registrationMethod: protocol.MethodTypeDefinition, requestMethod: protocol.MethodTypeDefinition},
		{name: "references", registrationMethod: protocol.MethodReferences, requestMethod: protocol.MethodReferences},
		{name: "call_hierarchy", registrationMethod: protocol.MethodPrepareCallHierarchy, requestMethod: protocol.MethodPrepareCallHierarchy},
		{name: "type_hierarchy", registrationMethod: protocol.MethodPrepareTypeHierarchy, requestMethod: protocol.MethodPrepareTypeHierarchy},
		{name: "code_action", registrationMethod: protocol.MethodCodeAction, requestMethod: protocol.MethodCodeAction},
		{name: "signature_help", registrationMethod: protocol.MethodSignatureHelp, requestMethod: protocol.MethodSignatureHelp},
		{name: "formatting", registrationMethod: protocol.MethodFormatting, requestMethod: protocol.MethodFormatting},
		{name: "folding_range", registrationMethod: protocol.MethodFoldingRange, requestMethod: protocol.MethodFoldingRange},
		{
			name:               "semantic_tokens_full",
			registrationMethod: protocol.MethodSemanticTokens,
			requestMethod:      protocol.MethodSemanticTokensFull,
			registerOptions:    map[string]any{"range": true, "full": true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tracker := newDynamicRegistrationTracker()
			client := &dynamicDocumentSymbolsClient{tracker: tracker}
			handler := dynamicRegistrationRequestHandler(tracker, nil)
			registrationEntry := map[string]any{"id": tc.name, "method": tc.registrationMethod}
			if tc.registerOptions != nil {
				registrationEntry["registerOptions"] = tc.registerOptions
			}
			registration, err := json.Marshal(map[string]any{
				"registrations": []map[string]any{registrationEntry},
			})
			if err != nil {
				t.Fatalf("marshal registration: %v", err)
			}
			if _, err := handler(context.Background(), LSPCompatMethodClientRegisterCapability, registration); err != nil {
				t.Fatalf("register %s: %v", tc.registrationMethod, err)
			}
			if !clientSupportsMethod(client, tc.requestMethod) {
				t.Fatalf("dynamic registration %q did not enable %q", tc.registrationMethod, tc.requestMethod)
			}

			unregistration, err := json.Marshal(map[string]any{
				"unregisterations": []map[string]any{{"id": tc.name, "method": tc.registrationMethod}},
			})
			if err != nil {
				t.Fatalf("marshal unregistration: %v", err)
			}
			if _, err := handler(context.Background(), LSPCompatMethodClientUnregisterCapability, unregistration); err != nil {
				t.Fatalf("unregister %s: %v", tc.registrationMethod, err)
			}
			if clientSupportsMethod(client, tc.requestMethod) {
				t.Fatalf("dynamic unregister %q left %q enabled", tc.registrationMethod, tc.requestMethod)
			}
		})
	}
}

func TestSemanticTokensFullGuardRequiresFullSubcapability(t *testing.T) {
	cases := []struct {
		name     string
		provider any
		want     bool
	}{
		{name: "absent", provider: nil, want: false},
		{name: "range_only", provider: map[string]any{"range": true}, want: false},
		{name: "explicit_false", provider: map[string]any{"range": true, "full": false}, want: false},
		{name: "full_true", provider: map[string]any{"full": true}, want: true},
		{name: "full_options", provider: map[string]any{"full": map[string]any{"delta": true}}, want: true},
		{name: "raw_false", provider: json.RawMessage(`{"range":true,"full":false}`), want: false},
		{name: "raw_true", provider: json.RawMessage(`{"full":{"delta":true}}`), want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &staticCapabilitiesClient{capabilities: protocol.ServerCapabilities{
				SemanticTokensProvider: tc.provider,
			}}
			if got := clientSupportsMethod(client, protocol.MethodSemanticTokensFull); got != tc.want {
				t.Fatalf("clientSupportsMethod(semanticTokens/full) = %t, want %t for %#v", got, tc.want, tc.provider)
			}
		})
	}

	client := &staticCapabilitiesClient{capabilities: protocol.ServerCapabilities{
		SemanticTokensProvider: map[string]any{"range": true, "full": false},
	}}
	_, err := requestDocumentForClientWithCapability[protocol.SemanticTokens](
		context.Background(),
		&manager{},
		client,
		documentRef{},
		protocol.MethodSemanticTokensFull,
		nil,
		nil,
		clientMethodCapabilityGuard(protocol.MethodSemanticTokensFull),
	)
	assertCapabilityRequestWasSkipped(t, err, protocol.MethodSemanticTokensFull, client.requested)

	advertised := &staticCapabilitiesClient{capabilities: protocol.ServerCapabilities{
		SemanticTokensProvider: map[string]any{"full": true},
	}}
	requestManager := &manager{workspaces: map[string]*workspaceClient{
		"semantic-tokens": {
			key:        "semantic-tokens",
			client:     advertised,
			generation: 1,
			state:      workspaceStateActive,
		},
	}}
	_, err = requestDocumentForClientWithCapability[protocol.SemanticTokens](
		context.Background(),
		requestManager,
		advertised,
		documentRef{},
		protocol.MethodSemanticTokensFull,
		nil,
		nil,
		clientMethodCapabilityGuard(protocol.MethodSemanticTokensFull),
	)
	if err != nil {
		t.Fatalf("advertised semanticTokens/full request: %v", err)
	}
	if !advertised.requested {
		t.Fatal("advertised semanticTokens/full capability did not send the request")
	}
}

func TestDynamicSemanticTokensRangeOnlyDoesNotEnableFull(t *testing.T) {
	tracker := newDynamicRegistrationTracker()
	handler := dynamicRegistrationRequestHandler(tracker, nil)
	client := &dynamicDocumentSymbolsClient{tracker: tracker}
	if _, err := handler(context.Background(), LSPCompatMethodClientRegisterCapability, json.RawMessage(`{
		"registrations":[{
			"id":"semantic-range-only",
			"method":"textDocument/semanticTokens",
			"registerOptions":{"range":true,"full":false}
		}]
	}`)); err != nil {
		t.Fatalf("register range-only semantic tokens: %v", err)
	}
	snapshot := client.ServerCapabilities()
	if clientSupportsMethod(client, protocol.MethodSemanticTokensFull) {
		t.Fatal("range-only dynamic semantic token registration enabled semanticTokens/full")
	}

	requestClient := &staticCapabilitiesClient{capabilities: snapshot}
	_, err := requestDocumentForClientWithCapability[protocol.SemanticTokens](
		context.Background(),
		&manager{},
		requestClient,
		documentRef{},
		protocol.MethodSemanticTokensFull,
		nil,
		nil,
		clientMethodCapabilityGuard(protocol.MethodSemanticTokensFull),
	)
	assertCapabilityRequestWasSkipped(t, err, protocol.MethodSemanticTokensFull, requestClient.requested)
}

func TestDynamicSemanticTokensFullPreservesStaticProvider(t *testing.T) {
	tracker := newDynamicRegistrationTracker()
	handler := dynamicRegistrationRequestHandler(tracker, nil)
	if _, err := handler(context.Background(), LSPCompatMethodClientRegisterCapability, json.RawMessage(`{
		"registrations":[{
			"id":"semantic-full",
			"method":"textDocument/semanticTokens",
			"registerOptions":{"range":false,"full":{"delta":true}}
		}]
	}`)); err != nil {
		t.Fatalf("register semantic tokens full: %v", err)
	}
	legend := map[string]any{"tokenTypes": []any{"function"}, "tokenModifiers": []any{}}
	snapshot := tracker.serverCapabilities(protocol.ServerCapabilities{
		SemanticTokensProvider: map[string]any{"range": true, "full": false, "legend": legend},
	})
	provider, ok := snapshot.SemanticTokensProvider.(map[string]any)
	if !ok {
		t.Fatalf("merged semantic token provider = %T, want map", snapshot.SemanticTokensProvider)
	}
	if provider["range"] != true || !reflect.DeepEqual(provider["legend"], legend) || provider["full"] != true {
		t.Fatalf("merged semantic token provider = %#v, want preserved range/legend and full=true", provider)
	}
}

func TestDynamicRegistrationRejectsEmptyIDAtomically(t *testing.T) {
	tracker := newDynamicRegistrationTracker()
	handler := dynamicRegistrationRequestHandler(tracker, nil)
	client := &dynamicDocumentSymbolsClient{tracker: tracker}

	_, err := handler(context.Background(), LSPCompatMethodClientRegisterCapability, json.RawMessage(`{
		"registrations":[
			{"id":"implementation-valid","method":"textDocument/implementation"},
			{"id":" ","method":"textDocument/completion"}
		]
	}`))
	if err == nil {
		t.Fatal("registration with an empty ID succeeded")
	}
	if clientSupportsMethod(client, protocol.MethodImplementation) {
		t.Fatal("registration request partially mutated the capability ledger before rejecting an empty ID")
	}

	if _, err := handler(context.Background(), LSPCompatMethodClientRegisterCapability, json.RawMessage(`{
		"registrations":[{"id":"implementation-valid","method":"textDocument/implementation"}]
	}`)); err != nil {
		t.Fatalf("register valid implementation capability: %v", err)
	}
	_, err = handler(context.Background(), LSPCompatMethodClientUnregisterCapability, json.RawMessage(`{
		"unregisterations":[
			{"id":"implementation-valid","method":"textDocument/implementation"},
			{"id":"","method":"textDocument/completion"}
		]
	}`))
	if err == nil {
		t.Fatal("unregistration with an empty ID succeeded")
	}
	if !clientSupportsMethod(client, protocol.MethodImplementation) {
		t.Fatal("unregistration request partially mutated the capability ledger before rejecting an empty ID")
	}
}

func assertCapabilityRequestWasSkipped(t *testing.T, err error, method string, requested bool) {
	t.Helper()
	if !errors.Is(err, lspmanager.ErrUnsupportedCapability) {
		t.Fatalf("capability guard error = %v, want ErrUnsupportedCapability", err)
	}
	var codedErr *common.CodedToolError
	if !errors.As(err, &codedErr) {
		t.Fatalf("capability guard error = %T, want *common.CodedToolError", err)
	}
	if codedErr.Code != "capability_unsupported" || codedErr.Retryable {
		t.Fatalf("capability guard error = %#v, want non-retryable capability_unsupported", codedErr)
	}
	if codedErr.Meta["lsp_method"] != method {
		t.Fatalf("capability metadata = %#v, want lsp_method=%q", codedErr.Meta, method)
	}
	if requested {
		t.Fatalf("capability guard sent %s despite absent server capability", method)
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

type unadvertisedCompletionClient struct {
	noopClient
	requested bool
}

func (c *unadvertisedCompletionClient) Request(context.Context, string, any) (json.RawMessage, error) {
	c.requested = true
	return json.RawMessage(`{"isIncomplete":false,"items":[]}`), nil
}

func (unadvertisedCompletionClient) ServerCapabilities() protocol.ServerCapabilities {
	return protocol.ServerCapabilities{CompletionProvider: false}
}

type advertisedCompletionClient struct {
	noopClient
}

func (advertisedCompletionClient) ServerCapabilities() protocol.ServerCapabilities {
	return protocol.ServerCapabilities{CompletionProvider: true}
}

type staticCapabilitiesClient struct {
	noopClient
	capabilities protocol.ServerCapabilities
	requested    bool
}

func (c *staticCapabilitiesClient) Request(context.Context, string, any) (json.RawMessage, error) {
	c.requested = true
	return json.RawMessage("[]"), nil
}

func (c *staticCapabilitiesClient) ServerCapabilities() protocol.ServerCapabilities {
	return c.capabilities
}
