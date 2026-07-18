package toolbridge

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"

	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
)

func legacyScopedToolCallParams(name string) json.RawMessage {
	payload := map[string]any{
		"name":      name,
		"arguments": map[string]any{},
		"_agentId":  "agent-1",
		"_threadId": "provider-thread-1",
		"_callId":   "call-1",
		"_cwd":      "/repo",
	}
	raw, _ := json.Marshal(payload)
	return raw
}

type fakeMCPClient struct {
	tools           []mcpdto.MCPTool
	calls           []string
	arguments       []json.RawMessage
	requests        []ToolCallRequest
	closed          int
	listStarted     chan<- string
	listStartedName string
	listRelease     <-chan struct{}
}

type fakeSkillToolProvider struct {
	tools   []contract.SkillToolSurfaceTool
	content string
	calls   []contract.SkillToolCall
}

func (p *fakeSkillToolProvider) ListSkillToolsForSurface(_ context.Context, _ string) ([]contract.SkillToolSurfaceTool, error) {
	return append([]contract.SkillToolSurfaceTool(nil), p.tools...), nil
}

func (p *fakeSkillToolProvider) CallSkillTool(_ context.Context, call contract.SkillToolCall) (string, error) {
	p.calls = append(p.calls, call)
	return p.content, nil
}

func (c *fakeMCPClient) ListTools(ctx context.Context) ([]mcpdto.MCPTool, error) {
	if c.listStarted != nil {
		c.listStarted <- c.listStartedName
	}
	if c.listRelease != nil {
		select {
		case <-c.listRelease:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return append([]mcpdto.MCPTool(nil), c.tools...), nil
}

func (c *fakeMCPClient) CallTool(_ context.Context, name string, arguments json.RawMessage, req ToolCallRequest) (*ToolCallResult, error) {
	c.calls = append(c.calls, name)
	c.arguments = append(c.arguments, append(json.RawMessage(nil), arguments...))
	cloned := req
	cloned.WorkspaceRoots = append([]string(nil), req.WorkspaceRoots...)
	c.requests = append(c.requests, cloned)
	return toolCallTextResult(true, "ok"), nil
}

func (c *fakeMCPClient) Close() error {
	c.closed++
	return nil
}

func (c *fakeMCPClient) calledWith(name string) bool {
	return slices.Contains(c.calls, name)
}

func fakeClientFactory(clients map[string]mcpClient) func(context.Context, providerdto.MCPBinary) (mcpClient, error) {
	return func(_ context.Context, binary providerdto.MCPBinary) (mcpClient, error) {
		return clients[binary.Name], nil
	}
}

type rawToolsMCPClientForTest struct {
	mcpClient
}

func (c rawToolsMCPClientForTest) ListTools(ctx context.Context) ([]mcpdto.MCPTool, error) {
	tools, err := c.mcpClient.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	for i, tool := range tools {
		if len(tool.RawJSON()) != 0 {
			continue
		}
		if len(tool.InputSchema) == 0 {
			tool.InputSchema = strictEmptyObjectSchema()
		}
		raw, marshalErr := json.Marshal(tool)
		if marshalErr != nil {
			return nil, marshalErr
		}
		tools[i] = mcpdto.NewRawTool(raw)
	}
	return tools, nil
}

func prepareCodexToolSurfaceForTest(
	t *testing.T,
	h *Handler,
	ctx context.Context,
	scope contract.CodexToolSurfaceScope,
) ([]contract.DynamicToolSchema, error) {
	t.Helper()
	if h.authorityOwner == nil {
		h.authorityOwner = newTask4BAuthorityOwner()
	}
	if h.schemaExecutor == nil {
		h.schemaExecutor = &task4BSchemaExecutor{}
	}
	for i, binary := range scope.Manifest.Binaries {
		if contract.IsManagedRuntimeMCPServerName(binary.Name) {
			scope.Manifest.Binaries[i] = providerdto.NewManagedMCPBinary(binary)
			continue
		}
		if binary.TrustedServerID == "" {
			binary.TrustedServerID = binary.Name
			scope.Manifest.Binaries[i] = binary
		}
	}
	if h.stdioClientFactory != nil {
		factory := h.stdioClientFactory
		h.stdioClientFactory = func(ctx context.Context, binary providerdto.MCPBinary) (mcpClient, error) {
			client, err := factory(ctx, binary)
			if err != nil || client == nil {
				return client, err
			}
			return rawToolsMCPClientForTest{mcpClient: client}, nil
		}
	}
	return h.PrepareCodexToolSurface(ctx, scope)
}

func jsonEscape(value string) string {
	data, _ := json.Marshal(value)
	return string(data[1 : len(data)-1])
}

func orchestrationToolNamesForTest(t *testing.T) []string {
	t.Helper()
	return contract.OrchestrationToolCanonicalNames()
}

func orchestrationToolsForTest(t *testing.T) []mcpdto.MCPTool {
	t.Helper()
	names := orchestrationToolNamesForTest(t)
	tools := make([]mcpdto.MCPTool, 0, len(names))
	for _, name := range names {
		tools = append(tools, mcpdto.MCPTool{
			Name:        name,
			Description: name,
			InputSchema: strictEmptyObjectSchema(),
		})
	}
	return tools
}

func orchestrationToolCallParamsForTest(name string) json.RawMessage {
	payload := map[string]any{
		"name":      name,
		"arguments": map[string]any{},
		"_agentId":  "agent-1",
		"_threadId": "provider-thread-1",
		"_callId":   "call-1",
		"_cwd":      "/repo",
	}
	raw, _ := json.Marshal(payload)
	return raw
}

func waitStartedToolLists(t *testing.T, started <-chan string, want ...string) {
	t.Helper()
	pending := make(map[string]struct{}, len(want))
	for _, name := range want {
		pending[name] = struct{}{}
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for len(pending) > 0 {
		select {
		case name := <-started:
			delete(pending, name)
		case <-timer.C:
			t.Fatalf("timed out waiting for parallel tools/list starts; pending=%#v", pending)
		}
	}
}

func assertDynamicToolNames(t *testing.T, tools []contract.DynamicToolSchema, want []string) {
	t.Helper()
	got := make(map[string]bool, len(tools))
	for _, tool := range tools {
		got[tool.Name] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Fatalf("dynamic tools missing %q; got %#v", name, got)
		}
	}
	for _, legacy := range []string{"lsp_grep", "lsp_format_preview"} {
		if got[legacy] {
			t.Fatalf("dynamic tools advertised legacy alias %q; got %#v", legacy, got)
		}
	}
	for _, legacy := range []string{"orchestration_launch_agent", "orchestration_list_agents"} {
		if got[legacy] {
			t.Fatalf("dynamic tools advertised legacy orchestration alias %q; got %#v", legacy, got)
		}
	}
}

func assertDynamicToolSchema(t *testing.T, tools []contract.DynamicToolSchema, name, description string, inputSchema, outputSchema json.RawMessage) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name != name {
			continue
		}
		if tool.Description != description {
			t.Fatalf("%s description = %q, want %q", name, tool.Description, description)
		}
		assertJSONRawMessageEqual(t, name+" inputSchema", tool.InputSchema, inputSchema)
		assertJSONRawMessageEqual(t, name+" outputSchema", tool.OutputSchema, outputSchema)
		return
	}
	t.Fatalf("dynamic tools missing %q; got %+v", name, tools)
}

func assertDynamicSkillToolSchema(t *testing.T, tools []contract.DynamicToolSchema, name, description string, inputSchema json.RawMessage) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name != name {
			continue
		}
		if tool.Description != description {
			t.Fatalf("%s description = %q, want %q", name, tool.Description, description)
		}
		assertJSONRawMessageEqual(t, name+" inputSchema", tool.InputSchema, inputSchema)
		if len(tool.OutputSchema) != 0 {
			t.Fatalf("%s outputSchema = %s, want omitted for plain text skill content", name, tool.OutputSchema)
		}
		return
	}
	t.Fatalf("dynamic tools missing %q; got %+v", name, tools)
}

func assertJSONRawMessageEqual(t *testing.T, label string, got, want json.RawMessage) {
	t.Helper()
	var gotValue any
	var wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("%s invalid JSON: %v", label, err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("%s invalid expected JSON: %v", label, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("%s = %s, want %s", label, got, want)
	}
}

func waitCodexSurfaceToolBegin(t *testing.T, ch <-chan tooldto.ToolCallBegin) tooldto.ToolCallBegin {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for codex surface ToolCallBegin")
		return tooldto.ToolCallBegin{}
	}
}

func waitCodexSurfaceToolEnd(t *testing.T, ch <-chan tooldto.ToolCallEnd) tooldto.ToolCallEnd {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for codex surface ToolCallEnd")
		return tooldto.ToolCallEnd{}
	}
}

func assertNoCodexSurfaceToolEvents(t *testing.T, beginCh <-chan tooldto.ToolCallBegin, endCh <-chan tooldto.ToolCallEnd) {
	t.Helper()
	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()
	select {
	case ev := <-beginCh:
		t.Fatalf("unexpected codex surface ToolCallBegin = %+v", ev)
	case ev := <-endCh:
		t.Fatalf("unexpected codex surface ToolCallEnd = %+v", ev)
	case <-timer.C:
	}
}
