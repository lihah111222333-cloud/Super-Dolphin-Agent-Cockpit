package toolbridge

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"

	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	"github.com/kelindar/event"
	"github.com/stretchr/testify/require"
)

func TestPrepareCodexToolSurfaceAdvertisesShortNamesAndRoutesCalls(t *testing.T) {
	lspInputSchema := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"search query"}}}`)
	lspOutputSchema := json.RawMessage(`{"type":"object","properties":{"files":{"type":"object","description":"matches by file"}}}`)
	lsp := &fakeMCPClient{tools: []mcpdto.MCPTool{
		{Name: "lsp_grep", Description: "grep source", InputSchema: lspInputSchema, OutputSchema: lspOutputSchema},
		{Name: "lsp_format_preview", Description: "preview formatting", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}}
	orch := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "orchestration_launch_agent", Description: "launch", InputSchema: json.RawMessage(`{"type":"object"}`)}}}

	h := &Handler{stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": lsp, "orch": orch})}

	tools, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/repo",
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{
			{Name: "lsp", Command: []string{"mcp-lsp"}},
			{Name: "orch", Command: []string{"mcp-orch"}},
		}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	assertDynamicToolNames(t, tools, []string{"grep", "format_preview", "launch_agent"})
	assertDynamicToolSchema(t, tools, "grep", "Recommended tool: grep. Why: grep source", lspInputSchema, lspOutputSchema)

	result, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"format_preview","arguments":{"file_path":"smoke.go"},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-1","_cwd":"/repo"}`)})
	if err != nil {
		t.Fatalf("HandleToolCall(format_preview) error = %v", err)
	}
	if !lsp.calledWith("lsp_format_preview") {
		t.Fatalf("lsp calls = %#v, want real legacy name lsp_format_preview", lsp.calls)
	}
	if result == nil {
		t.Fatal("HandleToolCall(format_preview) result = nil")
	}

	result, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"grep","arguments":{"query":"x"},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-2","_cwd":"/repo"}`)})

	if err != nil {
		t.Fatalf("HandleToolCall(short) error = %v", err)
	}
	if !lsp.calledWith("lsp_grep") {
		t.Fatalf("lsp calls = %#v, want real legacy name lsp_grep", lsp.calls)
	}
	if result == nil {
		t.Fatal("HandleToolCall(short) result = nil")
	}
}

func TestPrepareCodexToolSurfaceAdvertisesSkillToolsAndReturnsSkillText(t *testing.T) {
	inputSchema := json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
	skillTools := &fakeSkillToolProvider{
		tools: []contract.SkillToolSurfaceTool{{
			Name:        "backend",
			Description: "Return backend skill details",
			InputSchema: inputSchema,
		}},
		content: "---\nname: backend\n---\n# Backend\nUse Go backend conventions carefully.\n",
	}
	h := &Handler{
		skillTools:         skillTools,
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": &fakeMCPClient{}}),
	}
	projectRoot := t.TempDir()

	tools, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              projectRoot,
		Manifest:         providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{Name: "lsp", Command: []string{"mcp-lsp"}}}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	assertDynamicToolNames(t, tools, []string{"backend"})
	assertDynamicSkillToolSchema(t, tools, "backend", "Return backend skill details", inputSchema)

	resultAny, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
		Params: json.RawMessage(`{"name":"backend","arguments":{},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-1","_cwd":"` + jsonEscape(projectRoot) + `"}`),
	})
	if err != nil {
		t.Fatalf("HandleToolCall(backend) error = %v", err)
	}
	result, ok := resultAny.(*ToolCallResult)
	if !ok {
		t.Fatalf("HandleToolCall result type = %T, want *ToolCallResult", resultAny)
	}
	if !result.Success || len(result.ContentItems) != 1 || result.ContentItems[0].Text != skillTools.content {
		t.Fatalf("HandleToolCall result = %#v", result)
	}
	if len(skillTools.calls) != 1 || skillTools.calls[0].Name != "backend" || skillTools.calls[0].CWD != projectRoot {
		t.Fatalf("skill tool calls = %#v", skillTools.calls)
	}
}

func TestCodexToolSurfaceLaunchInjectsManagedContextWithoutCWD(t *testing.T) {
	for _, realName := range []string{"launch_agent", "orchestration_launch_agent"} {
		t.Run(realName, func(t *testing.T) {
			orch := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: realName, Description: "launch", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
			h := &Handler{
				stdioClientFactory: fakeClientFactory(map[string]mcpClient{"orch": orch}),
				bindingStore: &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
					"agent-parent": {
						AgentID:  "agent-parent",
						Provider: "codex",
						CWD:      "/repo/project",
					},
				}},
				threadStore: &toolCallThreadStoreStub{},
				preferences: &stubUIPreferenceReader{},
			}
			_, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
				AgentID:          "agent-parent",
				ProviderThreadID: "provider-thread-parent",
				CWD:              "/repo/project",
				Manifest:         providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{Name: "orch", Command: []string{"mcp-orch"}}}},
			})
			if err != nil {
				t.Fatalf("PrepareCodexToolSurface() error = %v", err)
			}

			_, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"launch_agent","arguments":{"name":"idle-agent"},"_agentId":"agent-parent","_threadId":"provider-thread-parent","_callId":"call-1","_cwd":"/repo/current"}`)})
			if err != nil {
				t.Fatalf("HandleToolCall(launch_agent) error = %v", err)
			}
			if !orch.calledWith(realName) {
				t.Fatalf("orch calls = %#v, want %s", orch.calls, realName)
			}
			assertCodexSurfaceLaunchInjectedArgs(t, orch.arguments)
		})
	}
}

func assertCodexSurfaceLaunchInjectedArgs(t *testing.T, arguments []json.RawMessage) {
	t.Helper()
	if len(arguments) != 1 {
		t.Fatalf("orch arguments calls = %d, want 1", len(arguments))
	}
	gotArgs := decodeToolArguments(arguments[0])
	for key, want := range map[string]string{
		"name":      "idle-agent",
		"parent_id": "agent-parent",
		"provider":  "codex",
		"model":     "gpt-5.5",
		"effort":    "xhigh",
	} {
		if got := mapString(gotArgs, key); got != want {
			t.Fatalf("argument %s = %q, want %q; args=%s", key, got, want, string(arguments[0]))
		}
	}
	if got := mapString(gotArgs, "cwd"); got != "" {
		t.Fatalf("argument cwd = %q, want omitted so parent cwd inheritance remains authoritative", got)
	}
}

func TestCodexToolSurfaceAcceptsLegacyAliasWithoutAdvertisingIt(t *testing.T) {
	lsp := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	h := &Handler{stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": lsp})}
	tools, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/repo",
		Manifest:         providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{Name: "lsp", Command: []string{"mcp-lsp"}}}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	assertDynamicToolNames(t, tools, []string{"grep"})

	_, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"lsp_grep","arguments":{},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-1","_cwd":"/repo"}`)})
	if err != nil {
		t.Fatalf("HandleToolCall(legacy alias) error = %v", err)
	}
	if !lsp.calledWith("grep") {
		t.Fatalf("lsp calls = %#v, want canonical real name grep", lsp.calls)
	}
}

func TestPrepareCodexToolSurfaceListsMCPBinariesInParallel(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	lsp := &fakeMCPClient{
		tools:           []mcpdto.MCPTool{{Name: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		listStarted:     started,
		listStartedName: "lsp",
		listRelease:     release,
	}
	orch := &fakeMCPClient{
		tools:           []mcpdto.MCPTool{{Name: "orchestration_launch_agent", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		listStarted:     started,
		listStartedName: "orch",
		listRelease:     release,
	}
	h := &Handler{stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": lsp, "orch": orch})}

	var tools []contract.DynamicToolSchema
	done := make(chan error, 1)
	go func() {
		var err error
		tools, err = h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
			AgentID:          "agent-1",
			ProviderThreadID: "provider-thread-1",
			CWD:              "/repo",
			Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{
				{Name: "lsp", Command: []string{"mcp-lsp"}},
				{Name: "orch", Command: []string{"mcp-orch"}},
			}},
		})
		done <- err
	}()

	waitStartedToolLists(t, started, "lsp", "orch")
	close(release)
	require.NoError(t, <-done)
	assertDynamicToolNames(t, tools, []string{"grep", "launch_agent"})
}

func TestCodexToolSurfacePublishesLifecycleEvents(t *testing.T) {
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	endCh := make(chan tooldto.ToolCallEnd, 1)
	cancelBegin := event.Subscribe(dispatcher, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	t.Cleanup(cancelBegin)
	cancelEnd := event.Subscribe(dispatcher, func(ev tooldto.ToolCallEnd) { endCh <- ev })
	t.Cleanup(cancelEnd)

	lsp := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "lsp_grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	h := &Handler{
		dispatcher:         dispatcher,
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": lsp}),
	}
	_, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/repo",
		Manifest:         providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{Name: "lsp", Command: []string{"mcp-lsp"}}}},
	})
	require.NoError(t, err)

	_, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
		ID:     json.RawMessage(`"call-1"`),
		Params: json.RawMessage(`{"name":"grep","arguments":{"query":"targetName"},"_agentId":"agent-1","_threadId":"provider-thread-1","_cwd":"/repo"}`),
	})
	require.NoError(t, err)

	begin := waitCodexSurfaceToolBegin(t, beginCh)
	require.Equal(t, "agent-1", begin.ThreadID)
	require.Equal(t, "agent-1", begin.AgentID)
	require.Equal(t, "call-1", begin.CallID)
	require.Equal(t, "grep", begin.ToolName)
	require.Equal(t, `{"query":"targetName"}`, begin.ArgumentsPreview)
	end := waitCodexSurfaceToolEnd(t, endCh)
	require.Equal(t, begin.ThreadID, end.ThreadID)
	require.Equal(t, begin.AgentID, end.AgentID)
	require.Equal(t, begin.CallID, end.CallID)
	require.Equal(t, begin.ToolName, end.ToolName)
	require.Truef(t, end.Success, "end = %+v, want success", end)
}

func TestCodexToolSurfaceSkipsLifecycleWhenCallerAlreadyPublishes(t *testing.T) {
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	endCh := make(chan tooldto.ToolCallEnd, 1)
	cancelBegin := event.Subscribe(dispatcher, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	t.Cleanup(cancelBegin)
	cancelEnd := event.Subscribe(dispatcher, func(ev tooldto.ToolCallEnd) { endCh <- ev })
	t.Cleanup(cancelEnd)

	lsp := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "lsp_grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	h := &Handler{
		dispatcher:         dispatcher,
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": lsp}),
	}
	_, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/repo",
		Manifest:         providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{Name: "lsp", Command: []string{"mcp-lsp"}}}},
	})
	require.NoError(t, err)

	ctx := contract.WithToolLifecycleAlreadyPublished(context.Background())
	_, err = h.HandleToolCall(ctx, contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"grep","arguments":{"query":"targetName"},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-1","_cwd":"/repo"}`)})
	require.NoError(t, err)
	assertNoCodexSurfaceToolEvents(t, beginCh, endCh)
}

func TestCodexToolSurfaceMissingSurfaceFails(t *testing.T) {
	h := &Handler{}
	_, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"grep","arguments":{},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-1","_cwd":"/repo"}`)})
	if err == nil {
		t.Fatal("HandleToolCall() error = nil, want missing surface failure")
	}
	if got := err.Error(); got != `toolbridge: codex tool surface is not prepared for agent "agent-1" thread "provider-thread-1"` {
		t.Fatalf("HandleToolCall() error = %q", got)
	}
}

func TestPrepareCodexToolSurfaceReplacesOverlappingSurface(t *testing.T) {
	oldClient := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	newClient := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	clients := []mcpClient{oldClient, newClient}
	h := &Handler{stdioClientFactory: func(context.Context, providerdto.MCPBinary) (mcpClient, error) {
		client := clients[0]
		clients = clients[1:]
		return client, nil
	}}

	_, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-old",
		CWD:              "/repo",
		Manifest:         providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{Name: "lsp", Command: []string{"mcp-lsp"}}}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface(old) error = %v", err)
	}
	_, err = h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-new",
		CWD:              "/repo",
		Manifest:         providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{Name: "lsp", Command: []string{"mcp-lsp"}}}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface(new) error = %v", err)
	}
	if oldClient.closed != 1 {
		t.Fatalf("old client close count = %d, want 1", oldClient.closed)
	}

	if _, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"grep","arguments":{},"_threadId":"provider-thread-old","_callId":"call-1","_cwd":"/repo"}`)}); err == nil {
		t.Fatal("HandleToolCall(old thread) error = nil, want stale surface failure")
	}
	_, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"grep","arguments":{},"_threadId":"provider-thread-new","_callId":"call-2","_cwd":"/repo"}`)})
	if err != nil {
		t.Fatalf("HandleToolCall(new thread) error = %v", err)
	}
	if !newClient.calledWith("grep") {
		t.Fatalf("new client calls = %#v, want grep", newClient.calls)
	}
}

func TestReleaseCodexToolSurfaceClosesAndRemovesSurface(t *testing.T) {
	client := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	h := &Handler{stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": client})}
	_, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/repo",
		Manifest:         providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{Name: "lsp", Command: []string{"mcp-lsp"}}}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	if err = h.ReleaseCodexToolSurface(contract.CodexToolSurfaceScope{AgentID: "agent-1"}); err != nil {
		t.Fatalf("ReleaseCodexToolSurface() error = %v", err)
	}
	if client.closed != 1 {
		t.Fatalf("client close count = %d, want 1", client.closed)
	}
	_, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"grep","arguments":{},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-1","_cwd":"/repo"}`)})
	if err == nil {
		t.Fatal("HandleToolCall() error = nil, want released surface failure")
	}
}

func TestBindCodexToolSurfaceAddsProviderReleaseKey(t *testing.T) {
	client := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	h := &Handler{stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": client})}
	_, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:  "agent-1",
		CWD:      "/repo",
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{Name: "lsp", Command: []string{"mcp-lsp"}}}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	if err = h.BindCodexToolSurface(contract.CodexToolSurfaceScope{AgentID: "agent-1", ProviderThreadID: "provider-thread-1"}); err != nil {
		t.Fatalf("BindCodexToolSurface() error = %v", err)
	}
	if err = h.ReleaseCodexToolSurface(contract.CodexToolSurfaceScope{ProviderThreadID: "provider-thread-1"}); err != nil {
		t.Fatalf("ReleaseCodexToolSurface() error = %v", err)
	}
	if client.closed != 1 {
		t.Fatalf("client close count = %d, want 1", client.closed)
	}
	_, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"grep","arguments":{},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-1","_cwd":"/repo"}`)})
	if err == nil {
		t.Fatal("HandleToolCall() error = nil, want released surface failure")
	}
}

func TestReleaseCodexToolSurfaceByStaleProviderDoesNotRemoveReplacement(t *testing.T) {
	oldClient := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	newClient := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	clients := []mcpClient{oldClient, newClient}
	h := &Handler{stdioClientFactory: func(context.Context, providerdto.MCPBinary) (mcpClient, error) {
		client := clients[0]
		clients = clients[1:]
		return client, nil
	}}

	_, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-old",
		CWD:              "/repo",
		Manifest:         providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{Name: "lsp", Command: []string{"mcp-lsp"}}}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface(old) error = %v", err)
	}
	_, err = h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-new",
		CWD:              "/repo",
		Manifest:         providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{Name: "lsp", Command: []string{"mcp-lsp"}}}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface(new) error = %v", err)
	}

	if err = h.ReleaseCodexToolSurface(contract.CodexToolSurfaceScope{ProviderThreadID: "provider-thread-old"}); err != nil {
		t.Fatalf("ReleaseCodexToolSurface(stale provider) error = %v", err)
	}
	if newClient.closed != 0 {
		t.Fatalf("new client close count = %d, want 0", newClient.closed)
	}
	_, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"grep","arguments":{},"_agentId":"agent-1","_threadId":"provider-thread-new","_callId":"call-1","_cwd":"/repo"}`)})
	if err != nil {
		t.Fatalf("HandleToolCall(new thread) error = %v", err)
	}
	if !newClient.calledWith("grep") {
		t.Fatalf("new client calls = %#v, want grep", newClient.calls)
	}
}

func TestReleaseCodexToolSurfaceByStaleSurfaceIDDoesNotRemoveReplacement(t *testing.T) {
	oldClient := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	newClient := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	clients := []mcpClient{oldClient, newClient}
	h := &Handler{stdioClientFactory: func(context.Context, providerdto.MCPBinary) (mcpClient, error) {
		client := clients[0]
		clients = clients[1:]
		return client, nil
	}}

	_, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		SurfaceID: "surface-old",
		AgentID:   "agent-1",
		CWD:       "/repo",
		Manifest:  providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{Name: "lsp", Command: []string{"mcp-lsp"}}}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface(old) error = %v", err)
	}
	_, err = h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		SurfaceID:        "surface-new",
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-new",
		CWD:              "/repo",
		Manifest:         providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{Name: "lsp", Command: []string{"mcp-lsp"}}}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface(new) error = %v", err)
	}

	if err = h.ReleaseCodexToolSurface(contract.CodexToolSurfaceScope{SurfaceID: "surface-old"}); err != nil {
		t.Fatalf("ReleaseCodexToolSurface(stale surface) error = %v", err)
	}
	if newClient.closed != 0 {
		t.Fatalf("new client close count = %d, want 0", newClient.closed)
	}
	_, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"grep","arguments":{},"_agentId":"agent-1","_threadId":"provider-thread-new","_callId":"call-1","_cwd":"/repo"}`)})
	if err != nil {
		t.Fatalf("HandleToolCall(new thread) error = %v", err)
	}
	if !newClient.calledWith("grep") {
		t.Fatalf("new client calls = %#v, want grep", newClient.calls)
	}
}

func TestCodexToolSurfaceLookupDoesNotFallbackFromStaleThreadToAgent(t *testing.T) {
	client := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	h := &Handler{stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": client})}
	_, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-new",
		CWD:              "/repo",
		Manifest:         providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{Name: "lsp", Command: []string{"mcp-lsp"}}}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}

	_, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"grep","arguments":{},"_agentId":"agent-1","_threadId":"provider-thread-old","_callId":"call-1","_cwd":"/repo"}`)})
	if err == nil {
		t.Fatal("HandleToolCall(stale thread) error = nil, want missing surface failure")
	}
	if len(client.calls) != 0 {
		t.Fatalf("client calls = %#v, want no fallback call", client.calls)
	}
}

func TestCodexToolSurfaceLegacyNamesFailClosedWhenSurfaceMissing(t *testing.T) {
	h := &Handler{}
	for _, name := range []string{"lsp_grep", "mcp__lsp__lsp_grep", "mcp__lsp__grep", "orchestration_launch_agent", "mcp__orch__orchestration_launch_agent"} {
		_, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: legacyScopedToolCallParams(name)})
		if err == nil {
			t.Fatalf("HandleToolCall(%s) error = nil, want missing surface failure", name)
		}
	}
}
