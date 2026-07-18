package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
)

func TestPrepareCodexToolSurfaceFiltersDisabledMCPTools(t *testing.T) {
	root := t.TempDir()
	client := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep"}, {Name: "inspect"}}}
	owner := newFakeMCPToolLifecycleOwner()
	owner.setDecision(root, mcpdto.ClientKindLSP, "grep", contract.MCPToolLifecycleDisabled, "disabled for test")
	owner.setDecision(root, mcpdto.ClientKindLSP, "inspect", contract.MCPToolLifecycleEnabled, "")
	h := &Handler{
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{mcpdto.ClientKindLSP: client}),
		lifecycle:          owner,
		lifecyclePolicy:    owner,
	}

	tools, err := prepareCodexToolSurfaceForTest(t, h, context.Background(), contract.CodexToolSurfaceScope{
		AgentID: "agent-1",
		CWD:     root,
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{
			{Name: mcpdto.ClientKindLSP, Command: []string{"mcp-lsp"}},
		}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}

	assertDynamicToolNames(t, tools, []string{"inspect"})
	assertNoDynamicToolName(t, tools, "grep")
}

func TestListToolsForCodexFiltersDisabledPeerToolsAfterBackfill(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	owner := newFakeMCPToolLifecycleOwner()
	owner.setDecision(root, mcpdto.ClientKindLSP, "grep", contract.MCPToolLifecycleDisabled, "disabled for test")
	h := &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			mcpdto.ClientKindOrch: {listToolsPeer([]mcpdto.MCPTool{{Name: "launch_agent"}}, nil)},
			mcpdto.ClientKindLSP:  {listToolsPeer([]mcpdto.MCPTool{{Name: "grep"}}, nil)},
		}},
		lifecycle:       owner,
		lifecyclePolicy: owner,
	}

	tools, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}

	assertNoDynamicToolName(t, tools, "grep")
	assertHasDynamicToolName(t, tools, "launch_agent")
	assertMCPToolLifecycleBackfill(t, owner.backfills, root, mcpdto.ClientKindLSP, "grep")
	assertLifecyclePolicyRequest(t, owner.policyRequests, root, mcpdto.ClientKindLSP, "grep")
}

func TestProxyToolsListFiltersDisabledPeerTools(t *testing.T) {
	root := t.TempDir()
	owner := newFakeMCPToolLifecycleOwner()
	owner.setDecision(root, mcpdto.ClientKindLSP, "grep", contract.MCPToolLifecycleDisabled, "disabled for test")
	owner.setDecision(root, mcpdto.ClientKindLSP, "inspect", contract.MCPToolLifecycleEnabled, "")
	h := &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			mcpdto.ClientKindLSP: {listToolsPeer([]mcpdto.MCPTool{{Name: "grep"}, {Name: "inspect"}}, nil)},
		}},
		bindingStore: &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
			"agent-1": {AgentID: "agent-1", CWD: root},
		}},
		lifecycle:       owner,
		lifecyclePolicy: owner,
		proxyAuthToken:  newProxyAuthToken(),
	}

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", got.Error)
	}

	tools := proxyToolNames(t, got)
	if tools["grep"] {
		t.Fatalf("proxy tools/list exposed disabled grep; tools=%#v", tools)
	}
	if !tools["inspect"] {
		t.Fatalf("proxy tools/list missing enabled inspect; tools=%#v", tools)
	}
}

func TestMCPToolLifecyclePolicyMissingOwnerFailsClosed(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	owner := newFakeMCPToolLifecycleOwner()
	owner.skipBackfillRows = true
	h := &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			mcpdto.ClientKindOrch: {listToolsPeer([]mcpdto.MCPTool{{Name: "launch_agent"}}, nil)},
			mcpdto.ClientKindLSP:  {listToolsPeer([]mcpdto.MCPTool{{Name: "grep"}}, nil)},
		}},
		lifecycle:       owner,
		lifecyclePolicy: owner,
	}

	_, err := h.ListToolsForCodex(context.Background())
	if err == nil || !strings.Contains(err.Error(), "missing lifecycle owner row") {
		t.Fatalf("ListToolsForCodex() error = %v, want missing lifecycle owner row", err)
	}
}

func TestMCPToolLifecycleBackfillReadinessFiltersAfterBackfill(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	owner := newFakeMCPToolLifecycleOwner()
	h := &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			mcpdto.ClientKindOrch: {listToolsPeer([]mcpdto.MCPTool{{Name: "launch_agent"}}, nil)},
			mcpdto.ClientKindLSP:  {listToolsPeer([]mcpdto.MCPTool{{Name: "grep"}}, nil)},
		}},
		lifecycle:       owner,
		lifecyclePolicy: owner,
	}

	tools, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}

	assertHasDynamicToolName(t, tools, "grep")
	assertHasDynamicToolName(t, tools, "launch_agent")
	assertMCPToolLifecycleBackfill(t, owner.backfills, root, mcpdto.ClientKindLSP, "grep")
	assertLifecyclePolicyRequest(t, owner.policyRequests, root, mcpdto.ClientKindLSP, "grep")
}

func TestCodexSurfaceToolCallDeniesNonEnabledLifecycleStates(t *testing.T) {
	root := t.TempDir()
	client := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep", InputSchema: strictEmptyObjectSchema()}}}
	owner := newFakeMCPToolLifecycleOwner()
	owner.setDecision(root, mcpdto.ClientKindLSP, "grep", contract.MCPToolLifecycleEnabled, "")
	h := &Handler{
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{mcpdto.ClientKindLSP: client}),
		lifecycle:          owner,
		lifecyclePolicy:    owner,
	}
	_, err := prepareCodexToolSurfaceForTest(t, h, context.Background(), contract.CodexToolSurfaceScope{
		AgentID: "agent-1",
		CWD:     root,
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{
			{Name: mcpdto.ClientKindLSP, Command: []string{"mcp-lsp"}},
		}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	tests := []struct {
		name     string
		state    contract.MCPToolLifecycleState
		denyCode string
	}{
		{name: "disabled", state: contract.MCPToolLifecycleDisabled, denyCode: contract.MCPToolLifecycleDenyCodeDisabled},
		{name: "suspended", state: contract.MCPToolLifecycleSuspended, denyCode: contract.MCPToolLifecycleDenyCodeSuspended},
		{name: "removed", state: contract.MCPToolLifecycleRemoved, denyCode: contract.MCPToolLifecycleDenyCodeRemoved},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner.setDecision(root, mcpdto.ClientKindLSP, "grep", tt.state, "blocked")
			for _, name := range []string{"grep", "mcp__lsp__grep"} {
				t.Run(name, func(t *testing.T) {
					result, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
						Params: mustRawJSON(t, map[string]any{
							"name":      name,
							"arguments": map[string]any{"illegal": true},
							"_agentId":  "agent-1",
							"_cwd":      root,
						}),
					})
					if err != nil {
						t.Fatalf("HandleToolCall(%q) error = %v", name, err)
					}
					got, ok := result.(*ToolCallResult)
					if !ok {
						t.Fatalf("HandleToolCall(%q) result = %T, want *ToolCallResult", name, result)
					}
					assertLifecycleDeniedResult(t, got, mcpdto.ClientKindLSP, "grep", tt.denyCode)
					if len(client.calls) != 0 {
						t.Fatalf("%s lifecycle call reached MCP client: calls=%#v", tt.name, client.calls)
					}
				})
			}
		})
	}
}

func TestCodexSurfaceToolCallRejectsRequestScopeExpansionBeforePolicyAndClient(t *testing.T) {
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	client := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep", InputSchema: strictEmptyObjectSchema()}}}
	owner := newFakeMCPToolLifecycleOwner()
	owner.setDecision(workspaceA, mcpdto.ClientKindLSP, "grep", contract.MCPToolLifecycleEnabled, "")
	h := &Handler{
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{mcpdto.ClientKindLSP: client}),
		lifecycle:          owner,
		lifecyclePolicy:    owner,
	}
	_, err := prepareCodexToolSurfaceForTest(t, h, context.Background(), contract.CodexToolSurfaceScope{
		AgentID: "agent-1",
		CWD:     workspaceA,
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{
			{Name: mcpdto.ClientKindLSP, Command: []string{"mcp-lsp"}},
		}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}

	tests := []struct {
		name     string
		params   map[string]any
		wantText string
	}{
		{
			name: "cwd mismatch",
			params: map[string]any{
				"name":      "grep",
				"arguments": map[string]any{},
				"_agentId":  "agent-1",
				"_cwd":      workspaceB,
			},
			wantText: "does not match prepared surface cwd",
		},
		{
			name: "workspace roots expand surface",
			params: map[string]any{
				"name":            "grep",
				"arguments":       map[string]any{},
				"_agentId":        "agent-1",
				"_cwd":            workspaceA,
				"_workspaceRoots": []string{workspaceB},
			},
			wantText: "outside prepared surface scope",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner.policyRequests = nil
			client.calls = nil
			client.requests = nil

			_, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: mustRawJSON(t, tt.params)})
			if err == nil || !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("HandleToolCall() error = %v, want %q", err, tt.wantText)
			}
			if len(owner.policyRequests) != 0 {
				t.Fatalf("scope mismatch resolved lifecycle policy: %#v", owner.policyRequests)
			}
			if len(client.calls) != 0 || len(client.requests) != 0 {
				t.Fatalf("scope mismatch reached MCP client: calls=%#v requests=%#v", client.calls, client.requests)
			}
		})
	}
}

func TestCodexSurfaceToolCallBindsPreparedWorkspaceScope(t *testing.T) {
	workspaceA := t.TempDir()
	extraRoot := filepath.Join(workspaceA, "extra")
	client := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep", InputSchema: strictEmptyObjectSchema()}}}
	owner := newFakeMCPToolLifecycleOwner()
	owner.setDecision(workspaceA, mcpdto.ClientKindLSP, "grep", contract.MCPToolLifecycleEnabled, "")
	h := &Handler{
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{mcpdto.ClientKindLSP: client}),
		lifecycle:          owner,
		lifecyclePolicy:    owner,
	}
	_, err := prepareCodexToolSurfaceForTest(t, h, context.Background(), contract.CodexToolSurfaceScope{
		AgentID:        "agent-1",
		CWD:            workspaceA,
		WorkspaceRoots: []string{extraRoot},
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{
			{Name: mcpdto.ClientKindLSP, Command: []string{"mcp-lsp"}},
		}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}

	tests := []struct {
		name   string
		params map[string]any
	}{
		{
			name: "empty cwd",
			params: map[string]any{
				"name":      "grep",
				"arguments": map[string]any{},
				"_agentId":  "agent-1",
			},
		},
		{
			name: "matching prepared scope",
			params: map[string]any{
				"name":            "grep",
				"arguments":       map[string]any{},
				"_agentId":        "agent-1",
				"_cwd":            filepath.Join(workspaceA, "."),
				"_workspaceRoots": []string{extraRoot},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner.policyRequests = nil
			client.calls = nil
			client.requests = nil

			_, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: mustRawJSON(t, tt.params)})
			if err != nil {
				t.Fatalf("HandleToolCall() error = %v", err)
			}
			if len(client.requests) != 1 {
				t.Fatalf("MCP client requests = %#v, want one bound request", client.requests)
			}
			assertCodexSurfaceClientWorkspaceScope(t, client.requests[0], workspaceA, []string{workspaceA, extraRoot})
			assertLifecyclePolicyRequest(t, owner.policyRequests, workspaceA, mcpdto.ClientKindLSP, "grep")
		})
	}
}

func TestCodexSurfaceToolCallDeniesHiddenDisabledLifecycleAliases(t *testing.T) {
	root := t.TempDir()
	client := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep", InputSchema: strictEmptyObjectSchema()}}}
	owner := newFakeMCPToolLifecycleOwner()
	owner.setDecision(root, mcpdto.ClientKindLSP, "grep", contract.MCPToolLifecycleDisabled, "blocked before prepare")
	h := &Handler{
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{mcpdto.ClientKindLSP: client}),
		lifecycle:          owner,
		lifecyclePolicy:    owner,
	}

	tools, err := prepareCodexToolSurfaceForTest(t, h, context.Background(), contract.CodexToolSurfaceScope{
		AgentID: "agent-1",
		CWD:     root,
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{
			{Name: mcpdto.ClientKindLSP, Command: []string{"mcp-lsp"}},
		}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	assertNoDynamicToolName(t, tools, "grep")

	for _, name := range []string{"grep", "mcp__lsp__grep"} {
		t.Run(name, func(t *testing.T) {
			result, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
				Params: mustRawJSON(t, map[string]any{
					"name":      name,
					"arguments": map[string]any{"illegal": true},
					"_agentId":  "agent-1",
					"_cwd":      root,
				}),
			})
			if err != nil {
				t.Fatalf("HandleToolCall(%q) error = %v", name, err)
			}
			got, ok := result.(*ToolCallResult)
			if !ok {
				t.Fatalf("HandleToolCall(%q) result = %T, want *ToolCallResult", name, result)
			}
			assertLifecycleDeniedResult(t, got, mcpdto.ClientKindLSP, "grep", contract.MCPToolLifecycleDenyCodeDisabled)
		})
	}
	if len(client.calls) != 0 {
		t.Fatalf("hidden disabled lifecycle call reached MCP client: calls=%#v", client.calls)
	}
}

func TestCodexSurfaceToolCallDeniesDisabledOrchShortNames(t *testing.T) {
	root := t.TempDir()
	client := &fakeMCPClient{tools: orchestrationToolsForTest(t)}
	owner := newFakeMCPToolLifecycleOwner()
	for _, canonical := range orchestrationToolNamesForTest(t) {
		owner.setDecision(root, mcpdto.ClientKindOrch, canonical, contract.MCPToolLifecycleDisabled, "blocked before prepare")
	}
	h := &Handler{
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{mcpdto.ClientKindOrch: client}),
		lifecycle:          owner,
		lifecyclePolicy:    owner,
	}

	tools, err := prepareCodexToolSurfaceForTest(t, h, context.Background(), contract.CodexToolSurfaceScope{
		AgentID: "agent-1",
		CWD:     root,
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{
			{Name: mcpdto.ClientKindOrch, Command: []string{"mcp-orch"}},
		}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}

	for _, canonical := range orchestrationToolNamesForTest(t) {
		assertNoDynamicToolName(t, tools, canonical)
		for _, name := range []string{
			canonical,
			wrappedMCPToolName(mcpdto.ClientKindOrch, canonical),
		} {
			t.Run(name, func(t *testing.T) {
				result, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
					Params: mustRawJSON(t, map[string]any{
						"name":      name,
						"arguments": map[string]any{"illegal": true},
						"_agentId":  "agent-1",
						"_cwd":      root,
					}),
				})
				if err != nil {
					t.Fatalf("HandleToolCall(%q) error = %v", name, err)
				}
				got, ok := result.(*ToolCallResult)
				if !ok {
					t.Fatalf("HandleToolCall(%q) result = %T, want *ToolCallResult", name, result)
				}
				assertLifecycleDeniedResult(t, got, mcpdto.ClientKindOrch, canonical, contract.MCPToolLifecycleDenyCodeDisabled)
			})
		}
	}
	if len(client.calls) != 0 {
		t.Fatalf("hidden disabled lifecycle call reached MCP client: calls=%#v", client.calls)
	}
}

func TestPeerToolCallDeniesDisabledLifecycleAliasesBeforePeerSelection(t *testing.T) {
	root := t.TempDir()
	owner := newFakeMCPToolLifecycleOwner()
	owner.setDecision(root, mcpdto.ClientKindLSP, "grep", contract.MCPToolLifecycleDisabled, "blocked")
	owner.setDecision(root, mcpdto.ClientKindOrch, "launch_agent", contract.MCPToolLifecycleDisabled, "blocked")
	h, registry := newHandlerForTest(&mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(_ context.Context, _ string, _ any, _ any) error {
		t.Fatal("disabled lifecycle call reached peer")
		return nil
	}}})
	h.lifecyclePolicy = owner

	tests := []struct {
		name       string
		wantServer string
		wantTool   string
	}{
		{name: "grep", wantServer: mcpdto.ClientKindLSP, wantTool: "grep"},
		{name: "grep", wantServer: mcpdto.ClientKindLSP, wantTool: "grep"},
		{name: "mcp__lsp__grep", wantServer: mcpdto.ClientKindLSP, wantTool: "grep"},
		{name: "mcp__lsp__grep", wantServer: mcpdto.ClientKindLSP, wantTool: "grep"},
		{name: "launch_agent", wantServer: mcpdto.ClientKindOrch, wantTool: "launch_agent"},
		{name: "mcp__orch__launch_agent", wantServer: mcpdto.ClientKindOrch, wantTool: "launch_agent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := h.routeToolCall(context.Background(), ToolCallRequest{
				Name:      tt.name,
				Arguments: json.RawMessage(`{}`),
				CWD:       root,
			})
			if err != nil {
				t.Fatalf("routeToolCall(%q) error = %v", tt.name, err)
			}
			assertLifecycleDeniedResult(t, got, tt.wantServer, tt.wantTool, contract.MCPToolLifecycleDenyCodeDisabled)
		})
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("disabled lifecycle call selected peer: gotKinds=%#v", registry.gotKinds)
	}
}

type fakeMCPToolLifecycleOwner struct {
	backfills        []MCPToolLifecycleBackfillRequest
	policyRequests   []contract.MCPToolLifecyclePolicyRequest
	decisions        map[string]contract.MCPToolLifecycleDecision
	err              error
	skipBackfillRows bool
}

func newFakeMCPToolLifecycleOwner() *fakeMCPToolLifecycleOwner {
	return &fakeMCPToolLifecycleOwner{decisions: map[string]contract.MCPToolLifecycleDecision{}}
}

func (o *fakeMCPToolLifecycleOwner) BackfillMCPTools(_ context.Context, req MCPToolLifecycleBackfillRequest) error {
	cloned := req
	cloned.Tools = append([]contract.MCPToolLifecycleObservedTool(nil), req.Tools...)
	o.backfills = append(o.backfills, cloned)
	if o.skipBackfillRows {
		return o.err
	}
	for _, tool := range req.Tools {
		key := lifecycleDecisionKey(req.WorkspaceRoot, req.ServerName, tool.Name)
		if _, ok := o.decisions[key]; !ok {
			o.decisions[key] = contract.MCPToolLifecycleDecision{
				WorkspaceRoot: req.WorkspaceRoot,
				ServerName:    req.ServerName,
				ManifestName:  firstNonEmptyTestString(tool.ManifestName, req.ManifestName),
				ToolName:      tool.Name,
				State:         contract.MCPToolLifecycleEnabled,
			}
		}
	}
	return o.err
}

func (o *fakeMCPToolLifecycleOwner) ResolveMCPToolLifecycle(
	_ context.Context,
	req contract.MCPToolLifecyclePolicyRequest,
) (contract.MCPToolLifecycleDecision, error) {
	o.policyRequests = append(o.policyRequests, req)
	if o.err != nil {
		return contract.MCPToolLifecycleDecision{}, o.err
	}
	decision, ok := o.decisions[lifecycleDecisionKey(req.WorkspaceRoot, req.ServerName, req.ToolName)]
	if !ok {
		return contract.MCPToolLifecycleDecision{}, fmt.Errorf("missing lifecycle owner row for %s/%s", req.ServerName, req.ToolName)
	}
	return decision, nil
}

func (o *fakeMCPToolLifecycleOwner) setDecision(root, serverName, toolName string, state contract.MCPToolLifecycleState, reason string) {
	decision := contract.MCPToolLifecycleDecision{
		WorkspaceRoot: root,
		ServerName:    serverName,
		ManifestName:  serverName,
		ToolName:      toolName,
		State:         state,
		Reason:        reason,
	}
	switch state {
	case contract.MCPToolLifecycleDisabled:
		decision.DenyCode = contract.MCPToolLifecycleDenyCodeDisabled
	case contract.MCPToolLifecycleSuspended:
		decision.DenyCode = contract.MCPToolLifecycleDenyCodeSuspended
	case contract.MCPToolLifecycleRemoved:
		decision.DenyCode = contract.MCPToolLifecycleDenyCodeRemoved
	}
	o.decisions[lifecycleDecisionKey(root, serverName, toolName)] = decision
}

func lifecycleDecisionKey(root, serverName, toolName string) string {
	return strings.TrimSpace(root) + "\x00" + strings.TrimSpace(serverName) + "\x00" + strings.TrimSpace(toolName)
}

func firstNonEmptyTestString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func strictEmptyObjectSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

func assertNoDynamicToolName(t *testing.T, tools []contract.DynamicToolSchema, name string) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			t.Fatalf("dynamic tools exposed %q; tools=%#v", name, tools)
		}
	}
}

func assertHasDynamicToolName(t *testing.T, tools []contract.DynamicToolSchema, name string) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return
		}
	}
	t.Fatalf("dynamic tools missing %q; tools=%#v", name, tools)
}

func proxyToolNames(t *testing.T, got proxyJSONRPCResponse) map[string]bool {
	t.Helper()
	raw, err := json.Marshal(got.Result)
	if err != nil {
		t.Fatalf("json.Marshal(proxy result) error = %v", err)
	}
	var result peerToolsListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("json.Unmarshal(proxy tools/list) error = %v raw=%s", err, string(raw))
	}
	out := make(map[string]bool, len(result.Tools))
	for _, tool := range result.Tools {
		out[tool.Name] = true
	}
	return out
}

func assertLifecyclePolicyRequest(
	t *testing.T,
	requests []contract.MCPToolLifecyclePolicyRequest,
	workspaceRoot string,
	serverName string,
	toolName string,
) {
	t.Helper()
	for _, req := range requests {
		if req.WorkspaceRoot == workspaceRoot && req.ServerName == serverName && req.ToolName == toolName {
			return
		}
	}
	t.Fatalf("policy requests = %#v, want %s/%s in %s", requests, serverName, toolName, workspaceRoot)
}

func assertCodexSurfaceClientWorkspaceScope(t *testing.T, req ToolCallRequest, cwd string, roots []string) {
	t.Helper()
	if req.CWD != cwd {
		t.Fatalf("MCP client request cwd = %q, want %q", req.CWD, cwd)
	}
	if len(req.WorkspaceRoots) != len(roots) {
		t.Fatalf("MCP client workspace roots = %#v, want %#v", req.WorkspaceRoots, roots)
	}
	for i, root := range roots {
		if req.WorkspaceRoots[i] != root {
			t.Fatalf("MCP client workspace roots[%d] = %q, want %q; all roots %#v", i, req.WorkspaceRoots[i], root, req.WorkspaceRoots)
		}
	}
}

func assertLifecycleDeniedResult(
	t *testing.T,
	got *ToolCallResult,
	serverName string,
	toolName string,
	denyCode string,
) {
	t.Helper()
	if got == nil {
		t.Fatal("ToolCallResult = nil")
	}
	if got.Success {
		t.Fatalf("ToolCallResult.Success = true, want false")
	}
	var envelope map[string]any
	if err := json.Unmarshal(got.StructuredContent, &envelope); err != nil {
		t.Fatalf("decode lifecycle denial structuredContent %q: %v", string(got.StructuredContent), err)
	}
	if envelope["kind"] != "mcp_tool_lifecycle_denied" {
		t.Fatalf("denial kind = %v, want mcp_tool_lifecycle_denied; envelope=%#v", envelope["kind"], envelope)
	}
	if envelope["server"] != serverName || envelope["tool"] != toolName || envelope["code"] != denyCode {
		t.Fatalf("denial envelope = %#v, want server=%q tool=%q code=%q", envelope, serverName, toolName, denyCode)
	}
}
