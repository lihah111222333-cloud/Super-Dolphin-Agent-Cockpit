package toolbridge

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"

	"github.com/kelindar/event"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	"github.com/stretchr/testify/require"
)

func TestPrepareCodexToolSurfaceAdvertisesShortNamesAndRoutesCalls(t *testing.T) {
	lspInputSchema := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"search query"}}}`)
	lspOutputSchema := json.RawMessage(`{"type":"object","properties":{"files":{"type":"object","description":"matches by file"}}}`)
	lsp := &fakeMCPClient{tools: []mcpdto.MCPTool{
		{Name: "grep", Description: "grep source", InputSchema: lspInputSchema, OutputSchema: lspOutputSchema},
		{Name: "completion", Description: "complete source", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}}
	orch := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "launch_agent", Description: "launch", InputSchema: json.RawMessage(`{"type":"object"}`)}}}

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
	assertDynamicToolNames(t, tools, []string{"grep", "completion", "launch_agent"})
	assertDynamicToolSchema(t, tools, "grep", "Recommended tool: grep. Why: grep source", lspInputSchema, lspOutputSchema)

	result, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"completion","arguments":{"pos":"smoke.go:1:1"},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-1","_cwd":"/repo"}`)})
	if err != nil {
		t.Fatalf("HandleToolCall(completion) error = %v", err)
	}
	if !lsp.calledWith("completion") {
		t.Fatalf("lsp calls = %#v, want real name completion", lsp.calls)
	}
	if result == nil {
		t.Fatal("HandleToolCall(completion) result = nil")
	}

	result, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"grep","arguments":{"query":"x"},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-2","_cwd":"/repo"}`)})

	if err != nil {
		t.Fatalf("HandleToolCall(short) error = %v", err)
	}
	if !lsp.calledWith("grep") {
		t.Fatalf("lsp calls = %#v, want real name grep", lsp.calls)
	}
	if result == nil {
		t.Fatal("HandleToolCall(short) result = nil")
	}
}

func TestPrepareCodexToolSurfaceRejectsStaleLSPToolNames(t *testing.T) {
	for _, stale := range []string{"lsp_grep", "lsp_edit", "lsp_completion", "format_preview"} {
		t.Run(stale, func(t *testing.T) {
			lsp := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: stale, Description: "stale", InputSchema: strictEmptyObjectSchema()}}}
			h := &Handler{stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": lsp})}

			_, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
				AgentID:          "agent-stale-lsp",
				ProviderThreadID: "provider-thread-stale-lsp",
				CWD:              "/repo",
				Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{
					{Name: "lsp", Command: []string{"mcp-lsp"}},
				}},
			})
			if err == nil || !strings.Contains(err.Error(), "LSP peer returned unsupported tool") {
				t.Fatalf("PrepareCodexToolSurface(%q) error = %v, want unsupported LSP tool", stale, err)
			}
		})
	}
}

func TestPrepareCodexToolSurfaceRejectsLegacyOrchestrationAliases(t *testing.T) {
	orch := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "list_agents", Description: "list", InputSchema: strictEmptyObjectSchema()}}}
	h := &Handler{stdioClientFactory: fakeClientFactory(map[string]mcpClient{mcpdto.ClientKindOrch: orch})}

	tools, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-legacy-deny",
		ProviderThreadID: "provider-thread-legacy-deny",
		CWD:              "/repo",
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{
			{Name: mcpdto.ClientKindOrch, Command: []string{"mcp-orch"}},
		}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	assertDynamicToolNames(t, tools, []string{"list_agents"})

	for _, legacy := range []string{"orchestration_list_agents", "mcp__orch__orchestration_list_agents"} {
		_, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: mustRawJSON(t, map[string]any{
			"name":      legacy,
			"arguments": map[string]any{},
			"_agentId":  "agent-legacy-deny",
			"_threadId": "provider-thread-legacy-deny",
			"_callId":   "call-" + legacy,
			"_cwd":      "/repo",
		})})
		if err == nil || !strings.Contains(err.Error(), "unknown codex surface tool") {
			t.Fatalf("HandleToolCall(%q) error = %v, want unknown codex surface tool", legacy, err)
		}
	}
	if len(orch.calls) != 0 {
		t.Fatalf("legacy alias calls reached orch client: %#v", orch.calls)
	}

	result, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: mustRawJSON(t, map[string]any{
		"name":      "list_agents",
		"arguments": map[string]any{},
		"_agentId":  "agent-legacy-deny",
		"_threadId": "provider-thread-legacy-deny",
		"_callId":   "call-list-agents",
		"_cwd":      "/repo",
	})})
	if err != nil {
		t.Fatalf("HandleToolCall(list_agents) error = %v", err)
	}
	if result == nil {
		t.Fatal("HandleToolCall(list_agents) result = nil")
	}
	if !orch.calledWith("list_agents") {
		t.Fatalf("orch calls = %#v, want list_agents", orch.calls)
	}
}

func TestPrepareCodexToolSurfaceFiltersDisabledToolsAndRejectsStaleCalls(t *testing.T) {
	host := &stubHostToolRegistry{
		hasToolName: ToolNameMemoryWrite,
		tools:       []mcpdto.MCPTool{{Name: ToolNameMemoryWrite, Description: "host memory write", InputSchema: strictEmptyObjectSchema()}},
		result:      toolCallTextResult(true, "host ok"),
	}
	skills := &fakeSkillToolProvider{
		tools: []contract.SkillToolSurfaceTool{{Name: "skill_write_note", Description: "skill writer", InputSchema: strictEmptyObjectSchema()}},
	}
	orch := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "launch_agent", Description: "launch", InputSchema: strictEmptyObjectSchema()}}}
	external := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "connect_tool_source", Description: "connect source", InputSchema: strictEmptyObjectSchema()}}}
	h := &Handler{
		hostTools:          host,
		skillTools:         skills,
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{mcpdto.ClientKindOrch: orch, "external": external}),
	}
	manifest := providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{
		{Name: mcpdto.ClientKindOrch, Command: []string{"mcp-orch"}},
		{Name: "external", Command: []string{"mcp-external"}},
	}}
	cwd := t.TempDir()
	assertDisabledCodexSurfaceTools(t, h, manifest, cwd, host, skills, orch, external)
	assertAllowedCodexSurfaceTools(t, h, manifest, cwd, host, skills, orch, external)
}

func assertDisabledCodexSurfaceTools(
	t *testing.T,
	h *Handler,
	manifest providerdto.MCPManifest,
	cwd string,
	host *stubHostToolRegistry,
	skills *fakeSkillToolProvider,
	orch *fakeMCPClient,
	external *fakeMCPClient,
) {
	t.Helper()

	disabledNames := []string{ToolNameMemoryWrite, "skill__skill_write_note", "launch_agent", "mcp__orch__launch_agent", "launch_agent", "connect_tool_source"}
	disabledTools, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-deny",
		ProviderThreadID: "provider-thread-deny",
		CWD:              cwd,
		DisabledTools:    []string{ToolNameMemoryWrite, "skill_write_note", "launch_agent", "connect_tool_source"},
		Manifest:         manifest,
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface(disabled) error = %v", err)
	}
	assertNoDynamicToolNames(t, disabledTools, disabledNames)
	assertDisabledCodexSurfaceToolCalls(t, h, cwd, disabledNames)
	assertNoDisabledCodexSurfaceToolCallsReachedBackends(t, host, skills, orch, external)
}

func assertAllowedCodexSurfaceTools(
	t *testing.T,
	h *Handler,
	manifest providerdto.MCPManifest,
	cwd string,
	host *stubHostToolRegistry,
	skills *fakeSkillToolProvider,
	orch *fakeMCPClient,
	external *fakeMCPClient,
) {
	t.Helper()

	allowedTools, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-allow",
		ProviderThreadID: "provider-thread-allow",
		CWD:              cwd,
		Manifest:         manifest,
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface(allowed) error = %v", err)
	}
	assertDynamicToolNames(t, allowedTools, []string{ToolNameMemoryWrite, "skill__skill_write_note", "launch_agent", "connect_tool_source"})
	callAllowedCodexSurfaceTools(t, h, cwd)
	assertAllowedCodexSurfaceCallsReachedBackends(t, host, skills, orch, external)
}

func assertNoDynamicToolNames(t *testing.T, tools []contract.DynamicToolSchema, names []string) {
	t.Helper()

	for _, name := range names {
		assertNoDynamicToolName(t, tools, name)
	}
}

func assertDisabledCodexSurfaceToolCalls(t *testing.T, h *Handler, cwd string, names []string) {
	t.Helper()

	for _, name := range names {
		t.Run("disabled stale call "+name, func(t *testing.T) {
			result := callCodexSurfaceToolForTest(t, h, "agent-deny", "provider-thread-deny", cwd, name)
			assertDisabledCodexSurfaceToolResult(t, result, name)
		})
	}
}

func assertNoDisabledCodexSurfaceToolCallsReachedBackends(
	t *testing.T,
	host *stubHostToolRegistry,
	skills *fakeSkillToolProvider,
	orch *fakeMCPClient,
	external *fakeMCPClient,
) {
	t.Helper()

	if host.calls != 0 {
		t.Fatalf("disabled host tool reached host registry: calls=%d", host.calls)
	}
	if len(skills.calls) != 0 {
		t.Fatalf("disabled skill tool reached skill provider: calls=%#v", skills.calls)
	}
	if len(orch.calls) != 0 || len(external.calls) != 0 {
		t.Fatalf("disabled MCP tool reached clients: orch=%#v external=%#v", orch.calls, external.calls)
	}
}

func callAllowedCodexSurfaceTools(t *testing.T, h *Handler, cwd string) {
	t.Helper()

	callCodexSurfaceToolForTest(t, h, "agent-allow", "provider-thread-allow", cwd, ToolNameMemoryWrite)
	callCodexSurfaceToolForTest(t, h, "agent-allow", "provider-thread-allow", cwd, "skill__skill_write_note")
	callCodexSurfaceToolForTest(t, h, "agent-allow", "provider-thread-allow", cwd, "launch_agent")
	callCodexSurfaceToolForTest(t, h, "agent-allow", "provider-thread-allow", cwd, "connect_tool_source")
}

func assertAllowedCodexSurfaceCallsReachedBackends(
	t *testing.T,
	host *stubHostToolRegistry,
	skills *fakeSkillToolProvider,
	orch *fakeMCPClient,
	external *fakeMCPClient,
) {
	t.Helper()

	if host.calls != 1 {
		t.Fatalf("allowed host calls = %d, want 1", host.calls)
	}
	if len(skills.calls) != 1 {
		t.Fatalf("allowed skill calls = %#v, want one call", skills.calls)
	}
	if !orch.calledWith("launch_agent") {
		t.Fatalf("allowed launch_agent did not reach orchestration client: %#v", orch.calls)
	}
	if !external.calledWith("connect_tool_source") {
		t.Fatalf("allowed connect_tool_source did not reach external client: %#v", external.calls)
	}
}

func callCodexSurfaceToolForTest(t *testing.T, h *Handler, agentID, threadID, cwd, name string) *ToolCallResult {
	t.Helper()

	result, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: mustRawJSON(t, map[string]any{
		"name":      name,
		"arguments": map[string]any{},
		"_agentId":  agentID,
		"_threadId": threadID,
		"_callId":   "call-" + name,
		"_cwd":      cwd,
	})})
	if err != nil {
		t.Fatalf("HandleToolCall(%q) error = %v", name, err)
	}
	got, ok := result.(*ToolCallResult)
	if !ok {
		t.Fatalf("HandleToolCall(%q) result = %T, want *ToolCallResult", name, result)
	}
	return got
}

func assertDisabledCodexSurfaceToolResult(t *testing.T, got *ToolCallResult, name string) {
	t.Helper()

	if got == nil {
		t.Fatalf("HandleToolCall(%q) result = nil", name)
	}
	if got.Success {
		t.Fatalf("HandleToolCall(%q) Success = true, want disabled result", name)
	}
	if len(got.ContentItems) == 0 || got.ContentItems[0].Text == "" {
		t.Fatalf("HandleToolCall(%q) disabled result missing text: %#v", name, got)
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
	assertDynamicToolNames(t, tools, []string{"skill__backend"})
	assertDynamicSkillToolSchema(t, tools, "skill__backend", "Return backend skill details", inputSchema)

	resultAny, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
		Params: json.RawMessage(`{"name":"skill__backend","arguments":{},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-1","_cwd":"` + jsonEscape(projectRoot) + `"}`),
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
	for _, realName := range []string{"launch_agent", "launch_agent"} {
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

func TestCodexToolSurfaceAcceptsShortLSPName(t *testing.T) {
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

	_, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"grep","arguments":{},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-1","_cwd":"/repo"}`)})
	if err != nil {
		t.Fatalf("HandleToolCall(short name) error = %v", err)
	}
	if !lsp.calledWith("grep") {
		t.Fatalf("lsp calls = %#v, want canonical real name grep", lsp.calls)
	}

	_, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"lsp_grep","arguments":{},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-legacy","_cwd":"/repo"}`)})
	if err == nil || !strings.Contains(err.Error(), "unknown codex surface tool") {
		t.Fatalf("HandleToolCall(lsp_grep) error = %v, want unknown codex surface tool", err)
	}
	if lsp.calledWith("lsp_grep") {
		t.Fatalf("legacy lsp_grep reached LSP client: %#v", lsp.calls)
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
		tools:           []mcpdto.MCPTool{{Name: "launch_agent", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		listStarted:     started,
		listStartedName: "orch",
		listRelease:     release,
	}
	h := &Handler{stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": lsp, "orch": orch})}

	var tools []contract.DynamicToolSchema
	done := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
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
	})

	waitStartedToolLists(t, started, "lsp", "orch")
	close(release)
	require.NoError(t, <-done)
	wg.Wait()
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

	lsp := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
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

	lsp := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
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

func TestCodexToolSurfaceMissingSurfaceReservedHostOnlyDoesNotReachBackend(t *testing.T) {
	host := &stubHostToolRegistry{
		hasToolName: ToolNameMemoryWrite,
		tools:       []mcpdto.MCPTool{{Name: ToolNameMemoryWrite, Description: "host memory write", InputSchema: strictEmptyObjectSchema()}},
		result:      map[string]any{"status": "host backend should not run"},
	}
	h := &Handler{hostTools: host}

	_, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: mustRawJSON(t, map[string]any{
		"name":      ToolNameMemoryWrite,
		"arguments": map[string]any{},
		"_agentId":  "agent-1",
		"_threadId": "provider-thread-1",
		"_callId":   "call-memory-write",
		"_cwd":      "/repo",
	})})
	if err == nil {
		t.Fatal("HandleToolCall() error = nil, want missing surface failure")
	}
	if got := err.Error(); got != `toolbridge: codex tool surface is not prepared for agent "agent-1" thread "provider-thread-1"` {
		t.Fatalf("HandleToolCall() error = %q", got)
	}
	if host.calls != 0 {
		t.Fatalf("reserved host-only scoped missing-surface call reached host backend: calls=%d", host.calls)
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
	for _, name := range []string{
		"grep",
		"mcp__lsp__grep",
		"mcp__lsp__grep",
		"launch_agent",
		"mcp__orch__launch_agent",
	} {
		_, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: legacyScopedToolCallParams(name)})
		if err == nil {
			t.Fatalf("HandleToolCall(%s) error = nil, want missing surface failure", name)
		}
		if !strings.Contains(err.Error(), "codex tool surface is not prepared") {
			t.Fatalf("HandleToolCall(%s) error = %v, want missing surface failure", name, err)
		}
	}
}

func TestCodexToolSurfaceCWDOnlyScopeFailsClosedWhenSurfaceMissing(t *testing.T) {
	h := &Handler{}

	_, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
		Params: json.RawMessage(`{"name":"mcp__lsp__grep","arguments":{},"_cwd":"/repo"}`),
	})
	if err == nil {
		t.Fatal("HandleToolCall(cwd-only scoped lsp) error = nil, want missing surface failure")
	}
}

func TestCodexToolSurfaceWorkspaceRootsOnlyScopeFailsClosedWhenSurfaceMissing(t *testing.T) {
	h := &Handler{}

	_, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
		Params: json.RawMessage(`{"name":"mcp__lsp__grep","arguments":{},"_workspaceRoots":["/repo"]}`),
	})
	if err == nil {
		t.Fatal("HandleToolCall(workspace-roots scoped lsp) error = nil, want missing surface failure")
	}
}
