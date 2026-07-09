package toolbridge

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"

	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
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

func (c *fakeMCPClient) CallTool(_ context.Context, name string, arguments json.RawMessage, _ ToolCallRequest) (*ToolCallResult, error) {
	c.calls = append(c.calls, name)
	c.arguments = append(c.arguments, append(json.RawMessage(nil), arguments...))
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

func jsonEscape(value string) string {
	data, _ := json.Marshal(value)
	return string(data[1 : len(data)-1])
}

func orchestrationToolAliasesForTest(t *testing.T) []contract.OrchestrationToolAlias {
	t.Helper()
	canonicals := contract.OrchestrationToolCanonicalNames()
	aliases := make([]contract.OrchestrationToolAlias, 0, len(canonicals))
	for _, canonical := range canonicals {
		legacy, ok := contract.OrchestrationLegacyPeerRealName(canonical)
		if !ok {
			t.Fatalf("OrchestrationLegacyPeerRealName(%q) = false, want true", canonical)
		}
		if got, ok := contract.OrchestrationCanonicalToolName(legacy); !ok || got != canonical {
			t.Fatalf("OrchestrationCanonicalToolName(%q) = %q, %v; want %q, true", legacy, got, ok, canonical)
		}
		aliases = append(aliases, contract.OrchestrationToolAlias{Canonical: canonical, LegacyPeerRealName: legacy})
	}
	return aliases
}

func orchestrationLegacyPeerToolsForTest(t *testing.T) []mcpdto.MCPTool {
	t.Helper()
	aliases := orchestrationToolAliasesForTest(t)
	tools := make([]mcpdto.MCPTool, 0, len(aliases))
	for _, alias := range aliases {
		tools = append(tools, mcpdto.MCPTool{
			Name:        alias.LegacyPeerRealName,
			Description: alias.Canonical,
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
	for _, legacy := range contract.OrchestrationToolLegacyPeerRealNames() {
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
